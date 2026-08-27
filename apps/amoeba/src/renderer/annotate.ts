// Pointing at something in a page that is not ours to touch.
//
// ── the page is another process, and that decides everything below ─────────
//
// The web panel is a real WKWebView drawn over the renderer by the native
// side. Nothing in this window can reach that document: no ref, no query, no
// event bubbling out of it. The only two wires between here and there are the
// ones Electrobun already runs, and they are both one-way:
//
//   this window  ──  element.executeJavascript(js)   ──►  the page
//   this window  ◄──  window.__electrobunSendToHost  ──   the page
//                     arriving as a "host-message" event on the tag
//
// `executeJavascript` returns nothing at all — it is `send`, not `request`, and
// the native call behind it is `evaluateJavaScriptWithNoCompletion`. So the
// picker cannot be asked a question; it has to volunteer an answer. Hence a
// script that installs listeners and reports, rather than a function that is
// called and returns.
//
// `__electrobunSendToHost` is defined by Electrobun's own preload, which every
// BrowserView gets — including the child webview a tag creates for an arbitrary
// page. That is why this works on a site nobody here controls, and it is not
// something this file arranged.
//
// ── what comes back is the page's words, and the page is a stranger ────────
//
// A person chose the site, pointed at the element and wrote the remark. The
// element's own `text` is none of those: it is whatever the document says, and
// it ends up in a prompt typed at an agent. So it is trimmed, collapsed and
// capped before it goes anywhere, and it is delivered on a labelled line rather
// than as prose — see `notePrompt` in the daemon. Nothing here treats it as an
// instruction, and neither should anything downstream.

/** The marker every message from the picker carries. */
const FROM_PICKER = "awp-annotate";

/** What the picker reports when somebody clicks an element. */
export interface Picked {
  /** Where the page was, as the document reports it. */
  readonly url: string;
  /** A CSS selector for the element. Best effort — see `selectorFor` below. */
  readonly selector: string;
  /** A short readable name: tag, id, and one class. */
  readonly label: string;
  /** What the element said. Possibly empty; an icon button has no text. */
  readonly text: string;
}

/** How much of an element's text the page is asked to send. */
const TEXT_CAP = 400;

const str = (value: unknown): string | undefined => (typeof value === "string" ? value : undefined);

/**
 * What the picker can say. Two things, and the second one matters.
 *
 * A cancel is a message rather than a silence because the button in the panel
 * is a toggle: pressing Escape in the page has to put it back up, and nothing
 * over here can see a keystroke that happened in another process. Without it
 * the panel goes on claiming to be armed over a picker that has gone.
 */
export type Message =
  | { readonly kind: "picked"; readonly picked: Picked }
  | { readonly kind: "cancelled" };

/**
 * A message off the tag, if it is one of ours.
 *
 * Guarded rather than cast, and the marker is the whole reason: `host-message`
 * is the page's channel, not this feature's. Any script on any site can call
 * `__electrobunSendToHost` with anything, and that value arrives here already
 * `JSON.parse`d by the native side. A cast would put a stranger's object
 * straight into a prompt.
 */
export const messageFrom = (detail: unknown): Message | undefined => {
  if (typeof detail !== "object" || detail === null) {
    return undefined;
  }
  const one = detail as Record<string, unknown>;
  if (one["from"] !== FROM_PICKER) {
    return undefined;
  }
  if (one["cancelled"] === true) {
    return { kind: "cancelled" };
  }
  const selector = str(one["selector"]);
  if (selector === undefined || selector === "") {
    // The one field with no sensible default. A note whose anchor is the empty
    // string names every element and therefore none.
    return undefined;
  }
  return {
    kind: "picked",
    picked: {
      url: str(one["url"]) ?? "",
      selector,
      label: str(one["label"]) ?? selector,
      text: (str(one["text"]) ?? "").slice(0, TEXT_CAP),
    },
  };
};

/**
 * The picker, as source to hand the page.
 *
 * A template string rather than a function put through `toString()`, which was
 * the first shape and is a trap here: this file is compiled — by Vite, by the
 * React Compiler, by StyleX's Babel pass — and a function stringified after
 * those have run is not the function that was written. Minified names, injected
 * helpers, hoisted constants referencing module scope that does not exist in
 * the page. A string is the same string wherever it is read.
 *
 * Three properties it has to have, each of which is a way to get this wrong:
 *
 * **Idempotent.** The panel re-injects on every navigation, and a person can
 * press the button twice. A second copy of the listeners means two messages per
 * click, so the script parks itself on `window` and the second run only re-arms
 * the first.
 *
 * **Removable.** Disarming has to take the listeners off and the overlay out.
 * A highlight left painted over somebody's page is indistinguishable from the
 * site being broken.
 *
 * **Non-destructive.** It adds one absolutely-positioned div and cancels the
 * clicks it consumes. It does not restyle anything, and it puts the div back
 * where it found it — the page is somebody's live application, possibly with
 * unsaved state in it.
 */
