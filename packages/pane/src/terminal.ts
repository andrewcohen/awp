import { installClipboard } from "./clipboard";
import { installDictation } from "./dictation";
import { installKeys } from "./keys";
import { FitAddon, type ITheme, Terminal, init } from "ghostty-web";
import { paneFontSize } from "./palette";
import { WHEEL_DOWN, WHEEL_UP, wheelLines, wheelReport } from "./wheel";
import { meterSent, meterWheel, meterWrite, startMeter } from "./meter";

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

    installClipboard(host, () => term);
    installKeys(host, (data) => dataSink?.(data));
    // Dictation, and anything else that inserts text without a keystroke. See
    // dictation.ts: the emulator cancels those before reading them, so this has
    // to get there first.
    installDictation(host, (data) => dataSink?.(data));

    // Registered once, for the terminal's whole life. The indirection through
    // the sinks is what makes that safe.
    term.onData((data: string) => {
      // Counted here rather than in each installer, because this is the one
      // path every keystroke takes. `installDictation` counts its own, on the
      // other side of the split the meter exists to show.
      meterSent(data, true);
      dataSink?.(data);
    });
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

// ── the retained stream, and what it is for ────────────────────────────────
//
// The pane keeps what it has been sent, because a theme change has to rebuild
// the emulator and there is nowhere else the screen could come back from.
//
// ghostty-web 0.4.0 compiles the colours into the wasm terminal when it is
// built — `buildWasmConfig` hands it `fgColor`, `bgColor`, `cursorColor` and
// the sixteen-colour palette — and its own option handler admits the
// consequence:
//
//   case "theme":
//     console.warn("theme changes after open() are not yet fully supported");
//
// The cells the emulator hands back carry **resolved RGB** — `fg_r/g/b`,
// `bg_r/g/b` — not palette indexes, so the renderer cannot re-map them. That
// is the finding that decides the whole design: no amount of repainting on
// this side can recolour a cell the program asked for by number, because the
// number was resolved against a palette baked in at creation.
//
// `reset()` does rebuild the config. What it costs is the scrollback, and for
// a pane watching an agent work that used to be the worse of the two evils.
// It stops being a cost once the bytes are still here to write back.
const REPLAY_CAP = 4 * 1024 * 1024;
let replay = "";

/**
 * The last point in a chunk after which everything before it stops mattering.
 *
 * A truncated byte stream is the hazard replay has, and it is a real one: cut
 * in the middle of an escape sequence and the replay paints the wrong colours
 * from there on. These four sequences are the places a terminal says "forget
 * the screen", which makes them the only cut points that are certainly safe:
 *
 *   ESC [ 2J · ESC [ 3J   erase the screen, and the scrollback
 *   ESC c                 a full reset
 *   ESC [ ? 1049 h/l      enter or leave the alternate screen
 *
 * The last one is why this is worth doing rather than keeping a fixed tail: an
 * agent's TUI lives in the alternate screen, so its entry is a perfect restart
 * point and everything before it is a shell prompt nobody needs back.
 */
// eslint-disable-next-line no-control-regex
const RESTART = /\u001B\[[23]J|\u001Bc|\u001B\[\?1049[hl]/gu;

const lastRestart = (data: string): number => {
  RESTART.lastIndex = 0;
  let at = -1;
  for (let found = RESTART.exec(data); found !== null; found = RESTART.exec(data)) {
    at = found.index;
  }
  return at;
};

/** Remember a chunk, discarding what a restart or the cap makes irrelevant. */
const remember = (data: string): void => {
  replay += data;
  const at = lastRestart(data);
  if (at >= 0) {
    replay = replay.slice(replay.length - data.length + at);
    return;
  }
  if (replay.length > REPLAY_CAP) {
    // No restart to cut at, so cut after a newline — the next best boundary,
    // and the one a line-oriented program never straddles.
    const from = replay.length - REPLAY_CAP;
    const cut = replay.indexOf("\n", from);
    replay = replay.slice(cut < 0 ? from : cut + 1);
  }
};

/** Exposed for the probe, which has no other way to see what would be replayed. */
export const replayLength = (): number => replay.length;

// setPaneTheme recolours the terminal, and it costs a rebuild.
//
// The order matters and each step is doing something the others cannot:
//
//   options.theme = …   what `buildWasmConfig` reads. Warns, and still sets.
//   reset()             frees the wasm terminal and makes one with the new
//                       palette. This is the only thing that recolours a cell
//                       the program asked for by number.
//   renderer.setTheme   the other half — the ground, the cursor, selection
//   write(replay)       puts the screen back. Without it `reset` is what it
//                       always was: the right colours on an empty terminal.
//   clear() + forceAll  a line only paints the cells it has, so the ground
//                       behind a blank row keeps the last theme's colour
//                       until something fills it.
//
// **The nudge this replaced never did anything.** Setting `canvas.width = 0`
// to put the canvas' pixel size in disagreement with the renderer's metrics
// reads in the library's source like the one full redraw reachable from public
// API. It is not one, the canvas returns to its own size, and not one pixel
// changed. That was written into this file as a finding without a pixel ever
// being sampled — a mechanism read out of someone else's source is a
// hypothesis.
//
// **A single corner pixel is the wrong probe**, and cost an hour twice: the
// fixture draws ramps and blocks, so the corner is whatever it painted there
// rather than the theme's ground, and it reads "unchanged" for a swap that
// worked and one that did nothing alike. Count the canvas' most common
// colours instead.
export function setPaneTheme(theme: ITheme): void {
  if (!term || theme === currentTheme) {
    return;
  }
  currentTheme = theme;
  term.options.theme = theme;
  term.reset();
  term.renderer?.setTheme(theme);
  if (replay !== "") {
    term.write(replay);
  }
  term.renderer?.clear();
  if (term.wasmTerm) {
    term.renderer?.render(term.wasmTerm, true, term.viewportY, term);
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
  const lines = wheelLines(event, { rows: term.rows });
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
  remember(data);
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
