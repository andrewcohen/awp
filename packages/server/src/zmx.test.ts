import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer } from "effect";
import { describe, expect, test } from "vitest";
import { Multiplexer, identity, isLive } from "./multiplexer";
import { currentZmxSession } from "./zmx-session";
import * as Zmx from "./zmx";

// These run against a REAL zmx, from inside a zmx session, on purpose.
//
// That is the whole reason `attach` is a different service. Everything on the
// Multiplexer answers a question and changes nothing, so asking it costs
// nothing — no client is opened, no session is resized, nothing redraws. Which
// makes this the only part of the server that can be tested here at all.
//
// ── how a program gets its dependencies ────────────────────────────────────
// `run` below is the whole story. An Effect that needs a `Multiplexer` cannot
// be executed — its type says so. Providing `Zmx.layer` settles that debt, and
// `Zmx.layer` in turn needs something able to spawn a subprocess, which the
// platform layer supplies. Once nothing is owed, it runs.

// The spawner comes from platform-node-shared rather than platform-bun, and
// deliberately.
//
// `@effect/platform-bun`'s barrel imports `bun` (through BunRedis), so anything
// touching it cannot be loaded by a test runner on Node — and vitest is on
// Node. But `BunChildProcessSpawner` is literally `export * from` the Node one:
// there is no Bun-specific spawner to lose. Depending on the shared module runs
// under both, which is what lets these tests exercise a real subprocess at all.
const platform = NodeChildProcessSpawner.layer.pipe(
  Layer.provideMerge(NodeFileSystem.layer),
  Layer.provideMerge(NodePath.layer),
);

const run = <A, E>(effect: Effect.Effect<A, E, Multiplexer>): Promise<A> =>
  Effect.runPromise(
    effect.pipe(Effect.provide(Zmx.layer), Effect.provide(platform)) as Effect.Effect<A, E>,
  );

// A tag is itself yieldable inside Effect.gen — `yield* Multiplexer` is how a
// program asks for the implementation it was promised.
const withMultiplexer = <A, E>(f: (mux: Multiplexer["Service"]) => Effect.Effect<A, E>) =>
  run(
    Effect.gen(function* () {
      const mux = yield* Multiplexer;
      return yield* f(mux);
    }),
  );

describe("list", () => {
  test("returns sessions from the real zmx", async () => {
    const sessions = await withMultiplexer((mux) => mux.list());
    // This suite runs inside an awp session, so there is at least one.
    expect(sessions.length).toBeGreaterThan(0);
  });

  test("every session has an addressable name", async () => {
    const sessions = await withMultiplexer((mux) => mux.list());
    for (const session of sessions) {
      expect(session.name).not.toBe("");
      expect(session.name.length).toBeLessThanOrEqual(46);
    }
  });

  test("includes the session these tests are running in", async () => {
    // The row that goes missing when the caller's arrow is not handled. Worth
    // asserting against the real listing rather than only against a fixture,
    // because the fixture is a copy of output from one particular day.
    const own = currentZmxSession();
    if (own === undefined) {
      // Running outside zmx is legitimate; there is simply nothing to check.
      return;
    }
    const sessions = await withMultiplexer((mux) => mux.list());
    expect(sessions.map((session) => session.name)).toContain(own);
  });

  test("reads awp's identity labels off the sessions that have them", async () => {
    const sessions = await withMultiplexer((mux) => mux.list());
    const ours = sessions.filter((session) => identity(session) !== undefined);
    expect(ours.length).toBeGreaterThan(0);
    for (const session of ours) {
      const id = identity(session);
      expect(id?.project).not.toBe("");
      expect(id?.workspace).not.toBe("");
    }
  });

  test("reports live and ended sessions without conflating them", async () => {
    const sessions = await withMultiplexer((mux) => mux.list());
    for (const session of sessions) {
      // Whatever the mix on the day, the two must agree with each other:
      // listed is not the same question as running.
      expect(isLive(session)).toBe(!session.ended);
    }
  });
});

describe("lookup", () => {
  test("finds a session that exists", async () => {
    const own = currentZmxSession();
    if (own === undefined) {
      return;
    }
    const session = await withMultiplexer((mux) => mux.lookup(own));
    expect(session?.name).toBe(own);
  });

  test("returns undefined for one that does not, rather than failing", async () => {
    // Absence is an answer, not an error. A caller asking whether a session
    // exists should not have to catch anything.
    const session = await withMultiplexer((mux) => mux.lookup("awp.no.such.session"));
    expect(session).toBeUndefined();
  });

  test("refuses an empty name instead of asking zmx what it means", async () => {
    const failure = await withMultiplexer((mux) => Effect.flip(mux.lookup("   ")));
    expect(failure.reason).toContain("no name given");
  });
});

describe("history", () => {
  test("reads the scrollback of a live session", async () => {
    const own = currentZmxSession();
    if (own === undefined) {
      return;
    }
    const history = await withMultiplexer((mux) => mux.history(own));
    expect(typeof history).toBe("string");
  });

  test("refuses an empty name", async () => {
    const failure = await withMultiplexer((mux) => Effect.flip(mux.history("")));
    expect(failure.reason).toContain("no name given");
  });
});

// kill and setLabels are deliberately not exercised against a real zmx here.
// Both change something, and the only sessions available to change are real
// ones somebody is working in. They belong in the probe, against a session it
// created itself.
