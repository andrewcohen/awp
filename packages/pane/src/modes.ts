// Which private modes the program has turned on.
//
// ghostty-web does not track these, so it cannot know what a program wants from
// a wheel. Its fallback is to send arrow keys whenever the alternate screen is
// up, which is wrong for anything that asked for mouse reporting: a scroll
// arrives as cursor movement, and in an agent TUI that means the selection
// moves instead of the view.
//
// Everything needed to answer properly is already in the byte stream the pane
// receives, so this reads it on the way past. Pure, and therefore testable
// without a terminal — which the wheel handling itself is not.

const ESC = "\u001B";

// Private mode sets and resets: `CSI ? Ps [; Ps ...] h` or `... l`.
//
// The control character is the point of the pattern, so no-control-regex is off
// for this line rather than worked around — an escape sequence that did not
// start with ESC would match ordinary prose.
// oxlint-disable-next-line no-control-regex
const DECSET = /\u001B\[\?([\d;]+)([hl])/gu;

export interface Modes {
  /**
   * The program is on the alternate screen — a full-screen application rather
   * than a shell with scrollback. 1049 is the modern one; 47 and 1047 are the
   * older spellings and still appear.
   */
  readonly alternateScreen: boolean;
  /**
   * The program asked to be told about the mouse. 1000 is click, 1002 adds
   * drag, 1003 adds every motion; any of them means wheel events are wanted.
   */
  readonly mouseTracking: boolean;
  /**
   * Mouse events should be encoded as SGR (1006) rather than the original
   * scheme. It matters beyond tidiness: the old encoding adds 32 to each
   * coordinate and puts it in a single byte, so it cannot address a column past
   * 223 — and this pane is regularly wider than that.
   */
  readonly sgrMouse: boolean;
}

export const initialModes: Modes = {
  alternateScreen: false,
  mouseTracking: false,
  sgrMouse: false,
};

const ALTERNATE = new Set([47, 1047, 1049]);
const TRACKING = new Set([1000, 1002, 1003]);

/**
 * The modes after `chunk`, given the modes before it.
 *
 * A chunk boundary can fall inside an escape sequence, and this will miss one
 * that does. Deliberate rather than overlooked: carrying a partial sequence
 * between calls means holding parser state, and the cost of being wrong here is
 * one wheel gesture behaving as it did a moment ago — where the cost of a
 * stateful parser drifting out of step with the terminal's own is a pane that
 * disagrees with itself for as long as it stays open. These sequences also
 * arrive at the head of an application's output, which is where a chunk
 * boundary is least likely to fall.
 */
export const applyModes = (modes: Modes, chunk: string): Modes => {
  let { alternateScreen, mouseTracking, sgrMouse } = modes;

  DECSET.lastIndex = 0;
  let match = DECSET.exec(chunk);
  while (match !== null) {
    const on = match[2] === "h";
    for (const raw of (match[1] ?? "").split(";")) {
      const mode = Number(raw);
      if (ALTERNATE.has(mode)) {
        alternateScreen = on;
      } else if (TRACKING.has(mode)) {
        mouseTracking = on;
      } else if (mode === 1006) {
        sgrMouse = on;
      }
    }
    match = DECSET.exec(chunk);
  }

  return { alternateScreen, mouseTracking, sgrMouse };
};

/** Wheel up and down, as the mouse protocol numbers them. */
export const WHEEL_UP = 64;
export const WHEEL_DOWN = 65;

/**
 * A wheel event, encoded for the program.
 *
 * Columns and rows are 1-based, as the protocol counts them; a caller working
 * from canvas cells has 0-based ones and must add one.
 */
// The original encoding puts each field in one byte, offset by 32. Anything
// past column 223 cannot be expressed, so it is clamped rather than allowed to
// wrap — a wheel reported at the edge is wrong in a way a reader can see, where
// one that wrapped is wrong in a way that looks deliberate.
const offset = (value: number) => Math.min(Math.max(value, 1), 223) + 32;

export const encodeWheel = (modes: Modes, button: number, column: number, row: number): string => {
  if (modes.sgrMouse) {
    return `${ESC}[<${button};${column};${row}M`;
  }
  // fromCharCode and not fromCodePoint, despite the lint. This encoding is
  // bytes, not characters — each field is one byte and the values never exceed
  // 255 — and code points are the wrong unit to build it out of.
  return `${ESC}[M${String.fromCharCode(button + 32, offset(column), offset(row))}`;
};

// ── turning a wheel event into lines ───────────────────────────────────────
//
// Not one event, one answer. A trackpad reports a stream of small pixel deltas
// — a gentle drag is dozens of events of two or three pixels each — so
// rounding each one on its own and taking at least a line means the lightest
// touch scrolls at sixty lines a second. Rounding without a floor is no better:
// every one of those events rounds to zero and the surface does nothing at all.
//
// So the remainder is kept. Deltas accumulate until they add up to a line, that
// line is emitted, and what is left over stays for the next event. Gentle
// movement scrolls slowly, a flick scrolls far, and the distance travelled
// matches the distance moved — which is the whole of what "feels right" means
// here.

/** How much scrolling has been asked for but not yet delivered, in pixels. */
export interface WheelCarry {
  readonly pixels: number;
}

export const noCarry: WheelCarry = { pixels: 0 };

/**
 * The most one event may deliver, in screens.
 *
 * Not a guard on the carry — that is bounded by construction, since what is
 * kept is always less than a single cell. This bounds one absurd delta: a
 * hundred thousand pixels would otherwise become five thousand line reports in
 * one message, and no program benefits from being told to scroll further than
 * its own scrollback.
 *
 * Three screens rather than one, because a page-mode delta is a whole screen by
 * definition and capping at a screen would quietly break it.
 */
const MAX_SCREENS_PER_EVENT = 3;

/**
 * The lines to scroll, and what to remember for next time.
 *
 * `deltaMode` is the browser saying what unit `deltaY` is in, and all three
 * occur in practice: pixels from a trackpad, lines from some mice, pages from a
 * few. Treating a line delta as pixels is the difference between scrolling one
 * line and scrolling one pixel.
 */
export const wheelLines = (
  carry: WheelCarry,
  event: { readonly deltaY: number; readonly deltaMode: number },
  cell: { readonly height: number; readonly rows: number },
): { readonly lines: number; readonly carry: WheelCarry } => {
  const height = cell.height > 0 ? cell.height : 1;
  const pixels =
    event.deltaMode === 1
      ? event.deltaY * height
      : event.deltaMode === 2
        ? event.deltaY * height * cell.rows
        : event.deltaY;

  const total = carry.pixels + pixels;
  // Truncated, not rounded: rounding would deliver a line before a line's worth
  // of movement had happened, which reads as the surface running ahead of the
  // hand.
  const exact = Math.trunc(total / height);
  const limit = Math.max(cell.rows, 1) * MAX_SCREENS_PER_EVENT;
  const lines = Math.max(Math.min(exact, limit), -limit);

  // The remainder from the *exact* figure, not the capped one. Carrying the
  // difference would bank thousands of lines from a single flick and pay them
  // out over the following events, so the pane would keep moving long after the
  // hand stopped.
  return { lines, carry: { pixels: total - exact * height } };
};
