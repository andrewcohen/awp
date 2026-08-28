import { Effect, Layer, Result } from "effect";
import { describe, expect, test } from "vitest";
import { Attachment, AttachError } from "./attachment";
import * as AttachmentImpl from "./attachment";
import { Multiplexer, type Session } from "./multiplexer";
import { makeFake } from "./pty-fake";

// No pty, no zmx, no subprocess. Both dependencies are tags, so a fake pty and
// a fake multiplexer let every one of Attachment's own decisions be checked from
// inside a zmx session — where a real attach cannot run at all.

const session = (over: Partial<Session> = {}): Session => ({
  name: "awp.awp.other.agent",
  pid: 4242,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  busy: true,
  taskEnded: false,
  exitCode: 0,
  created: undefined,
  cmd: "claude",
  labels: {},
  ...over,
});

/** A Multiplexer that knows about exactly the sessions it is given. */
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

const SIZE = { cols: 100, rows: 30 };

interface Harness {
  readonly sessions?: ReadonlyArray<Session>;
  readonly chunks?: ReadonlyArray<string>;
}

/**
 * Run one attach against fakes and report both the outcome and what the
 * spawner was asked to do.
 *
 * The scope is closed before the log is read, which is what makes `killed` a
 * meaningful assertion: it says the release actually ran.
 */
const run = (harness: Harness, name = "awp.awp.other.agent") => {
  const program = Effect.gen(function* () {
    const fake = yield* makeFake({ chunks: harness.chunks ?? [] });

    const outcome = yield* Effect.scoped(
      Effect.gen(function* () {
        const attachment = yield* Attachment;
        return yield* attachment.attach({ session: name, size: SIZE });
      }),
    ).pipe(
      Effect.result,
      Effect.provide(AttachmentImpl.layer),
      Effect.provide(fake.layer),
      Effect.provide(fakeMux(harness.sessions ?? [session()])),
    );

    // Read the log AFTER the scope closed, so `killed` reflects the release.
    const spawns = yield* fake.log.spawns();
    return { outcome, spawns };
  });

  return Effect.runPromise(program);
};

describe("attach", () => {
  test("spawns `zmx attach <session>` in a pty", async () => {
    const { outcome, spawns } = await run({});
    expect(Result.isSuccess(outcome)).toBe(true);
    expect(spawns).toHaveLength(1);
    expect(spawns[0]?.command.command).toBe("zmx");
    expect(spawns[0]?.command.args).toEqual(["attach", "awp.awp.other.agent"]);
  });

  test("neutralises ZMX_SESSION in the child", async () => {
    // The rule with a track record. A child that keeps the marker switches the
    // caller's own client instead of making a new one, stealing the terminal
    // awp was launched from.
    const { spawns } = await run({});
    // Empty, not missing. A missing key is a request the spawner may ignore,
    // and bun-pty's does — see childEnv.
    expect(spawns[0]?.command.env.ZMX_SESSION).toBe("");
  });

  test("passes the size through, since the session will take it", async () => {
    const { spawns } = await run({});
    expect(spawns[0]?.command.size).toEqual(SIZE);
  });

  test("kills the pty when the scope closes", async () => {
    // The thing the hand-rolled probe got wrong twice — once by leaking, once
    // by cleaning up on two paths.
    const { spawns } = await run({});
    expect(spawns[0]?.killed).toBe(true);
  });
});

describe("what attach refuses", () => {
  test("a session that does not exist", async () => {
    const { outcome, spawns } = await run({ sessions: [] });
    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect(outcome.failure).toBeInstanceOf(AttachError);
      expect((outcome.failure as AttachError).reason).toContain("no such session");
    }
    // And nothing was spawned. A refusal that still opened a pty would be worse
    // than no refusal at all.
    expect(spawns).toHaveLength(0);
  });

  test("a session that is listed but has ended", async () => {
    // Attaching to one of these renders a dead program's last screen, which
    // reads as a live pane that stopped responding.
    const dead = session({ ended: true, exitCode: 3 });
    const { outcome, spawns } = await run({ sessions: [dead] });
    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect((outcome.failure as AttachError).reason).toContain("ended");
      expect((outcome.failure as AttachError).reason).toContain("history");
    }
    expect(spawns).toHaveLength(0);
  });

  test("the session this process is running in", async () => {
    const own = process.env.ZMX_SESSION;
    if (own === undefined) {
      return;
    }
    const { outcome, spawns } = await run({ sessions: [session({ name: own })] }, own);
    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect((outcome.failure as AttachError).reason).toContain("fight over one size");
    }
    expect(spawns).toHaveLength(0);
  });
});
