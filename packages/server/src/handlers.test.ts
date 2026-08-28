import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { erase, layer as jobsLayer, layerMemory } from "@awp-kit/jobs";
import { layer as dbLayer } from "@awp-kit/store";
import { AwpRpcs, type CommentSide, type ReviewComment } from "@awp-kit/protocol";
import { NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Fiber, Layer, Result, type Scope, Stream } from "effect";
import type { RpcClient } from "effect/unstable/rpc";
import { RpcTest } from "effect/unstable/rpc";
import { afterAll, describe, expect, it } from "vitest";
import * as attachment from "./attachment";
import { Github, GithubError, type Remark } from "./github";
import type { PullRequest } from "./github-parse";
import { layer as inboxLayer, migrations as inboxMigrations } from "./inbox-feed";
import * as handlers from "./handlers";
import { IntentError, WorkspaceIntent } from "./intent";
import { type DiffOf, Jj, JjError, type RevisionsIn } from "./jj";
import * as settings from "./settings";
import { Multiplexer, type Session } from "./multiplexer";
import { type WorkspaceDeps, createWorkspace } from "./jobs/create-workspace";
import { makeFake } from "./pty-fake";
import * as sessions from "./sessions";
import { migrations as reviewMigrations, layer as reviewsLayer } from "./reviews";
import { layer as projectsLayer, migrations as projectMigrations } from "./projects";
import * as workspaceState from "./workspace-state";
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
  busy: true,
  taskEnded: false,
  exitCode: 0,
  created: new Date("2026-08-25T09:14:00.000Z"),
  cmd: "claude",
  labels: { "awp.kind": "agent" },
  ...over,
});

const all = [session({}), session({ name: DEAD, ended: true, exitCode: 130 })];

/**
 * The listing, plus a review session when a test asks for one.
 *
 * A function of the fakes rather than a constant, because one thing the inbox
 * joins against is the *session* list — a review workspace is openable as soon
 * as its session exists, which is a step before the thread claims it.
 */
const sessionsFor = (fakes: Fakes): ReadonlyArray<Session> =>
  fakes.sessionWorkspace === undefined
    ? all
    : [...all, session({ name: `awp.awp.${fakes.sessionWorkspace}.agent` })];

