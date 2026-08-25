// What the pane is actually doing, in numbers.
//
// "Laggy" and "looks bad" are two complaints with at least four possible
// causes, and two rounds of guessing at them produced two wrong answers. This
// turns the guess into a reading: what the pointing device is emitting, how
// many reports that becomes, how much comes back, and how long a frame takes.
//
// Deliberately cheap. Counters and a ring of frame times, read on demand rather
// than pushed — a meter that costs something is measuring itself.

const FRAMES = 120;

export interface Meter {
  /** Wheel events seen from the device, and what they carried. */
  readonly wheelEvents: number;
  readonly lastDeltaY: number;
  readonly lastDeltaMode: number;
  /** Line reports sent to the program. */
  readonly linesSent: number;
  /** Writes into the emulator, and their total size. */
  readonly writes: number;
  readonly bytes: number;
  /** Milliseconds between renders, worst and typical, over the last frames. */
  readonly frameP50: number;
  readonly frameMax: number;
}

let wheelEvents = 0;
let lastDeltaY = 0;
let lastDeltaMode = 0;
let linesSent = 0;
let writes = 0;
let bytes = 0;

const frames = new Float64Array(FRAMES);
let frameAt = 0;
let lastFrame = 0;
let running = false;

/**
 * Frame intervals, sampled from the same clock the renderer runs on.
 *
 * A separate rAF loop rather than a hook into ghostty-web's: what matters is
 * whether the window is producing frames, and a loop that only ticks when the
 * renderer does would report a healthy rate while the page was locked up.
 */
const tick = (now: number): void => {
  if (lastFrame !== 0) {
    frames[frameAt] = now - lastFrame;
    frameAt = (frameAt + 1) % FRAMES;
  }
  lastFrame = now;
  requestAnimationFrame(tick);
};

/** Begin sampling. Safe to call more than once. */
export const startMeter = (): void => {
  if (running || typeof requestAnimationFrame !== "function") {
    return;
  }
  running = true;
  requestAnimationFrame(tick);
};

export const meterWheel = (deltaY: number, deltaMode: number, lines: number): void => {
  wheelEvents += 1;
  lastDeltaY = deltaY;
  lastDeltaMode = deltaMode;
  linesSent += lines;
};

export const meterWrite = (size: number): void => {
  writes += 1;
  bytes += size;
};

export const readMeter = (): Meter => {
  const sorted = [...frames].filter((ms) => ms > 0).toSorted((a, b) => a - b);
  return {
    wheelEvents,
    lastDeltaY,
    lastDeltaMode,
    linesSent,
    writes,
    bytes,
    frameP50: sorted[Math.floor(sorted.length / 2)] ?? 0,
    frameMax: sorted.at(-1) ?? 0,
  };
};

export const resetMeter = (): void => {
  wheelEvents = 0;
  linesSent = 0;
  writes = 0;
  bytes = 0;
  frames.fill(0);
  frameAt = 0;
};
