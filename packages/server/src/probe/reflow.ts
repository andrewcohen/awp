// Does a program in an attached session learn the pane's size?
//
// "I opened the editor and nvim did not reflow" is two possible faults with one
// appearance: either the size never reaches the program, or it does and the
// program is not repainted. They are in different files, so this separates them
// before either is guessed at.
//
// Two attaches at two different sizes, and the program is asked its own opinion
// each time:
//
//     attach 100x30 ─▶ stty size ─▶ ?      does the first client set the size
//     detach
//     attach  60x20 ─▶ stty size ─▶ ?      does a *later* client change it
//
// ── the session this touches ───────────────────────────────────────────────
// Its own, and only its own. It creates a session with a name nothing else
// uses, refuses to run if that name is already taken, and kills it on the way
// out — which is why this one is safe from inside a zmx session. The rule its
// neighbours obey is about not disturbing sessions this repo did not create.
//
//     bun run daemon                              # in another terminal
//     bun packages/server/src/probe/reflow.ts

import * as client from "@awp-kit/protocol/client";
import { Effect, Result, Stream } from "effect";
import { DAEMON_HOST, DAEMON_PORT } from "../daemon";
import { PtySpawner } from "../pty";
import * as ptyBun from "../pty-bun";
import { childEnv } from "../zmx-session";

const SESSION = "awp-reflow-probe";
const url = `ws://${DAEMON_HOST}:${DAEMON_PORT}`;

/** See probe/skeleton.ts — the belt already failed once. */
const assertNoLeak = Effect.gen(function* () {
  const spawner = yield* PtySpawner;
  const pty = yield* spawner.spawn({
    command: "/bin/sh",
    args: ["-c", `printf 'leak:[%s]' "$ZMX_SESSION"`],
    size: { cols: 80, rows: 24 },
    env: childEnv(),
  });
  const chunks: Array<string> = [];
  yield* Stream.runForEach(Stream.take(pty.output, 4), (chunk) =>
    Effect.sync(() => chunks.push(chunk)),
  ).pipe(Effect.timeout("3 seconds"), Effect.ignore);
  const value = /leak:\[(?<value>[^\]]*)\]/u.exec(chunks.join(""))?.groups?.value;
  if (value !== "") {
    return yield* Effect.die(
      new Error(`child inherited ZMX_SESSION=[${value ?? "<no answer>"}]. Refusing to run.`),
    );
  }
});

// v4 spells it `Effect.Success<T>` — there is no nested `Effect.Effect` namespace.
type Rpc = Effect.Success<typeof client.make>;

/**
 * Attach at a size, ask the shell how big it thinks it is, and report.
 *
 * `stty size` prints rows then columns, which is the opposite order to
 * everything else here — said out loud rather than transposed silently below.
 */
const askSize = (rpc: Rpc, cols: number, rows: number) =>
  Effect.gen(function* () {
    const marker = `size-${cols}x${rows}`;
    const pattern = new RegExp(`${marker}=(?<got>[0-9]+ [0-9]+)`, "u");
    let seen = "";
    let asked = false;
    let answer = "";

    yield* Stream.runForEach(rpc.Attach({ session: SESSION, cols, rows }), (chunk) =>
      Effect.gen(function* () {
        seen += chunk;
        if (!asked && seen.length > 0) {
          // Typed once the session has drawn something, so there is a shell to
          // type at rather than a prompt that has not arrived.
          asked = true;
          yield* Effect.sleep("600 millis");
          yield* rpc.Write({
            session: SESSION,
            data: `printf '${marker}=%s\\n' "$(stty size)"\r`,
          });
          return;
        }
        const found = pattern.exec(seen);
        if (found?.groups?.got !== undefined) {
          answer = found.groups.got;
          // Stopping the stream interrupts the request, which closes the pty's
          // Scope, which kills this client. Detaching is not a separate call.
          return yield* Effect.fail("done" as const);
        }
      }),
    ).pipe(
      // Refusals are reported, not swallowed. An earlier version hid one and
      // the probe read as "the size never reached the program" when the truth
      // was that it had never attached — the daemon does not create sessions,
      // it only attaches to ones that exist.
      Effect.catchTag("AttachRefused", (error) =>
        Effect.sync(() => {
          console.log(`  refused: ${error.reason}`);
        }),
      ),
      Effect.timeout("10 seconds"),
      Effect.catchCause((cause) =>
        Effect.sync(() => {
          const rendered = String(cause);
          if (!rendered.includes("done")) {
            console.log(`  stream ended: ${rendered}`);
          }
        }),
      ),
    );

    if (process.env.REFLOW_DEBUG !== undefined) {
      console.log("  ---- raw ----\n" + JSON.stringify(seen.slice(0, 1200)) + "\n  -------------");
    }

    const [gotRows, gotCols] = answer.split(" ");
    const want = `${cols}x${rows}`;
    const got = answer === "" ? "no answer" : `${gotCols ?? "?"}x${gotRows ?? "?"}`;
    console.log(
      `  attached ${want.padEnd(9)} the program says ${got.padEnd(11)}${got === want ? "ok" : "MISMATCH"}`,
    );
    return got === want;
  });

const program = Effect.gen(function* () {
  yield* assertNoLeak;
  const rpc = yield* client.make;

  const listed = yield* rpc.SessionList();
  if (listed.some((s) => s.name === SESSION)) {
    return yield* Effect.die(
      new Error(`a session named ${SESSION} already exists. Kill it and rerun.`),
    );
  }

  // Created here rather than by attaching: the daemon refuses to attach to a
  // session that does not exist, which is the right rule — a pane opening a
  // session by looking at it would make a typo into a new agent.
  console.log(`\n  creating ${SESSION}\n`);
  // `zmx run <name> -d <command>` is the detached create. `zmx attach` also
  // creates, and is the one thing this file must never do from inside a session.
  const made = Bun.spawnSync(["zmx", "run", SESSION, "-d", "sh"], {
    env: childEnv() as Record<string, string>,
  });
  if (made.exitCode !== 0) {
    return yield* Effect.die(new Error(`could not create ${SESSION}: ${made.stderr.toString()}`));
  }
  yield* Effect.sleep("700 millis");
  const first = yield* askSize(rpc, 100, 30);
  const second = yield* askSize(rpc, 60, 20);

  console.log(
    `\n  ${
      first && second
        ? "the size reaches the program on both attaches — a program that did not\n" +
          "  reflow was not repainted, which is a different bug in a different file."
        : "the size does not reach the program. That is this repo's, not the editor's."
    }\n`,
  );
}).pipe(Effect.provide(client.layer(url)), Effect.provide(ptyBun.layer), Effect.scoped);

const result = await Effect.runPromise(Effect.result(program));

// Reported rather than assumed. A cleanup that fails silently is worse than
// none, because the next run inherits a session it did not create and can no
// longer tell the difference.
const killed = Bun.spawnSync(["zmx", "kill", SESSION, "--force"], {
  env: childEnv() as Record<string, string>,
});
console.log(
  killed.exitCode === 0
    ? `  killed ${SESSION}\n`
    : `  COULD NOT KILL ${SESSION} — remove it by hand:\n` +
        `    env -u ZMX_SESSION zmx kill ${SESSION} --force\n`,
);

if (Result.isFailure(result)) {
  console.error("\n  reflow probe failed:", result.failure, "\n");
  process.exit(1);
}
process.exit(0);
