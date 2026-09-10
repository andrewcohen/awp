// Getting text out of a terminal that has taken the mouse.
//
// ── why this has to exist at all ──────────────────────────────────────────
//
// opentui enables mouse reporting, so a drag across the transcript is *its*
// gesture rather than the terminal's: the terminal never sees a selection, so
// there is nothing for cmd+C to copy and nothing highlighted for the eye
// either. Reported as "i cant copy paste".
//
// What opentui does instead is hand the drag to its own selection — which is
// better, because it knows where the text is — and then stop. Nothing writes
// it anywhere. So this is the last step of the gesture: **selecting copies.**
// No chord, because there is no chord a terminal reliably delivers —
// ctrl+shift+c needs the kitty protocol, and cmd+C never reaches a program at
// all.
//
// ── two routes, because either one can be the wrong one ──────────────────
//
//   OSC 52   the terminal is asked to put it on its clipboard. The only
//            route that works over ssh or from inside a multiplexer — and
//            it is refused by default by some terminals
//   host     this machine's own clipboard, through opentui's native
//            backend. Certain locally, meaningless on the far end of ssh
//
// Both, every time, rather than choosing: a copy that silently went nowhere
// is the failure being fixed, and the cost of the redundant one is a few
// hundred bytes.
//
// Paste needs nothing. Bracketed paste is enabled by the renderer and the
// textarea inserts it whole — measured in `probe:paste`, which asserts on
// what the child process received rather than on what was sent to it.

import type { CliRenderer } from "@opentui/core";
import { createHostClipboard } from "@opentui/core";
import { notify } from "./notices";

/** Built once: the backend spawns work per instance, and one is plenty. */
let host: ReturnType<typeof createHostClipboard> | undefined;

const toHost = (text: string) => {
  host ??= createHostClipboard();
  // Nothing is awaited and nothing is reported. A machine with no clipboard
  // — a container, a remote box — is the ordinary case for the OSC 52 route
  // above, and a failure here says nothing a person could act on.
  void host.writeText(text).catch(() => undefined);
};

/**
 * Copy whatever was just selected, for as long as the returned function is
 * not called.
 *
 * On the *end* of the drag, not during it: `selection` fires as the pointer
 * moves, and copying on every frame would put a hundred half-selections on
 * the clipboard and leave the last one there if the gesture ended outside a
 * selectable renderable.
 */
export const copyOnSelect = (renderer: CliRenderer): (() => void) => {
  const copy = () => {
    const selection = renderer.getSelection();
    if (selection === null || selection.isDragging) return;
    const text = selection.getSelectedText();
    if (text.trim() === "") return;
    renderer.copyToClipboardOSC52(text);
    toHost(text);
    // Copying leaves the screen exactly as it was, which is what a gesture
    // that did nothing also looks like. So it says so — see `toast.ts`.
    const lines = text.split("\n").length;
    notify(
      lines > 1
        ? `copied ${String(lines)} lines`
        : `copied ${String(text.length)} character${text.length === 1 ? "" : "s"}`,
    );
  };
  renderer.on("selection", copy);
  return () => {
    renderer.off("selection", copy);
  };
};
