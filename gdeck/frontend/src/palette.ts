import type { ITheme } from "ghostty-web";

// The deck's palette states its chrome as ANSI 16 indices so the developer's
// terminal theme remaps them — that indirection is the point there, and it is
// why `internal/charm/palette.go` holds indices rather than hexes.
//
// A webview has no terminal to remap anything. So gdeck has to state the hexes
// the working theme resolves those indices to, and this file is the one place it
// does: Catppuccin Macchiato, the theme the TUI is developed against. Keeping the
// two in the same order as the Go palette's table is deliberate — a pane and a
// deck row showing the same status should not be two different greens.
export const macchiato = {
  base: "#181926",
  text: "#cad3f5",
  black: "#494d64",
  red: "#ed8796",
  green: "#a6da95",
  yellow: "#eed49f",
  blue: "#8aadf4",
  magenta: "#f5bde6",
  cyan: "#8bd5ca",
  white: "#b8c0e0",
  brightBlack: "#5b6078",
  brightWhite: "#a5adcb",
} as const;

// The face a pane is drawn in, named once so the static pane and the live one
// cannot drift onto different fonts and be compared as if they were the same.
//
// Maple Mono is the developer's working face, and matching it is not cosmetic
// here: gdeck's whole claim is that a pane in a webview is the same pane, and a
// pane that renders in a different typeface than `awp deck` invites every
// difference to be blamed on the surface. The fallbacks are the platform's own
// monospace rather than a second choice of face — if Maple Mono is missing, a
// generic mono is an obvious substitution, where a near-miss is a confusing one.
// Stylistic sets are deliberately absent. Ghostty is configured here with
// `font-feature = +cv01, +cv04` and `font-thicken = true`, and none of the three
// can cross: ghostty-web draws glyphs through Canvas2D, whose font API is family,
// size, weight, stretch and letter-spacing — there is no font-feature-settings on
// a canvas context. Ghostty proper shapes the text itself, which is what buys it
// the choice. Matching those would mean a feature-frozen Maple Mono build
// installed under its own family name, which is a decision about what a face is
// rather than a setting, so it waits.
export const paneFontFamily = '"Maple Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

// The faces worth comparing, all already installed on this machine.
//
// A terminal font is a taste decision, and taste decisions do not survive being
// made from a constant: each guess costs an edit, and by the time the third one
// is on screen the first is a memory. They are listed so the choice can be made
// by looking at the same agent in each.
//
// Weighted toward faces that need no configuration, because that is the trait
// this renderer forces: Ghostty is set up here with `+cv01, +cv04`, which Canvas2D
// cannot enable, so Maple Mono renders with its personality switched off. A face
// whose defaults are already what you want does not have that problem.
export const paneFonts: { label: string; family: string; note: string }[] = [
  { label: "Maple Mono", family: '"Maple Mono", monospace', note: "no cv01/cv04 here" },
  { label: "CommitMono", family: '"CommitMono", monospace', note: "neutral, no sets needed" },
  { label: "Monaspace Neon", family: '"Monaspace Neon", monospace', note: "grotesque" },
  { label: "Monaspace Xenon", family: '"Monaspace Xenon", monospace', note: "slab" },
  { label: "JetBrains Mono", family: '"JetBrains Mono", monospace', note: "tall x-height" },
  { label: "Geist Mono", family: '"Geist Mono", monospace', note: "clean, stylised" },
  { label: "Fantasque Sans", family: '"Fantasque Sans Mono", monospace', note: "cursive by default" },
  { label: "SF Mono", family: "ui-monospace, monospace", note: "system default" },
];

// Not every size renders equally well, and it is worth knowing why before
// changing this one.
//
// ghostty-web sizes a cell as Math.ceil(measureText("M").width), once, so every
// cell is the same width and the only question is how much of it the glyph
// fills. Maple Mono advances 0.618 × size, which at 15px is 9.27 in a 10px cell
// — 7% of every cell is padding, and that is what read as loose columns and box
// rules that stop touching. Sizes differ a lot here: 18px advances 11.13 into
// 12, so 7% again, where 16px is 9.89 into 10 and only 1%.
//
// 18 is what was asked for and 7% looseness is legible, so this stays 18. If it
// reads loose, the neighbours are the thing to try: 16 and 19 are both ~1%.
export const paneFontSize = 18;

// The pane's theme, in ghostty-web's shape. Bright variants that Macchiato does
// not distinguish are pointed at their normal counterparts rather than invented:
// a made-up bright red is a colour the terminal the deck is being compared
// against would never produce.
export const paneTheme: ITheme = {
  background: macchiato.base,
  foreground: macchiato.text,
  cursor: macchiato.text,
  black: macchiato.black,
  red: macchiato.red,
  green: macchiato.green,
  yellow: macchiato.yellow,
  blue: macchiato.blue,
  magenta: macchiato.magenta,
  cyan: macchiato.cyan,
  white: macchiato.white,
  brightBlack: macchiato.brightBlack,
  brightRed: macchiato.red,
  brightGreen: macchiato.green,
  brightYellow: macchiato.yellow,
  brightBlue: macchiato.blue,
  brightMagenta: macchiato.magenta,
  brightCyan: macchiato.cyan,
  brightWhite: macchiato.brightWhite,
};
