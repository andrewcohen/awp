// Releasing a conversation is supposed to kill its adapter. Does it?
//
// ── the report this answers ────────────────────────────────────────────────
//
// Measured on the live daemon, 2026-09-17: one daemon, one workspace key, one
// stored session id, and **two adapter processes alive on it at once** — the
// first eight minutes older than the second, and the second resuming the
// session the first had opened. Two `claude` processes on one transcript is
// the exact thing `chat_claims` exists to prevent, and it cannot see this
// one, because both holders are the same pid.
//
// `RcMap` is the guarantee inside a process, and it had already been asked to
// release the first: the second was spawned by a lookup, which only runs when
// the key is absent. So the release happened and the adapter did not die.
//
// `probe:child-tree` already measured that a scope closing kills its child,
// and the whole group under it. So the question is what `conversation` does
// that a bare spawn does not, and there are exactly two candidates:
//
//   stdin from a queue     `endOnDone: false`, a stream that never completes.
//                          If closing waits for the writer to finish, it waits
//                          for a queue nothing ever ends
//   a forked reader        `Stream.runForEach` over stdout, parked on a pipe
//                          that is still open, interrupted by the close
//
// Guessing between them is how the afternoon this came out of went, so this
// asks all four combinations and names the ingredient.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// Four `sh` processes and a `ps`, one at a time. It never invokes zmx, never
// attaches, never names a session, and kills anything it planted that lived.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Option, Queue, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { execFileSync } from "node:child_process";

/** How long a planted process lives if nothing kills it. */
const LINGER = 300;

/** Long enough for the child to have printed its pid. */
const SPEAKS = "1 second";

/** Long enough for a signal to be delivered and reaped. */
const SETTLE = 500;

/**
 * How long a close is allowed to take before it counts as not returning.
 *
 * Generous on purpose. What is being told apart is "slow" from "never", and a
 * close that takes a second is still a close; the failure being looked for
 * leaves a process alive for the rest of the daemon's life.
 */
const PATIENCE = "6 seconds";

const alive = (pid: number): boolean => {
  try {
    const out = execFileSync("ps", ["-o", "stat=", "-p", String(pid)], { encoding: "utf8" }).trim();
    return out !== "" && !out.startsWith("Z");
  } catch {
    return false;
  }
};

const pause = (ms: number): Promise<void> => new Promise((done) => setTimeout(done, ms));

interface Outcome {
  readonly label: string;
  readonly pid: number;
  /** Milliseconds the scope took to close, or undefined if it never did. */
  readonly closed: number | undefined;
  readonly lived: boolean;
}

/**
 * One spawn, one close, one question.
 *
 * `stdin` and `reader` are the two things `conversation` does that
 * `probe:child-tree`'s spawn does not, and they are separated so the answer
 * names a line rather than a file.
 */
