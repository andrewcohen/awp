// A pty to look at a TUI through.
//
// This process has no tty — it is an agent's stdout, a pipe — and opentui needs
// one: `stdout.columns` is where the renderer takes its width from. So the probe
// is the terminal. Bun.Terminal gives a real pty, the child sees a tty of a size
// this file chose, and what the child painted arrives here as bytes.
//
// The same shape as every other probe in this repo: assert on what the other
// process saw, not on what was handed to it.

export type Pty = {
  send: (text: string) => void;
  bytes: () => number;
  raw: () => string;
  /** What was painted, with the escape sequences taken out. */
  text: () => string;
  /** Does the painted text contain this, ignoring runs of whitespace? */
  saw: (needle: string) => boolean;
  resize: (cols: number, rows: number) => void;
  close: () => Promise<void>;
};

// Enough of an ANSI stripper to read a screen back. Not a VT parser: the text a
// renderer painted arrives in paint order, so this answers "did the word appear"
// and never "what is at row 4".
//
// The escape and the bell are built from their codepoints rather than typed:
// a control character in a source file is invisible in every tool that reads it.
const ESCAPE = String.fromCodePoint(27);
const BELL = String.fromCodePoint(7);

const patterns = [
  // OSC: ESC ] ... (BEL | ESC backslash)
  new RegExp(`${ESCAPE}\\][^${BELL}${ESCAPE}]*(?:${BELL}|${ESCAPE}\\\\)`, "gu"),
  // DCS/SOS/PM/APC: ESC (P|X|^|_) ... ESC backslash
  new RegExp(`${ESCAPE}[PX^_][\\S\\s]*?${ESCAPE}\\\\`, "gu"),
  // CSI: ESC [ params intermediates final
  new RegExp(`${ESCAPE}\\[[\\d;?<>= ]*[!-/]*[@-~]`, "gu"),
  // Anything else two-byte: ESC ( B, ESC =, ESC 7 …
  new RegExp(`${ESCAPE}[ -/]?[\\d@-~]`, "gu"),
];

export const strip = (s: string) => patterns.reduce((acc, re) => acc.replaceAll(re, ""), s);

export const openPty = (
  cmd: string[],
  options: { cols?: number; rows?: number; cwd?: string; env?: Record<string, string> } = {},
): Pty => {
  const cols = options.cols ?? 120;
  const rows = options.rows ?? 34;
  let out = "";
  let count = 0;

  const proc = Bun.spawn(cmd, {
    cwd: options.cwd ?? process.cwd(),
    env: {
      ...process.env,
      TERM: "xterm-256color",
      COLORTERM: "truecolor",
      // Present and empty, never absent — see AGENTS.md. bun's pty hands its
      // pairs to a Rust Command that inherits this process's environment, so a
      // key left out is a key left alone.
      ZMX_SESSION: "",
      ...options.env,
    },
    terminal: {
      cols,
      rows,
      data(_pty, data) {
        count += data.byteLength;
        out += new TextDecoder().decode(data);
      },
    },
  });

  return {
    send: (text) => proc.terminal?.write(text),
    bytes: () => count,
    raw: () => out,
    text: () => strip(out),
    saw: (needle) => strip(out).replaceAll(/\s+/gu, " ").includes(needle.replaceAll(/\s+/gu, " ")),
    resize: (c, r) => proc.terminal?.resize(c, r),
    close: async () => {
      proc.kill();
      proc.terminal?.close();
      await proc.exited;
    },
  };
};

export const wait = (ms: number) =>
  new Promise<void>((resolve) => {
    setTimeout(resolve, ms);
  });

/** Poll until `check` holds, so a probe waits on a condition and not a duration. */
export const until = async (
  label: string,
  check: () => boolean,
  timeoutMs = 8000,
): Promise<void> => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (check()) return;
    await wait(50);
  }
  throw new Error(`timed out waiting for ${label}`);
};

/**
 * Run until the byte counter stops moving, and report the volume and the time
 * between the first byte and the last.
 */
export const quiet = async (
  count: () => number,
  before: number,
  quietMs = 750,
): Promise<{ megabytes: number; elapsed: number }> => {
  let firstAt: number | null = null;
  let lastAt = performance.now();
  let last = before;
  for (;;) {
    await wait(50);
    const now = count();
    if (now !== last) {
      if (firstAt === null) firstAt = performance.now();
      lastAt = performance.now();
      last = now;
      continue;
    }
    if (firstAt !== null && performance.now() - lastAt > quietMs) break;
    if (firstAt === null && performance.now() - lastAt > 20_000)
      throw new Error("nothing ever arrived");
  }
  return { megabytes: (last - before) / 1024 / 1024, elapsed: (lastAt - firstAt!) / 1000 };
};
