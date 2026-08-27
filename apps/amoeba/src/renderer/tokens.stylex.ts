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
  // Catppuccin peach. The accent, and the only hue in this table chosen for
  // what it means rather than for a role it already had — see `colors.accent`.
  latteAccent: "#fe640b",
  // The states an agent can be in. Yellow for waiting because it is the one a
  // person has to act on and yellow is what the eye finds first; blue for
  // ready because it is present without being urgent.
  latteWaiting: "#df8e1d",
  latteReady: "#1e66f5",

  // Catppuccin Macchiato, mantle for the same reason. The pane's base is
  // #24273a and sits above this.
  macchiatoBase: "#1e2030",
  macchiatoText: "#cad3f5",
  macchiatoMuted: "#5b6078",
  macchiatoBorder: "#363a4f",
  macchiatoLive: "#a6da95",
  macchiatoWarn: "#ed8796",
  macchiatoAccent: "#f5a97f",
  macchiatoWaiting: "#eed49f",
  macchiatoReady: "#8aadf4",
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
  /**
   * The one hue that means "this, here" — a selected row, a focused column, the
   * button a dialog is about.
   *
   * There was no accent at all until this, and its absence is why the window
   * reads flat: the only colours in it were green for alive and red for broken,
   * so everything that was neither — which is nearly everything — was a shade of
   * grey. `warn` was doing this job by accident wherever something needed to
   * stand out, which spends the failure colour on things that have not failed.
   *
   * Catppuccin's peach in both flavours, and picked from that table rather than
   * freely: the pane's sixteen slots are Catppuccin's exact hexes, so an accent
   * already in the palette is one the terminal and the window share. See #21.
   */
  accent: { default: hue.latteAccent, [dark]: hue.macchiatoAccent },
  /**
   * An agent stopped to ask something.
   *
   * Its own token rather than `warn`, because a question is not a failure and a
   * strip that draws them alike teaches the eye to ignore both.
   */
  waiting: { default: hue.latteWaiting, [dark]: hue.macchiatoWaiting },
  /** An agent finished and has not been read. */
  ready: { default: hue.latteReady, [dark]: hue.macchiatoReady },
});

export const text = stylex.defineVars({
  // One family for the whole window. amoeba is a terminal with furniture around
  // it, and furniture in a different typeface reads as a different application.
  mono: "ui-monospace, SFMono-Regular, Menlo, monospace",

  // ── the scale, and why it moved ──────────────────────────────────────────
  // These were 13/11/10, which came from the Go deck — where a row *is* a
  // terminal line and the whole strip is drawn in the terminal's own font at
  // the terminal's own size. In a window there is no such constraint and the
  // sizes read as cramped, which is the note this scale exists to answer.
  //
  // Four steps rather than three. `body` and `small` were doing the work of
  // four roles between them, so a chip and a heading were the same size as a
  // caption; `lead` gives the thing a screen is *about* somewhere to go.
  //
  // **Change them here and nowhere else.** Every rule in the renderer reads
  // these variables — a literal px anywhere is a size that will not move with
  // the rest, and is the reason this file exists rather than a constant per
  // component.
  lead: "16px",
  body: "15px",
  small: "13px",
  tiny: "12px",
});

export const space = stylex.defineVars({
  // The window has no title bar of its own, so the traffic lights sit over the
  // top-left of the content. Every column's first row clears them.
  titlebar: "2.5rem",
  /**
   * The band macOS draws its window controls in, on a `hiddenInset` window.
   *
   * A fact about the platform rather than a decision — the close, minimise and
   * zoom buttons are placed by AppKit and nothing here can move them. It is a
   * height and not the inline offset it replaced: the bar used to start 5.25rem
   * in to get out of their way sideways, which left our own control six pixels
   * from theirs and reading as a fourth one. Going *under* them instead gives
   * the leftmost control the window's real left edge.
   */
  lights: "1.75rem",
  /**
   * How far in the window controls reach, on a `hiddenInset` window.
   *
   * Used as padding on *both* sides of the title band, which is what makes the
   * title centre in the window rather than in the space left over — and what
   * stops it ever reaching the buttons in a narrow window.
   */
  lightsInline: "5.25rem",
  row: "0.35rem",
  gutter: "1rem",
});