const fakeMux = (fakes: Fakes) =>
  Layer.succeed(Multiplexer, {
    list: () => Effect.succeed(sessionsFor(fakes)),
    lookup: (name: string) => Effect.succeed(sessionsFor(fakes).find((s) => s.name === name)),
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
  // A review's fetch step reaches for this and nothing else here does.
  github: { fetchFork: () => Effect.void },
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
  /**
   * What `jj bookmark list` reports.
   *
   * A bare string is a local bookmark pointing nowhere in particular, which is
   * all most tests here need. The object form exists for the one question that
   * needs targets: what `trunk()` is called, which is answered by matching the
   * commit it resolves to against this list.
   */
  readonly bookmarks?:
    | ReadonlyArray<
        string | { readonly name: string; readonly remote?: string; readonly target?: string }
      >
    | undefined;
  /** The commit `trunk()` resolves to, for the label question above. */
  readonly trunkCommit?: string | undefined;
  /**
   * Refuse any revset mentioning `trunk()`, the way jj does when it cannot
   * settle on one. The only branch in `Revisions` a test can reach.
   */
  readonly noTrunk?: boolean | undefined;
  /** What `gh pr list` reports, for every repository asked about. */
  readonly prs?: ReadonlyArray<PullRequest> | undefined;
  /** Who `gh` is signed in as, or absent for nobody. */
  readonly viewer?: string | undefined;
  /** A repository root with no GitHub remote, which must be skipped silently. */
  readonly offGithub?: string | undefined;
  /** The workspace the live session belongs to. Default is the fixture's own. */
  readonly sessionWorkspace?: string | undefined;
  /** False when a checkout does not contain its pull request's head. */
  readonly contains?: boolean | undefined;
  /** True when the working copy has uncommitted work in it. */
  readonly dirty?: boolean | undefined;
  /** What the listing had to give up, if a test is about that. */
  readonly degraded?: string | undefined;
  /** The pull request description the detail call answers with. */
  readonly body?: string | undefined;
  /** What has already been said on the pull request. */
  readonly remarks?: ReadonlyArray<Remark> | undefined;
}

/** A pull request as `gh` would have answered, with the dull fields filled. */
export const pr = (over: Partial<PullRequest>): PullRequest => ({
  number: 1,
  headRef: "feature",
  headOid: "abc",
  baseRef: "main",
  title: "a change",
  author: "someone",
  url: "https://example.invalid/pull/1",
  draft: false,
  ci: "passing",
  review: "none",
  mergeState: "clean",
  labels: [],
  requested: [],
  requestedTeams: [],
  reviewers: [],
  hasReviewComments: false,
  fork: undefined,
  ...over,
});

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
                (fakes.bookmarks ?? []).map((entry) =>
                  typeof entry === "string"
                    ? { name: entry, remote: undefined, target: undefined }
                    : { name: entry.name, remote: entry.remote, target: entry.target },
                ),
              ),
            fetch: () => Effect.void,
            importGit: () => Effect.void,
            // Answers with the revset it was handed, as the description of its
            // one row. What is under test is which revset the handler chose,
            // and a fake that returned plausible commits would hide it.
            revisions: ({ revset, limit }: RevisionsIn) =>
              // Four revsets this fake answers about, and each is a different
              // test's knob. Ordered so the two specific ones are matched before
              // the general fallback.
              //
              //   present(<oid>) & ::@   does this checkout contain the head —
              //                          an empty answer is what `moved` is
              //   @                      the working copy, whose `empty` is what
              //                          a stale-checkout prompt reads
              //   trunk()                the base a thread starts from
              //   anything else          echoed back, so a test can see which
              //                          revset the handler chose
              revset.startsWith("present(")
                ? Effect.succeed(fakes.contains === false ? [] : [revision(revset)])
                : revset === "@"
                  ? Effect.succeed([
                      { ...revision(`${revset} limit ${limit}`), empty: fakes.dirty !== true },
                    ])
                  : fakes.noTrunk === true && revset.includes("trunk()")
                    ? Effect.fail(
                        new JjError({
                          op: "list revisions",
                          reason: "Revset `trunk()` is ambiguous",
                        }),
                      )
                    : Effect.succeed([
                        {
                          ...revision(`${revset} limit ${limit}`),
                          // The commit `trunk()` sits on, when a test says. Every
                          // other revset gets the placeholder, which matches no
                          // bookmark and so leaves the label at its fallback.
                          commitId:
                            revset === "trunk()" && fakes.trunkCommit !== undefined
                              ? fakes.trunkCommit
                              : "bbb",
                        },
                      ]),
            // Likewise: the patch it hands back is the request it was given, so
            // a test can assert on the snapshot decision the handler made.
            diff: (options: DiffOf) => Effect.succeed(JSON.stringify(options)),
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
        // Threads, reviews and projects on a database of their own, one file
        // per test in a temp directory. There is no memory store for any of
        // them because there is no store abstraction — a thread *is* rows, and
        // a fake would be testing something the daemon does not run.
        //
        // One database for both, and both sets of migrations on it, because
        // that is what the daemon does: a review names a workspace by the same
        // `(project, workspace)` pair a thread claims it by, and separating
        // them here would hide any future statement that joins the two.
        Layer.provide(
          // The inbox feed joins this group because its cache is rows now too —
          // and it goes on the same connection for the reason the daemon uses
          // one file: a listing read from disk and a thread that claims the
          // workspace it names are two halves of one answer.
          Layer.mergeAll(threadsLayer, reviewsLayer, projectsLayer, inboxLayer).pipe(
            Layer.provide(
              Layer.orDie(
                dbLayer(join(scratch, `stores-${(files += 1)}.sqlite`), [
                  ...threadMigrations,
                  ...reviewMigrations,
                  ...projectMigrations,
                  ...inboxMigrations,
                ]),
              ),
            ),
          ),
        ),
        // The memory store, not sqlite: what is under test is the seam between
        // the contract and the runner, and a file on disk would make these
        // tests share state with each other and with the developer's daemon.
        Layer.provide(jobsLayer([erase(createWorkspace(inert))]).pipe(Layer.provide(layerMemory))),
        // A fake `gh`, and the *real* inbox feed over it — provided with the
        // stores below, because its cache is rows. The feed is where the cache,
        // the per-project failure and the assembly live, so faking it instead
        // would leave the whole of `InboxList` untested: what the fake has to
        // stand in for is the subprocess, and nothing above it.
        Layer.provide(
          Layer.succeed(Github)({
            pullRequests: () => Effect.succeed({ prs: fakes.prs ?? [], degraded: fakes.degraded }),
            // The detail call, answered from the same fixtures with the fields
            // only it has. A test that cares about the body says so.
            pullRequest: (_repo: string, number: number) =>
              Effect.succeed(
                (fakes.prs ?? [])
                  .filter((one) => one.number === number)
                  .map((one) => ({
                    ...one,
                    body: fakes.body ?? "",
                    state: "open",
                    hasReviewComments: one.hasReviewComments,
                    remarks: fakes.remarks ?? [],
                    additions: 0,
                    deletions: 0,
                    files: 0,
                  }))[0],
              ),
            // Every project is on GitHub unless a test names one that is not.
            // The skip is what stops a vault of notes producing a red sentence
            // on every refresh — see `onKnownHost`.
            isGithub: (repo: string) => Effect.succeed(repo !== (fakes.offGithub ?? "")),
            viewer: () =>
              fakes.viewer === undefined
                ? Effect.fail(
                    new GithubError({ op: "read the gh login", reason: "gh is not signed in" }),
                  )
                : Effect.succeed({ login: fakes.viewer, teams: [] }),
            repository: () => Effect.succeed({ owner: "acme", repo: "widgets" }),
            fetchFork: () => Effect.void,
          }),
        ),
        Layer.provide(sessions.layer),
        // Pointed at a file that is not there, which answers with an empty
        // table — the honest state for a machine that has only ever run
        // amoeba, and the one this suite is about. `workspace-state.test.ts`
        // is where the contents matter.
        //
        // Before the file system below it, not after: each `Layer.provide`
        // feeds everything already in the pipe, so a layer with requirements
        // has to be added before the layer that satisfies them.
        Layer.provide(workspaceState.layer(join(scratch, "no-such-state.json"))),
        // The real one, and it is never read here: WorkspaceChanges is the only
        // handler that watches anything and this suite calls no stream that
        // does. A fake would be a second implementation of a service whose
        // whole behaviour is the operating system's.
        Layer.provide(NodeFileSystem.layer),
        // Path, for the walk `ProjectCandidates` does. Real for the same reason
        // the file system is: it is the operating system's answer, and a fake
        // would be a second implementation of `join`.
        Layer.provide(NodePath.layer),
        Layer.provide(attachment.layer),
        Layer.provide(fake.layer),
        Layer.provide(fakeMux(fakes)),
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

