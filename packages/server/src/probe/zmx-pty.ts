// Does bun-pty host `zmx attach`?
//
// Run from a plain terminal, OUTSIDE zmx and outside zdeck:
//
//   bun run probe:zmx
//
// It refuses to run inside a session, and the refusal is the point — see
// zmx-session.ts. Every earlier attempt to answer this from inside one
// disturbed the session doing the asking.
//
// A probe, not a test. It reports numbers and exits; nothing asserts. The
// numbers to beat are gdeck's, measured in Go: 25ms to attach, of which spawn
// 3ms, first byte 9ms, screen 11ms.

import { spawn } from "bun-pty";
import { requireOutsideZmxSession, zmxChildEnv } from "../zmx-session.js";

requireOutsideZmxSession();

const NAME = `awp-pty-probe-${process.pid}`;
const env = zmxChildEnv();

const t0 = performance.now();
const at = () => `+${(performance.now() - t0).toFixed(1)}ms`;
const say = (...parts: unknown[]) => console.log("[probe]", ...parts);

say(`session ${NAME}, a fresh one — never an existing session`);

const pty = spawn("zmx", ["attach", NAME, "/bin/sh", "-c", "echo PROBE_READY; stty size; cat"], {
  name: "xterm-256color",
  cols: 100,
  rows: 30,
  env,
});

say(`spawned pid=${pty.pid} ${at()}`);
const spawnedAt = performance.now() - t0;

let seen = "";
let firstByteAt: number | undefined;
let readyAt: number | undefined;
let echoedAt: number | undefined;

pty.onData((data) => {
  firstByteAt ??= performance.now() - t0;
  seen += data;

  if (readyAt === undefined && seen.includes("PROBE_READY")) {
    readyAt = performance.now() - t0;
    say(`session live ${at()} — writing a keystroke`);
    pty.write("round-trip\n");
    return;
  }

  if (readyAt !== undefined && echoedAt === undefined && seen.includes("round-trip")) {
    echoedAt = performance.now() - t0;
    say(`keystroke echoed ${at()}`);
    finish();
  }
});

pty.onExit((event) => {
  say(`pty exit ${at()}`, event);
  if (echoedAt === undefined) {
    // Exiting before the keystroke round-tripped is the interesting failure:
    // it means zmx said something and left. Show what it said.
    say("output before exit:", JSON.stringify(seen));
    finish(1);
  }
});

const ms = (v: number | undefined) => (v === undefined ? "—" : `${v.toFixed(1)}ms`);

function finish(code = 0): void {
  say("RESULT", {
    spawn: ms(spawnedAt),
    firstByte: ms(firstByteAt),
    sessionLive: ms(readyAt),
    keystrokeRoundTrip: ms(echoedAt),
    sizeReported: /\b30 100\b/u.test(seen),
    gdeckBaseline: "attach 25ms — spawn 3ms, first byte 9ms, screen 11ms",
  });

  try {
    pty.kill();
  } catch {
    // Already gone. Cleaning up the session below is what matters.
  }

  // Leave nothing behind. The name was ours, so this kills only ours.
  Bun.spawnSync(["zmx", "kill", NAME, "--force"], { env });
  say(`cleaned up ${NAME}`);
  process.exit(code);
}

setTimeout(() => {
  say("TIMEOUT after 8s — no round trip");
  finish(1);
}, 8000);
