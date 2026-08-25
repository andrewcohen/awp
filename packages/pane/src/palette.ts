import type { ITheme } from "ghostty-web";

// The deck's palette states its chrome as ANSI 16 indices so the developer's
// terminal theme remaps them — that indirection is the point there, and it is
// why `internal/charm/palette.go` holds indices rather than hexes.
//
// A webview has no terminal to remap anything. So the pane has to state the hexes
// the working theme resolves those indices to, and this file is the one place it
// does: Catppuccin, the theme the TUI is developed against. Keeping the tables in
// the same order as the Go palette's is deliberate — a pane and a deck row
// showing the same status should not be two different greens.

export type ColorScheme = "light" | "dark";

export const macchiato = {
  // base, and not crust.
  //
  // This was #181926 — Catppuccin's crust, two steps below base — and being two
  // steps too dark is not a matter of taste. A program picks its own colours on
  // the assumption that the terminal's background is roughly where the theme
  // says it is: Claude Code paints a message block in #373737, which is a
  // subtle lift off #24273a and a stark slab off #181926. The palette being
  // wrong made a correct program look wrong.
  base: "#24273a",
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

// Latte is not Macchiato with the ends swapped, and the difference is worth
// stating because it reads as a mistake otherwise.
//
// Macchiato's ANSI black is surface1 — a dark grey that sits just above a dark
// background. The mirror of that on Latte would be surface1 again, #bcc0cc,
// which against Latte's near-white base is barely ink at all: dim text drawn in
// it would be unreadable rather than merely dim. Catppuccin's own terminal port
// resolves this by moving Latte's black up to subtext1 and its white down to
// surface2, so both ends keep their contrast against their own background. These
// are those hexes, not a recolouring of the table above.
export const latte = {
  base: "#eff1f5",
  text: "#4c4f69",
  black: "#5c5f77",
  red: "#d20f39",
  green: "#40a02b",
  yellow: "#df8e1d",
  blue: "#1e66f5",
  magenta: "#ea76cb",
  cyan: "#179299",
  white: "#acb0be",
  brightBlack: "#6c6f85",
  brightWhite: "#bcc0cc",
} as const;

// Spelled out rather than `typeof macchiato`, which would infer each field as
// its own hex literal and make Latte unassignable to it.
export type Palette = { readonly [K in keyof typeof macchiato]: string };

export const paletteFor = (scheme: ColorScheme): Palette => (scheme === "dark" ? macchiato : latte);

// The stylistic sets from the Ghostty config: cv01 for the @, cv04 for the
// cursive l. Measured to work through canvas — see below — but not applied,
// because the installed Maple Mono build already renders them: setting the tags
// on top of a build that has them baked in changes nothing at best, and pins
// two tags against the build's own defaults at worst.
//
// Applied as CSS on the canvas element rather than through the 2D context,
// which has no font-feature-settings — the spec's font shorthand does not carry
// features and there is no property for them. WebKit nonetheless honours the
// element's computed style when rasterising canvas text, which was measured
// rather than assumed: the same glyphs at the same size lay down measurably
// different ink with these on. An earlier claim here that features were
// unreachable through canvas was simply wrong.
export const paneFontFeatures = '"cv01" 1, "cv04" 1';

export const paneFontFamily = '"Maple Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

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

// The pane's theme, in ghostty-web's shape. Bright variants that Catppuccin does
// not distinguish are pointed at their normal counterparts rather than invented:
// a made-up bright red is a colour the terminal the deck is being compared
// against would never produce.
//
// Built once per scheme rather than per call, and setPaneTheme depends on that:
// it decides whether a theme is already applied by identity, so a fresh object
// every call would force a full repaint on every render.
const themeFrom = (p: Palette): ITheme =>
  ({
    background: p.base,
    foreground: p.text,
    cursor: p.text,
    black: p.black,
    red: p.red,
    green: p.green,
    yellow: p.yellow,
    blue: p.blue,
    magenta: p.magenta,
    cyan: p.cyan,
    white: p.white,
    brightBlack: p.brightBlack,
    brightRed: p.red,
    brightGreen: p.green,
    brightYellow: p.yellow,
    brightBlue: p.blue,
    brightMagenta: p.magenta,
    brightCyan: p.cyan,
    brightWhite: p.brightWhite,
  }) as const;

const themes: Record<ColorScheme, ITheme> = {
  dark: themeFrom(macchiato),
  light: themeFrom(latte),
};

export const paneThemeFor = (scheme: ColorScheme): ITheme => themes[scheme];

// The chrome around the pane is not this package's business.
//
// It was, briefly: a `Chrome` type and a `chromeFor(scheme)` beside the themes.
// The reason to remove it is the same reason the themes stay. A pane's sixteen
// slots are answerable to the upstream table, because a program picks its own
// colours assuming those exact values. The furniture around it answers to the
// application, and a package that a different application is meant to embed has
// no standing to say what that application's borders look like.
//
// It now lives in the renderer as StyleX variables —
// `apps/amoeba/src/renderer/tokens.stylex.ts` — which is also what lets a
// forced light or dark override exist at all.
