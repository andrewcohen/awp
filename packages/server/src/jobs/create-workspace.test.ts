import { type Job, Jobs, erase, isTerminal, layer as jobsLayer, layerMemory } from "@awp-kit/jobs";
import type { CreateWorkspace } from "@awp-kit/protocol";
import { Context, Effect, Layer } from "effect";
import { beforeEach, describe, expect, test } from "vitest";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import type { Threads } from "../threads";
import {
  type WorkspaceDeps,
  createWorkspace,
  createWorkspaceRef,
  workspacePath,
} from "./create-workspace";

// What the job actually did, in order.
//
// Written against a trace rather than against the record, for the reason
// runner.test.ts gives: a record saying "succeeded" is exactly what a broken
// rollback would also produce. The trace is the evidence.
//
// The four services are fakes rather than the real jj and zmx. This is not
// avoiding a subprocess for speed — jj is tested against a real repository in
// `jj.test.ts`. It is that the thing under test here is the *order*, and above
// all the order things are undone in, which no amount of real jj would make
// more visible.

type JobsService = Context.Service.Shape<typeof Jobs>;

let trace: Array<string> = [];
/** Step names rigged to fail, by the call that should refuse. */
let breaking: Set<string> = new Set();
/** Paths `exists` should answer yes for. */
let present: Set<string> = new Set();

beforeEach(() => {
  trace = [];
  breaking = new Set();
  present = new Set();
});

const act = (what: string): Effect.Effect<void, { readonly reason: string }> =>
  Effect.suspend(() => {
    if (breaking.has(what)) {
      trace.push(`${what}!`);
      return Effect.fail({ reason: `${what} refused` });
    }
    trace.push(what);
    return Effect.void;
  });

const deps = (): WorkspaceDeps => ({
  // Only the methods this job calls. The casts widen a partial fake to the
  // whole service — a fake with every method would say the job might use them.
  jj: {
    addWorkspace: ({ name }: { readonly name: string }) => act(`jj.add(${name})`),
    forgetWorkspace: (_repo: string, name: string) => act(`jj.forget(${name})`),
    setBookmark: (_repo: string, name: string) => act(`jj.bookmark(${name})`),
    deleteBookmark: (_repo: string, name: string) => act(`jj.unbookmark(${name})`),
  } as unknown as Jj["Service"],

  mux: {
    start: ({ name }: { readonly name: string }) => act(`zmx.start(${name})`),
    kill: (name: string) => act(`zmx.kill(${name})`),
    setLabels: (name: string, labels: Readonly<Record<string, string>>) =>
      act(`zmx.label(${name}:${labels["awp_workspace"] ?? ""})`),
  } as unknown as Multiplexer["Service"],

  threads: {
    attach: (thread: string, member: { readonly workspace: string }) =>
      act(`thread.claim(${thread}:${member.workspace})`),
    detach: (thread: string, member: { readonly workspace: string }) =>
      act(`thread.release(${thread}:${member.workspace})`),
  } as unknown as Threads["Service"],

  files: {
    exists: (path: string) => Effect.sync(() => present.has(path)),
    remove: (path: string) => act(`rm(${path})`),
  },
});

const input = (over: Partial<CreateWorkspace> = {}): CreateWorkspace => ({
  thread: "20260826-aaaa",
  project: "thicket",
  workspace: "tiered-discounts",
  repo: "/repos/thicket",
  base: "main",
  bookmark: "andrew/tiered-discounts",
  agent: ["claude"],
  ...over,
});

const run = <A>(program: (jobs: JobsService) => Effect.Effect<A, unknown>): Promise<A> =>
  Effect.gen(function* () {
    const jobs = yield* Jobs;
    return yield* program(jobs);
  }).pipe(
    Effect.provide(jobsLayer([erase(createWorkspace(deps()))]).pipe(Layer.provide(layerMemory))),
    Effect.scoped,
    Effect.runPromise,
  ) as Promise<A>;

const settle = (jobs: JobsService, id: string): Effect.Effect<Job, unknown> =>
  Effect.gen(function* () {
    for (let tick = 0; tick < 4000; tick += 1) {
      const job = yield* jobs.get(id);
      if (job !== undefined && isTerminal(job.status)) {
        return job;
      }
      yield* Effect.sleep("1 millis");
    }
    return yield* Effect.die(new Error(`never settled: ${trace.join(" ")}`));
  });

const make = (over: Partial<CreateWorkspace> = {}) =>
  run((jobs) =>
    Effect.gen(function* () {
      const queued = yield* jobs.enqueue(createWorkspaceRef, input(over));
      return yield* settle(jobs, queued.id);
    }),
  );

