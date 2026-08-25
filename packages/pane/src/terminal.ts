import { FitAddon, type ITheme, Terminal, init } from "ghostty-web";
import { paneFontSize } from "./palette";

// One Terminal for the life of the window, reused by every pane.
//
// Building a fresh one per view was the single cause behind four different
// complaints, and the evidence is in the log of the tree this came from. ghostty-web's dispose()
// frees wasm state the module-level Ghostty instance keeps handing out, so:
//
//   - a second Terminal writes into freed memory and dies with "Out of bounds
//     memory access" (this is what React StrictMode's double-mount hit);
//   - a recycled handle receives another pane's bytes, which is how a *static*
//     pane with no process behind it ended up with a live agent's output
//     interleaved into its cells — the "overlap" that looked like a drawing bug
//     and was really two terminals sharing one buffer;
//   - and every switch re-allocated a 10,000-line scrollback and re-measured the
//     font, which is where a "slow attach" went when Go's own timings showed the
//     whole attach costing 25ms.
//
// Never disposing is not a leak worth worrying about — it is one terminal — and
// it sidesteps the entire lifecycle. The font picker and session switching go
// through the public setters instead, which is what they are for.
//
// The canvas lives in a host element this module owns, so a component "mounting"
// the pane re-parents that element rather than building a new one. React can
// then mount and unmount views as it likes without the terminal noticing.

let term: Terminal | undefined;
let fit: FitAddon | undefined;
let host: HTMLDivElement | undefined;
let currentFont = "";
let currentTheme: ITheme | undefined;

// ghostty-web compiles its wasm before a Terminal can exist — constructing one
// first throws "ghostty-web not initialized".
//
// Started at import rather than on first mount, and kept as one promise for the
// module's life. The load is the expensive part (35ms, measured through
// WKWebView, where the wasm arrives as an inline base64 data: URL rather than
// over the custom asset scheme), and every caller awaiting the same promise
// means it happens once no matter how many panes mount at once.
const ready: Promise<void> = init();

/** Resolves when the emulator can be used. Await before mounting. */
export const paneReady = (): Promise<void> => ready;

// dataSink is where keystrokes go. Swapped per view rather than re-subscribed,
// because onData has no unsubscribe in the API and a stale handler would keep
// writing to a pane the user has left.
let dataSink: ((data: string) => void) | undefined;
let resizeSink: ((cols: number, rows: number) => void) | undefined;

export type PaneTerminal = {
  term: Terminal;
  fit: FitAddon;
};

export type PaneOptions = {
  readonly fontFamily: string;
  readonly theme: ITheme;
};

// ensure builds the terminal on first use and returns it thereafter.
export function ensurePaneTerminal(options: PaneOptions): PaneTerminal {
  if (!term || !host || !fit) {
    host = document.createElement("div");
    host.style.height = "100%";
    host.style.width = "100%";
    term = new Terminal({
      theme: options.theme,
      fontFamily: options.fontFamily,
      fontSize: paneFontSize,
      scrollback: 10_000,
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    currentFont = options.fontFamily;
    currentTheme = options.theme;

    // Registered once, for the terminal's whole life. The indirection through
    // the sinks is what makes that safe.
    term.onData((data: string) => dataSink?.(data));
    term.onResize(({ cols, rows }: { cols: number; rows: number }) => resizeSink?.(cols, rows));
  }
  return { term, fit };
}

// mountPaneTerminal moves the terminal into a view's container and fits it.
export function mountPaneTerminal(parent: HTMLElement, options: PaneOptions): PaneTerminal {
  const pane = ensurePaneTerminal(options);
  if (host && host.parentElement !== parent) {
    parent.appendChild(host);
  }
  setPaneFont(options.fontFamily);
  setPaneTheme(options.theme);
  pane.fit.fit();
  // Re-armed on every mount, not once at construction. observeResize watches
  // the element the terminal is currently in, and this one moves between views
  // — so an observer set up against a previous parent stops seeing the window
  // change shape, which is a terminal that renders at yesterday's size and
  // never reflows.
  pane.fit.observeResize();
  return pane;
}

// setPaneTheme recolours in place, so the window can follow the system's
// light/dark preference without rebuilding the terminal.
//
// Through the renderer's setter for the same reason setPaneFont is: constructing
// a second Terminal is the one operation that corrupts wasm state.
//
// The nudge afterwards is not superstition. `setTheme` updates the renderer's
// theme and palette and returns — it repaints nothing. The render loop runs
// every frame but asks the buffer which rows are dirty, and recolouring marks
// none of them, so a swapped theme would sit invisible until the next write.
// The only full redraw reachable from public API is the one `render` triggers
// itself when the canvas' pixel dimensions disagree with the renderer's metrics:
// it resizes and forces the frame. So the canvas is put into disagreement
// deliberately. Recovery is the next animation frame.
export function setPaneTheme(theme: ITheme): void {
  if (!term || theme === currentTheme) {
    return;
  }
  currentTheme = theme;
  term.renderer?.setTheme(theme);
  const canvas = term.renderer?.getCanvas();
  if (canvas) {
    canvas.width = 0;
  }
}

// setPaneFont changes the face without rebuilding anything. setFontFamily and
// remeasureFont are public API precisely so this does not require a new
// Terminal — which is the operation that corrupts state.
export function setPaneFont(fontFamily: string): void {
  if (!term || fontFamily === currentFont) {
    return;
  }
  currentFont = fontFamily;
  term.renderer?.setFontFamily(fontFamily);
  term.renderer?.setFontSize(paneFontSize);
  term.renderer?.remeasureFont();
  fit?.fit();
}

// resetPane clears the screen and scrollback between panes, so one session's
// output never appears above another's.
export function resetPane(): void {
  term?.clear();
}

export function setPaneSinks(
  onData: (data: string) => void,
  onResize: (cols: number, rows: number) => void,
): void {
  dataSink = onData;
  resizeSink = onResize;
}

// focusPane puts the keyboard back in the terminal.
//
// Needed on a tab switch as well as on mount: the terminal never unmounts, so
// coming back from the chat runs no mount effect, and the keyboard is still
// wherever the click left it.
export function focusPane(): void {
  term?.focus();
}

export function clearPaneSinks(): void {
  dataSink = undefined;
  resizeSink = undefined;
}
