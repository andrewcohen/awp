import { meterSent } from "./meter";

// Text that arrives without anyone pressing a key.
//
// ── the hole this fills ───────────────────────────────────────────────────
//
// A terminal emulator listens for keys. ghostty-web listens on the host
// element for `keydown`, `keypress`, `paste` and the three composition events,
// and that covers a keyboard and an input method. What it does not cover is
// text *inserted* into the document by something else:
//
//   dictation            speech, transcribed, then put into the focused field
//   an accessibility     an assistive tool writing on someone's behalf
//   tool
//   autofill, expanders  a snippet tool replacing what was typed
//
// All of those reach a page the same way — the browser raises `beforeinput`
// with an `inputType` and the text in `data`, and no key event at all. The
// emulator has no listener for it, and the host is `contenteditable`, so
// ghostty-web's own line in `open()` is what settles it:
//
//   A.addEventListener("beforeinput", (E) => E.preventDefault())
//
// Cancelled, every time, with nothing reading it first. So the text is thrown
// away before anything can send it, and the failure is the quietest one there
// is: someone speaks, and the terminal does not move. No error, no key, no
// event anyone was watching.
//
// That `preventDefault` is right, incidentally — the host must not accumulate
// real DOM text under the canvas. What is missing is reading the event before
// cancelling it, and this runs in the capture phase to get there first.
//
// ── the trap: everything is a beforeinput ─────────────────────────────────
//
// Ordinary typing raises `beforeinput` too. Sending on both routes doubles
// every character a person types — which is a far worse bug than the one being
// fixed, and it looks like a broken keyboard rather than a bad listener.
//
// There is no flag on the event that says "a key did this". What there is, is
// the ordering: a key produces `keydown` and then `beforeinput` in the same
// task, immediately. Insertion produces `beforeinput` alone. So a keystroke is
// remembered for a moment and an insertion arriving inside that moment is
// taken to be its echo.
//
// Two frames rather than a microtask or a timestamp comparison, both of which
// were considered and are worse. A microtask checkpoint can run before the
// input event is dispatched, so the flag would already be down. Comparing
// `event.timeStamp` assumes the engine copies the key's timestamp onto the
// input event it derives, which is not something the spec requires.
//
// Composition is left alone entirely. `isComposing` marks an input method
// mid-word, and ghostty-web already sends the finished text on `compositionend`
// — reading it here as well would double a whole word rather than a character.

/** How long after a key an insertion is treated as that key's own echo. */
const AFTER_KEY_MS = 32;

/**
 * What can carry text that a person meant to type.
 *
 * A short list on purpose. `deleteContentBackward`, `historyUndo` and the rest
 * describe an edit to a document, and this host is not a document — there is
 * nothing under the canvas to edit. A terminal's backspace is a keystroke and
 * arrives as one.
 */
const CARRIES_TEXT = new Set([
  "insertText",
  "insertReplacementText",
  "insertFromPaste",
  "insertFromDrop",
  "insertFromYank",
]);

/** Everything the decision below depends on, and nothing else. */
export interface Insertion {
  readonly inputType: string;
  /** An input method is mid-word. ghostty-web sends the finished text itself. */
  readonly isComposing: boolean;
  /** Milliseconds since the last keystroke, or Infinity if there has been none. */
  readonly sinceKey: number;
  /** `event.data` — an insertion carries its text here. */
  readonly data: string | null;
  /** `event.dataTransfer` — a paste or a drop carries it here instead. */
  readonly transfer: string | null;
}

/**
 * What to send for one `beforeinput`, or nothing.
 *
 * Pulled out of the listener so it can be tested. What is left in the listener
 * is wiring — add, read, cancel — and what is here is every way this can be
 * wrong: sending a composition twice, sending a keystroke twice, missing the
 * spelling the text arrived under, or sending an edit that carries no text.
 */
export const textFrom = (event: Insertion): string | undefined => {
  if (event.isComposing || !CARRIES_TEXT.has(event.inputType)) {
    return undefined;
  }
  if (event.sinceKey < AFTER_KEY_MS) {
    return undefined;
  }
  // `data` for an insertion, the transfer for a paste or a drop. Both
  // spellings exist because the event carries the text in whichever place
  // suits the kind, and a reader that only knows one silently drops the other.
  const text = event.data ?? event.transfer ?? "";
  return text === "" ? undefined : text;
};

/**
 * @param host  the element the canvas lives in — where ghostty-web listens.
 * @param send  where a keystroke goes, the same sink ordinary typing uses, so
 *              dictated text cannot arrive out of order with what is typed
 *              around it.
 */
export function installDictation(host: HTMLElement, send: (data: string) => void): void {
  let lastKey = Number.NEGATIVE_INFINITY;

  host.addEventListener(
    "keydown",
    () => {
      lastKey = performance.now();
    },
    { capture: true },
  );

  // A paste handled elsewhere counts as a key for this purpose: it has already
  // sent the text, and cancelling the paste normally stops the insertion — but
  // "normally" is doing work in that sentence, and a double paste is the same
  // failure as a double keystroke.
  host.addEventListener(
    "paste",
    () => {
      lastKey = performance.now();
    },
    { capture: true },
  );

  host.addEventListener(
    "beforeinput",
    (event: InputEvent) => {
      const text = textFrom({
        inputType: event.inputType,
        isComposing: event.isComposing,
        sinceKey: performance.now() - lastKey,
        data: event.data,
        transfer: event.dataTransfer?.getData("text/plain") ?? null,
      });
      if (text === undefined) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      meterSent(text, false);
      send(text);
    },
    { capture: true },
  );
}
