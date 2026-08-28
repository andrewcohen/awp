import { type Job, Jobs, erase, isTerminal, layer as jobsLayer, layerMemory } from "@awp-kit/jobs";
import type { ArchiveThread as ArchiveThreadInput } from "@awp-kit/protocol";
import { Context, Effect, Layer } from "effect";
import { beforeEach, describe, expect, test } from "vitest";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import type { Projects } from "../projects";
import type { Settings } from "../settings";
import type { Threads } from "../threads";
import { type ArchiveDeps, archiveThread, archiveThreadRef } from "./archive-thread";
import { workspacePath } from "./create-workspace";

// What archiving actually did, in order.
//
// Against a trace rather than a record, for the reason create-workspace.test.ts
// gives: a record saying "succeeded" is what a job that skipped its work would
// also produce. The thing under test is the order, and the guard on the
// directory — neither of which a real jj would make more visible.

type JobsService = Context.Service.Shape<typeof Jobs>;

let trace: Array<string> = [];
/** Paths `exists` answers yes for. The `.jj` guard is read off this. */
let present: Set<string> = new Set();
/** Where the job will look. Derived, not spelled — it is a home-relative path. */
const LANTERN = workspacePath("rowan", "lantern");
/** What the thread store holds. */
let members: Array<{ readonly project: string; readonly workspace: string }> = [];
/** Which projects have been imported, and so have a repository path. */
let roots: Array<{ readonly name: string; readonly root: string }> = [];
/** What `jj sourceRoot` answers per directory. Empty means the checkout is gone. */
let sourceRoots: Map<string, string> = new Map();
/** What `zmx ls` reports. */
let sessions: Array<{ readonly name: string; readonly workspace: string }> = [];
beforeEach(() => {
  trace = [];
  present = new Set([`${LANTERN}/.jj`]);
  members = [{ project: "rowan", workspace: "lantern" }];
  roots = [{ name: "rowan", root: "/repos/rowan" }];
  sourceRoots = new Map([[LANTERN, "/repos/rowan"]]);
  sessions = [
    { name: "awp.rowan.lantern.agent", workspace: "lantern" },
    { name: "awp.rowan.lantern.editor", workspace: "lantern" },
    // A session belonging to something else entirely. If this is ever killed
    // the filter is wrong, and the failure would be somebody else's terminal.
    { name: "awp.rowan.other.agent", workspace: "other" },
  ];
});

const act = (what: string): Effect.Effect<void, { readonly reason: string }> =>
  Effect.sync(() => {
    trace.push(what);
  });

const deps = (): ArchiveDeps => ({
  jj: {
    forgetWorkspace: (_repo: string, name: string) => act(`jj.forget(${name})`),
    // The plan step's first question. A directory that is not a checkout
    // fails, which is what the fallback exists for.
    sourceRoot: (dir: string) => {
      const root = sourceRoots.get(dir);
      return root === undefined
        ? Effect.fail({ reason: `no jj repo in ${dir}` })
        : Effect.succeed(root);
    },
    deleteBookmark: (_repo: string, name: string) => act(`jj.unbookmark(${name})`),
  } as unknown as Jj["Service"],

  mux: {
    list: () =>
      Effect.sync(() =>
        sessions.map((session) => ({
          name: session.name,
          labels: { awp_project: "rowan", awp_workspace: session.workspace },
        })),
      ),
    kill: (name: string) => act(`zmx.kill(${name})`),
  } as unknown as Multiplexer["Service"],

  threads: {
    list: () =>
      Effect.succeed([
        { id: "20260828-aaaa", title: "the lantern rewrite", members: [...members] },
      ]),
    // Traced with its argument, so the undo — `archived: false` — would show
    // up as a different entry rather than as the same one twice. Nothing here
    // exercises it: the archive is the last step, so its undo runs only on a
    // cancellation, which the runner's own tests cover.
    archive: (thread: string, yes: boolean) => act(`thread.archive(${thread}:${String(yes)})`),
  } as unknown as Threads["Service"],

  projects: {
    list: () => Effect.sync(() => [...roots]),
  } as unknown as Projects["Service"],

  files: {
    exists: (path: string) => Effect.sync(() => present.has(path)),
    makeDirectory: (path: string) => act(`mkdir(${path})`),
    remove: (path: string) => act(`rm(${path})`),
  },

  settings: {
    read: () =>
      Effect.succeed({
        agent: ["claude"],
        bootstrap: [],
        bookmarkPrefix: "andrew",
        problem: undefined,
      }),
  } as unknown as Settings["Service"],
});

const input = (over: Partial<ArchiveThreadInput> = {}): ArchiveThreadInput => ({
  thread: "20260828-aaaa",
  title: "the lantern rewrite",
  deleteBookmarks: false,
  ...over,
});

