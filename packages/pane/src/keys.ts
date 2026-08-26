// Chords the emulator would otherwise flatten.
//
// ghostty-web sends Enter as a carriage return whether or not Shift is held,
// which is correct for a shell and wrong for everything a person composes in.
// An agent's prompt, a commit message, a `read -d ''` — all of them want a
// newline that is not "run it", and the terminal has no way to express that
// beyond what the keyboard sends.

/** Escape, then carriage return: the Alt-modified form of Enter. */
const ESC_ENTER = "\r";

/**
 * Shift+Enter, and why it is spelled as Alt+Enter.
 *
 * A terminal has no encoding for Shift+Enter. The stream carries characters,
 * and Shift+Enter is the same character as Enter — which is why it does nothing
 * in most terminals, and why the ones where it works are the ones someone
 * configured to send something else on that key.
 *
 * What they configure it to send is `ESC` then `CR`: the Alt-modified form,
 * which programs already understand because Alt+Enter is a chord a terminal
 * *can* express. Claude Code reads it as a newline, and so does readline, and
 * so does anything else taking ESC as a meta prefix. This is not a private
 * arrangement between the pane and one program — it is the existing convention,
 * moved to the key people actually reach for.
 *
 * @param host  the element the canvas lives in.
 * @param send  where a keystroke goes — the same sink ordinary typing uses, so
 *              this cannot arrive out of order with the characters around it.
 */
export function installKeys(host: HTMLElement, send: (data: string) => void): void {
  host.addEventListener(
    "keydown",
    (event: KeyboardEvent) => {
      // Shift alone. With Ctrl or Meta held this is some other chord, and with
      // Alt held the emulator already sends exactly this.
      if (event.key !== "Enter" || !event.shiftKey || event.ctrlKey || event.metaKey) {
        return;
      }
      // Capture phase, ahead of ghostty-web's own handler, which would send a
      // bare carriage return and submit whatever was being written.
      event.preventDefault();
      event.stopPropagation();
      send(ESC_ENTER);
    },
    { capture: true },
  );
}
