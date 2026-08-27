import { ArrowClockwiseIcon } from "@phosphor-icons/react/ArrowClockwise";
import { CaretLeftIcon } from "@phosphor-icons/react/CaretLeft";
import { CaretRightIcon } from "@phosphor-icons/react/CaretRight";
import { CrosshairSimpleIcon } from "@phosphor-icons/react/CrosshairSimple";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { type Picked, messageFrom, pickerSource, stopSource } from "./annotate";
import { addressFor } from "./browse";
import { sendNote } from "./daemon";
import { useOverlaysOpen } from "./overlays";
import { rememberPage, rememberedPage } from "./remembered";
import { colors, text } from "./tokens.stylex";

// A browser in the accessory column.
//
// ── a native webview, not an iframe ───────────────────────────────────────
//
// An `<iframe>` is one import and works in every context this code runs in,
// and it is the wrong answer: most of what a person wants beside an agent —
// a docs site, a dashboard, an issue tracker — sends `X-Frame-Options` or a
// `frame-ancestors` policy and renders as a blank rectangle with a console
// error nobody sees. A panel that works for localhost and nothing else is a
// panel that gets opened twice.
//
// Electrobun's `<electrobun-webview>` is a real WKWebView, positioned over the
// renderer by the native side. Nothing has to be imported for it: the tag is
// defined by the preload script that runs in every Electrobun webview.
//
// Three consequences follow, and each one shapes something below.
//
// **It is not a DOM element that clips and stacks.** It floats above the page
// at the rectangle it is told to occupy, so it cannot be hidden by an ancestor
// with `overflow: hidden` or covered by a dialog. What saves this is that Base
// UI unmounts a hidden tab panel: switching tabs runs `disconnectedCallback`,
// which tears the native webview down. The cost is that the page is reloaded
// when the tab comes back, which is why the address is remembered.
//
// **It does not exist outside Electrobun.** In a plain browser — `bun run dev`
// opened in Safari, and every Playwright probe — `customElements.get` answers
// undefined and there is nothing to render. That case says so in words rather
// than showing an empty box, because an empty box is also what a page that
// failed to load looks like.
//
// **It is driven by methods, not by props.** So it is created imperatively and
// held in a ref, the same shape the pane uses, rather than through JSX — which
// would additionally need the tag declared in `JSX.IntrinsicElements` to say
// anything TypeScript could check.
//
// The same fact is why this panel watches `useOverlaysOpen`: a native webview
// is drawn over the renderer by another process, so it is in front of every
// dialog whatever any `z-index` says, and the only thing that puts a modal in
// front of it is not drawing it. See overlays.ts for the rest of that.

/** What the preload defines, narrowed to the parts this panel uses. */
interface WebviewTag extends HTMLElement {
  loadURL(url: string): void;
  reload(): void;
  goBack(): void;
  goForward(): void;
  toggleHidden?(value?: boolean): void;
  syncDimensions?(force?: boolean): void;
  executeJavascript?(js: string): void;
  on?(event: string, listener: (event: CustomEvent) => void): void;
  off?(event: string, listener: (event: CustomEvent) => void): void;
}

const TAG = "electrobun-webview";

