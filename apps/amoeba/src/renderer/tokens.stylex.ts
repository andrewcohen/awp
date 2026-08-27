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
// ── Latte's accents are not text colours, and this is measured ─────────────
//
// Catppuccin's Latte palette is tuned to be *an accent* on a light surface, not
// ink on one. Taking its hexes at face value gave a window where nothing but
// the body text was legible — every ratio below is against the base above it:
//
//   muted    #8c8fa1   2.63      accent   #fe640b   2.45
//   live     #40a02b   2.75      waiting  #df8e1d   2.15
//
// 4.5 is the threshold for text and 3.0 for a mark, so those fail both. That
// is the whole of "everything is grey and low contrast and hard to see": it was
// not a shortage of colour, it was colour nobody could see.
//
// So the chrome's Latte hues are Catppuccin's, **darkened along their own hue
// and saturation until each clears 4.6** — the smallest change that fixes it,
// rather than a different palette. Macchiato needed none of this: every one of
// its hues is between 6.5 and 11 against its base already, which is the
// asymmetry a dark-first palette has and nobody notices until they look.
//
// This is allowed here and would not be in palette.ts. The pane's sixteen slots
// have to be upstream's exact hexes or a program choosing colours against them
// looks wrong; the chrome answers to nothing but this app.
export const hue = stylex.defineConsts({
  // Catppuccin Latte. Mantle rather than base, so a surface can sit above it —
  // the pane draws its own base, which is the lighter one.
  latteBase: "#e6e9ef",
  latteSurface: "#eff1f5",
  latteRaised: "#dce0e8",
  latteText: "#4c4f69",
  latteMuted: "#63677a",
  latteBorder: "#ccd0da",
  latteLive: "#2f7620",
  latteWarn: "#ce0f38",
  // Peach, darkened. The accent, and the only hue here chosen for what it means
  // rather than for a role it already had — see `colors.accent`.
  latteAccent: "#b44201",
  // The states an agent can be in. Yellow for waiting because it is the one a
  // person has to act on and yellow is what the eye finds first; blue for
  // ready because it is present without being urgent.
  latteWaiting: "#915c13",
  latteReady: "#0c59f4",

  // Catppuccin Macchiato, mantle for the same reason. The pane's base is
  // #24273a and sits above this.
  macchiatoBase: "#1e2030",
  macchiatoSurface: "#24273a",
  macchiatoRaised: "#363a4f",
  macchiatoText: "#cad3f5",
  // Overlay1 rather than surface2. The old value was 2.60 against the base,
  // which is below the mark threshold let alone the text one — and this is the
  // colour every second line in the window is drawn in.
  macchiatoMuted: "#8087a2",
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
  /**
   * One step off the base, for something that sits on top of it — a panel, a
   * dialog, an input.
   *
   * There were two levels in this window and there needed to be three. With
   * only base and border, anything raised had to borrow the *rule* colour for
   * its fill, so a dialog and a divider were the same value and nothing could
   * be lifted without also looking like a line.
   */
  surface: { default: hue.latteSurface, [dark]: hue.macchiatoSurface },
  /** A row under the pointer, or a control pressed in. */
  raised: { default: hue.latteRaised, [dark]: hue.macchiatoRaised },
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
  // ── two families, and the line is address versus prose ──────────────────
  //
  //   mono   a slug, a bookmark, a revision, a path, a command, the pane
  //   ui     a title, a label, a heading, a count, a sentence, a button
  //
  // Not "chrome versus pane". A slug is something somebody will type somewhere
  // else and the monospace is what says so — the same reason a URL is
  // monospace in a browser's devtools and not in its bookmarks bar.
  //
  // ── how these two were chosen, since two others were not ─────────────────
  //
  // Every proportional face installed on this machine is monospace; the only
  // real candidates are the system's own. Rendered side by side at the sizes
  // this window uses, SF Pro is the tightest and the most legible small, and
  // the width matters — the sidebar's caption line lives in a 260px column,
  // and JetBrains Mono spends about a third more of it on the same words.
  //
  //   system-ui / -apple-system / 'SF Pro Text'   the same face, all resolve
  //   'Helvetica Neue'                            resolves, wider, older
  //   Inter                                       NOT INSTALLED — would need
  //                                               bundling to have any effect
  //   'New York'                                  NEVER RESOLVES, see below
  //
  // **A font stack that misses fails in silence**, which is the same shape as
  // the React Compiler that was not running and the worker pool that had no
  // workers. `/System/Library/Fonts/NewYork.ttf` is on the disk and the family
  // name does not resolve in the web view, so every rule naming it fell
  // through to Georgia while reading as applied.
  //
  // Measured by rendering a string in each family and comparing widths against
  // a family nobody has — do this rather than trusting
  // `getComputedStyle().fontFamily`, which echoes the declaration back whether
  // or not anything in it exists:
  //
  //   'NoSuchFaceAnywhere'   371.05    the control
  //   'New York'             371.05    ← identical: never found
  //   Georgia                398.81
  //   'JetBrains Mono'       528.00
  //
  // Canvas is no good for this. `measureText` reported every family as the
  // same width including ones that certainly exist, so it was measuring its
  // own fallback. Use a real element.
  mono: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  ui: "system-ui, -apple-system, 'SF Pro Text', 'Helvetica Neue', sans-serif",

  // ── the scale, and its floor ─────────────────────────────────────────────
  //
  // **Nothing below 14px.** That is a stated requirement and not a preference —
  // it came back as "stop using such tiny fonts in headers my eyesight isnt
  // amazing" — and it is the constraint the rest of the scale is built around
  // rather than a minimum applied afterwards.
  //
  // What it costs is a step. The first version was 15/14/13/12/11, which put
  // four of its five sizes under the floor; raising them collapses `small` and
  // `tiny` onto the same number, so there is no `tiny` any more. Four steps
  // that are all readable beat five where the bottom two are not.
  //
  // A caption is therefore separated from the text above it by weight and
  // colour rather than by size, which is the better separation anyway: size is
  // the one axis that trades legibility for hierarchy, and this window has no
  // legibility to spare.
  //
  // **Change them here and nowhere else.** Every rule in the renderer reads
  // these variables; a literal px anywhere is a size that will not move with
  // the rest, and is the reason this file exists rather than a constant per
  // component.
  // The floor applies to **text**, and not to a mark drawn with a glyph. A
  // status bullet is sized against the name beside it and a 14px bullet is a
  // blob; an icon's `font-size` is its em box and not something anybody reads.
  // Checked rather than assumed — everything in the window under 14px is one
  // of those two:
  //
  //   11px  <button><svg>   an icon button
  //   10px  <span>●         the status dot
  //
  // If a *word* ever turns up in that list, it is a bug.
  title: "1.125rem",
  lead: "1rem",
  body: "0.9375rem",
  small: "0.875rem",

  // ── weight, which the window had none of ─────────────────────────────────
  // One weight everywhere is the other half of why it read flat: a name and
  // the caption under it were the same size *and* the same weight, so the only
  // thing separating them was a colour that was itself failing contrast.
  //
  // Three, and no more. `medium` is what a row's name wears — enough to lead
  // the caption without shouting — and `strong` is for the one thing on a
  // screen that is the screen's subject.
  regular: "400",
  medium: "500",
  strong: "600",
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
