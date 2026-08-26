import { Effect, Layer, Result, Stream } from "effect";
import { describe, expect, it } from "vitest";
import * as attachment from "./attachment";
import { Multiplexer, type Session } from "./multiplexer";
import { type FakeLog, makeFake } from "./pty-fake";
import { Sessions, layer as sessionsLayer } from "./sessions";

// No pty, no zmx, no subprocess. Every dependency is a tag, so what is under
// test here is only this file's own decisions: that one session gets one pty
// however many clients look at it, that the pty outlives every client but not
// the last one, and that writing to a name nothing is attached to fails rather
// than quietly opening something.

const session = (over: Partial<Session> = {}): Session => ({
  name: "awp.awp.other.agent",
  pid: 4242,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  exitCode: 0,
  created: undefined,
  cmd: "claude",
  labels: {},
  ...over,
});

const fakeMux = (sessions: ReadonlyArray<Session>) =>
  Layer.succeed(Multiplexer, {
    list: () => Effect.succeed(sessions),
    lookup: (name: string) => Effect.succeed(sessions.find((s) => s.name === name)),
    // The fake exists to be a Multiplexer, and a Multiplexer can now start a
    // session. Nothing under test calls it.
    start: () => Effect.void,
    send: () => Effect.void,
    kill: () => Effect.void,
    setLabels: () => Effect.void,
    history: () => Effect.succeed(""),
  });

const NAME = "awp.awp.other.agent";
const SIZE = { cols: 100, rows: 30 };

/**
 * Runs `body` with a Sessions built over fakes, and hands it the pty log.
 *
 * The log is read *after* the whole thing has finished, which is what makes the
 * release assertions possible: a finalizer that ran is only observable once the
 * scope it belonged to has closed.
 */
const harness = <A>(
  body: (sessions: Sessions["Service"], log: FakeLog) => Effect.Effect<A, unknown, never>,
  script: { readonly chunks?: ReadonlyArray<string> } = {},
) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const fake = yield* makeFake(script);
      const layers = sessionsLayer.pipe(
        Layer.provide(attachment.layer),
        Layer.provide(fake.layer),
        Layer.provide(fakeMux([session()])),
      );
      const value = yield* Effect.gen(function* () {
        const sessions = yield* Sessions;
        return yield* body(sessions, fake.log);
      }).pipe(Effect.provide(layers));
      return { value, spawns: yield* fake.log.spawns() };
    }),
  );

describe("one pty per session", () => {
  it("opens a single pty however many clients attach", async () => {
    const { spawns } = await harness((sessions) =>
      Effect.scoped(
        Effect.gen(function* () {
          yield* sessions.attach(NAME, SIZE);
          yield* sessions.attach(NAME, SIZE);
          yield* sessions.attach(NAME, SIZE);
        }),
      ),
    );
    // Three clients, one `zmx attach`. Two ptys on one session would be two zmx
    // clients, and the session would reflow to whichever resized last.
    expect(spawns).toHaveLength(1);
    expect(spawns[0]?.command.args).toEqual(["attach", NAME]);
  });

  it("keeps the pty until the last client lets go, not the first", async () => {
    const { value, spawns } = await harness((sessions, log) =>
      Effect.scoped(
        Effect.gen(function* () {
          yield* sessions.attach(NAME, SIZE);

          // A second client comes and goes inside the first one's lifetime.
          yield* Effect.scoped(sessions.attach(NAME, SIZE).pipe(Effect.asVoid));

          // Read while the first client is still holding on. Releasing when the
          // *first* scope closes is the bug this exists for: it would kill a
          // pty a window is still showing, and read as the agent crashing.
          return (yield* log.spawns())[0]?.killed;
        }),
      ),
    );
    expect(value).toBe(false);
    expect(spawns).toHaveLength(1);
    // …but killed once everyone had let go.
    expect(spawns[0]?.killed).toBe(true);
  });
});

describe("writing and resizing", () => {
  it("sends keystrokes to the shared pty", async () => {
    const { spawns } = await harness((sessions) =>
      Effect.scoped(
        Effect.gen(function* () {
          yield* sessions.attach(NAME, SIZE);
          yield* sessions.write(NAME, "ls\r");
        }),
      ),
    );
    expect(spawns[0]?.writes).toEqual(["ls\r"]);
  });

  it("refuses to write to a session nothing is attached to", async () => {
    const { value, spawns } = await harness((sessions) =>
      Effect.result(sessions.write(NAME, "ls\r")),
    );
    expect(Result.isFailure(value)).toBe(true);
    if (Result.isFailure(value)) {
      expect(value.failure).toMatchObject({ _tag: "NotAttached", session: NAME });
    }
    // The point of failing rather than acquiring: an attach the caller did not
    // ask for would open a zmx client and reflow the session, then close it.
    expect(spawns).toHaveLength(0);
  });

  it("takes the size of whoever attached last", async () => {
    const { spawns } = await harness((sessions) =>
      Effect.scoped(
        Effect.gen(function* () {
          yield* sessions.attach(NAME, SIZE);
          yield* sessions.attach(NAME, { cols: 120, rows: 40 });
        }),
      ),
    );
    // Not this file's preference — it is what zmx does. A session takes its
    // size from the client looking at it, and the second window is a client.
    expect(spawns[0]?.resizes.at(-1)).toEqual({ cols: 120, rows: 40 });
  });
});

describe("output", () => {
  it("delivers the session's bytes to a client, unaltered", async () => {
    const chunks = ["\u001B[2J", "hello", "\r\n"];
    const { value } = await harness(
      (sessions) =>
        Effect.scoped(
          Effect.gen(function* () {
            const output = yield* sessions.attach(NAME, SIZE);
            return yield* Stream.runCollect(Stream.take(output, chunks.length));
          }),
        ),
      { chunks },
    );
    expect((value as ReadonlyArray<string>).join("")).toBe(chunks.join(""));
  });
});