const trial = (
  label: string,
  options: {
    readonly stdin: boolean;
    readonly reader: boolean;
    /** What to run. `sh` is the control; the adapter is bun, which is not. */
    readonly bun?: boolean;
  },
): Effect.Effect<Outcome, never, ChildProcessSpawner.ChildProcessSpawner> =>
  Effect.gen(function* () {
    const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
    // Outside the scope, so what the child said survives the close — the same
    // reason `probe:child-tree` collects into an array.
    const chunks: Array<string> = [];
    const at = performance.now();

    const done = yield* Effect.scoped(
      Effect.gen(function* () {
        const outbox = yield* Queue.unbounded<Uint8Array>();
        // Separately rather than spread into `make`: a conditional tuple is not
        // a tuple to tsc, and the probe has to typecheck like everything else.
        const command = options.bun === true ? process.execPath : "/bin/sh";
        const args =
          options.bun === true
            ? [
                "-e",
                // A bun process with a live event loop and a reader on its own
                // stdin, which is what the ACP adapter is. It installs no
                // handler: whatever bun does with a signal by default is what
                // the adapter does with it.
                `console.log("child:" + process.pid); process.stdin.resume(); setInterval(() => {}, 1000);`,
              ]
            : ["-c", `echo child:$$; sleep ${String(LINGER)}`];

        const handle = yield* Effect.orDie(
          spawner.spawn(
            ChildProcess.make(
              command,
              args,
              options.stdin
                ? {
                    // Byte for byte what `conversation` passes: a queue nothing
                    // ever ends, and `endOnDone: false` so the child's stdin is
                    // never closed either.
                    stdin: { stream: Stream.fromQueue(outbox), endOnDone: false },
                  }
                : {},
            ),
          ),
        );

        const collect = Stream.runForEach(Stream.decodeText(handle.stdout), (chunk) =>
          Effect.sync(() => chunks.push(chunk)),
        );

        if (options.reader) {
          // Still running when the scope closes, which is the adapter's shape:
          // the reader is parked on a pipe that has not ended.
          yield* Effect.forkScoped(Effect.ignore(collect));
          yield* Effect.sleep(SPEAKS);
        } else {
          yield* collect.pipe(Effect.timeout(SPEAKS), Effect.ignore);
        }
      }),
      // The close is what is being measured, so the bound is around the whole
      // scoped block rather than inside it.
    ).pipe(Effect.timeoutOption(PATIENCE));

    // `Option.isNone`, not `_tag` — see AGENTS.md. None is the close that never
    // returned, which is the outcome this whole probe is about.
    const closed = Option.isNone(done) ? undefined : performance.now() - at;
    const pid = Number(/child:(?<pid>\d+)/u.exec(chunks.join(""))?.groups?.["pid"] ?? Number.NaN);

    yield* Effect.promise(() => pause(SETTLE));

    return {
      label,
      pid,
      closed: closed === undefined ? undefined : Math.round(closed),
      lived: Number.isFinite(pid) && alive(pid),
    };
  });

const program = Effect.gen(function* () {
  const outcomes: Array<Outcome> = [];

  // In order of how much of `conversation` each one is, so the first row that
  // fails is the ingredient.
  outcomes.push(yield* trial("bare spawn", { stdin: false, reader: false }));
  outcomes.push(yield* trial("stdin from a queue", { stdin: true, reader: false }));
  outcomes.push(yield* trial("a forked reader", { stdin: false, reader: true }));
  outcomes.push(yield* trial("both, over sh", { stdin: true, reader: true }));
  // The one that matters. Everything above is `sh`, which dies on anything; the
  // adapter is a bun process with an event loop, and what a runtime does with
  // the default signal is not a property of the spawner.
  outcomes.push(yield* trial("the adapter: bun", { stdin: true, reader: true, bun: true }));

  console.log("");
  console.log("                        close      child");
  for (const one of outcomes) {
    const took = one.closed === undefined ? "NEVER  " : `${String(one.closed).padStart(5)}ms`;
    console.log(
      `  ${one.label.padEnd(20)}  ${took}    ${one.lived ? "STILL RUNNING" : "gone"}` +
        (Number.isFinite(one.pid) ? "" : "   (said no pid)"),
    );
  }
  console.log("");

  for (const one of outcomes) {
    if (one.lived && Number.isFinite(one.pid)) {
      // A probe about stray processes must not leave one.
      try {
        execFileSync("kill", ["-9", String(one.pid)], { stdio: "ignore" });
      } catch {
        // Already gone between the question and the answer. Nothing to do.
      }
    }
  }

  const leaked = outcomes.filter((one) => one.lived);
  if (leaked.length === 0) {
    console.log("  every close killed its child. The adapter surviving its release is not");
    console.log("  this call, and the next place to look is what holds the reference.\n");
    return 0;
  }

  console.log(`  ${leaked.map((one) => one.label).join(", ")} left a process alive.`);
  console.log("  A conversation released is an adapter that keeps running — and a second");
  console.log("  lookup then opens the same session beside it, which is two agents on one");
  console.log("  transcript with nothing able to see it.\n");
  return 1;
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
