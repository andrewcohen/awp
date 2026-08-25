// The walking skeleton, end to end, with a number on it.
//
// Everything else in this tree has been measured with something taken out — a
// fake pty, a fake multiplexer, or RpcTest standing in for the socket. This
// runs the whole thing: a real daemon, a real websocket, a real `zmx attach`,
// and a real keystroke going down one and coming back up the other.
//
// ── the session this touches ───────────────────────────────────────────────
// Its own, and only its own. A session takes its size from the client looking
// at it, so attaching to one of the user's would reflow whatever is running in
// there. This creates a session with a name nothing else uses, refuses to run
// if that name is already taken, and kills it on the way out.
//
// That is also why this is safe from inside a zmx session, unlike its
// neighbours here: the rule those obey is about not disturbing sessions this
// repo did not create, and the only session this one ever opens a client on is
// the one it just made.
//
// ── this needs a daemon running ────────────────────────────────────────────
// Start one first, in another terminal:
//
//     bun run daemon
//
// Two processes rather than one, and not only for realism. Building the daemon
// layer inside `Effect.runPromise` and letting `Effect.scoped` close it hangs at
// 100% CPU: there is no runtime to run the SocketServer's finalizer to
// completion, so the scope never closes. `NodeRuntime.runMain` does handle it —
// the real daemon exits on SIGINT in 270ms — which is why this is the shape of
// the probe rather than a defect in the daemon.

import * as client from "@awp-kit/protocol/client";
import { Effect, Result, Stream } from "effect";
import { DAEMON_HOST, DAEMON_PORT } from "../daemon";
import { PtySpawner } from "../pty";
import * as ptyBun from "../pty-bun";
import { zmxChildEnv } from "../zmx-session";

const SESSION = "awp-skeleton-probe";
const url = `ws://${DAEMON_HOST}:${DAEMON_PORT}`;

const ms = (from: number) => `${(Date.now() - from).toString().padStart(4)}ms`;

/**
 * Refuse to go near zmx until a child is proven not to inherit the marker.
 *
 * Not belt and braces — the belt already failed once. `zmxChildEnv` returning
 * the right object says nothing about what the spawner delivers, and when it
 * did not deliver it, this probe attached to the user's session instead of its
 * own. Checking the child's actual environment is the only thing that would
 * have stopped that, so it happens before anything else here.
 */
const assertNoLeak = Effect.gen(function* () {
  const spawner = yield* PtySpawner;
  const pty = yield* spawner.spawn({
    command: "/bin/sh",
    args: ["-c", `printf 'leak:[%s]' "$ZMX_SESSION"`],
    size: { cols: 80, rows: 24 },
    env: zmxChildEnv(),
  });
  const chunks: Array<string> = [];
  yield* Stream.runForEach(Stream.take(pty.output, 4), (chunk) =>
    Effect.sync(() => chunks.push(chunk)),
  ).pipe(Effect.timeout("3 seconds"), Effect.ignore);

  const match = /leak:\[(?<value>[^\]]*)\]/u.exec(chunks.join(""));
  const value = match?.groups?.value;
  if (value !== "") {
    return yield* Effect.die(
      new Error(
        `child inherited ZMX_SESSION=[${value ?? "<no answer>"}]. ` +
          "Refusing to run: `zmx attach` here would hijack the caller's session. " +
          "See probe/child-env.ts.",
      ),
    );
  }
});

