import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
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
import { IntentError, WorkspaceIntent } from "./intent";
import { Jj } from "./jj";
import * as settings from "./settings";
import { Multiplexer, type Session } from "./multiplexer";
import { type WorkspaceDeps, createWorkspace } from "./jobs/create-workspace";
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
  // The fake exists to be a Multiplexer, and a Multiplexer can now start a
  // session. Nothing under test calls it.
  start: () => Effect.void,
  send: () => Effect.void,
  kill: () => Effect.void,
  setLabels: () => Effect.void,
  history: () => Effect.succeed(""),
});

/**
 * A create-workspace kind whose services do nothing.
 *
 * Registered so the runner recognises the kind by name — `enqueue` refuses one
 * it has never heard of. What the steps do is not under test here; they have
 * their own suite, against a trace.
 */
const inert = {
  jj: {
    addWorkspace: () => Effect.void,
    forgetWorkspace: () => Effect.void,
    setBookmark: () => Effect.void,
    deleteBookmark: () => Effect.void,
  },
  mux: {
    start: () => Effect.void,
    kill: () => Effect.void,
    setLabels: () => Effect.void,
    send: () => Effect.void,
  },
  threads: { attach: () => Effect.void, detach: () => Effect.void },
  files: {
    exists: () => Effect.succeed(false),
    makeDirectory: () => Effect.void,
    remove: () => Effect.void,
  },
} as unknown as WorkspaceDeps;

