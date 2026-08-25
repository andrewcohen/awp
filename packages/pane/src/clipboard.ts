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

  // Route one: the platform delivered a paste. This is what a right-click
  // paste, an Edit menu, and a remote client synthesising one all produce,
  // and it needs no permission because the user's gesture carried the data.
  host.addEventListener(
    "paste",
    (event: ClipboardEvent) => {
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

  // Route two: the keystroke, read directly.
  //
  // On macOS the `paste` event only exists if something turns the chord into
  // a paste command — an application Edit menu, normally. An Electrobun window
  // has none, so the keystroke arrives as an ordinary keydown and nothing
  // else happens.
  //
  // Three chords rather than one. Cmd+V is macOS, Ctrl+Shift+V and
  // Shift+Insert are what terminals use everywhere else, and a remote or
  // handheld keyboard may only be able to send one of them.
  //
  // Capture phase, ahead of ghostty-web's own keydown handler, which would
  // otherwise send the key to the program as a control character.
  host.addEventListener(
    "keydown",
    (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();

      const pasteChord =
        (key === "v" && (event.metaKey || (event.ctrlKey && event.shiftKey)) && !event.altKey) ||
        (key === "insert" && event.shiftKey);

      if (pasteChord) {
        event.preventDefault();
        event.stopPropagation();
        void navigator.clipboard.readText().then(pasteText, (error: unknown) => {
          // Said out loud. A webview can refuse a clipboard read — the
          // document has to be focused, and some hosts refuse regardless —
          // and the previous version swallowed that, so the only symptom was
          // a paste that did nothing. The meter is in the accessory column
          // precisely because guessing at symptoms like this costs a day.
          console.warn("[pane] clipboard read refused:", error);
        });
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
