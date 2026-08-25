// What does a pty child actually receive?
//
// Not what `zmxChildEnv` returns — what arrives on the other side. The gap
// between those two is where a hijack hid for weeks.
//
// `zmxChildEnv` used to strip ZMX_SESSION by leaving the key out, and a unit
// test asserted the returned object had no such property, and it passed. But
// bun-pty hands its pairs to a Rust `Command`, which **inherits the parent
// environment** and applies what it is given on top. Without an `env_clear()`
// there is no way to express a removal by omission, so every child saw the
// marker intact and `zmx attach <name>` resolved ZMX_SESSION and switched the
// calling client instead — the exact hijack the function exists to prevent,
// aimed at whatever session the daemon happened to be running in.
//
// No unit test could have caught that, because the bug is in the spawner and
// not in the function. Only spawning something can.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// This runs `/bin/sh` and prints a variable. It never invokes zmx, never
// attaches, and never names a session, so it disturbs nothing — which is what
// makes it the right shape for a check that has to run near a live session.

import { Effect, Stream } from "effect";
import { PtySpawner } from "../pty";
import * as ptyBun from "../pty-bun";
import { zmxChildEnv } from "../zmx-session";

const MARKER = "awp-child-env";

const program = Effect.gen(function* () {
  const spawner = yield* PtySpawner;

  const pty = yield* spawner.spawn({
    command: "/bin/sh",
    args: ["-c", `printf '${MARKER}:[%s]\\n' "$ZMX_SESSION"`],
    size: { cols: 80, rows: 24 },
    env: zmxChildEnv(),
  });

  const chunks: Array<string> = [];
  yield* Stream.runForEach(Stream.take(pty.output, 4), (chunk) =>
    Effect.sync(() => chunks.push(chunk)),
  ).pipe(Effect.timeout("3 seconds"), Effect.ignore);

  const seen = chunks.join("");
  const match = /awp-child-env:\[(?<value>[^\]]*)\]/.exec(seen);
  const value = match?.groups?.value;

  console.log(`\n  parent  ZMX_SESSION=[${process.env.ZMX_SESSION ?? ""}]`);
  console.log(`  child   ZMX_SESSION=[${value ?? "<no answer>"}]\n`);

  if (value === undefined) {
    console.log("  the child never answered — that is a failure, not a pass\n");
    return 1;
  }
  if (value !== "") {
    console.log("  LEAKED. `zmx attach` in a child would hijack the caller's session.\n");
    return 1;
  }
  console.log("  neutralised.\n");
  return 0;
}).pipe(Effect.provide(ptyBun.layer), Effect.scoped);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
