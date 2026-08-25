// A PtySpawner that opens no pty.
//
// The reason PtySpawner is a tag. A real attach cannot run from inside a zmx
// session, and this repo is developed from inside one — so without this, every
// caller of Attachment would be untestable in the environment it is written in.
//
// It records what it was asked to do, which is what the interesting assertions
// are about: that ZMX_SESSION was stripped, that the size was passed through,
// that the scope killed the process.

import { Effect, Layer, Ref } from "effect";
import {
  type PtyCommand,
  type PtyExit,
  type PtyHandle,
  PtySpawner,
  streamFromCallback,
} from "./pty";

export interface FakeSpawn {
  readonly command: PtyCommand;
  /** Everything written to it, in order. */
  readonly writes: ReadonlyArray<string>;
  /** Every size it was resized to, in order. */
  readonly resizes: ReadonlyArray<{ cols: number; rows: number }>;
  /** Whether the scope closed and killed it. */
  readonly killed: boolean;
}

export interface FakeLog {
  /** Every spawn, in order. */
  readonly spawns: () => Effect.Effect<ReadonlyArray<FakeSpawn>>;
}

export interface FakeScript {
  /** Chunks the pty emits as soon as anything reads its output. */
  readonly chunks?: ReadonlyArray<string>;
  /** What it exits with. Omit to leave it running until the scope closes. */
  readonly exit?: PtyExit;
}

/**
 * A fake spawner and a handle on what it saw.
 *
 * Returned together because the assertions are about the interaction, not the
 * result: a test wants to know what argv and env the spawner was handed.
 */
export const makeFake = (
  script: FakeScript = {},
): Effect.Effect<{ layer: Layer.Layer<PtySpawner>; log: FakeLog }> =>
  Effect.gen(function* () {
    interface Entry {
      command: PtyCommand;
      writes: string[];
      resizes: Array<{ cols: number; rows: number }>;
      killed: boolean;
    }

    const entries = yield* Ref.make<Entry[]>([]);
    let pid = 1000;

    const spawner = {
      spawn: (command: PtyCommand) =>
        Effect.acquireRelease(
          Effect.gen(function* () {
            const entry: Entry = { command, writes: [], resizes: [], killed: false };
            yield* Ref.update(entries, (all) => [...all, entry]);
            pid += 1;
            return { entry, pid };
          }),
          ({ entry }) =>
            // Recording rather than doing. That the scope ran this at all is
            // the thing worth asserting — the probe this replaces had a path
            // where cleanup ran twice and another where it never ran.
            Effect.sync(() => {
              entry.killed = true;
            }),
        ).pipe(
          Effect.map(({ entry, pid: assigned }): PtyHandle => {
            const output = streamFromCallback((emit, done) =>
              Effect.sync(() => {
                for (const chunk of script.chunks ?? []) {
                  emit(chunk);
                }
                if (script.exit !== undefined) {
                  done();
                }
              }),
            );

            return {
              pid: assigned,
              output,
              write: (data) =>
                Effect.sync(() => {
                  entry.writes.push(data);
                }),
              resize: (size) =>
                Effect.sync(() => {
                  entry.resizes.push({ cols: size.cols, rows: size.rows });
                }),
              exit: Effect.succeed(script.exit ?? { code: 0, signal: undefined }),
            };
          }),
        ),
    };

    return {
      layer: Layer.succeed(PtySpawner, spawner),
      log: {
        spawns: () =>
          Ref.get(entries).pipe(
            Effect.map((all) =>
              all.map((entry): FakeSpawn => ({
                command: entry.command,
                writes: [...entry.writes],
                resizes: [...entry.resizes],
                killed: entry.killed,
              })),
            ),
          ),
      },
    };
  });
