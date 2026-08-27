import type { Terminal } from "ghostty-web";

// Copy and paste for a window with no Edit menu.
//
// Its own file because it is its own subject, and because terminal.ts is the
// one place that owns the Terminal — a module that only needs to reach it
// through a getter does not belong in there.

/**
 * @param host   the element the canvas lives in; listeners are attached here.
 * @param get    the current Terminal, read at event time rather than captured.
 *               The pane keeps one Terminal for the window's life, but it does
 *               not exist yet when this is installed.
 */
/**
 * How long to let the system paste before asking for the clipboard.
 *
 * A menu item's key equivalent is dispatched by AppKit in the same gesture, so
 * this only has to outlast one turn of the event loop and a round trip into the
 * web view. Long enough that a native paste is never raced; short enough that
 * the fallback does not read as a delay.
 */
const WAIT_FOR_NATIVE = 120;

/** Read the clipboard, and say so when the host refuses. */
const readClipboard = (use: (text: string) => void): void => {
  void navigator.clipboard.readText().then(use, (error: unknown) => {
    // Said out loud. A webview can refuse a clipboard read — the document has
    // to be focused, and some hosts prompt or refuse regardless — and an
    // earlier version swallowed that, so the only symptom was a paste that did
    // nothing. Guessing at symptoms like this is what the meter exists for.
    console.warn("[pane] clipboard read refused:", error);
  });
};

export function installClipboard(host: HTMLElement, get: () => Terminal | undefined): void {
  // **Two routes in, deliberately.** The keystroke route reads the clipboard
  // itself; the event route takes what the platform hands over. Neither is
  // sufficient alone and the reason they are both here is that the first one
  // failed silently for someone on a different machine — a paste that does
  // nothing and says nothing is indistinguishable from a broken terminal.

  /**
   * Put text in, the way the program asked for it.
   *
   * `paste()` rather than `write()`: it wraps the text in bracketed-paste
   * markers when the program has turned them on, which is what stops a
   * pasted newline being run as a command.
   */
  const pasteText = (text: string): void => {
    if (text !== "") {
      get()?.paste(text);
    }
  };

  // When the platform last delivered a paste of its own. Read by the keystroke
  // route below to decide whether it needs to do anything at all.
  let nativeAt = Number.NEGATIVE_INFINITY;

  // Route one: the platform delivered a paste. This is what a right-click
  // paste, an Edit menu, and a remote client synthesising one all produce,
  // and it needs no permission because the user's gesture carried the data.
  host.addEventListener(
    "paste",
    (event: ClipboardEvent) => {
      nativeAt = performance.now();
      const text = event.clipboardData?.getData("text/plain") ?? "";
      if (text === "") {
        return;
      }
      // Ours to handle, so ghostty-web's own handler does not do it again.
      event.preventDefault();
      event.stopPropagation();
      pasteText(text);
    },
    { capture: true },
  );

  // Route two: the keystroke, read directly — and only when route one did not
  // already do it.
  //
  // On macOS the `paste` event only exists if something turns the chord into a
  // paste command, which is an application menu item's job. This window had no
  // menu, so cmd+V arrived as an ordinary keydown, and this route reading the
  // clipboard itself was the whole of paste.
  //
  // **That is what raised the permission prompt.** `navigator.clipboard
  // .readText()` is gated by WebKit behind a small native "Paste" button beside
  // the cursor. A person can click it. Dictation cannot — it puts text on the
  // clipboard, synthesises cmd+V, and has no way through a prompt — so speaking
  // at the terminal produced a paste menu and nothing else.
  //
  // The window has an Edit menu now (see the app's menu.ts), so the ordinary
  // path is native again and this route is the fallback rather than the plan:
  //
  //   with a Paste item     AppKit runs `paste:` → a `paste` event with the
  //                         text on it → route one, no permission, no prompt
  //   without one           nothing arrives, and after a moment this asks
  //
  // Deferred rather than decided up front, because there is no way to ask
  // whether the chord is claimed. `WAIT_FOR_NATIVE` is long enough for AppKit
  // to dispatch a paste it is going to dispatch and short enough not to read as
  // a delay. The keydown is deliberately **not** cancelled on the macOS chord:
  // cancelling it is what would stop the native paste from ever happening.
  //
  // The two non-macOS chords are cancelled and read immediately, as before.
  // Nothing turns those into a paste command, so there is nothing to wait for.
  //
  // Capture phase, ahead of ghostty-web's own keydown handler, which would
  // otherwise send the key to the program as a control character.
  host.addEventListener(
    "keydown",
    (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();

      // The macOS chord, which the system may turn into a paste of its own.
      const nativeChord = key === "v" && event.metaKey && !event.ctrlKey && !event.altKey;
      // The ones nothing else claims. Ctrl+Shift+V and Shift+Insert are what
      // terminals use everywhere else, and a remote or handheld keyboard may
      // only be able to send one of them.
      const ownChord =
        (key === "v" && event.ctrlKey && event.shiftKey && !event.metaKey && !event.altKey) ||
        (key === "insert" && event.shiftKey);

      if (nativeChord) {
        // Not cancelled, and that is the fix rather than an oversight:
        // cancelling this is what stops the system pasting.
        const at = performance.now();
        setTimeout(() => {
          if (nativeAt > at) {
            return;
          }
          readClipboard(pasteText);
        }, WAIT_FOR_NATIVE);
        return;
      }

      if (ownChord) {
        event.preventDefault();
        event.stopPropagation();
        readClipboard(pasteText);
        return;
      }

      if (key === "c" && (event.metaKey || (event.ctrlKey && event.shiftKey))) {
        if (!(get()?.hasSelection() ?? false)) {
          // Without a selection, Cmd+C on macOS and Ctrl+C everywhere else
          // must still reach the program as an interrupt — taking that away
          // would be worse than having no copy at all.
          return;
        }
        event.preventDefault();
        event.stopPropagation();
        void navigator.clipboard.writeText(get()?.getSelection() ?? "");
      }
    },
    { capture: true },
  );
}
