import { Effect, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";

// Running a command and finding out whether it worked.
//
// ── why this exists ────────────────────────────────────────────────────────
// `ChildProcessSpawner.string` collects stdout and **ignores the exit code**.
// That is not a bug in Effect — it is what a function returning a string can
// promise — but it is the wrong default for a command whose failure matters.
// Measured 2026-08-26: `sh -c 'echo out; exit 3'` through `string` succeeds
// with `"out\n"`, and the 3 is nowhere.
//
// What that looked like in practice: `jj workspace add` on a workspace that
// already exists prints `Error: Workspace named 'second' already exists` and
// exits 1. Through `string` that is a successful empty answer, so the service
// reported that it had created a workspace it had not. The same hole was in
// `zmx.ts`, where a failing `zmx ls` parses to an empty list and the sidebar
// says "no sessions" — which is indistinguishable from there being none.
//
// It was found by a mutation check rather than by a test: removing the
// idempotence guard from `addWorkspace` should have failed the test that adds a
// workspace twice, and did not. A guard whose removal changes nothing is not
// doing the thing it claims.
//
// ── the two streams have to be read together ───────────────────────────────
// stdout and stderr are collected concurrently with each other and with the
// wait for the exit code. Reading one to the end before starting the other
// deadlocks as soon as a command writes more to the unread stream than its pipe
// buffer holds — which is rare enough to pass every test and then happen on a
// long jj error.

export interface Captured {
  readonly stdout: string;
  readonly stderr: string;
  readonly exitCode: number;
}

/**
 * Run a command to completion, keeping both streams and the exit code.
 *
 * The spawner is an argument rather than something this asks for, so a service
 * resolves it once when its layer is built instead of on every call — and the
 * service's own methods stay free of a dependency in their types.
 */
export const capture = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  command: ChildProcess.Command,
) =>
  Effect.gen(function* () {
    const handle = yield* spawner.spawn(command);

    const [stdout, stderr, exitCode] = yield* Effect.all(
      [
        Stream.mkString(Stream.decodeText(handle.stdout)),
        Stream.mkString(Stream.decodeText(handle.stderr)),
        handle.exitCode,
      ],
      { concurrency: "unbounded" },
    );

    return { stdout, stderr, exitCode: Number(exitCode) } satisfies Captured;
  }).pipe(Effect.scoped);

/**
 * What a failing command said, as one line.
 *
 * stderr first, because that is where a CLI writes the sentence explaining
 * itself; stdout only if stderr was empty, which is how some commands report.
 * Trimmed to the first few lines: jj prints a hint block under its errors, and
 * a reason field that carries a paragraph is a reason field nothing displays.
 */
export const said = (captured: Captured): string => {
  const text = captured.stderr.trim() === "" ? captured.stdout : captured.stderr;
  return text.trim().split("\n").slice(0, 3).join("; ").trim();
};