describe("the diff a workspace is asked for", () => {
  it("asks for the working copy and everything since the main line", async () => {
    const [only] = await run((rpc) => rpc.Revisions({ from: "/w/rowan" }));

    // `@` is named on its own beside `trunk()..@`, so a workspace sitting on
    // trunk with nothing done in it is a stack of one rather than nothing.
    expect(only?.description).toBe("@ | trunk()..@ limit 50");
  });

  it("takes the client's limit, because the client is what has to draw them", async () => {
    const [only] = await run((rpc) => rpc.Revisions({ from: "/w/rowan", limit: 5 }));

    expect(only?.description).toBe("@ | trunk()..@ limit 5");
  });

  it("drops the trunk when the revset will not resolve, rather than failing", async () => {
    const [only] = await run((rpc) => rpc.Revisions({ from: "/w/rowan" }), { noTrunk: true });

    // A repository whose `trunk()` is ambiguous still has a working copy, and
    // an error about the revset would read as an error about the repository.
    expect(only?.description).toBe("@ limit 1");
  });

  it("snapshots the working copy when no revision was named", async () => {
    const answer = await run((rpc) => rpc.Diff({ from: "/w/rowan" }));

    // The one read in the daemon allowed to write, and the reason the panel is
    // not permanently empty: an agent edits files and runs no jj command, so
    // without the snapshot there is nothing to diff. See `Diff` in jj.ts.
    expect(JSON.parse(answer.patch)).toEqual({
      dir: "/w/rowan",
      revision: "@",
      snapshot: true,
    });
    expect(answer.revision).toBe("@");
  });

  it("treats an explicit @ as the same request, not as a revision", async () => {
    // Otherwise which spelling was used decides whether the answer is current,
    // which is a difference nobody could see until it was wrong.
    const answer = await run((rpc) => rpc.Diff({ from: "/w/rowan", revision: "@" }));

    expect(JSON.parse(answer.patch).snapshot).toBe(true);
  });

  it("reads a named revision without touching the working copy", async () => {
    const answer = await run((rpc) => rpc.Diff({ from: "/w/rowan", revision: "kmnpqrs" }));

    // History does not move, so a snapshot there would be a write for nothing.
    expect(JSON.parse(answer.patch)).toEqual({
      dir: "/w/rowan",
      revision: "kmnpqrs",
      snapshot: false,
    });
    // Echoed, so a client can drop a reply for a commit it has moved off.
    expect(answer.revision).toBe("kmnpqrs");
  });
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
        const job = yield* rpc.WorkspaceCreate({
          thread: "20260101-aaaa",
          project: "thicket",
          description: "a thing to do",
          workspace: "lantern",
          label: "a thing",
          repo: "/repos/thicket",
          agent: ["sh"],
        });
        return [job, yield* rpc.JobList()] as const;
      }),
    );

    expect(queued.kind).toBe("create-workspace");
    expect(queued.title).toContain("a thing to do");
    // Taken from the kind rather than from the payload: the number a client
    // shows has to be the number the runner will honour. One, because every
    // failure this job has is a refusal and none pass on their own.
    expect(queued.attempts).toBe(1);
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
        // A pause between forking and enqueuing, and it is not padding. The
        // feed is a sliding PubSub, so a subscriber sees what is published
        // *after* it subscribes — and `forkScoped` returns before the fiber
        // has got as far as subscribing. Against the fake dependencies this
        // job finishes in well under a millisecond, so without this the whole
        // job can come and go inside that gap and the stream waits forever for
        // a record that has already been and gone.
        //
        // The demo kind hid this: its steps slept, so it was still running by
        // the time anyone was listening.
        yield* Effect.sleep("50 millis");
        yield* rpc.WorkspaceCreate({
          thread: "20260101-aaaa",
          project: "thicket",
          description: "a thing to do",
          workspace: "lantern",
          label: "a thing",
          repo: "/repos/thicket",
          agent: ["sh"],
        });
        return yield* Fiber.join(changes);
      }),
    );

    expect(seen).toHaveLength(1);
    expect(seen[0]?.kind).toBe("create-workspace");
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

    // Titled with what was typed. The model has not been asked yet — that is
    // the job's first step — so this is the best title that exists, and the
    // job renames the thread once it has a better one.
    expect(found.thread.title).toBe("add tiered discounts to checkout");
    expect(found.job.kind).toBe("create-workspace");
    expect(found.job.title).toContain("add tiered discounts to checkout");
    // The name is not on the input yet, and that is the point: this call no
    // longer waits ten seconds for one.
    expect((found.job.input as { readonly workspace?: string }).workspace).toBeUndefined();
  });

  it("makes no thread when nothing was typed", async () => {
    // This used to assert an ordering — resolve, then create — because the
    // model was called here and refused an empty sentence. Naming moved into
    // the job, so the refusal moved too: it is now a check on the way in,
    // which is cheaper and says something a person can act on. What has to
    // stay true either way is that a rejected start leaves nothing behind.
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

  // The picker's whole content. It used to be a list of *threads*, which was
  // wrong in a way only use showed: most workspaces on a real machine predate
  // threads and belong to none, so the list came up empty exactly when someone
  // was standing in a branch they wanted to continue from.
  it("offers the main line and every local bookmark", async () => {
    const offered = await run((rpc) => rpc.ThreadBases({ from: "/somewhere/thicket" }), {
      bookmarkPrefix: "andrew",
      bookmarks: ["andrew/lantern", "main", "andrew/orchard"],
    });

    // Named for the place, not the method. With no bookmark sitting on the
    // commit `trunk()` resolves to, there is nothing to name it after.
    expect(offered[0]).toEqual({ revset: "trunk()", label: "main line", workspace: undefined });
    expect(offered.slice(1).map((entry) => entry.revset)).toEqual([
      "andrew/lantern",
      "andrew/orchard",
      "main",
    ]);
    // The workspace behind a bookmark, recovered from the prefix. It is what
    // lets cmd+shift+N start on the branch a person is in, and what records
    // which thread the new one followed from.
    expect(offered.find((entry) => entry.revset === "andrew/lantern")?.workspace).toBe("lantern");
    // Not everything prefixed is awp's, and nothing here pretends otherwise:
    // a bookmark outside the prefix names no workspace at all.
    expect(offered.find((entry) => entry.revset === "main")?.workspace).toBeUndefined();
  });

  // "trunk" names the *method* — jj's alias for the remote's default bookmark,
  // then main, then master. A person branching from it wants the name they
  // would say out loud, so the row is labelled with the bookmark that is
  // actually there.
  it("labels the main line with the bookmark it resolves to", async () => {
    const offered = await run((rpc) => rpc.ThreadBases({ from: "/somewhere/thicket" }), {
      trunkCommit: "9f239c56",
      bookmarks: [{ name: "main", target: "9f239c56" }, "andrew/lantern"],
    });

    expect(offered[0]?.label).toBe("main");
    // And it appears once. The bookmark trunk() resolved to would otherwise be
    // listed again under its own revset, so one name would name two rows.
    expect(offered.filter((entry) => entry.label === "main")).toHaveLength(1);
    // The robust revset survives — it is what keeps working when the bookmark
    // is moved or renamed.
    expect(offered[0]?.revset).toBe("trunk()");
  });

  it("spells a remote main line the way jj does", async () => {
    // The ordinary case on this machine, where local `main` is behind the
    // remote by a fetch: trunk() lands on the remote row, and calling that
    // `main` would claim a commit the local bookmark is not on.
    //
    //   trunk()                 9f239c56
    //   main  remote origin  →  9f239c56    ← the match
    //   main  local          →  158b02fe    behind
    const offered = await run((rpc) => rpc.ThreadBases({ from: "/somewhere/thicket" }), {
      trunkCommit: "9f239c56",
      bookmarks: [
        { name: "main", remote: "origin", target: "9f239c56" },
        { name: "main", target: "158b02fe" },
      ],
    });

    expect(offered[0]?.label).toBe("main@origin");
    // The local `main` is a different place and is still offered as itself.
    expect(offered.slice(1).map((entry) => entry.label)).toEqual(["main"]);
  });

  it("prefers the local name when both sit on the same commit", async () => {
    // A local name is what a person types, so it wins over the remote spelling
    // when the two agree — which is what a repository looks like just after a
    // fetch.
    const offered = await run((rpc) => rpc.ThreadBases({ from: "/somewhere/thicket" }), {
      trunkCommit: "9f239c56",
      bookmarks: [
        { name: "main", remote: "origin", target: "9f239c56" },
        { name: "main", target: "9f239c56" },
      ],
    });

    expect(offered[0]?.label).toBe("main");
    expect(offered).toHaveLength(1);
  });

  it("records the thread a chosen base belongs to, and shrugs when it has none", async () => {
    const followed = await run(
      (rpc) =>
        Effect.gen(function* () {
          const parent = yield* parentWith(rpc, "thicket", "lantern");
          const started = yield* rpc.ThreadStart({
            description: "follow on",
            project: "thicket",
            from: "/somewhere/thicket",
            base: "andrew/lantern",
          });
          return { parent, started };
        }),
      { bookmarkPrefix: "andrew", bookmarks: ["andrew/lantern"] },
    );

    expect(followed.started.thread.parentId).toBe(followed.parent);
    expect((followed.started.job.input as { readonly base: string }).base).toBe("andrew/lantern");

    // The case that used to be impossible. A bookmark no thread has claimed is
    // a perfectly good base; it simply records no lineage.
    const loose = await run(
      (rpc) =>
        rpc.ThreadStart({
          description: "off a branch nobody owns",
          project: "thicket",
          from: "/somewhere/thicket",
          base: "main",
        }),
      { bookmarkPrefix: "andrew", bookmarks: ["main"] },
    );

    expect(loose.thread.parentId).toBeUndefined();
    expect((loose.job.input as { readonly base: string }).base).toBe("main");
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

// ── review comments ────────────────────────────────────────────────────────

/** A comment, named for where it points. The body is the only thing that varies. */
/** One revision row, described by the revset that asked for it. */
const revision = (revset: string) => ({
  changeId: "aaa",
  commitId: "bbb",
  description: revset,
  author: "someone",
  authored: undefined,
  empty: false,
  workingCopy: true,
  bookmarks: [] as ReadonlyArray<string>,
});

const at = (
  path: string,
  line: number,
  body: string,
  side: CommentSide = "additions",
  endLine: number = line,
): ReviewComment => ({
  id: `${path}:${String(line)}`,
  project: "thicket",
  workspace: "lantern",
  revision: "vtknsnwv",
  path,
  side,
  line,
  body,
  endLine,
  author: "human",
  kind: "comment",
  text: undefined,
  createdAt: new Date("2026-08-27T09:00:00.000Z"),
  sentAt: undefined,
});

describe("reviewPrompt", () => {
  it("groups by file and orders lines within one", () => {
    // Six comments across three files is three pieces of work; interleaved it
    // is six, and an agent given a flat list opens the same file three times.
    const prompt = handlers.reviewPrompt([
      at("src/router.ts", 90, "and this"),
      at("src/app.tsx", 12, "here"),
      at("src/router.ts", 42, "this branch never runs"),
    ]);

    expect(prompt).toBe(
      [
        "Review feedback — 3 comments on vtknsnwv:",
        "",
        "- src/app.tsx:12",
        "  here",
        "",
        "- src/router.ts:42",
        "  this branch never runs",
        "- src/router.ts:90",
        "  and this",
      ].join("\n"),
    );
  });

  it("writes a range as path:first-last, and one line as path:line", () => {
    // The spelling an editor, a stack trace and a GitHub link all use, so it
    // needs no explaining to whatever reads the prompt. The single-line form is
    // asserted in the same test because the interesting property is that the
    // two are told apart at all — a range renderer that always wrote `12-12`
    // would pass a test that only looked at blocks.
    const prompt = handlers.reviewPrompt([
      at("a.ts", 12, "the whole condition", "additions", 18),
      at("a.ts", 40, "just this one"),
    ]);

    expect(prompt).toContain("- a.ts:12-18");
    expect(prompt).toContain("- a.ts:40\n");
    expect(prompt).not.toContain("40-40");
  });

  it("orders a range by where it starts", () => {
    // Two comments whose ranges overlap are ordered by their first line, which
    // is the order they appear on screen. Ordering by the last would put a
    // comment about lines 10-90 after one about 20-21.
    const prompt = handlers.reviewPrompt([
      at("a.ts", 20, "inner", "additions", 21),
      at("a.ts", 10, "outer", "additions", 90),
    ]);

    expect(prompt.indexOf("outer")).toBeLessThan(prompt.indexOf("inner"));
  });

  it("says which revision the comments are against", () => {
    // Without it `Diff.tsx:71` is an instruction to look at the working copy,
    // which is what an agent will do — and a comment made against a commit
    // three back names a line that has since moved. The failure is silent: the
    // agent edits the wrong line, or says the comment makes no sense.
    // `@` is spelled out rather than passed through: it means something to jj
    // and nothing to a reader who is not standing in this repository.
    const working = handlers.reviewPrompt([{ ...at("a.ts", 1, "x"), revision: "@" }]);
    expect(working).toContain("on the working copy");
    expect(working).not.toContain("@");

    // A change id is a name, and goes through as one.
    const commit = handlers.reviewPrompt([at("a.ts", 1, "x")]);
    expect(commit).toContain("on vtknsnwv");
  });

  it("names the side only for a removed line", () => {
    // Almost every comment is on a line being added or kept. Saying "on the
    // added line" against all of them is a phrase repeated down the whole
    // prompt; saying it for the rare case is what makes it carry information.
    const additions = handlers.reviewPrompt([at("a.ts", 1, "x")]);
    const deletions = handlers.reviewPrompt([at("a.ts", 1, "x", "deletions")]);

    expect(additions).not.toContain("line)");
    expect(deletions).toContain("- a.ts:1 (on the removed line)");
  });

  it("counts in words a person would use", () => {
    expect(handlers.reviewPrompt([at("a.ts", 1, "x")])).toContain("— 1 comment on");
    expect(handlers.reviewPrompt([at("a.ts", 1, "x"), at("a.ts", 2, "y")])).toContain(
      "— 2 comments on",
    );
  });
});

describe("ReviewSend", () => {
  it("marks nothing when there is no agent to tell", async () => {
    // The ordering that matters. The session is resolved before anything is
    // marked, so a workspace whose agent has ended keeps its drafts — marking
    // first would lose a review to a delivery that never happened, and it
    // would look delivered afterwards.
    const outcome = await run((rpc) =>
      Effect.gen(function* () {
        yield* rpc.ReviewAdd({
          project: "thicket",
          workspace: "lantern",
          revision: "vtknsnwv",
          path: "src/router.ts",
          side: "additions",
          line: 42,
          endLine: 42,
          body: "this branch never runs",
        });
        const failed = yield* Effect.result(
          rpc.ReviewSend({ project: "thicket", workspace: "lantern" }),
        );
        const after = yield* rpc.ReviewList({ project: "thicket", workspace: "lantern" });
        return { failed, after };
      }),
    );

    expect(Result.isFailure(outcome.failed)).toBe(true);
    expect(outcome.after).toHaveLength(1);
    // Still a draft, which is the whole assertion.
    expect(outcome.after[0]?.sentAt).toBeUndefined();
  });

  it("keeps a comment as written and always as a draft", async () => {
    // Nothing on the contract can create a comment the agent has already been
    // told about, which is what makes `sentAt` trustworthy as "it heard this".
    const got = await run((rpc) =>
      rpc.ReviewAdd({
        project: "thicket",
        workspace: "lantern",
        revision: "vtknsnwv",
        path: "src/router.ts",
        side: "deletions",
        line: 7,
        endLine: 9,
        body: "  trailing space is deliberate  ",
      }),
    );

    expect(got.sentAt).toBeUndefined();
    expect(got.side).toBe("deletions");
    // The range survives the round trip. Asserted here rather than in its own
    // test because this is the only place a comment goes out over the wire and
    // comes back, which is where a field silently dropped from the schema would
    // show up.
    expect([got.line, got.endLine]).toStrictEqual([7, 9]);
    // Stored verbatim. Trimming happens where the prompt is composed, so the
    // panel can still show what was typed.
    expect(got.body).toBe("  trailing space is deliberate  ");
  });
});

describe("notePrompt", () => {
  const note = {
    url: "https://example.test/pricing",
    selector: "main > section:nth-of-type(2) > button",
    label: "button.cta",
    text: "Start free trial",
    body: "  this wraps onto two lines under 400px  ",
  };

  it("puts the file above the selector when the page said which file", () => {
    // The useful ordering. StyleX has already written down the file and line,
    // so the agent gets somewhere to open rather than a selector it would have
    // to go looking for. The selector stays for pages that have nothing else.
    const rich = handlers.notePrompt({
      ...note,
      react: "Accessory > Tabs.Tab",
      source: "amoeba:src/renderer/Accessory.tsx:137",
    });

    expect(rich.indexOf("styles: ")).toBeLessThan(rich.indexOf("selector: "));
    expect(rich).toContain("components: Accessory > Tabs.Tab");
  });

  it("leaves both out for a page that is neither React nor ours", () => {
    const plain = handlers.notePrompt(note);
    expect(plain).not.toContain("styles:");
    expect(plain).not.toContain("components:");
  });

  it("says where, which, what it said, and then what is wrong", () => {
    // The order is the whole shape. Everything above the remark is address; a
    // reader given the complaint first has to hold it while parsing the
    // location, and an agent reads top to bottom the same way.
    expect(handlers.notePrompt(note)).toBe(
      [
        "— a note about an element on a page",
        "page: https://example.test/pricing",
        "element: button.cta",
        "selector: main > section:nth-of-type(2) > button",
        "text: Start free trial",
        "",
        "this wraps onto two lines under 400px",
      ].join("\n"),
    );
  });

  it("leaves the text line out when the element had none", () => {
    // An icon button says nothing, and a line reading `text:` with nothing
    // after it is a line about nothing.
    const quiet = handlers.notePrompt({ ...note, text: "   " });
    expect(quiet).not.toContain("text:");
    expect(quiet).toContain("selector: ");
  });

  it("caps what the page said, and marks the cut", () => {
    // The page is a stranger and this is the only field it authored. Pointing
    // at <body> must not paste a whole document into somebody's terminal.
    const long = handlers.notePrompt({ ...note, text: "word ".repeat(500) });
    const line = long.split("\n").find((one) => one.startsWith("text: "));
    expect(line?.length).toBe("text: ".length + 240);
    expect(line?.endsWith("…")).toBe(true);
  });

  it("collapses the whitespace a document is full of", () => {
    // innerText off a real page arrives with newlines and runs of spaces in
    // it. Left alone they break the one-line-per-field shape above.
    expect(handlers.notePrompt({ ...note, text: "Start\n\n   free   trial" })).toContain(
      "text: Start free trial",
    );
  });
});

describe("NoteSend", () => {
  const note = {
    url: "https://example.test/pricing",
    selector: "#save",
    label: "button#save",
    text: "Save",
    body: "this is unreachable at 400px",
  };

  it("types the note at the workspace's agent and answers with what it said", async () => {
    // The reply is the prompt, for the same reason ReviewSent carries one: it
    // is the only way to know what an agent was actually told without scrolling
    // its terminal back.
    const prompt = await run((rpc) => rpc.NoteSend({ project: "awp", workspace: "other", note }));

    expect(prompt).toBe(handlers.notePrompt(note));
    expect(prompt).toContain("selector: #save");
  });

  it("refuses when the workspace's agent has ended", async () => {
    // An ended session is in the listing and is not somewhere to type. Nothing
    // is marked either way — a page note has no draft state — so what this
    // protects is the composer: the remark stays on screen, in front of someone
    // still looking at the element it is about.
    const outcome = await run((rpc) =>
      Effect.result(rpc.NoteSend({ project: "awp", workspace: "finished", note })),
    );

    expect(Result.isFailure(outcome)).toBe(true);
  });

  it("refuses when there is no session at all", async () => {
    const outcome = await run((rpc) =>
      Effect.result(rpc.NoteSend({ project: "thicket", workspace: "lantern", note })),
    );

    expect(Result.isFailure(outcome)).toBe(true);
  });
});

describe("projects over the contract", () => {
  /** A directory with a `.jj` in it, and one nested inside without. */
  const repo = (name: string): { readonly root: string; readonly inside: string } => {
    // The counter goes on the directory *above*, not in the name: the name is
    // the project's identity, and `repo-3-thicket` would be what got imported.
    const root = join(scratch, `repos-${(files += 1)}`, name);
    mkdirSync(join(root, ".jj"), { recursive: true });
    const inside = join(root, "src", "deep");
    mkdirSync(inside, { recursive: true });
    return { root, inside };
  };

  it("walks up from a directory inside the project", async () => {
    const { inside } = repo("thicket");
    const made = await run((rpc) => rpc.ProjectImport({ path: inside }));
    // `jj -R <dir> root` does *not* walk up — `-R` names a repository exactly
    // — so without the walk this is a refusal about a directory that is
    // plainly inside a repository. The probe found that; this pins it.
    expect(made.name).toBe("thicket");
  });

  it("still resolves through sourceRoot after the walk", async () => {
    const { root } = repo("thicket");
    const made = await run((rpc) => rpc.ProjectImport({ path: root }));
    // The fake answers `/repos/<basename>`, so a root that came back unchanged
    // would mean the walk had replaced the resolution rather than preceded it
    // — and a secondary workspace would then be imported as a project.
    expect(made.root).toMatch(/^\/repos\//u);
  });

  it("an empty path is refused before anything is walked", async () => {
    // It would otherwise walk up from the daemon's own working directory and
    // find *this* repository, which is the worst available success.
    const failed = await run((rpc) => rpc.ProjectImport({ path: "   " }).pipe(Effect.flip));
    expect(failed).toMatchObject({ reason: "no path given" });
  });

  it("a relative path is refused by name", async () => {
    const failed = await run((rpc) =>
      rpc.ProjectImport({ path: "code/thicket" }).pipe(Effect.flip),
    );
    expect(failed).toMatchObject({ reason: "give a full path" });
  });

  it("a path with no repository above it says so", async () => {
    const nowhere = join(scratch, `bare-${(files += 1)}`);
    mkdirSync(nowhere, { recursive: true });
    const failed = await run((rpc) => rpc.ProjectImport({ path: nowhere }).pipe(Effect.flip));
    expect(failed).toMatchObject({ reason: expect.stringContaining("no jj repository") });
  });

  it("the list holds imported projects and the ones sessions imply", async () => {
    const { root } = repo("thicket");
    const list = await run((rpc) =>
      Effect.gen(function* () {
        yield* rpc.ProjectImport({ path: root });
        return yield* rpc.ProjectList();
      }),
    );
    const named = list.map((one) => one.name);
    expect(named).toContain("thicket");
    // The fixture's sessions are `awp.awp.<workspace>.<kind>`, so the project
    // they imply is `awp` — derived, with no import behind it.
    expect(named).toContain("awp");
    expect(list.find((one) => one.name === "awp")?.importedAt).toBeUndefined();
    expect(list.find((one) => one.name === "thicket")?.importedAt).toBeInstanceOf(Date);
  });

  it("importing the same repository twice is not an error", async () => {
    const { root, inside } = repo("thicket");
    const twice = await run((rpc) =>
      Effect.gen(function* () {
        const first = yield* rpc.ProjectImport({ path: root });
        // The same repository named from inside it — the case a person makes
        // by clicking a row twice, and it must not be told off.
        const again = yield* rpc.ProjectImport({ path: inside });
        return [first, again];
      }),
    );
    expect(twice[0]).toEqual(twice[1]);
    // The collision that *is* refused — two roots sharing a basename — cannot
    // be reached from here: the fake jj answers `/repos/<basename>`, so two
    // roots with one basename is not a state it can produce. It is proved
    // against the real store in projects.test.ts instead.
  });

  it("forgetting says whether there was anything to forget", async () => {
    const { root } = repo("thicket");
    const answers = await run((rpc) =>
      Effect.gen(function* () {
        yield* rpc.ProjectImport({ path: root });
        const first = yield* rpc.ProjectForget({ name: "thicket" });
        const again = yield* rpc.ProjectForget({ name: "thicket" });
        return [first, again];
      }),
    );
    expect(answers).toEqual([true, false]);
  });

  it("no configured roots is an empty candidate list, not a failure", async () => {
    expect(await run((rpc) => rpc.ProjectCandidates())).toEqual([]);
  });
});

// ── the inbox over the contract ────────────────────────────────────────────
//
// What only this suite can check: the join between GitHub's answer and awp's
// own records. The bucket rules and the ordering are pure and live in
// `inbox.test.ts`; what is here is the seam — that a derived project is asked
// about at all, that a thread member called `pr-<n>` is found and reported as
// the row's workspace, and that one repository's failure keeps the others.
describe("InboxList", () => {
  it("sections the pull requests of every project awp knows", async () => {
    const inbox = await run((rpc) => rpc.InboxList({}), {
      viewer: "me",
      prs: [
        pr({ number: 1, headRef: "theirs", author: "someone", requested: ["me"] }),
        pr({ number: 2, headRef: "mine", author: "me", review: "approved" }),
        pr({ number: 3, headRef: "broken", author: "me", ci: "failing" }),
      ],
    });

    // The fixture's sessions imply the project `awp`, which is the only one
    // here — so every PR appears once, under the heading its state earns.
    expect(inbox.viewer).toBe("me");
    expect(inbox.items.map((item) => [item.number, item.bucket])).toEqual([
      [1, "needs-your-review"],
      [3, "needs-action"],
      [2, "ready-to-merge"],
    ]);
    expect(inbox.sources.map((source) => source.project)).toEqual(["awp"]);
    expect(inbox.sources[0]?.fetchedAt).toBeInstanceOf(Date);
    expect(inbox.sources[0]?.failure).toBeUndefined();
  });

  it("with nobody signed in, nothing is yours and nothing wants you", async () => {
    // The failure mode this guards: every viewer-relative bucket is empty, and
    // an inbox that is empty because `gh` is not authenticated looks exactly
    // like an inbox with nothing in it. The login on the answer is what lets a
    // client say which it is.
    const inbox = await run((rpc) => rpc.InboxList({}), {
      prs: [pr({ number: 1, author: "me", requested: ["me"] })],
    });
    expect(inbox.viewer).toBeUndefined();
    expect(inbox.items.map((item) => item.bucket)).toEqual(["other-open"]);
    expect(inbox.items[0]?.mine).toBe(false);
  });

  it("names the job building a review, so the row can show its progress", async () => {
    // The id and not the record: a job changes on its own and the window has a
    // live feed of every one, so a copy here would be the staler of two.
    const answer = await run(
      (rpc) =>
        Effect.gen(function* () {
          const started = yield* rpc.ReviewStart({ project: "awp", number: 44 });
          return { started, inbox: yield* rpc.InboxList({}) };
        }),
      { viewer: "me", prs: [pr({ number: 44, title: "a change" })] },
    );

    const row = answer.inbox.items.find((item) => item.number === 44);
    expect(row?.job).toBe(answer.started.job?.id);
  });

  it("links a pull request opened for work that already had a workspace", async () => {
    // The commonest case: a thread, then work, then a push, then a PR. The
    // workspace is named after the work rather than `pr-<n>`, and no link could
    // have been recorded when the thread was made — there was no pull request.
    //
    // The head branch is what identifies it: awp names a workspace's bookmark
    // `<prefix>/<workspace>`, so `andrew/lantern` is the PR for `lantern`.
    const answer = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "the lantern rewrite" });
          yield* rpc.ThreadAttach({
            thread: thread.id,
            member: { project: "awp", workspace: "lantern" },
          });
          const inbox = yield* rpc.InboxList({});
          // Read again, from the store, to show the link was written down and
          // not merely reported — which is what makes it survive the branch
          // being renamed.
          const every = yield* rpc.ThreadList();
          return { inbox, thread: every.find((one) => one.id === thread.id) };
        }),
      {
        viewer: "me",
        bookmarkPrefix: "andrew",
        prs: [pr({ number: 51, headRef: "andrew/lantern", author: "me" })],
      },
    );

    const row = answer.inbox.items.find((item) => item.number === 51);
    expect(row?.thread).toBe(answer.thread?.id);
    // And the row now offers to open rather than to create: it said "makes a
    // workspace" for a workspace already on disk.
    expect(row?.workspace).toBe("lantern");
    expect(answer.thread?.prs).toEqual([{ project: "awp", number: 51 }]);
  });

  it("does not guess when the branch is not one of ours", async () => {
    const answer = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "the lantern rewrite" });
          yield* rpc.ThreadAttach({
            thread: thread.id,
            member: { project: "awp", workspace: "lantern" },
          });
          return yield* rpc.InboxList({});
        }),
      {
        viewer: "me",
        bookmarkPrefix: "andrew",
        // Somebody else's branch that happens to end with the same word. The
        // prefix is what makes the inference safe, and without it there is
        // nothing to be safe about.
        prs: [pr({ number: 52, headRef: "someone/lantern" })],
      },
    );

    expect(answer.items[0]?.thread).toBeUndefined();
    expect(answer.items[0]?.workspace).toBeUndefined();
  });

  it("finds the thread through the recorded link, not the workspace's name", async () => {
    // The name is a convention and this is a claim: a workspace renamed, or a
    // review done in a checkout somebody made by hand, has no `pr-<n>` to
    // parse — and the link still says which thread the work is in.
    const answer = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "the lantern rewrite" });
          const linked = yield* rpc.ThreadLinkPr({
            thread: thread.id,
            pr: { project: "awp", number: 88 },
          });
          return { linked, inbox: yield* rpc.InboxList({}) };
        }),
      { viewer: "me", prs: [pr({ number: 88 })] },
    );

    expect(answer.linked.prs).toEqual([{ project: "awp", number: 88 }]);
    expect(answer.inbox.items[0]?.thread).toBe(answer.linked.id);
    // And no workspace: the link says which thread, not that anything is built.
    expect(answer.inbox.items[0]?.workspace).toBeUndefined();
  });

  it("a pull request belongs to one thread, and the second claim wins", async () => {
    // The same rule a workspace has, and for the same reason: two threads about
    // one PR would leave the row picking which of them to point at.
    const answer = await run(
      (rpc) =>
        Effect.gen(function* () {
          const first = yield* rpc.ThreadCreate({ title: "first" });
          const second = yield* rpc.ThreadCreate({ title: "second" });
          yield* rpc.ThreadLinkPr({ thread: first.id, pr: { project: "awp", number: 90 } });
          const taken = yield* rpc.ThreadLinkPr({
            thread: second.id,
            pr: { project: "awp", number: 90 },
          });
          const every = yield* rpc.ThreadList();
          return { taken, every };
        }),
      { viewer: "me" },
    );

    expect(answer.taken.prs).toEqual([{ project: "awp", number: 90 }]);
    expect(answer.every.find((one) => one.title === "first")?.prs).toEqual([]);
  });

  it("starting a review links the pull request to the thread it made", async () => {
    const started = await run(
      (rpc) =>
        Effect.gen(function* () {
          const answer = yield* rpc.ReviewStart({ project: "awp", number: 61 });
          const every = yield* rpc.ThreadList();
          return { answer, thread: every.find((one) => one.id === answer.thread.id) };
        }),
      { viewer: "me", prs: [pr({ number: 61, title: "a change" })] },
    );

    // Linked at creation rather than by the job, so the sidebar and the row can
    // name the pull request now instead of in half a minute.
    expect(started.thread?.prs).toEqual([{ project: "awp", number: 61 }]);
  });

  it("a session makes the row openable before the thread claim lands", async () => {
    // The fixture's own sessions stand in for a review workspace's: the claim
    // is the create job's second-to-last step, so a row that waited for it said
    // nothing for the thirty seconds a person is actually watching.
    const inbox = await run((rpc) => rpc.InboxList({}), {
      viewer: "me",
      // `session()` in this suite names `awp.awp.<workspace>.agent`, and
      // `identities` recovers the pair from the labels — so a session called
      // `pr-31` is a workspace called `pr-31` in the project `awp`.
      sessionWorkspace: "pr-31",
      prs: [pr({ number: 31, requested: ["me"] })],
    });

    expect(inbox.items[0]?.workspace).toBe("pr-31");
    // And no thread has claimed it, which the row has to be able to tell apart:
    // it is what says whether the job finished.
    expect(inbox.items[0]?.thread).toBeUndefined();
  });

  it("a project with no GitHub remote is not a source, and not a complaint", async () => {
    // Reported from a real window: a vault of notes and a scratch repository
    // with no remote each produced a red sentence on every refresh — both true,
    // neither actionable. A permanent warning is a warning that gets skipped.
    const inbox = await run((rpc) => rpc.InboxList({}), {
      viewer: "me",
      // The derived project `awp` resolves to this root through the fake jj.
      offGithub: "/repos/tmp",
      prs: [pr({ number: 1 })],
    });

    expect(inbox.sources).toEqual([]);
    expect(inbox.items).toEqual([]);
  });

  it("reports the workspace already reviewing a pull request", async () => {
    const inbox = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "#7 a change" });
          // Claimed by hand, which is what the create job's last-but-one step
          // does — and the only record that says a review exists.
          yield* rpc.ThreadAttach({
            thread: thread.id,
            member: { project: "awp", workspace: "pr-7" },
          });
          return { inbox: yield* rpc.InboxList({}), thread };
        }),
      { viewer: "me", prs: [pr({ number: 7, requested: ["me"] })] },
    );

    expect(inbox.inbox.items[0]?.workspace).toBe("pr-7");
    expect(inbox.inbox.items[0]?.thread).toBe(inbox.thread.id);
  });
});