const scratch = mkdtempSync(join(tmpdir(), "awp-handlers-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));
let files = 0;

type Client = RpcClient.RpcClient<
  (typeof AwpRpcs)["requests"] extends ReadonlyMap<string, infer R> ? R : never
>;

/**
 * What a test needs to vary to exercise the base a thread starts from.
 *
 * Two knobs and no more. Resolving a parent thread reads the bookmark prefix
 * out of the config and then asks jj whether that bookmark is actually there,
 * so those are exactly the two answers that decide the outcome — and both have
 * a real branch in `baseOfThread` that no other test reaches.
 */
interface Fakes {
  /** Written into a config file, because Settings reads one. */
  readonly bookmarkPrefix?: string | undefined;
  /** What `jj bookmark list` reports. Local rows only; remotes are filtered. */
  readonly bookmarks?: ReadonlyArray<string> | undefined;
}

const configFor = (fakes: Fakes): string => {
  const path = join(scratch, `config-${(files += 1)}.json`);
  writeFileSync(
    path,
    fakes.bookmarkPrefix === undefined
      ? "{}"
      : JSON.stringify({ deck: { bookmark_prefix: fakes.bookmarkPrefix } }),
  );
  return path;
};

const run = <A>(body: (rpc: Client) => Effect.Effect<A, unknown, Scope.Scope>, fakes: Fakes = {}) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const fake = yield* makeFake({ chunks: ["\u001B[2J", "ready$ "] });
      const stack = handlers.layer.pipe(
        // The two the handlers need and this suite does not exercise. Settings
        // real, because it reads a file that is not there and answers with
        // defaults — which is the honest behaviour and needs no fake. Intent
        // faked, because the real one spawns claude and takes ten seconds; the
        // model call has its own probe.
        Layer.provide(settings.layer(configFor(fakes))),
        Layer.provide(
          Layer.succeed(Jj)({
            sourceRoot: (dir: string) => Effect.succeed(`/repos/${dir.split("/").at(-1) ?? ""}`),
            bookmarks: () =>
              Effect.succeed(
                (fakes.bookmarks ?? []).map((name) => ({ name, remote: undefined, target: [] })),
              ),
          } as unknown as Jj["Service"]),
        ),
        Layer.provide(
          Layer.succeed(WorkspaceIntent)({
            resolve: (description: string) =>
              // Refuses an empty description, the way the real one does — that
              // is the failure the ordering test below leans on.
              description.trim() === ""
                ? Effect.fail(new IntentError({ reason: "nothing typed" }))
                : Effect.succeed({ name: "a-name", label: description, prompt: description }),
          }),
        ),
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
        Layer.provide(
          jobsLayer([erase(demo), erase(createWorkspace(inert))]).pipe(Layer.provide(layerMemory)),
        ),
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

/** Start a thread, attach a workspace to it, and hand back its id. */
const parentWith = (rpc: Client, project: string, workspace: string) =>
  Effect.gen(function* () {
    const made = yield* rpc.ThreadCreate({ title: "the first thing" });
    yield* rpc.ThreadAttach({ thread: made.id, member: { project, workspace } });
    return made.id;
  });

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
          member: { project: "rowan", workspace: "discounts" },
        });
        return yield* rpc.ThreadAttach({
          thread: made.id,
          member: { project: "beta", workspace: "discounts" },
        });
      }),
    );

    // The whole point of a thread: one piece of work, two checkouts.
    expect(found.title).toBe("tiered discounts");
    expect(found.members).toEqual([
      { project: "rowan", workspace: "discounts" },
      { project: "beta", workspace: "discounts" },
    ]);
  });

  it("starts a thread from a sentence, and hands back the job building it", async () => {
    const found = await run((rpc) =>
      rpc.ThreadStart({
        description: "add tiered discounts to checkout",
        project: "thicket",
        from: "/somewhere/thicket",
        base: undefined,
      }),
    );

    // The thread is titled with what the model called it, not with the raw
    // sentence — the fake here echoes the description back as the label.
    expect(found.thread.title).toBe("add tiered discounts to checkout");
    expect(found.job.kind).toBe("create-workspace");
    // Resolved first and stored on the job, which is what makes a retry re-run
    // against the same answer rather than asking the model again.
    expect(found.job.title).toContain("thicket/a-name");
  });

  it("makes no thread when the model cannot be reached", async () => {
    // Ordering, asserted: resolve, *then* create. An empty thread titled with
    // half a sentence would be litter a person has to tidy by hand.
    const before = await run((rpc) => rpc.ThreadList());
    const outcome = await run((rpc) =>
      Effect.result(
        rpc.ThreadStart({
          description: "  ",
          project: "thicket",
          from: "/somewhere/thicket",
          base: undefined,
        }),
      ),
    );
    const after = await run((rpc) => rpc.ThreadList());

    expect(Result.isFailure(outcome)).toBe(true);
    expect(after.length).toBe(before.length);
  });

  // ── where a thread starts from ───────────────────────────────────────────
  //
  // The correction that produced `baseOfThread`. The obvious answer for "start
  // from this thread" was `<name>@` — jj's revset for that workspace's
  // working-copy commit, which carries whatever is uncommitted in it right
  // now. Branching off that inherits someone's half-finished edits, which is
  // not what following on from work means. The bookmark is where the work is
  // named, and it moves when a person decides it should.

  it("branches from the parent's bookmark, not from its working copy", async () => {
    const job = await run(
      (rpc) =>
        Effect.gen(function* () {
          const parent = yield* parentWith(rpc, "thicket", "lantern");
          const started = yield* rpc.ThreadStart({
            description: "follow on from that",
            project: "thicket",
            from: "/somewhere/thicket",
            parent,
          });
          // Recorded, not merely used. The relationship is a claim about work
          // that outlives the bookmark it resolved to.
          expect(started.thread.parentId).toBe(parent);
          return started.job;
        }),
      { bookmarkPrefix: "andrew", bookmarks: ["andrew/lantern"] },
    );

    expect((job.input as { readonly base: string }).base).toBe("andrew/lantern");
  });

  // Deliberate rather than a failure. Someone with no `bookmark_prefix` set
  // has no bookmarks at all, and refusing there would make the feature
  // unavailable to them; the working copy is worse, having nothing is worse
  // still.
  it("falls back to the working copy when the bookmark is not there", async () => {
    const withoutPrefix = await run(
      (rpc) =>
        Effect.gen(function* () {
          const parent = yield* parentWith(rpc, "thicket", "lantern");
          return yield* rpc.ThreadStart({
            description: "follow on",
            project: "thicket",
            from: "/somewhere/thicket",
            parent,
          });
        }),
      {},
    );
    expect((withoutPrefix.job.input as { readonly base: string }).base).toBe("lantern@");

    // A prefix is configured, but jj has never heard of that bookmark. Asked
    // rather than assumed: the prefix says what awp *would* have named it, and
    // only jj says whether it is there. Composing it blind would fail inside
    // the job, one backoff later, in a message about the wrong thing.
    const missing = await run(
      (rpc) =>
        Effect.gen(function* () {
          const parent = yield* parentWith(rpc, "thicket", "lantern");
          return yield* rpc.ThreadStart({
            description: "follow on",
            project: "thicket",
            from: "/somewhere/thicket",
            parent,
          });
        }),
      { bookmarkPrefix: "andrew", bookmarks: ["andrew/something-else"] },
    );
    expect((missing.job.input as { readonly base: string }).base).toBe("lantern@");
  });

  it("starts from trunk when no parent was named", async () => {
    const started = await run((rpc) =>
      rpc.ThreadStart({
        description: "a fresh line of work",
        project: "thicket",
        from: "/somewhere/thicket",
      }),
    );
    expect((started.job.input as { readonly base: string }).base).toBe("trunk()");
    expect(started.thread.parentId).toBeUndefined();
  });

  // A revision is only meaningful inside one repository, so this cannot be
  // resolved — and resolving it anyway would produce a revset jj cannot find,
  // failing inside the job rather than here where it can be explained.
  it("refuses a parent whose workspace is in another project", async () => {
    const outcome = await run(
      (rpc) =>
        Effect.result(
          Effect.gen(function* () {
            const parent = yield* parentWith(rpc, "orchard", "lantern");
            return yield* rpc.ThreadStart({
              description: "follow on",
              project: "thicket",
              from: "/somewhere/thicket",
              parent,
            });
          }),
        ),
      { bookmarkPrefix: "andrew", bookmarks: ["andrew/lantern"] },
    );

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect(String((outcome.failure as { readonly reason: string }).reason)).toContain("orchard");
    }
  });

  // A thread made a moment ago has claimed nothing, so there is no work to
  // follow on from. Said out loud rather than quietly falling back to trunk,
  // which would put the new thread somewhere nobody asked for.
  it("refuses a parent that has no workspace yet", async () => {
    const outcome = await run((rpc) =>
      Effect.result(
        Effect.gen(function* () {
          const made = yield* rpc.ThreadCreate({ title: "nothing in it" });
          return yield* rpc.ThreadStart({
            description: "follow on",
            project: "thicket",
            from: "/somewhere/thicket",
            parent: made.id,
          });
        }),
      ),
    );
    expect(Result.isFailure(outcome)).toBe(true);
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
