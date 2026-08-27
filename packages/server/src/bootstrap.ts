import { Context, Data, Effect, Layer } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { capture, said } from "./run";
import { childEnv } from "./zmx-session";

// Whatever a person wants run in a workspace the moment it exists.
//
// `bun install`, `cp .env.example .env`, `direnv allow` — the handful of things
// that stand between a fresh checkout and one somebody can work in. Without
// them the first thing every new workspace's agent does is discover the same
// missing dependency and fix it again.
//
// ── a line, not an argv, and that is the opposite of `agent` ───────────────
//
// `Settings.agent` is split on whitespace because it names a program awp
// launches. A hook is not that: it is a line somebody wrote in a config file,
// and the normal shape of one is `bun install && bun run build` or
// `cp .env.example .env`. Splitting that on whitespace produces nonsense, so it
// goes to `sh -c` whole and the shell does what a shell is for.
//
// The cost is stated rather than hidden: a hook is arbitrary code, running with
// the daemon's permissions, from a file on this machine. So is `agent`. The
// line this does not cross is running anything awp inferred — a hook is only
// ever what the config file says.
//
// ── the marker, again ──────────────────────────────────────────────────────
//
// `childEnv()`, for the reason the whole of this repo keeps restating: a
// hook is free to run `zmx` — plenty of people's bootstrap opens a shell or a
// server in one — and a child that inherits `ZMX_SESSION` resolves it and
// switches the *calling* client, which is whichever session the daemon happens
// to be running in. Set to empty rather than omitted: bun-pty is not the only
// spawner that merges onto the parent environment rather than replacing it.

/** How long any one hook may take before it is given up on. */
const PATIENCE = "10 minutes";

export class BootstrapError extends Data.TaggedError("BootstrapError")<{
  readonly command: string;
  readonly reason: string;
}> {}

export class Bootstrap extends Context.Service<
  Bootstrap,
  {
    /**
     * Run one hook in `cwd`, and answer with what it printed.
     *
     * Fails with the command's own sentence rather than one composed here —
     * the same rule the whole repo follows for a CLI, because "bun install
     * failed" is a worse message than the one bun already wrote.
     */
    readonly run: (options: {
      readonly command: string;
      readonly cwd: string;
    }) => Effect.Effect<string, BootstrapError>;
  }
>()("awp/Bootstrap") {}

export const layer = Layer.effect(Bootstrap)(
  Effect.gen(function* () {
    const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

    return {
      run: ({ command, cwd }) =>
        Effect.gen(function* () {
          const captured = yield* capture(
            spawner,
            ChildProcess.make("sh", ["-c", command], { cwd, env: childEnv() }),
          ).pipe(
            Effect.mapError(
              (error) => new BootstrapError({ command, reason: (error as Error).message }),
            ),
          );

          if (captured.exitCode !== 0) {
            // The exit code is the whole reason this goes through `capture`.
            // `ChildProcessSpawner.string` discards it, so a hook that failed
            // would arrive as a successful empty answer — the exact shape that
            // once had this repo reporting a workspace it had not created.
            const reason = said(captured);
            return yield* Effect.fail(
              new BootstrapError({
                command,
                reason: reason === "" ? `exited ${captured.exitCode}` : reason,
              }),
            );
          }

          return captured.stdout;
        }).pipe(
          // A hook that never returns is a job that never returns, and there is
          // nothing a person can do about that from the panel. Interrupting
          // closes `capture`'s Scope, which is what kills the child.
          Effect.timeoutOrElse({
            duration: PATIENCE,
            orElse: () =>
              Effect.fail(
                new BootstrapError({ command, reason: `still running after ${PATIENCE}` }),
              ),
          }),
        ),
    };
  }),
);

// The spawner is a requirement rather than something provided here, the same
// way `jj-cli` leaves it: main.ts provides one layer for the whole daemon, so a
// second one built in here would be a second set of child processes nothing
// else knew about.