describe("a checkout that is behind its pull request", () => {
  // The signal only. What to *do* about it is the repair prompt's business —
  // an agent is told to fetch and re-anchor, which is a thing a person reads
  // before it happens rather than a button that moves their checkout.
  const wanted = {
    viewer: "me",
    bookmarks: ["feature"],
    prs: [pr({ number: 70, headRef: "feature", headOid: "cafe" })],
  };

  it("says a row has moved when its checkout does not contain the head", async () => {
    const inbox = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "#70 a change" });
          yield* rpc.ThreadAttach({
            thread: thread.id,
            member: { project: "awp", workspace: "pr-70" },
          });
          return yield* rpc.InboxList({});
        }),
      { ...wanted, contains: false },
    );

    expect(inbox.items[0]?.moved).toBe(true);
  });

  it("says nothing has moved when the checkout has the head behind it", async () => {
    // Asked as "is it an ancestor", not "is it equal": somebody who committed
    // something of their own on top is still reviewing the right code.
    const inbox = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "#70 a change" });
          yield* rpc.ThreadAttach({
            thread: thread.id,
            member: { project: "awp", workspace: "pr-70" },
          });
          return yield* rpc.InboxList({});
        }),
      { ...wanted, contains: true },
    );

    expect(inbox.items[0]?.moved).toBe(false);
  });
});

