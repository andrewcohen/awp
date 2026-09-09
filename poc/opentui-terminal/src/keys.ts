// The chords, in one place, because two screens answer some of the same ones.
//
// ctrl and never a bare letter wherever a terminal is on screen: an unmodified
// key belongs to whatever is running in the pty, and stealing it would break
// vim inside the very window whose keys are copied from vim.

import type { KeyEvent } from "@opentui/core";

/** ctrl+backslash — zmx's own detach, and this POC's "back". */
export const isBack = (key: KeyEvent) =>
  key.ctrl && (key.name === "\\" || key.sequence === String.fromCodePoint(28));

export const isQuit = (key: KeyEvent) => key.ctrl && key.name === "q";

// ── ctrl+j is Enter, and no code can tell them apart ───────────────────────
//
// A terminal sends 0x0A for both, and only the kitty keyboard protocol
// distinguishes them — which the outer terminal may or may not speak. So the
// list moves on arrows and on a bare `j`/`k`: nothing on that screen wants a
// plain letter, and the chords that would be safe there are the ones that are
// ambiguous. Found by driving the probe with 0x0A for "down" and watching it
// open a conversation instead.
export const isDown = (key: KeyEvent) => !key.ctrl && (key.name === "down" || key.name === "j");

export const isUp = (key: KeyEvent) => !key.ctrl && (key.name === "up" || key.name === "k");

export const isEnter = (key: KeyEvent) =>
  !key.ctrl && (key.name === "return" || key.name === "kpenter" || key.name === "linefeed");
