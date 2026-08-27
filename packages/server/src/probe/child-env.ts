// What does a pty child actually receive?
//
// Not what `childEnv` returns — what arrives on the other side. The gap
// between those two is where a hijack hid for weeks, and where a second
// marker hid after it.
//
// `childEnv` used to strip ZMX_SESSION by leaving the key out, and a unit
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
// ── and then it happened again, with a marker nobody had heard of ──────────
//
// Agents amoeba started had transcript saving off, because they inherited
// `CLAUDE_CODE_CHILD_SESSION` from whatever launched the daemon — along with
// the parent session's id and its messaging socket. Same shape: a parent
// describing itself, reaching a child that is meant to be its own.
//
// That is why this probe now asks about a *set* rather than about one name.
// The one variable that was checked was the one that was already fixed.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// This runs `/bin/sh` and prints a variable. It never invokes zmx, never
// attaches, and never names a session, so it disturbs nothing — which is what
// makes it the right shape for a check that has to run near a live session.

import { Effect, Stream } from "effect";
import { PtySpawner } from "../pty";
import * as ptyBun from "../pty-bun";
import { childEnv } from "../zmx-session";

const MARKER = "awp-child-env";

/**
 * What the child is asked about, and why each one.
 *
 * `CLAUDE_CODE_CHILD_SESSION` is the one that was reported. The other two are
 * here because they are the ones whose leak would be hardest to attribute: a
 * fresh agent holding the parent's session id, or talking to its messaging
 * socket, misbehaves somewhere else entirely.
 */
const ASKED = ["ZMX_SESSION", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID"] as const;

const program = Effect.gen(function* () {
  const spawner = yield* PtySpawner;

  const pty = yield* spawner.spawn({
    command: "/bin/sh",
    args: ["-c", ASKED.map((name) => `printf '${MARKER}:${name}:[%s]\\n' "$${name}"`).join("; ")],
    size: { cols: 80, rows: 24 },
    env: childEnv(),
  });

  const chunks: Array<string> = [];
  yield* Stream.runForEach(Stream.take(pty.output, 8), (chunk) =>
    Effect.sync(() => chunks.push(chunk)),
  ).pipe(Effect.timeout("3 seconds"), Effect.ignore);

  const seen = chunks.join("");

  let bad = 0;
  console.log("");
  for (const name of ASKED) {
    const match = new RegExp(`${MARKER}:${name}:\\[(?<value>[^\\]]*)\\]`, "u").exec(seen);
    const value = match?.groups?.value;
    const parent = process.env[name] ?? "";
    // `set=yes` and an empty value is the pass. Absent would print `set=` and
    // is *not* a pass — an omitted key is a request the spawner is free to
    // ignore, which is the whole finding this probe exists for.
    const state =
      value === undefined ? "<no answer>" : value === "" ? "[] set=yes" : `[${value}] LEAKED`;
    console.log(`  ${name.padEnd(26)} parent [${parent === "" ? "" : "set"}]   child ${state}`);
    if (value !== "") {
      bad += 1;
    }
  }
  console.log("");

  if (bad > 0) {
    console.log(`  ${bad} leaked. A child believes it is the session that spawned it.\n`);
    return 1;
  }
  console.log("  all neutralised.\n");
  return 0;
}).pipe(Effect.provide(ptyBun.layer), Effect.scoped);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