describe("PullRequestRepair", () => {
  // One press: compose and send. What the sentence *says* is `repair.test.ts`'s
  // business — this is the seam, which is the part that can be wrong in ways
  // that file cannot see: which workspace it went to, and what happens when
  // there is nothing to say or nobody to say it to.
  const broken = {
    viewer: "me",
    bookmarks: ["feature"],
    prs: [pr({ number: 80, headRef: "feature", ci: "failing", author: "me" })],
  };

  it("types the prompt at the workspace's agent and says what it said", async () => {
    const done = await run(
      (rpc) =>
        Effect.gen(function* () {
          const thread = yield* rpc.ThreadCreate({ title: "#80 a change" });
          yield* rpc.ThreadLinkPr({ thread: thread.id, pr: { project: "awp", number: 80 } });
          yield* rpc.ThreadAttach({
            thread: thread.id,
            // The fixture's own session is `awp.awp.other.agent`, so this is the
            // workspace that has a live agent to type into.
            member: { project: "awp", workspace: "other" },
          });
          return yield* rpc.PullRequestRepair({ project: "awp", number: 80 });
        }),
      broken,
    );

    expect(done.workspace).toBe("other");
    expect(done.prompt).toContain("failing CI checks");
    // Owner tone, because the head branch is under the configured prefix — and
    // that is what decides whether the agent is asked to push.
    expect(done.mine).toBe(true);
  });

  it("nothing wrong is an answer, not a failure", async () => {
    const done = await run((rpc) => rpc.PullRequestRepair({ project: "awp", number: 81 }), {
      viewer: "me",
      prs: [pr({ number: 81, author: "me" })],
    });

    expect(done.prompt).toBe("");
    expect(done.workspace).toBeUndefined();
  });

  it("something to say and nowhere to say it is refused by name", async () => {
    // No thread, so no workspace, so no agent. Silence here would be a button
    // that reports success and does nothing.
    const failed = await run(
      (rpc) => rpc.PullRequestRepair({ project: "awp", number: 80 }).pipe(Effect.flip),
      broken,
    );

    expect(failed).toMatchObject({ _tag: "NoAgent" });
  });
});

