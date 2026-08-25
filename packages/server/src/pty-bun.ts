// PtySpawner over bun-pty.
//
// The only file that names the binding. node-pty does not work under Bun — it
// loads and spawns, then no callback ever fires — so bun-pty is what there is;
// the measurements are in the spec.
//
// Everything here exists to turn a callback API with a manual kill into a
// scoped resource. `Effect.acquireRelease` is the whole trick: acquire opens
// the pty, release kills it, and the type gains `Scope` so a caller cannot run
// it without saying when it ends.

import { spawn } from "bun-pty";
import { Effect, Layer, type Scope } from "effect";
import { type PtyCommand, type PtyExit, type PtyHandle, PtyError, PtySpawner } from "./pty";
import { streamFromCallback } from "./pty";

/**
 * bun-pty's handle, as much of it as is used here.
 *
 * Written out rather than imported because the package's own types describe
 * `onData` as taking a listener and returning a disposable, and the only parts
 * that matter are these.
 */
interface BunPty {
  readonly pid: number;
  onData: (listener: (data: string) => void) => unknown;
  onExit: (listener: (event: { exitCode: number; signal?: string | number }) => void) => unknown;
  write: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  kill: (signal?: string) => void;
}

/** Wrap a synchronous bun-pty call, naming the operation in any failure. */
const attempt = (op: string, run: () => void) =>
  Effect.try({
    try: run,
    catch: (cause) => new PtyError({ op, reason: `pty ${op} failed`, cause }),
  });

const make = {
  // The Scope in the return type is not incidental — it is the promise that
  // this process gets killed. A caller cannot run it without saying when.
  spawn: (command: PtyCommand): Effect.Effect<PtyHandle, PtyError, Scope.Scope> =>
    Effect.acquireRelease(
      Effect.try({
        try: () =>
          spawn(command.command, [...command.args], {
            name: command.term ?? "xterm-256color",
            cols: command.size.cols,
            rows: command.size.rows,
            env: { ...command.env },
          }) as unknown as BunPty,
        catch: (cause) =>
          new PtyError({
            op: "spawn",
            reason: `could not open a pty for ${command.command}`,
            cause,
          }),
      }),
      (pty) =>
        // Release must not fail: the scope is already closing, and a failure
        // here would replace whatever error caused it. A pty whose process is
        // already gone throws, which is not a problem worth reporting.
        Effect.sync(() => {
          try {
            pty.kill();
          } catch {
            // Already dead. That is the desired state.
          }
        }),
    ).pipe(
      Effect.map((pty): PtyHandle => {
        // Registered once, for the pty's whole life. bun-pty's onData has no
        // unsubscribe worth relying on, so the stream is built from the single
        // registration rather than re-subscribing per consumer.
        const output = streamFromCallback((emit, done) =>
          Effect.sync(() => {
            pty.onData(emit);
            pty.onExit(done);
          }),
        );

        return {
          pid: pty.pid,
          output,
          write: (data) => attempt("write", () => pty.write(data)),
          resize: (size) => attempt("resize", () => pty.resize(size.cols, size.rows)),
          // Effect.callback, not v3's Effect.async — the name moved in v4.
          exit: Effect.callback<PtyExit, PtyError>((resume) => {
            pty.onExit((event: { exitCode: number; signal?: string | number }) =>
              resume(Effect.succeed({ code: event.exitCode, signal: event.signal })),
            );
          }),
        };
      }),
    ),
};

/** bun-pty as the PtySpawner. */
export const layer = Layer.succeed(PtySpawner, make);
