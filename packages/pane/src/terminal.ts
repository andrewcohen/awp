import { FitAddon, type ITheme, Terminal, init } from "ghostty-web";
import { paneFontSize } from "./palette";
import { WHEEL_DOWN, WHEEL_UP, wheelLines, wheelReport } from "./wheel";
import { meterWheel, meterWrite, startMeter } from "./meter";

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
    startMeter();
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    currentFont = options.fontFamily;
    currentTheme = options.theme;

    // The wheel, decided by asking the terminal rather than by guessing.
    //
    // ghostty-web's own handler goes straight from "alternate screen" to
    // synthesising arrow keys, which walks the program's input history instead
    // of scrolling it — it looks like the pane typing to itself. But an
    // alternate-screen program has no terminal scrollback to move through
    // either; what it has is mouse reporting, and a wheel notch is a mouse
    // event. That is how scrolling an agent works in Ghostty proper.
    //
    // Both questions are answered by the emulator, not by this file.
    // `buffer.active.type` and `hasMouseTracking()` are what it parsed itself,
    // where anything reading the byte stream here could only see modes set
    // after the pane attached — and a program sets its modes when it starts,
    // long before anyone looks at it.
    term.attachCustomWheelEventHandler((event: WheelEvent) => {
      // Normal screen: there is real scrollback, and ghostty-web moving through
      // it is the right behaviour. Returning false lets it.
      if (term?.buffer.active.type !== "alternate") {
        return false;
      }
      // Full-screen, and it never asked about the mouse. Nothing to scroll and
      // nothing that wants the event, so it is swallowed rather than turned
      // into keystrokes.
      if (term.wasmTerm?.hasMouseTracking() !== true) {
        return true;
      }
      sendWheel(event);
      return true;
    });

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

// sendWheel reports a wheel turn to the program, in the encoding it asked for.
//
// One event per notch rather than per pixel: a trackpad reports fine-grained
// deltas and a terminal program expects discrete clicks, so the distance is
// divided by the cell height. Capped at five, because a flick can carry a
// four-figure delta and a program that redraws per event should not be handed
// hundreds of them at once.
function sendWheel(event: WheelEvent): void {
  if (!term || !dataSink) {
    return;
  }
  const metrics = term.renderer?.getMetrics();
  const cellWidth = metrics?.width ?? 1;
  const cellHeight = metrics?.height ?? 1;

  const canvas = term.renderer?.getCanvas();
  const bounds = canvas?.getBoundingClientRect();
  // 1-based, as the protocol counts, and held inside the grid at both ends. A
  // report outside the terminal is a coordinate no program expects, and the
  // pane is often a fraction of a cell larger than the grid it draws.
  const column = clampCell((event.clientX - (bounds?.left ?? 0)) / cellWidth, term.cols);
  const row = clampCell((event.clientY - (bounds?.top ?? 0)) / cellHeight, term.rows);

  const button = event.deltaY > 0 ? WHEEL_DOWN : WHEEL_UP;
  const lines = wheelLines(event, { height: cellHeight, rows: term.rows });
  meterWheel(event.deltaY, event.deltaMode, lines);
  dataSink(wheelReport(button, column, row, lines));
}

const clampCell = (value: number, max: number): number =>
  Math.min(Math.max(Math.floor(value) + 1, 1), Math.max(max, 1));

// writePane puts the program's output on screen.
//
// One function rather than reaching for `term` directly, so a pane never holds
// the Terminal itself — which is what kept a stale handle alive in gdeck and
// interleaved one session's bytes into another's cells.
export function writePane(data: string): void {
  meterWrite(data.length);
  term?.write(data);
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
