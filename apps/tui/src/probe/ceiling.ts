#!/usr/bin/env bun
// Where the bytes actually stop, and it is not the emulator.
//
// The drive probe reported the same 40MB drain as 1.2s once and 11.4s the next,
// with the frame time flat at a third of a millisecond in both — so whatever
// the variance was, it was not the renderer. This runs the identical drain with
// the rendering taken out: the same `zmx attach`, into a pty that counts bytes
// and throws them away.
//
//   render  the TUI: pty → EmbeddedTerminalRenderable → a frame
//   drain   this: pty → a counter
//
// Measured: ~50MB/s here, stable, against 5–21MB/s through the TUI. So the
// emulator costs about half the pipe and the pipe is the ceiling. (The 1.2s
// reading was a probe fault, not a fast run — see the note on the end
// condition below.)

import { openPty, quiet, until, wait } from "./pty";

const SESSION = "poc.ceiling";
const ours = (name: string) => name.startsWith("poc.");

const zmx = async (args: string[]) => {
  const proc = Bun.spawn(["zmx", ...args], {
    env: { ...process.env, ZMX_SESSION: "" },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [out, err, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  return { out, err, code };
};

const drain = async (attempt: number) => {
  const pty = openPty(["zmx", "attach", SESSION], { cols: 120, rows: 31 });
  try {
    await until("a prompt", () => pty.saw("bash-"), 10_000);
    const before = pty.bytes();
    // Not a sentinel word. The first version of this waited for `echo DRAINED`
    // to appear, and matched the shell's echo of the command line it was in —
    // so it timed the round trip of one keystroke and called it 33MB/s. What
    // is wanted is the last byte, so the end condition is the counter going
    // quiet, and the clock runs from the first byte to the last one.
    pty.send("yes 'opentui embedded terminal throughput check line' | head -c 40000000\r");
    const { megabytes, elapsed } = await quiet(() => pty.bytes(), before);
    console.log(
      `  attempt ${attempt}   ${megabytes.toFixed(1)}MB in ${elapsed.toFixed(1)}s = ${(
        megabytes / elapsed
      ).toFixed(1)}MB/s`,
    );
  } finally {
    // ctrl+backslash: zmx's own detach, so the session outlives the client.
    pty.send(String.fromCodePoint(28));
    await wait(200);
    await pty.close();
  }
};

if (!ours(SESSION)) throw new Error(`refusing to touch ${SESSION}`);
const made = await zmx(["run", SESSION, "-d", "bash", "--norc", "-i"]);
if (made.code !== 0) throw new Error(`zmx run: ${made.err.trim()}`);
console.log("drain, no rendering anywhere");
try {
  for (let attempt = 1; attempt <= 3; attempt++) await drain(attempt);
} finally {
  await zmx(["kill", SESSION, "--force"]);
}
