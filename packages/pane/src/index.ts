// A terminal pane: libghostty compiled to wasm, rendered to canvas.
//
// One Terminal for the life of the window, reused by every pane, never
// disposed. ghostty-web's dispose() frees wasm state the module-level Ghostty
// instance keeps handing out, so building one per view is what caused four
// distinct bugs with a single cause — see terminal.ts. The canvas lives in a
// host element this package owns and re-parents on mount, so React can mount
// and unmount views without the terminal noticing.
//
// The renderer itself is patched: `patches/ghostty-web@0.4.0.patch`, applied by
// bun on install. Four fixes, each measured — row height from the font's line
// box rather than one glyph's ink, glyphs confined to their cell, stems
// thickened by a second offset fill rather than a stroke (which cost p50 734ms
// per keystroke in WKWebView), and block/shade/box-drawing characters drawn as
// snapped rectangles rather than taken from the font.

export {
  clearPaneSinks,
  ensurePaneTerminal,
  focusPane,
  mountPaneTerminal,
  paneReady,
  resetPane,
  setPaneFont,
  setPaneSinks,
  setPaneTheme,
  writePane,
  type PaneOptions,
  type PaneTerminal,
} from "./terminal";

export { WHEEL_DOWN, WHEEL_UP, wheelLines, wheelReport } from "./wheel";

export { readMeter, resetMeter, type Meter } from "./meter";

export {
  chromeFor,
  latte,
  macchiato,
  paletteFor,
  paneFontFamily,
  paneFontFeatures,
  paneFontSize,
  paneThemeFor,
  type Chrome,
  type ColorScheme,
  type Palette,
} from "./palette";
