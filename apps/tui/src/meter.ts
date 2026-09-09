// What "feels fast" means, in numbers.
//
// Three questions, and they fail differently, so they are counted separately:
//
//   echo    a key was pressed → the first byte came back from the pty.
//           This is the shell's and zmx's round trip, and nothing this POC
//           does can make it smaller.
//   paint   a key was pressed → the cells that key produced were composed
//           into a frame. This is the one opentui owns.
//   through bytes per second out of the pty while something is spewing, and
//           the frame time while it does. A terminal that keeps up at 30fps
//           on `yes` is a different thing from one that stalls.
//
// Peaks are kept beside live figures because by the time a hand leaves the
// keyboard the live figure is zero — a reading only somebody fast enough to
// catch is not a reading. That habit is from the debug meter in amoeba.

const SAMPLES = 240;

const percentile = (values: number[], p: number): number => {
  if (values.length === 0) return 0;
  const sorted = values.toSorted((a, b) => a - b);
  const at = Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length));
  return sorted[at]!;
};

const push = (values: number[], value: number) => {
  values.push(value);
  if (values.length > SAMPLES) values.shift();
};

export class Meter {
  bytes = 0;
  reads = 0;
  echo: number[] = [];
  paint: number[] = [];
  bytesPerSecond = 0;
  peakBytesPerSecond = 0;
  peakFrameMs = 0;

  private keyAt: number | null = null;
  private echoAt: number | null = null;
  private windowBytes = 0;
  private windowStart = performance.now();

  /** A key was pressed while the terminal had focus. */
  keyed(): void {
    // Keep the oldest unanswered key rather than the newest: somebody holding a
    // key down would otherwise reset the clock and report a latency of nothing.
    if (this.keyAt === null) this.keyAt = performance.now();
  }

  /** Bytes arrived from the pty. */
  read(n: number): void {
    this.bytes += n;
    this.reads += 1;
    this.windowBytes += n;
    if (this.keyAt !== null && this.echoAt === null) {
      push(this.echo, performance.now() - this.keyAt);
      this.echoAt = performance.now();
    }
  }

  /** The terminal composed its cells into a frame. */
  painted(): void {
    if (this.keyAt === null || this.echoAt === null) return;
    push(this.paint, performance.now() - this.keyAt);
    this.keyAt = null;
    this.echoAt = null;
  }

  /** Called about four times a second, which is where the rate comes from. */
  tick(frameMs: number): void {
    const now = performance.now();
    const elapsed = now - this.windowStart;
    if (elapsed >= 250) {
      this.bytesPerSecond = (this.windowBytes / elapsed) * 1000;
      this.peakBytesPerSecond = Math.max(this.peakBytesPerSecond, this.bytesPerSecond);
      this.windowBytes = 0;
      this.windowStart = now;
    }
    this.peakFrameMs = Math.max(this.peakFrameMs, frameMs);
  }

  summary(): {
    echoP50: number;
    echoP95: number;
    paintP50: number;
    paintP95: number;
    samples: number;
  } {
    return {
      echoP50: percentile(this.echo, 50),
      echoP95: percentile(this.echo, 95),
      paintP50: percentile(this.paint, 50),
      paintP95: percentile(this.paint, 95),
      samples: this.paint.length,
    };
  }
}

export const rate = (bytesPerSecond: number): string => {
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / 1024 / 1024).toFixed(1)}MB/s`;
  if (bytesPerSecond >= 1024) return `${(bytesPerSecond / 1024).toFixed(0)}KB/s`;
  return `${bytesPerSecond.toFixed(0)}B/s`;
};

export const ms = (value: number): string => (value >= 10 ? value.toFixed(0) : value.toFixed(1));
