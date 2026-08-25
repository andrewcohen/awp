// A wheel notch, spelled for the program.
//
// ── who decides, and why it is not this file ───────────────────────────────
// An earlier version parsed private modes out of the byte stream to work out
// whether the program wanted the mouse. That was wrong twice over. The emulator
// already knows — `wasmTerm.hasMouseTracking()` and `buffer.active.type` are
// what it parsed itself — and a second parser can only ever see sequences that
// arrive after the pane attached, where a program sets its modes when it
// starts, long before anyone looks at it. Ask the terminal. See terminal.ts.
//
// What is left here is the arithmetic, which is pure and therefore testable
// without a canvas.

const ESC = "\u001B";

/** Wheel up and down, as the mouse protocol numbers them. */
export const WHEEL_UP = 64;
export const WHEEL_DOWN = 65;

/**
 * The most one event may deliver.
 *
 * A flick can carry a four-figure delta, and a program that redraws once per
 * report should not be handed hundreds of them in a single message.
 */
const MAX_LINES = 10;

/**
 * How many lines one wheel event asks for.
 *
 * At least one, always. Rounding alone would drop every small movement — a
 * trackpad reports a stream of two- and three-pixel events — and a surface that
 * ignores gentle movement reads as broken rather than as precise.
 *
 * `deltaMode` is the browser saying what unit `deltaY` is in, and all three
 * occur: pixels from a trackpad, lines from some mice, pages from a few.
 * Treating a line delta as pixels is the difference between scrolling one line
 * and scrolling one pixel.
 */
export const wheelLines = (
  event: { readonly deltaY: number; readonly deltaMode: number },
  cell: { readonly height: number; readonly rows: number },
): number => {
  const height = cell.height > 0 ? cell.height : 1;
  const pixels =
    event.deltaMode === 1
      ? event.deltaY * height
      : event.deltaMode === 2
        ? event.deltaY * height * cell.rows
        : event.deltaY;
  return Math.min(MAX_LINES, Math.max(1, Math.round(Math.abs(pixels) / height)));
};

/**
 * A run of wheel reports, as one string.
 *
 * SGR unconditionally. The original encoding puts each field in a single byte
 * offset by 32, so it cannot address a column past 223 and this pane is
 * regularly wider than that; and a program that turns mouse reporting on at all
 * understands SGR. Guessing the encoding from the stream would be the same
 * mistake as guessing the modes from it.
 *
 * One string rather than one call per line: each of these becomes an rpc
 * message, and the program reads the run in a single pass regardless.
 */
export const wheelReport = (button: number, column: number, row: number, lines: number): string =>
  `${ESC}[<${button};${column};${row}M`.repeat(Math.max(lines, 0));
