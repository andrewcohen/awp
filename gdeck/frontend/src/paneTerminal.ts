import { Terminal, FitAddon } from "ghostty-web";
import { paneTheme, paneFontSize } from "./palette";

// One Terminal for the life of the window, reused by every pane.
//
// Building a fresh one per view was the single cause behind four different
// complaints, and the evidence is in gdeck's own log. ghostty-web's dispose()
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

// dataSink is where keystrokes go. Swapped per view rather than re-subscribed,
// because onData has no unsubscribe in the API and a stale handler would keep
// writing to a pane the user has left.
let dataSink: ((data: string) => void) | undefined;
let resizeSink: ((cols: number, rows: number) => void) | undefined;

export type PaneTerminal = {
  term: Terminal;
  fit: FitAddon;
};

// ensure builds the terminal on first use and returns it thereafter.
export function ensurePaneTerminal(fontFamily: string): PaneTerminal {
  if (!term || !host || !fit) {
    host = document.createElement("div");
    host.style.height = "100%";
    host.style.width = "100%";
    term = new Terminal({
      theme: paneTheme,
      fontFamily,
      fontSize: paneFontSize,
      scrollback: 10_000,
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    currentFont = fontFamily;

    // Registered once, for the terminal's whole life. The indirection through
    // the sinks is what makes that safe.
    term.onData((data: string) => dataSink?.(data));
    term.onResize(({ cols, rows }: { cols: number; rows: number }) => resizeSink?.(cols, rows));
  }
  return { term, fit };
}

// mountPaneTerminal moves the terminal into a view's container and fits it.
export function mountPaneTerminal(parent: HTMLElement, fontFamily: string): PaneTerminal {
  const pane = ensurePaneTerminal(fontFamily);
  if (host && host.parentElement !== parent) {
    parent.appendChild(host);
  }
  setPaneFont(fontFamily);
  pane.fit.fit();
  // Re-armed on every mount, not once at construction. observeResize watches
  // the element the terminal is currently in, and this one moves between views
  // — so an observer set up against a previous parent stops seeing the window
  // change shape, which is a terminal that renders at yesterday's size and
  // never reflows.
  pane.fit.observeResize();
  return pane;
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
