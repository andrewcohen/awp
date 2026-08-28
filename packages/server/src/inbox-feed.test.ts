import { layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { Github, type PullRequest, type PullRequestDetail } from "./github";
import { DatabaseSync } from "node:sqlite";
import { InboxFeed, PROJECTION, layer as inboxLayer, migrations } from "./inbox-feed";

// What only a real database can answer: that the cache survives the process.
//
// The in-memory half is easy to believe and was never the problem. The disk half
// is the one that matters here, because a daemon restarts every time this
// repository is worked on — and `gh pr list` with `statusCheckRollup` measured
// 4.5s for eleven pull requests, so a cold cache is five seconds of nothing per
// project at exactly the moment somebody opens a window.
//
// A second `InboxFeed` over the same file stands in for the restart. It is the
// same substitution `runner.test.ts` makes for the jobs runner, and for the same
// reason: nothing about one instance's behaviour can demonstrate what the next
// one finds.

const pr = (number: number): PullRequest => ({
  number,
  headRef: `feature-${String(number)}`,
  headOid: "abc",
  baseRef: "main",
  title: "a change",
  author: "someone",
  url: "https://example.invalid",
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
});

/** A `gh` that counts how often it was actually asked. */
const counting = () => {
  const calls = { lists: 0, details: 0 };
  const layer = Layer.succeed(Github)({
    pullRequests: () =>
      Effect.sync(() => {
        calls.lists += 1;
        return { prs: [pr(1), pr(2)], degraded: undefined };
      }),
    pullRequest: (_repo: string, number: number) =>
      Effect.sync(() => {
        calls.details += 1;
        return { number, title: "a change", body: "why" } as PullRequestDetail;
      }),
    viewer: () => Effect.succeed({ login: "me", teams: [] }),
    isGithub: () => Effect.succeed(true),
    repository: () => Effect.succeed({ owner: "acme", repo: "widgets" }),
    fetchFork: () => Effect.void,
  });
  return { calls, layer };
};

const project = { name: "thicket", root: "/repos/thicket", importedAt: undefined };

/** One database file, and as many feeds over it as a test asks for. */
const over = (file: string, gh: Layer.Layer<Github>) =>
  inboxLayer.pipe(Layer.provide(gh), Layer.provide(Layer.orDie(dbLayer(file, migrations))));

const read = (file: string, gh: Layer.Layer<Github>, refresh = false) =>
  Effect.gen(function* () {
    const feed = yield* InboxFeed;
    return yield* feed.read({
      projects: [project],
      refresh,
      claimed: () => undefined,
      contains: () => Effect.succeed(true),
    });
  }).pipe(Effect.provide(over(file, gh)), Effect.scoped, Effect.runPromise);

const detail = (file: string, gh: Layer.Layer<Github>) =>
  Effect.gen(function* () {
    const feed = yield* InboxFeed;
    return yield* feed.detail(project.root, 7);
  }).pipe(Effect.provide(over(file, gh)), Effect.scoped, Effect.runPromise);

const scratch = mkdtempSync(join(tmpdir(), "awp-feed-"));
let files = 0;
const file = () => join(scratch, `feed-${(files += 1)}.sqlite`);

describe("the pull request cache", () => {
  it("a second daemon over the same file asks gh nothing", async () => {
    const gh = counting();
    const one = file();

    const first = await read(one, gh.layer);
    expect(first.items).toHaveLength(2);
    expect(gh.calls.lists).toBe(1);

    // A different feed, a different in-memory map, the same database — which is
    // what a restart is.
    const again = await read(one, gh.layer);
    expect(again.items).toHaveLength(2);
    expect(gh.calls.lists).toBe(1);
  });

  it("refresh goes to gh even with a row on disk", async () => {
    const gh = counting();
    const one = file();

    await read(one, gh.layer);
    await read(one, gh.layer, true);

    // Otherwise the button would be a button that does nothing, which is worse
    // than not having one: a person presses it precisely when they believe the
    // list is wrong.
    expect(gh.calls.lists).toBe(2);
  });

  it("one pull request is kept too, across the restart", async () => {
    // The panel is unmounted every time somebody looks at the diff instead, so
    // this is asked far more often than the listing is.
    const gh = counting();
    const one = file();

    expect((await detail(one, gh.layer))?.number).toBe(7);
    expect((await detail(one, gh.layer))?.number).toBe(7);
    expect(gh.calls.details).toBe(1);
  });

  it("a row from another projection is a miss, not a wrong answer", async () => {
    // The failure this exists to stop: a row written by an older shape parses
    // as JSON perfectly well and comes back missing whatever is new, which then
    // fails on the *wire* — `Missing key at ["value"]["remarks"][0]["verdict"]`,
    // several layers from the cache nobody suspected. The TTL cannot help; the
    // row is fresh and simply the wrong shape.
    const gh = counting();
    const one = file();

    await read(one, gh.layer);
    expect(gh.calls.lists).toBe(1);

    // Stamp the stored row as something else, the way a change to
    // `github-parse.ts` and a bumped `PROJECTION` would.
    const db = new DatabaseSync(one);
    db.prepare("update pr_lists set projection = ?").run(PROJECTION + 1);
    db.close();

    await read(one, gh.layer);
    expect(gh.calls.lists).toBe(2);
  });

  it("a failure is not written down", async () => {
    // A repository whose token expired once must not serve an empty list from
    // disk for an hour after it was fixed.
    const failing = Layer.succeed(Github)({
      pullRequests: () => Effect.die("gh is not signed in"),
      pullRequest: () => Effect.succeed(undefined),
      viewer: () => Effect.succeed({ login: "me", teams: [] }),
      isGithub: () => Effect.succeed(true),
      repository: () => Effect.succeed({ owner: "acme", repo: "widgets" }),
      fetchFork: () => Effect.void,
    } as unknown as Github["Service"]);
    const one = file();

    await read(one, failing).catch(() => undefined);
    const gh = counting();
    await read(one, gh.layer);

    // Asked, because nothing usable was stored by the run that failed.
    expect(gh.calls.lists).toBe(1);
  });
});