/** Whether a native webview can be made at all. */
const available = (): boolean =>
  typeof customElements !== "undefined" && customElements.get(TAG) !== undefined;

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  bar: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    padding: "0.3rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  button: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    width: "1.4rem",
    height: "1.4rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: {
      default: colors.muted,
      ":hover": colors.text,
    },
    cursor: "pointer",
  },
  // `flex: 1` with `minWidth: 0`, which is the pair. Either alone is the bug
  // that grows a horizontal scrollbar out of a long address.
  address: {
    flex: 1,
    minWidth: 0,
    padding: "0.1rem 0.4rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.tiny,
  },
  buttonOn: {
    backgroundColor: colors.border,
    color: colors.text,
  },
  // The composer, between the bar and the page. It takes height from the stage
  // rather than floating over it — a native webview is drawn on top of
  // everything this window renders, so there is no floating to be done.
  note: {
    display: "flex",
    flexDirection: "column",
    flexShrink: 0,
    gap: "0.3rem",
    padding: "0.4rem 0.5rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  picked: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    minWidth: 0,
  },
  // `flex: 1` with `minWidth: 0`. A selector is long and must clip, not push.
  label: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
    fontSize: text.tiny,
  },
  body: {
    minHeight: "3.4rem",
    padding: "0.25rem 0.4rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.tiny,
    resize: "vertical",
  },
  row: { display: "flex", alignItems: "center", gap: "0.4rem" },
  hint: { flex: 1, minWidth: 0, color: colors.muted, fontSize: text.tiny },
  send: {
    flexShrink: 0,
    padding: "0.2rem 0.5rem",
    backgroundColor: colors.border,
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.tiny,
    cursor: "pointer",
  },
  off: { opacity: 0.5, cursor: "default" },
  // The rectangle the native webview is told to occupy. It has no children of
  // its own — what fills it is drawn by another process, over the top.
  stage: { flex: 1, minHeight: 0 },
  said: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    padding: "1rem",
    color: colors.muted,
    fontSize: text.tiny,
    textAlign: "center",
  },
});

/**
 * @param project   the open session's workspace, when it is one of ours. A
 *                  note is typed at *that* workspace's agent, so without the
 *                  pair there is nobody to send to and the picker says so
 *                  rather than offering a button that cannot work.
 */
