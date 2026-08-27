import { type Job, Jobs, erase, isTerminal, layer as jobsLayer, layerMemory } from "@awp-kit/jobs";
import type { CreateWorkspace } from "@awp-kit/protocol";
import { dirname } from "node:path";
import { Context, Effect, Layer } from "effect";
import { beforeEach, describe, expect, test } from "vitest";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import { IntentError, type WorkspaceIntent } from "../intent";
import type { Settings } from "../settings";
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
    setBookmark: (_repo: string, name: string, revision: string) =>
      act(`jj.bookmark(${name}@${revision})`),
    deleteBookmark: (_repo: string, name: string) => act(`jj.unbookmark(${name})`),
  } as unknown as Jj["Service"],

  mux: {
    start: ({ name }: { readonly name: string }) => act(`zmx.start(${name})`),
    kill: (name: string) => act(`zmx.kill(${name})`),
    setLabels: (name: string, labels: Readonly<Record<string, string>>) =>
      act(`zmx.label(${name}:${labels["awp_workspace"] ?? ""})`),
    send: (name: string) => act(`zmx.send(${name})`),
  } as unknown as Multiplexer["Service"],

  threads: {
    attach: (thread: string, member: { readonly workspace: string }) =>
      act(`thread.claim(${thread}:${member.workspace})`),
    detach: (thread: string, member: { readonly workspace: string }) =>
      act(`thread.release(${thread}:${member.workspace})`),
    // Called by the naming step, to replace the raw sentence the thread was
    // created with. Its absence is what found the runner's defect hole: the
    // step threw, and the job sat at `running` forever rather than failing.
    rename: (thread: string, title: string) => act(`thread.rename(${thread}:${title})`),
    // The `thread` step checks the thread is really there before building
    // anything for it, and removes it on the way back out if it ended up
    // holding nothing.
    list: () => Effect.succeed([{ id: "20260826-aaaa" }]),
    deleteIfEmpty: (thread: string) => act(`thread.delete(${thread})`).pipe(Effect.as(true)),
  } as unknown as Threads["Service"],

  files: {
    exists: (path: string) => Effect.sync(() => present.has(path)),
    makeDirectory: (path: string) => act(`mkdir(${path})`),
    remove: (path: string) => act(`rm(${path})`),
  },

  // The naming step calls these. Most tests here hand the job a workspace name
  // already, so it short-circuits and neither is touched — which is both the
  // resume path and what keeps these tests about the order of the steps.
  intent: {
    // Fails with a real `IntentError` rather than the plain object `act`
    // raises, because the step catches it *by tag*. A fake that failed with
    // something else would prove the fallback works against an error the real
    // service cannot produce — and would go on passing if the tag were
    // renamed.
    resolve: (description: string) =>
      act(`intent(${description})`).pipe(
        Effect.mapError(() => new IntentError({ reason: "claude is not there" })),
        Effect.as({ name: "named-by-the-model", label: "Named by the model", prompt: undefined }),
      ),
  } as unknown as WorkspaceIntent["Service"],

  settings: {
    read: () => Effect.succeed({ agent: ["claude"], bookmarkPrefix: "andrew", problem: undefined }),
  } as unknown as Settings["Service"],
});