describe("making a workspace", () => {
  test("the four steps run in order", async () => {
    const job = await make();

    expect(job.status).toBe("succeeded");
    expect(trace).toEqual([
      "jj.add(tiered-discounts)",
      "jj.bookmark(andrew/tiered-discounts)",
      "zmx.start(awp.thicket.tiered-discounts.agent)",
      "zmx.label(awp.thicket.tiered-discounts.agent:tiered-discounts)",
      "thread.claim(20260826-aaaa:tiered-discounts)",
    ]);
  });

  test("the thread claims it last", async () => {
    // A workspace shows in the sidebar under its thread once claimed, so
    // claiming first would present a half-built workspace as a finished one for
    // as long as the rest took.
    await make();
    expect(trace.at(-1)).toBe("thread.claim(20260826-aaaa:tiered-discounts)");
  });

  test("the session is labelled after it is started", async () => {
    // The name is shortened to fit a socket path and cannot be split back into
    // its parts, so the labels are the only unshortened truth about which
    // workspace a session belongs to. A session started and never labelled is a
    // session the sidebar cannot group.
    await make();
    const started = trace.indexOf("zmx.start(awp.thicket.tiered-discounts.agent)");
    const labelled = trace.indexOf(
      "zmx.label(awp.thicket.tiered-discounts.agent:tiered-discounts)",
    );

    expect(started).toBeGreaterThanOrEqual(0);
    expect(labelled).toBe(started + 1);
  });

  test("no bookmark asked for is a step that does nothing, not a step removed", async () => {
    // The step list is fixed per kind: the runner reads `done` back from the
    // store and resumes against it, so an optional bookmark cannot be an
    // optional step.
    const job = await make({ bookmark: undefined });

    expect(job.steps).toEqual(["workspace", "bookmark", "session", "claim"]);
    expect(trace.some((line) => line.startsWith("jj.bookmark"))).toBe(false);
    expect(job.status).toBe("succeeded");
  });
});

describe("when a step fails", () => {
  test("what was done is undone, backwards", async () => {
    breaking.add("zmx.start(awp.thicket.tiered-discounts.agent)");
    present.add(workspacePath("thicket", "tiered-discounts") + "/.jj");

    const job = await make();

    expect(job.status).toBe("failed");
    expect(job.cleanup).toBe("clean");
    // Backwards, and only over what completed. The session never finished, so
    // there is nothing of it to kill.
    expect(trace).toEqual([
      "jj.add(tiered-discounts)",
      "jj.bookmark(andrew/tiered-discounts)",
      "zmx.start(awp.thicket.tiered-discounts.agent)!",
      "jj.unbookmark(andrew/tiered-discounts)",
      "jj.forget(tiered-discounts)",
      `rm(${workspacePath("thicket", "tiered-discounts")})`,
    ]);
  });

  test("it does not try again", async () => {
    // One attempt, deliberately. Every failure this job can have is a refusal —
    // a name taken, a directory occupied — and none of them pass on their own,
    // so a retry only delays the rollback a person is waiting for.
    breaking.add("jj.bookmark(andrew/tiered-discounts)");
    const job = await make();

    expect(job.attempts).toBe(1);
    expect(trace.filter((line) => line.startsWith("jj.add"))).toHaveLength(1);
  });

  test("an undo that fails leaves the job dirty and stops there", async () => {
    breaking.add("thread.claim(20260826-aaaa:tiered-discounts)");
    breaking.add("zmx.kill(awp.thicket.tiered-discounts.agent)");

    const job = await make();

    expect(job.cleanup).toBe("dirty");
    // Stopped rather than pressed on: every undo assumes the ones after it
    // already ran, so once one has not, the rest are undoing a state that never
    // existed.
    expect(trace.some((line) => line.startsWith("jj.forget"))).toBe(false);
  });
});

describe("undoing the workspace", () => {
  test("the directory goes with it", async () => {
    // `jj workspace forget` does not touch the directory — jj says so in its
    // own help — so the undo has to do both, or the next attempt cannot create
    // into the directory the last one left.
    breaking.add("zmx.start(awp.thicket.tiered-discounts.agent)");
    present.add(workspacePath("thicket", "tiered-discounts") + "/.jj");

    await make();
    expect(trace).toContain(`rm(${workspacePath("thicket", "tiered-discounts")})`);
  });

  test("a directory that is not a jj workspace is left alone", async () => {
    // `present` is empty, so there is no .jj inside. Deleting a person's files
    // because a later step failed is a far worse outcome than leaving a stray
    // directory behind, and this is the only place the job could do it.
    breaking.add("zmx.start(awp.thicket.tiered-discounts.agent)");

    await make();

    expect(trace).toContain("jj.forget(tiered-discounts)");
    expect(trace.some((line) => line.startsWith("rm("))).toBe(false);
  });
});

describe("where a workspace goes", () => {
  test("the convention the rest of awp already reads", () => {
    // `suggestedBy` in multiplexer.ts recovers a workspace's identity from
    // exactly this shape when a session carries no labels. Changing it here
    // would quietly break that.
    expect(workspacePath("thicket", "discounts")).toMatch(
      /\/\.awp\/workspaces\/thicket\/discounts$/u,
    );
  });
});
