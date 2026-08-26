import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { erase, layer as jobsLayer, layerMemory } from "@awp-kit/jobs";
import { layer as dbLayer } from "@awp-kit/store";
import { AwpRpcs } from "@awp-kit/protocol";
import { Effect, Fiber, Layer, Result, type Scope, Stream } from "effect";
import type { RpcClient } from "effect/unstable/rpc";
import { RpcTest } from "effect/unstable/rpc";
import { afterAll, describe, expect, it } from "vitest";
import * as attachment from "./attachment";
import * as handlers from "./handlers";
import { Multiplexer, type Session } from "./multiplexer";
import { demo } from "./jobs/demo";
import { makeFake } from "./pty-fake";
import * as sessions from "./sessions";
import { migrations as threadMigrations, layer as threadsLayer } from "./threads";

// The contract, its handlers and the services under them — everything except
// the socket.
//
// Worth having separately from the services' own tests, because what it checks
// is the seam: that a refusal the daemon states as `AttachError` reaches the
// client as `AttachRefused`, and that the daemon's `Session` arrives as the
// wire shape. Those translations live only here and are invisible to both
// sides' tests.

const LIVE = "awp.awp.other.agent";
const DEAD = "awp.awp.finished.agent";

const session = (over: Partial<Session>): Session => ({
  name: LIVE,
  pid: 4242,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  exitCode: 0,
  created: new Date("2026-08-25T09:14:00.000Z"),
  cmd: "claude",
  labels: { "awp.kind": "agent" },
  ...over,
});

const all = [session({}), session({ name: DEAD, ended: true, exitCode: 130 })];

const fakeMux = Layer.succeed(Multiplexer, {
  list: () => Effect.succeed(all),
  lookup: (name: string) => Effect.succeed(all.find((s) => s.name === name)),
  kill: () => Effect.void,
  setLabels: () => Effect.void,
  history: () => Effect.succeed(""),
});

const scratch = mkdtempSync(join(tmpdir(), "awp-handlers-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));
let files = 0;

type Client = RpcClient.RpcClient<
  (typeof AwpRpcs)["requests"] extends ReadonlyMap<string, infer R> ? R : never
>;

const run = <A>(body: (rpc: Client) => Effect.Effect<A, unknown, Scope.Scope>) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const fake = yield* makeFake({ chunks: ["\u001B[2J", "ready$ "] });
      const stack = handlers.layer.pipe(
        // Threads on a database of their own, one file per test in a temp
        // directory. There is no memory store for threads because there is no
        // store abstraction — a thread *is* rows, and a fake would be testing
        // something the daemon does not run.
        Layer.provide(
          threadsLayer.pipe(
            Layer.provide(
              Layer.orDie(
                dbLayer(join(scratch, `threads-${(files += 1)}.sqlite`), threadMigrations),
              ),
            ),
          ),
        ),
        // The memory store, not sqlite: what is under test is the seam between
        // the contract and the runner, and a file on disk would make these
        // tests share state with each other and with the developer's daemon.
        Layer.provide(jobsLayer([erase(demo)]).pipe(Layer.provide(layerMemory))),
        Layer.provide(sessions.layer),
        Layer.provide(attachment.layer),
        Layer.provide(fake.layer),
        Layer.provide(fakeMux),
      );
      return yield* Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* RpcTest.makeClient(AwpRpcs);
          return yield* body(rpc);
        }),
      ).pipe(Effect.provide(stack));
    }),
  );