const input = (over: Partial<CreateWorkspace> = {}): CreateWorkspace => ({
  thread: "20260826-aaaa",
  project: "rowan",
  description: "add tiered discounts to checkout",
  workspace: "tiered-discounts",
  label: "Tiered discounts",
  prompt: "Add tiered discounts.",
  repo: "/repos/rowan",
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

describe("naming the workspace", () => {
  // The step that made the modal stop waiting. It is also the first step
  // anywhere to write back to its own input, so what is being checked is the
  // mechanism as much as the step: what it learns has to reach the four steps
  // after it, and has to be on the record rather than in a variable.
  test("what the model answers is used by every step after it", async () => {
    const job = await make({
      workspace: undefined,
      label: undefined,
      prompt: undefined,
      bookmark: undefined,
    });

    expect(job.status).toBe("succeeded");
    // The model was asked, once.
    expect(trace.filter((entry) => entry.startsWith("intent("))).toEqual([
      "intent(add tiered discounts to checkout)",
    ]);
    // And every later step used the answer rather than the empty input it was
    // enqueued with — a directory called `undefined` is what the alternative
    // looks like.
    expect(trace).toContain("jj.add(named-by-the-model)");
    // Matched loosely, because `sessionName` shortens a name that would not
    // fit a socket path — the exact string is that function's business and is
    // pinned by its own tests.
    expect(trace.find((entry) => entry.startsWith("zmx.start("))).toContain("named-by-the-model");
    expect(trace).toContain("zmx.label(awp.rowan.named-by-the-model.agent:named-by-the-model)");
    expect(trace).toContain("thread.claim(20260826-aaaa:named-by-the-model)");
    // Composed from the configured prefix and the name it had just decided,
    // neither of which existed at enqueue.
    expect(trace).toContain("jj.bookmark(andrew/named-by-the-model@named-by-the-model@)");
  });

  test("the answer is written back to the record, not just passed along", async () => {
    const job = await make({
      workspace: undefined,
      label: undefined,
      prompt: undefined,
      bookmark: undefined,
    });

    // The whole reason a step may return a patch. A job resumed by a restarted
    // daemon has only this record; a name held anywhere else did not happen.
    expect(job.input).toMatchObject({
      workspace: "named-by-the-model",
      label: "Named by the model",
      bookmark: "andrew/named-by-the-model",
    });
  });

  // ── the model being unreachable is not a reason to lose the work ──────
  //
  // A subprocess, twelve seconds and a network, any of which can be missing.
  // What was asked for is a workspace, and refusing the whole job because the
  // caption could not be composed loses the work over the label on it.
  test("a workspace is still made when the model cannot be reached", async () => {
    breaking.add("intent(add tiered discounts to checkout)");

    const job = await make({
      workspace: undefined,
      label: undefined,
      prompt: undefined,
      bookmark: undefined,
    });

    expect(job.status).toBe("succeeded");
    // Named from the words the person typed — four of them, made into a path
    // segment. Not a second namer: what the model adds is reading the
    // sentence, and this does not pretend to.
    expect(job.input).toMatchObject({
      workspace: "add-tiered-discounts-to",
      label: "add tiered discounts to checkout",
      bookmark: "andrew/add-tiered-discounts-to",
    });
    // And the later steps built on it, which is the part that would break if
    // the patch were merged anywhere other than the stored input.
    expect(trace).toContain("jj.add(add-tiered-discounts-to)");
    expect(trace).toContain("thread.claim(20260826-aaaa:add-tiered-discounts-to)");
    // Nothing was rolled back. A job that failed here would have unwound the
    // thread it was building for.
    expect(trace.filter((entry) => entry.startsWith("thread.delete("))).toEqual([]);
  });

  // Ten seconds and a second, different answer is what the alternative costs,
  // and every step after the first is built on the first one's answer.
  test("a job that already has a name does not ask for another", async () => {
    await make();
    expect(trace.filter((entry) => entry.startsWith("intent("))).toEqual([]);
    expect(trace).toContain("jj.add(tiered-discounts)");
  });
});

describe("making a workspace", () => {
  test("the bookmark points at the workspace's working copy, not its name", async () => {
    // `<name>@` is jj's revset for a workspace's working-copy commit. A bare
    // workspace name is not a revision, which the first end-to-end run of this
    // job established in jj's own words.
    await make();
    expect(trace).toContain("jj.bookmark(andrew/tiered-discounts@tiered-discounts@)");
  });

  test("the four steps run in order", async () => {
    const job = await make();

    expect(job.status).toBe("succeeded");
    expect(trace).toEqual([
      "mkdir(" + dirname(workspacePath("rowan", "tiered-discounts")) + ")",
      "jj.add(tiered-discounts)",
      "jj.bookmark(andrew/tiered-discounts@tiered-discounts@)",
      "zmx.start(awp.rowan.tiered-discounts.agent)",
      "zmx.label(awp.rowan.tiered-discounts.agent:tiered-discounts)",
      "thread.claim(20260826-aaaa:tiered-discounts)",
      "zmx.send(awp.rowan.tiered-discounts.agent)",
    ]);
  });

  test("the thread claims it last", async () => {
    // A workspace shows in the sidebar under its thread once claimed, so
    // claiming first would present a half-built workspace as a finished one for
    // as long as the rest took.
    await make();
    // Last but one now: briefing the agent comes after, because it is the step
    // that cannot be undone.
    expect(trace.at(-2)).toBe("thread.claim(20260826-aaaa:tiered-discounts)");
  });

  test("the session is labelled after it is started", async () => {
    // The name is shortened to fit a socket path and cannot be split back into
    // its parts, so the labels are the only unshortened truth about which
    // workspace a session belongs to. A session started and never labelled is a
    // session the sidebar cannot group.
    await make();
    const started = trace.indexOf("zmx.start(awp.rowan.tiered-discounts.agent)");
    const labelled = trace.indexOf("zmx.label(awp.rowan.tiered-discounts.agent:tiered-discounts)");

    expect(started).toBeGreaterThanOrEqual(0);
    expect(labelled).toBe(started + 1);
  });

  test("no bookmark asked for is a step that does nothing, not a step removed", async () => {
    // The step list is fixed per kind: the runner reads `done` back from the
    // store and resumes against it, so an optional bookmark cannot be an
    // optional step.
    const job = await make({ bookmark: undefined });

    expect(job.steps).toEqual([
      "thread",
      "name",
      "workspace",
      "bookmark",
      "session",
      "claim",
      "brief",
    ]);
    expect(trace.some((line) => line.startsWith("jj.bookmark"))).toBe(false);
    expect(job.status).toBe("succeeded");
  });
});

describe("when a step fails", () => {
  test("what was done is undone, backwards", async () => {
    breaking.add("zmx.start(awp.rowan.tiered-discounts.agent)");
    present.add(workspacePath("rowan", "tiered-discounts") + "/.jj");

    const job = await make();

    expect(job.status).toBe("failed");
    expect(job.cleanup).toBe("clean");
    // Backwards, and only over what completed. The session never finished, so
    // there is nothing of it to kill.
    //
    // The thread goes **last**, which is the whole reason its step is first:
    // compensation runs in reverse, so only from the front of the list can a
    // step ask "does this thread still hold anything" after everything else
    // has let go of it.
    expect(trace).toEqual([
      "mkdir(" + dirname(workspacePath("rowan", "tiered-discounts")) + ")",
      "jj.add(tiered-discounts)",
      "jj.bookmark(andrew/tiered-discounts@tiered-discounts@)",
      "zmx.start(awp.rowan.tiered-discounts.agent)!",
      "jj.unbookmark(andrew/tiered-discounts)",
      "jj.forget(tiered-discounts)",
      `rm(${workspacePath("rowan", "tiered-discounts")})`,
      "thread.delete(20260826-aaaa)",
    ]);
  });

  test("it does not try again", async () => {
    // One attempt, deliberately. Every failure this job can have is a refusal —
    // a name taken, a directory occupied — and none of them pass on their own,
    // so a retry only delays the rollback a person is waiting for.
    breaking.add("jj.bookmark(andrew/tiered-discounts@tiered-discounts@)");
    const job = await make();

    expect(job.attempts).toBe(1);
    expect(trace.filter((line) => line.startsWith("jj.add"))).toHaveLength(1);
  });

  test("an undo that fails leaves the job dirty and stops there", async () => {
    breaking.add("thread.claim(20260826-aaaa:tiered-discounts)");
    breaking.add("zmx.kill(awp.rowan.tiered-discounts.agent)");

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
    breaking.add("zmx.start(awp.rowan.tiered-discounts.agent)");
    present.add(workspacePath("rowan", "tiered-discounts") + "/.jj");

    await make();
    expect(trace).toContain(`rm(${workspacePath("rowan", "tiered-discounts")})`);
  });

  test("a directory that is not a jj workspace is left alone", async () => {
    // `present` is empty, so there is no .jj inside. Deleting a person's files
    // because a later step failed is a far worse outcome than leaving a stray
    // directory behind, and this is the only place the job could do it.
    breaking.add("zmx.start(awp.rowan.tiered-discounts.agent)");

    await make();

    expect(trace).toContain("jj.forget(tiered-discounts)");
    expect(trace.some((line) => line.startsWith("rm("))).toBe(false);
  });
});

describe("the parent directory", () => {
  test("is made before jj is asked, because jj will not", async () => {
    // Found by the first end-to-end run, not by a test: `jj workspace add`
    // creates the workspace directory but refuses when the directory above it
    // is missing. Every project's *first* workspace would have failed.
    await make();

    const parent = dirname(workspacePath("rowan", "tiered-discounts"));
    expect(trace.indexOf(`mkdir(${parent})`)).toBe(0);
    expect(trace.indexOf(`mkdir(${parent})`)).toBeLessThan(
      trace.indexOf("jj.add(tiered-discounts)"),
    );
  });
});

describe("where a workspace goes", () => {
  test("the convention the rest of awp already reads", () => {
    // `suggestedBy` in multiplexer.ts recovers a workspace's identity from
    // exactly this shape when a session carries no labels. Changing it here
    // would quietly break that.
    expect(workspacePath("rowan", "discounts")).toMatch(/\/\.awp\/workspaces\/rowan\/discounts$/u);
  });
});
