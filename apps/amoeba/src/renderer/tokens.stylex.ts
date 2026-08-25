import * as stylex from "@stylexjs/stylex";

// The chrome's colours, as CSS variables the whole renderer draws from.
//
// ── why this file and not palette.ts ───────────────────────────────────────
// The pane's theme is not a choice. A terminal reproduces whatever bytes the
// program sends, so its sixteen slots have to be Catppuccin's exact hexes or a
// program that picks colours against them looks wrong — palette.ts owns that
// and answers to the upstream table. The chrome answers to nothing but this
// app, so it is stated here.
//
// ── why the default is light ───────────────────────────────────────────────
// `defineVars` takes a bare value plus overrides keyed by media query, and the
// bare value is what applies when nothing matches. Light being the default is
// therefore a spelling and not a preference. What it buys is that following the
// system needs no JavaScript at all: the variables flip before React has
// rendered, so there is no frame of the wrong theme on launch and no re-render
// when the system changes.
//
// An explicit choice cannot be spelled as a media query — see theme.ts.

const dark = "@media (prefers-color-scheme: dark)";

/**
 * The two palettes, named rather than numbered.
 *
 * `defineConsts` and not plain string constants: these are inlined at compile
 * time, which is what lets both `defineVars` below and `createTheme` in
 * theme.ts read the same hex without either one restating it. A hex written
 * twice is a hex that will disagree with itself.
 */
export const hue = stylex.defineConsts({
  // Catppuccin Latte. Mantle rather than base, so a surface can sit above it —
  // the pane draws its own base, which is the lighter one.
  latteBase: "#e6e9ef",
  latteText: "#4c4f69",
  latteMuted: "#8c8fa1",
  latteBorder: "#ccd0da",
  latteLive: "#40a02b",
  latteWarn: "#d20f39",

  // Catppuccin Macchiato, mantle for the same reason. The pane's base is
  // #24273a and sits above this.
  macchiatoBase: "#1e2030",
  macchiatoText: "#cad3f5",
  macchiatoMuted: "#5b6078",
  macchiatoBorder: "#363a4f",
  macchiatoLive: "#a6da95",
  macchiatoWarn: "#ed8796",
});

export const colors = stylex.defineVars({
  /** Behind everything. The pane sits on its own, lighter, base. */
  base: { default: hue.latteBase, [dark]: hue.macchiatoBase },
  /** Ordinary reading weight. */
  text: { default: hue.latteText, [dark]: hue.macchiatoText },
  /** Present but secondary — a reason, a subtitle, a disabled row. */
  muted: { default: hue.latteMuted, [dark]: hue.macchiatoMuted },
  /**
   * Rules, and the fill behind a selected row. The same colour on purpose: a
   * selection is a surface lifted off the base by exactly as much as a divider
   * is, so the two never disagree about how far that is.
   */
  border: { default: hue.latteBorder, [dark]: hue.macchiatoBorder },
  /** A session still running. Green in both, and not the same green. */
  live: { default: hue.latteLive, [dark]: hue.macchiatoLive },
  /** Something went wrong and is being said out loud. */
  warn: { default: hue.latteWarn, [dark]: hue.macchiatoWarn },
});

export const text = stylex.defineVars({
  // One family for the whole window. amoeba is a terminal with furniture around
  // it, and furniture in a different typeface reads as a different application.
  mono: "ui-monospace, SFMono-Regular, Menlo, monospace",
  body: "13px",
  small: "11px",
  tiny: "10px",
});

export const space = stylex.defineVars({
  // The window has no title bar of its own, so the traffic lights sit over the
  // top-left of the content. Every column's first row clears them.
  titlebar: "2.5rem",
  row: "0.35rem",
  gutter: "1rem",
});