describe("the daemon over its contract", () => {
  it("reports sessions in the wire shape", async () => {
    const listed = await run((rpc) => rpc.SessionList());
    expect(listed).toHaveLength(2);
    expect(listed[0]).toMatchObject({ name: LIVE, cmd: "claude", labels: { "awp.kind": "agent" } });
    // `ended` is on the wire because listed and running are different
    // questions, and a client that conflates them attaches to a dead screen.
    expect(listed[1]).toMatchObject({ name: DEAD, ended: true, exitCode: 130 });
  });

  it("streams a session's bytes to the client unaltered", async () => {
    const chunks = await run((rpc) =>
      Stream.runCollect(Stream.take(rpc.Attach({ session: LIVE, cols: 100, rows: 30 }), 2)),
    );
    expect(chunks.join("")).toBe("\u001B[2Jready$ ");
  });

  it("turns the daemon's refusal into the client's, with the reason intact", async () => {
    const result = await run((rpc) =>
      Effect.result(Stream.runCollect(rpc.Attach({ session: DEAD, cols: 100, rows: 30 }))),
    );
    expect(Result.isFailure(result)).toBe(true);
    if (Result.isFailure(result)) {
      // AttachError on the daemon's side, AttachRefused on the client's. The
      // reason is written for a person and survives the translation, which is
      // the only way the pane can say why it did not open.
      expect(result.failure).toMatchObject({ _tag: "AttachRefused", session: DEAD });
      expect(String((result.failure as { reason: string }).reason)).toContain("ended");
    }
  });

  it("reports a write to an unattached session as SessionNotFound", async () => {
    const result = await run((rpc) => Effect.result(rpc.Write({ session: LIVE, data: "ls\r" })));
    expect(Result.isFailure(result)).toBe(true);
    if (Result.isFailure(result)) {
      expect(result.failure).toMatchObject({ _tag: "SessionNotFound", session: LIVE });
    }
  });
});

// ── jobs across the same seam ──────────────────────────────────────────────
//
// The runner's own behaviour is tested in @awp-kit/jobs against fake kinds.
// What is only visible here is the translation: that a whole record survives
// the contract, and that an id the daemon has never seen comes back as
// `JobNotFound` rather than as a crash or as a null.

describe("jobs over the contract", () => {
  it("enqueues, lists and reports a record a client can render", async () => {
    const [queued, listed] = await run((rpc) =>
      Effect.gen(function* () {
        const job = yield* rpc.JobDemo({
          pace: 1,
          failAt: undefined,
          retryable: false,
          undoFails: false,
        });
        return [job, yield* rpc.JobList()] as const;
      }),
    );

    expect(queued.kind).toBe("demo");
    expect(queued.title).toBe("a demo that works");
    // Three attempts, taken from the kind rather than from the payload: the
    // number a client shows has to be the number the runner will honour.
    expect(queued.attempts).toBe(3);
    expect(listed.map((job) => job.id)).toContain(queued.id);
  });

  it("says so when asked about a job it has never had", async () => {
    const outcome = await run((rpc) => Effect.result(rpc.JobLog({ job: "20260101-zzzz" })));

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      // As itself, with the id in it — not as a string, and not as a defect.
      expect(outcome.failure).toMatchObject({ job: "20260101-zzzz" });
    }
  });

  it("streams changes for as long as a client listens", async () => {
    const seen = await run((rpc) =>
      Effect.gen(function* () {
        // Subscribed before enqueuing, because the feed carries what happens
        // next rather than what already did.
        const changes = yield* Stream.runCollect(rpc.JobChanges().pipe(Stream.take(1))).pipe(
          Effect.forkScoped,
        );
        yield* rpc.JobDemo({ pace: 1, failAt: undefined, retryable: false, undoFails: false });
        return yield* Fiber.join(changes);
      }),
    );

    expect(seen).toHaveLength(1);
    expect(seen[0]?.kind).toBe("demo");
  });

  it("makes a thread and hands it back with the workspaces it claimed", async () => {
    const found = await run((rpc) =>
      Effect.gen(function* () {
        const made = yield* rpc.ThreadCreate({ title: "tiered discounts" });
        yield* rpc.ThreadAttach({
          thread: made.id,
          member: { project: "thicket", workspace: "discounts" },
        });
        return yield* rpc.ThreadAttach({
          thread: made.id,
          member: { project: "api", workspace: "discounts" },
        });
      }),
    );

    // The whole point of a thread: one piece of work, two checkouts.
    expect(found.title).toBe("tiered discounts");
    expect(found.members).toEqual([
      { project: "thicket", workspace: "discounts" },
      { project: "api", workspace: "discounts" },
    ]);
  });

  it("says so when asked about a thread it has never had", async () => {
    const outcome = await run((rpc) =>
      Effect.result(rpc.ThreadRename({ thread: "20260101-zzzz", title: "x" })),
    );

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      // Crosses the wire as itself. A store that cannot be written dies
      // instead — that is the daemon being broken, not a negative answer.
      expect(outcome.failure).toMatchObject({ thread: "20260101-zzzz" });
    }
  });
});