export const pickerSource = (): string => `
(() => {
  const KEY = "__awpAnnotate";
  if (window[KEY]) { window[KEY].arm(); return; }

  const send = (message) => {
    if (typeof window.__electrobunSendToHost === "function") {
      window.__electrobunSendToHost(message);
    }
  };

  // A selector a person could paste into devtools, and a machine could resolve.
  //
  // An id wins outright when it is unique, because it is short, stable across a
  // re-render and readable. Otherwise walk up appending :nth-of-type() and stop
  // the moment the accumulated selector matches exactly one element — a full
  // path from <html> is correct and unreadable, and the shortest unique suffix
  // is the one that survives an unrelated part of the page changing.
  const selectorFor = (el) => {
    if (el.id && document.querySelectorAll("#" + CSS.escape(el.id)).length === 1) {
      return "#" + CSS.escape(el.id);
    }
    const parts = [];
    let node = el;
    while (node && node.nodeType === 1 && node !== document.documentElement) {
      const tag = node.tagName.toLowerCase();
      const siblings = node.parentElement
        ? [...node.parentElement.children].filter((c) => c.tagName === node.tagName)
        : [node];
      const nth = siblings.length > 1 ? ":nth-of-type(" + (siblings.indexOf(node) + 1) + ")" : "";
      parts.unshift(tag + nth);
      const here = parts.join(" > ");
      try {
        if (document.querySelectorAll(here).length === 1) { return here; }
      } catch { return here; }
      node = node.parentElement;
    }
    return parts.join(" > ") || el.tagName.toLowerCase();
  };

  // The name a person recognises. One class, not all of them: a utility-class
  // codebase puts fourteen on a button and the label becomes the selector again.
  const labelFor = (el) => {
    const tag = el.tagName.toLowerCase();
    const id = el.id ? "#" + el.id : "";
    const cls = el.classList.length > 0 ? "." + el.classList[0] : "";
    return tag + id + cls;
  };

  const box = document.createElement("div");
  box.setAttribute("data-awp-annotate", "");
  // pointer-events:none is what keeps it out of elementFromPoint — without it
  // the overlay is the only thing ever hovered, over itself, forever.
  box.style.cssText = [
    "position:fixed", "z-index:2147483647", "pointer-events:none",
    "border:2px solid #8aadf4", "background:rgba(138,173,244,0.18)",
    "border-radius:2px", "display:none",
  ].join(";");

  let on = false;
  let over = null;

  const draw = (el) => {
    const r = el.getBoundingClientRect();
    box.style.display = "block";
    box.style.left = r.left + "px";
    box.style.top = r.top + "px";
    box.style.width = r.width + "px";
    box.style.height = r.height + "px";
  };

  const moved = (event) => {
    if (!on) { return; }
    const el = document.elementFromPoint(event.clientX, event.clientY);
    if (!el || el === box) { return; }
    over = el;
    draw(el);
  };

  const clicked = (event) => {
    if (!on) { return; }
    // Cancelled in the capture phase, before the page's own handler. Picking a
    // link must not navigate, and picking "delete" must not delete.
    event.preventDefault();
    event.stopPropagation();
    const el = over || document.elementFromPoint(event.clientX, event.clientY);
    if (!el) { return; }
    // settle, not disarm: the listeners come off but the highlight stays.
    // Somebody is about to type a sentence about this element and needs to be
    // able to see which one it was.
    settle();
    send({
      from: ${JSON.stringify(FROM_PICKER)},
      url: document.location.href,
      selector: selectorFor(el),
      label: labelFor(el),
      text: (el.innerText || el.textContent || "").trim().replace(/\\s+/g, " ").slice(0, ${TEXT_CAP}),
    });
  };

  const keyed = (event) => {
    if (on && event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      disarm();
      send({ from: ${JSON.stringify(FROM_PICKER)}, cancelled: true });
    }
  };

  const arm = () => {
    if (on) { return; }
    on = true;
    if (!box.isConnected) { document.body.appendChild(box); }
    document.addEventListener("mousemove", moved, true);
    document.addEventListener("click", clicked, true);
    document.addEventListener("keydown", keyed, true);
  };

  // Stop listening. Everything the page can now do, it does again.
  const settle = () => {
    on = false;
    over = null;
    document.removeEventListener("mousemove", moved, true);
    document.removeEventListener("click", clicked, true);
    document.removeEventListener("keydown", keyed, true);
  };

  // And take the paint off. A highlight left over somebody's page is
  // indistinguishable from the site being broken.
  const disarm = () => {
    settle();
    box.style.display = "none";
    box.remove();
  };

  window[KEY] = { arm, settle, disarm };
  arm();
})();
`;

/** Take the picker off, wherever it is. Safe when it was never installed. */
export const stopSource = (): string => `window.__awpAnnotate?.disarm();`;