export function Web({
  project,
  workspace,
}: {
  readonly project: string | undefined;
  readonly workspace: string | undefined;
}) {
  const stage = useRef<HTMLDivElement>(null);
  const view = useRef<WebviewTag | undefined>(undefined);

  // What is in the box, which is not the same as what is loaded — a half-typed
  // address is neither, and the two only agree after enter or a navigation.
  const [typed, setTyped] = useState(rememberedPage() ?? "");
  const [here, setHere] = useState(rememberedPage());
  const [can] = useState(available);

  // Something modal is open, and this panel is drawn over the top of it.
  const covered = useOverlaysOpen();

  // ── the annotator ────────────────────────────────────────────────────────
  //
  // Three states, and they are a cycle rather than a set: idle, armed, and
  // holding something picked. The middle one lives in the *page*, so what is
  // kept here is only this window's belief about it — which is why the picker
  // reports a cancel (see annotate.ts) instead of leaving the two to disagree.
  const [armed, setArmed] = useState(false);
  const [picked, setPicked] = useState<Picked | undefined>(undefined);
  const [remark, setRemark] = useState("");
  const [sending, setSending] = useState(false);
  const [said, setSaid] = useState<string | undefined>(undefined);
  const canSend = project !== undefined && workspace !== undefined;

  // The same fact as `armed`, readable from a listener installed once.
  //
  // The element is built in a mount-only effect, so every handler it registers
  // closes over the first render's state forever. A ref is the value those
  // handlers can actually read — and re-arming after a navigation is exactly a
  // handler needing to know something that changed after it was written.
  const armedRef = useRef(false);
  useEffect(() => {
    armedRef.current = armed;
  }, [armed]);

  const arm = () => {
    setSaid(undefined);
    setArmed(true);
    view.current?.executeJavascript?.(pickerSource());
  };

  const disarm = () => {
    setArmed(false);
    view.current?.executeJavascript?.(stopSource());
  };

  // Drop the note and take the highlight off the page with it. Used by the
  // dismiss button and after a successful send — a composer left holding a
  // delivered note reads as one that failed to go.
  const forget = () => {
    setPicked(undefined);
    setRemark("");
    setArmed(false);
    view.current?.executeJavascript?.(stopSource());
  };

  useEffect(() => {
    const element = view.current;
    if (element === undefined) {
      return;
    }
    element.toggleHidden?.(covered);
    // Forced on the way back, rather than waiting to be noticed. While hidden
    // the element's rectangle is zero, and the tag's own sync loop only polls
    // every 100ms — so without this the page returns a tenth of a second after
    // the dialog goes, which reads as the panel being slow to wake up.
    if (!covered) {
      element.syncDimensions?.(true);
    }
  }, [covered]);

  useEffect(() => {
    const parent = stage.current;
    if (parent === null || !available()) {
      return;
    }

    const element = document.createElement(TAG) as WebviewTag;
    element.style.width = "100%";
    element.style.height = "100%";
    if (here !== undefined) {
      element.setAttribute("src", here);
    }
    parent.append(element);
    view.current = element;

    // The address bar follows the page, not the other way round. A link
    // followed inside the webview is a navigation this side never asked for,
    // and an address bar that kept saying where the page started would be
    // lying about where it is.
    // Guarded, because this is someone else's custom element and the whole
    // panel is inside one error boundary. A missing method here would take the
    // accessory column out, and losing the address bar is a smaller loss than
    // losing the column.
    const navigated = (event: CustomEvent) => {
      const url = (event.detail as { readonly url?: string } | undefined)?.url;
      if (typeof url === "string" && url !== "") {
        setHere(url);
        setTyped(url);
        rememberPage(url);
      }
    };
    element.on?.("did-navigate", navigated);

    // What the picker says back. The only wire from that process to this one.
    const heard = (event: CustomEvent) => {
      const message = messageFrom(event.detail);
      if (message === undefined) {
        // Not ours. `host-message` is the page's channel and any script on any
        // site can put anything down it.
        return;
      }
      if (message.kind === "cancelled") {
        setArmed(false);
        return;
      }
      setArmed(false);
      setPicked(message.picked);
      setRemark("");
      setSaid(undefined);
    };
    element.on?.("host-message", heard);

    // A navigation is a new document, and the picker was in the old one.
    //
    // Re-injected on `dom-ready` rather than `did-navigate`: the second says a
    // navigation was committed, which is before there is a `document.body` to
    // append a highlight to. Only while armed — re-arming a picker somebody
    // turned off would put a highlight back on a page they are trying to read.
    const ready = () => {
      if (armedRef.current) {
        element.executeJavascript?.(pickerSource());
      }
    };
    element.on?.("dom-ready", ready);

    return () => {
      // The listener is taken off as well as the element being removed. It is
      // redundant — the element and its registry entry both go — and it is
      // written anyway, because a subscription whose cleanup is implied by
      // someone else's teardown is one that becomes a leak the day their
      // teardown changes.
      element.off?.("did-navigate", navigated);
      element.off?.("host-message", heard);
      element.off?.("dom-ready", ready);
      // Removing it is what tears the native webview down — see
      // `disconnectedCallback`. Leaving it attached would leave a webview
      // floating over the window with nothing rendering it.
      element.remove();
      view.current = undefined;
    };
    // Deliberately once. `here` is read at creation to restore the last page,
    // and afterwards navigation goes through `loadURL` — rebuilding the
    // element on every address change would throw away the history the back
    // button exists to walk.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const deliver = () => {
    if (picked === undefined || project === undefined || workspace === undefined) {
      return;
    }
    const body = remark.trim();
    if (body === "" || sending) {
      // A note with no remark is an address with nothing said. Refused here as
      // well as by the disabled button, because Enter reaches this directly.
      return;
    }
    setSending(true);
    setSaid(undefined);
    sendNote(project, workspace, { ...picked, body })
      .then(() => {
        forget();
        setSaid("sent to the agent");
      })
      .catch((error: unknown) => {
        // Said out loud, in the panel. The workspace's agent may have ended,
        // which is the ordinary case and reads as nothing happening otherwise.
        setSaid(error instanceof Error ? error.message : String(error));
      })
      .finally(() => setSending(false));
  };

  const go = (typedText: string) => {
    const url = addressFor(typedText);
    if (url === undefined) {
      return;
    }
    setHere(url);
    setTyped(url);
    rememberPage(url);
    view.current?.loadURL(url);
  };

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.bar)}>
        <button
          type="button"
          aria-label="back"
          title="back"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.goBack()}
        >
          <CaretLeftIcon size={13} weight="bold" aria-hidden />
        </button>
        <button
          type="button"
          aria-label="forward"
          title="forward"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.goForward()}
        >
          <CaretRightIcon size={13} weight="bold" aria-hidden />
        </button>
        <button
          type="button"
          aria-label="reload"
          title="reload"
          {...stylex.props(styles.button)}
          onClick={() => view.current?.reload()}
        >
          <ArrowClockwiseIcon size={13} weight="bold" aria-hidden />
        </button>
        <input
          aria-label="address"
          placeholder="address, or something to search for"
          value={typed}
          spellCheck={false}
          {...stylex.props(styles.address)}
          onChange={(event) => setTyped(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              go(typed);
            }
            if (event.key === "Escape") {
              // Back to what is actually loaded, rather than clearing. Escape
              // in an address bar means "forget what I was typing".
              event.stopPropagation();
              setTyped(here ?? "");
            }
          }}
          // An `<input>` already keeps ctrl+h/j/k on macOS — see navigation.ts
          // — and this is the one field in the window where that matters most,
          // because an address is edited character by character.
          onFocus={(event) => event.target.select()}
        />
        <button
          type="button"
          aria-label={armed ? "stop picking an element" : "pick an element"}
          aria-pressed={armed}
          title={
            can
              ? armed
                ? "click an element in the page, or press escape"
                : "point at an element and tell the agent about it"
              : "this needs the app window"
          }
          disabled={!can}
          {...stylex.props(styles.button, armed && styles.buttonOn, !can && styles.off)}
          onClick={() => (armed ? disarm() : arm())}
        >
          <CrosshairSimpleIcon size={13} weight="bold" aria-hidden />
        </button>
      </div>

      {picked === undefined ? undefined : (
        <div {...stylex.props(styles.note)}>
          <div {...stylex.props(styles.picked)}>
            <CrosshairSimpleIcon size={12} weight="bold" aria-hidden />
            {/* The label a person recognises, and the selector as the tooltip.
                The second is the one an agent can act on and the one nobody
                wants to read, which is exactly what a title attribute is for. */}
            <span title={picked.selector} {...stylex.props(styles.label)}>
              {picked.label}
            </span>
            <button
              type="button"
              aria-label="discard this note"
              title="discard"
              {...stylex.props(styles.button)}
              onClick={forget}
            >
              <XIcon size={12} weight="bold" aria-hidden />
            </button>
          </div>
          <textarea
            aria-label="what to say about this element"
            placeholder="what is wrong with it"
            value={remark}
            autoFocus
            {...stylex.props(styles.body)}
            onChange={(event) => setRemark(event.target.value)}
            onKeyDown={(event) => {
              // cmd/ctrl+enter sends, plain enter is a newline. A remark is
              // often two sentences, and a composer that submits on Enter is
              // one that delivers the first half of them.
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                event.preventDefault();
                deliver();
              }
              if (event.key === "Escape") {
                event.stopPropagation();
                forget();
              }
            }}
          />
          <div {...stylex.props(styles.row)}>
            <span {...stylex.props(styles.hint)}>
              {canSend ? (said ?? "⌘⏎ to send") : "no workspace open to send to"}
            </span>
            <button
              type="button"
              disabled={!canSend || remark.trim() === "" || sending}
              {...stylex.props(
                styles.send,
                (!canSend || remark.trim() === "" || sending) && styles.off,
              )}
              onClick={deliver}
            >
              {sending ? "sending…" : "send"}
            </button>
          </div>
        </div>
      )}

      {can ? (
        <div ref={stage} {...stylex.props(styles.stage)} />
      ) : (
        <div {...stylex.props(styles.said)}>
          this panel needs the app window — a native webview is not something a browser tab can make
        </div>
      )}
    </div>
  );
}
