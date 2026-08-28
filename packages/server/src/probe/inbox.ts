// The inbox, against real `gh` in a real repository.
//
// What only this can answer: whether the fields `gh pr list` is asked for are
// the fields the installed `gh` actually has, and whether the token in this
// user's keychain can read them. A test proves the projection; nothing but a
// subprocess proves the request.
//
// ── read-only, and safe from anywhere ─────────────────────────────────────
// `gh pr list`, `gh api user`, `gh repo view`. No zmx, no session, no
// workspace, no write of any kind — so unlike its neighbours here it needs no
// refusal to run inside a zmx session. It does not even need a daemon: the
// services are built directly, which is the point of them being layers.
//
//     bun run probe:inbox            the repository this file is in
//     bun run probe:inbox <path>     any other checkout
//
// What a pass looks like — and the first line is the one that matters, because
// with no login every viewer-relative section is empty and an inbox that is
// empty for that reason looks exactly like an inbox with nothing in it:
//
//   viewer   someone
//   read     14 open pull requests in 120ms
//   Needs your review (1)
//     #2340  d 0  ci passing  review review-required  someone  feature/discounts

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Result } from "effect";
import { bucketLabel, inboxBuckets } from "@awp-kit/protocol";
import { AWP_DB } from "../daemon";
import { Github } from "../github";
import * as githubCli from "../github-cli";
import { InboxFeed, layer as inboxLayer, migrations as inboxMigrations } from "../inbox-feed";
import { layer as dbLayer } from "@awp-kit/store";

const root = process.argv[2] ?? process.cwd();

const program = Effect.gen(function* () {
  const gh = yield* Github;
  const feed = yield* InboxFeed;

  // Asked separately from the listing so the two failures read differently: a
  // login that cannot be read is `gh auth login`, and a listing that cannot be
  // read is about this repository.
  const who = yield* Effect.result(gh.viewer());
  const login = Result.isSuccess(who) ? who.success.login : undefined;
  console.log(`viewer   ${login ?? "NOT SIGNED IN — every viewer bucket will be empty"}`);

  const started = Date.now();
  const inbox = yield* feed.read({
    projects: [{ name: root.split("/").at(-1) ?? "project", root, importedAt: undefined }],
    refresh: true,
    // Nothing is claimed: this probe is about the GitHub half. Which pull
    // requests already have a workspace is a join against the thread store,
    // which `handlers.test.ts` covers over the real one.
    claimed: () => undefined,
    // Nothing is claimed here, so nothing can be stale — this probe is about the
    // GitHub half. Whether a checkout still contains its pull request is a jj
    // question, and `handlers.test.ts` covers it against the real join.
    contains: () => Effect.succeed(true),
  });
  console.log(
    `read     ${inbox.items.length} open pull requests in ${Date.now() - started}ms  (${root})`,
  );

  for (const source of inbox.sources) {
    if (source.failure !== undefined) {
      console.log(`FAILED   ${source.project}: ${source.failure}`);
    }
  }

  for (const bucket of inboxBuckets) {
    const rows = inbox.items.filter((item) => item.bucket === bucket);
    if (rows.length === 0) {
      continue;
    }
    console.log(`\n${bucketLabel(bucket)} (${rows.length})`);
    for (const row of rows) {
      const marks = [
        row.draft ? "draft" : undefined,
        `ci ${row.ci}`,
        `review ${row.review}`,
        // Prefixed, because gh's own `BLOCKED` — which only means "not
        // approved yet" — reads identically to the stack flag below it
        // otherwise. The first run of this probe printed both as `blocked` and
        // every row looked stack-blocked.
        row.mergeState === "clean" ? undefined : `merge ${row.mergeState}`,
        row.blocked ? "stack-blocked" : undefined,
        row.depth > 0 ? `stacked ${row.depth}` : undefined,
      ].filter((one): one is string => one !== undefined);
      console.log(
        `  #${String(row.number).padEnd(6)}${row.author.padEnd(16)}${row.headRef.padEnd(28)}${marks.join("  ")}`,
      );
      console.log(`          ${row.title}`);
    }
  }
});

await Effect.runPromise(
  program.pipe(
    Effect.provide(
      inboxLayer.pipe(
        // The daemon's own database, so a probe run beside it reads and writes
        // the same cache — which is the point: "is this cached" is a question
        // about that file, not about a fresh one.
        Layer.provide(Layer.orDie(dbLayer(AWP_DB, [...inboxMigrations]))),
        // `provideMerge`, not `provide`: this probe asks the Github service the
        // login directly as well as going through the feed, so the layer has to
        // stay visible rather than being consumed on the way in.
        Layer.provideMerge(githubCli.layer),
        Layer.provide(NodeChildProcessSpawner.layer),
        // The spawner's, not this probe's — it resolves an executable before
        // running it. The same pair `main.ts` provides for the same reason.
        Layer.provide(NodeFileSystem.layer),
        Layer.provide(NodePath.layer),
      ),
    ),
  ),
);