const run = <A>(program: (jobs: JobsService) => Effect.Effect<A, unknown>): Promise<A> =>
  Effect.gen(function* () {
    const jobs = yield* Jobs;
    return yield* program(jobs);
  }).pipe(
    Effect.provide(jobsLayer([erase(archiveThread(deps()))]).pipe(Layer.provide(layerMemory))),
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

const archive = (over: Partial<ArchiveThreadInput> = {}): Promise<Job> =>
  run((jobs) =>
    Effect.gen(function* () {
      const started = yield* jobs.enqueue(archiveThreadRef, input(over));
      return yield* settle(jobs, started.id);
    }),
  );

describe("archiving a thread", () => {
  test("kills the sessions, then takes the checkout, then archives", async () => {
    const job = await archive();

    expect(job.status).toBe("succeeded");
    expect(trace).toEqual([
      "zmx.kill(awp.rowan.lantern.agent)",
      "zmx.kill(awp.rowan.lantern.editor)",
      "jj.forget(lantern)",
      `rm(${LANTERN})`,
      "thread.archive(20260828-aaaa:true)",
    ]);
  });

  // The whole reason the archive is last. A failure above it leaves the thread
  // where a person can still see it, with the job beside it saying what
  // stopped — where archiving first would take the row out of the sidebar and
  // then fail at removing the directory, which nothing on screen would explain.
  test("archives after everything it reclaims, not before", async () => {
    await archive();
    expect(trace.at(-1)).toBe("thread.archive(20260828-aaaa:true)");
  });

  // A session belonging to another workspace must survive. Sessions are found
  // by their labels rather than by composing a name, because a workspace has
  // one per kind and the kinds are open-ended — and the cost of getting the
  // filter wrong is somebody else's terminal.
  test("kills only the sessions of the workspaces it is reclaiming", async () => {
    await archive();
    expect(trace).not.toContain("zmx.kill(awp.rowan.other.agent)");
  });

  // Forgetting does not remove the directory — jj says so in its own help — so
  // both have to happen or the disk is never reclaimed.
  test("forgets the workspace and removes its directory", async () => {
    await archive();
    expect(trace).toContain("jj.forget(lantern)");
    expect(trace).toContain(`rm(${LANTERN})`);
  });

  // The guard that makes this safe to have at all. A directory with no `.jj`
  // in it is not a workspace this made, and deleting a person's files because
  // a path looked right is far worse than leaving a stray directory.
  test("leaves a directory alone when it is not a jj workspace", async () => {
    present = new Set();
    await archive();
    expect(trace).toContain("jj.forget(lantern)");
    expect(trace).not.toContain(`rm(${LANTERN})`);
  });

  // A bookmark is a name for a commit and outlives the checkout, so keeping it
  // is what keeps the work addressable. Off unless asked for.
  test("keeps the bookmarks unless asked", async () => {
    await archive();
    expect(trace.some((one) => one.startsWith("jj.unbookmark"))).toBe(false);
  });

  test("deletes the bookmarks when asked, using the project's prefix", async () => {
    await archive({ deleteBookmarks: true });
    expect(trace).toContain("jj.unbookmark(andrew/lantern)");
  });

  // Neither source can name the repository: the checkout is gone, so
  // `sourceRoot` fails, and the project was never imported. Skipped and said
  // out loud, because reclaiming nothing is better than acting on a
  // repository that could not be named.
  test("leaves a workspace alone when nothing can name its repository", async () => {
    roots = [];
    sourceRoots = new Map();
    const job = await archive();

    expect(job.status).toBe("succeeded");
    expect(trace).toEqual(["thread.archive(20260828-aaaa:true)"]);
  });

  // The fallback, for a resumed job whose earlier attempt already removed the
  // directory — jj still holds the workspace registration that needs
  // forgetting, and only the imported row can say where.
  test("falls back to the imported project when the checkout has gone", async () => {
    sourceRoots = new Map();
    await archive();
    expect(trace).toContain("jj.forget(lantern)");
  });

  // A thread with nothing in it is only a flag. The steps still run — the list
  // has to be the same for every payload, or a restarted daemon could not
  // reproduce it from the record.
  test("archives a thread that holds nothing", async () => {
    members = [];
    const job = await archive();

    expect(job.status).toBe("succeeded");
    expect(job.done).toEqual(["plan", "sessions", "workspaces", "bookmarks", "archive"]);
  });

  // The plan is recorded rather than re-read, so a resumed job reclaims what
  // the thread held when the button was pressed.
  test("writes what it is reclaiming onto the record", async () => {
    const job = await archive();
    expect(job.input).toMatchObject({
      plan: [{ project: "rowan", workspace: "lantern", repo: "/repos/rowan" }],
    });
  });

  // The job's row in the panel names the thread, not its id. A row reading
  // `archive 20260828-wjrq` names something only this system can resolve, in
  // the one place a person looks to find out what is happening.
  test("titles itself with the thread's name", async () => {
    const job = await archive();
    expect(job.title).toBe("archive the lantern rewrite");
  });

  // A thread can be untitled, and `archive ` is not a caption.
  test("falls back to the id when the thread has no name", async () => {
    const job = await archive({ title: "  " });
    expect(job.title).toBe("archive 20260828-aaaa");
  });

  // The bug the first version shipped with: the repository was looked up in
  // `projects`, which is empty on a machine where nobody has pressed import —
  // and every workspace on this one predates that button. The thread was
  // archived and its checkout, directory and session all stayed.
  //
  //   skipping awp/test1234 — awp is not an imported project
  //   nothing to reclaim — the thread holds no workspaces
  //   archived
  //
  // The checkout answers for itself now. This fake reports no imported
  // projects at all, which is the real machine's state.
  test("reclaims a workspace whose project has never been imported", async () => {
    roots = [];
    const job = await archive();

    expect(job.status).toBe("succeeded");
    expect(trace).toContain("jj.forget(lantern)");
    expect(job.input).toMatchObject({
      plan: [{ project: "rowan", workspace: "lantern", repo: "/repos/rowan" }],
    });
  });

  test("refuses a thread that is not there", async () => {
    const job = await archive({ thread: "20260828-zzzz" });
    expect(job.status).toBe("failed");
    expect(job.error).toContain("20260828-zzzz");
    expect(trace).toEqual([]);
  });
});