describe("ReviewStart", () => {
  const wanted = { viewer: "me", prs: [pr({ number: 12, title: "tiered discounts" })] };

  it("makes a thread and a job, named after the pull request", async () => {
    const started = await run((rpc) => rpc.ReviewStart({ project: "awp", number: 12 }), wanted);

    expect(started.created).toBe(true);
    expect(started.workspace).toBe("pr-12");
    expect(started.thread.title).toBe("#12 tiered discounts");
    expect(started.job?.key).toBe("review:awp:12");
    // The name is on the record from the start, which is what stops the naming
    // step spending ten seconds on a name that is already decided.
    expect(started.job?.input).toMatchObject({ workspace: "pr-12", base: "feature" });
  });

  it("pressed twice, it is one thread and one job", async () => {
    const both = await run(
      (rpc) =>
        Effect.all([
          rpc.ReviewStart({ project: "awp", number: 12 }),
          rpc.ReviewStart({ project: "awp", number: 12 }),
        ]),
      wanted,
    );

    expect(both[1].created).toBe(false);
    expect(both[1].thread.id).toBe(both[0].thread.id);
    expect(both[1].job?.id).toBe(both[0].job?.id);
  });

  it("a pull request that is not open is refused by name", async () => {
    const failed = await run(
      (rpc) => rpc.ReviewStart({ project: "awp", number: 99 }).pipe(Effect.flip),
      wanted,
    );
    expect(failed).toMatchObject({
      number: 99,
      reason: expect.stringContaining("not an open pull request"),
    });
  });

  it("a project awp has never heard of is refused before gh is asked", async () => {
    const failed = await run(
      (rpc) => rpc.ReviewStart({ project: "nowhere", number: 1 }).pipe(Effect.flip),
      wanted,
    );
    expect(failed).toMatchObject({ reason: expect.stringContaining("knows no project") });
  });
});