const program = Effect.gen(function* () {
  yield* assertNoLeak;

  const rpc = yield* client.make;

  const listed = yield* rpc.SessionList();
  const target = listed.find((s) => s.name === SESSION);
  if (target === undefined) {
    return yield* Effect.die(
      new Error(
        `no session named ${SESSION}. Create one first:\n` +
          `  env -u ZMX_SESSION zmx run ${SESSION} -d`,
      ),
    );
  }
  if (target.refusal !== undefined) {
    return yield* Effect.die(new Error(`the daemon will not attach: ${target.refusal}`));
  }

  console.log(`\n  attaching to ${SESSION} (pid ${target.pid}, ${target.clients} clients)\n`);

  // A marker the shell will echo back. Timing on a prompt would time whatever
  // the user's shell startup does; timing on this times the round trip.
  const marker = `awp-echo-${process.pid}`;

  const attachedAt = Date.now();
  let firstByteAt = 0;
  let sentAt = 0;
  let echoedAt = 0;
  let bytes = 0;

  yield* Stream.runForEach(rpc.Attach({ session: SESSION, cols: 100, rows: 30 }), (chunk) =>
    Effect.gen(function* () {
      bytes += chunk.length;
      if (firstByteAt === 0) {
        firstByteAt = Date.now();
        console.log(`  first byte           ${ms(attachedAt)}  (${chunk.length} chars)`);
        // Typed only once the session has drawn something, so the measurement
        // is of a warm session rather than of one still starting up.
        sentAt = Date.now();
        yield* rpc.Write({ session: SESSION, data: `echo ${marker}\r` });
        return;
      }
      if (echoedAt === 0 && chunk.includes(marker)) {
        echoedAt = Date.now();
        console.log(`  keystroke echoed     ${ms(sentAt)}`);
        // Stop reading, which interrupts the request, which closes the pty's
        // Scope, which kills `zmx attach`. Detaching is not a separate call.
        return yield* Effect.fail("done" as const);
      }
    }),
  ).pipe(
    Effect.catchTag("AttachRefused", (e) => Effect.die(new Error(e.reason))),
    Effect.catchCause((cause) =>
      Effect.sync(() => {
        // "done" is this probe stopping itself; anything else is the stream
        // ending for a reason worth seeing rather than swallowing.
        const rendered = String(cause);
        if (!rendered.includes("done")) {
          console.log(`  stream ended: ${rendered}`);
        }
      }),
    ),
  );

  console.log(`\n  attach → first byte  ${firstByteAt - attachedAt}ms`);
  console.log(`  keystroke → echo     ${echoedAt - sentAt}ms`);
  console.log(`  bytes received       ${bytes}`);
  // Five runs on 2026-08-25: attach 43·44·45·49·53ms, echo 7·8·8·8·9ms.
  //
  // The echo matches gdeck's Go path, which is the number that decides whether
  // typing feels direct — and it goes through an rpc round trip, a websocket
  // and a JSON frame to get there, so serialization is not in the budget in any
  // way worth optimising yet. The attach is roughly twice gdeck's, and the two
  // are not measured the same way: gdeck timed an in-process Go attach, this
  // times a request crossing a socket into another process which then spawns a
  // pty. Whether that gap is the extra process or something worth fixing is a
  // question for a profile, not a guess.
  console.log(`\n  gdeck, for comparison: 25ms attach, 8ms p50 echo\n`);
}).pipe(Effect.provide(client.layer(url)), Effect.provide(ptyBun.layer), Effect.scoped);

const result = await Effect.runPromise(Effect.result(program));

// Killed here rather than in a finalizer: the point is that it happens even if
// the measurement threw, and that the session this created does not outlive it.
//
// `--force`, and the outcome is reported. A plain kill left the session behind
// on the first run — a client had only just detached and zmx declined — and a
// cleanup that fails silently is worse than none, because the next run inherits
// a session it did not create and can no longer tell the difference.
const killed = Bun.spawnSync(["zmx", "kill", SESSION, "--force"], {
  env: zmxChildEnv() as Record<string, string>,
});
console.log(
  killed.exitCode === 0
    ? `  killed ${SESSION}\n`
    : `  COULD NOT KILL ${SESSION} — remove it by hand:\n` +
        `    env -u ZMX_SESSION zmx kill ${SESSION} --force\n`,
);

if (Result.isFailure(result)) {
  console.error("\n  skeleton failed:", result.failure, "\n");
  process.exit(1);
}
process.exit(0);
