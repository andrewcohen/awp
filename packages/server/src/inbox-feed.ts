// The inbox, assembled and remembered.
//
// Two things live here that `inbox.ts` deliberately does not have: the calls to
// GitHub, and the memory of what they answered.
//
// ── why there is a cache at all ────────────────────────────────────────────
// `gh pr list` against a busy repository is a couple of seconds, and this is
// asked every time a panel is opened — which, for a panel in a tab strip, is
// several times a minute. Without a cache the inbox is a spinner in the common
// case and a person stops opening it.
//
// ── and why it has a lifetime rather than only a refresh button ────────────
// A cache that is only invalidated by hand is a cache that is silently wrong
// for as long as nobody presses the button, and "is this list current" is not a
// question a person should have to hold. The answer to it is on the wire —
// `InboxSource.fetchedAt` — and the panel shows it, but a reading that is an
// hour old is worth re-taking whether or not anybody noticed.
//
// ── a failure is per project ───────────────────────────────────────────────
// One repository's `gh` being unauthenticated, or its remote not being GitHub
// at all, must not cost every other project its rows. So a project's failure is
// recorded against that project and the rest of the answer is built anyway. The
// only failure that is global is the viewer's, and even that is not fatal: what
// it costs is every viewer-relative bucket, which is why the login is on the
// answer for a client to say so.

import type { Inbox, InboxSource, Project } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Clock, Context, Data, Effect, Layer, Ref, Result } from "effect";
import {
  Github,
  type GithubError,
  type PullRequest,
  type PullRequestDetail,
  type Viewer,
} from "./github";
import { type Claim, type Source, inboxItems } from "./inbox";

/** The database would not answer. */
export class InboxStoreError extends Data.TaggedError("InboxStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * What `gh` last said, kept between runs.
 *
 * ── why this is on disk and not only in memory ─────────────────────────────
 *
 * It was a `Ref<Map>`, which is a cache that a daemon restart empties — and a
 * daemon restarts every time this repository is worked on. The cost is not
 * theoretical: `gh pr list` with `statusCheckRollup` measured 4.5s for eleven
 * pull requests, so the first inbox after every restart was a five-second wait
 * per project, which reads as the feature being slow rather than as a cold
 * cache.
 *
 * **The payload is JSON in a text column**, deliberately. What is stored is
 * gh's answer as this daemon projected it — a shape that belongs to
 * `github-parse.ts` and changes when that file does — and a column per field
 * would make every such change a migration. A row that cannot be parsed is
 * discarded as a miss, which is the honest reading of "written by a version
 * that is no longer here".
 *
 * Two tables rather than one keyed by kind, because the two are read at
 * different grains: the listing is per repository and the detail is per pull
 * request, and a shared table would need a discriminator column that no query
 * ever wants to see.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "inbox.001-cache",
    up: [
      `create table pr_lists (
         repo       text primary key,
         fetched_at integer not null,
         payload    text not null
       ) strict`,
      `create table pr_details (
         repo       text not null,
         number     integer not null,
         fetched_at integer not null,
         payload    text not null,
         primary key (repo, number)
       ) strict`,
    ],
  },
  {
    // Who `gh` is signed in as. Its own migration, and the reason is the rule
    // this file broke once: **a migration's name is fixed the moment it has
    // run anywhere.** This table was first added as a third statement inside
    // `001`, which had already run — so the name was recorded, the statement
    // was never executed, and the daemon died on the first `prepare` against a
    // table that did not exist:
    //
    //   ERROR: SQLiteError: no such table: gh_viewer
    //     at <anonymous> (packages/server/src/inbox-feed.ts:217:25)
    //
    // Loudly, at least, and at startup rather than later — which is what the
    // deliberate `create table` (rather than `if not exists`) buys.
    //
    // One row per login, upserted. It changes when somebody runs
    // `gh auth login`, which is rare enough to keep for a day — and reading it
    // is two `gh api` calls, one paginated, which measured about 1.7s. Without
    // it a restarted daemon paid that before it could answer anything, which
    // was most of what a warm inbox still cost.
    name: "inbox.002-viewer",
    up: [
      `create table gh_viewer (
         login      text primary key,
         teams      text not null,
         fetched_at integer not null
       ) strict`,
    ],
  },
  {
    // Which projection wrote each row. See `PROJECTION` — a row from an older
    // one parses and comes back the wrong shape, which fails on the wire rather
    // than here.
    //
    // `default 0` is what makes it safe over the rows already there: none of
    // them was written by a version that stamped anything, and 0 is a value no
    // projection uses — so every existing row is a miss, once, which is exactly
    // right.
    name: "inbox.003-projection",
    up: [
      `alter table pr_lists add column projection integer not null default 0`,
      `alter table pr_details add column projection integer not null default 0`,
    ],
  },
];

/**
 * How long a repository's pull requests are believed for.
 *
 * Two minutes, which is the shape of the thing being watched rather than a
 * round number: CI turning green, a review landing and a re-request are all
 * events a person reacts to within minutes, and none of them within seconds.
 * Shorter would put a `gh` call behind every tab switch; longer would make the
 * refresh button the only way the panel is ever right.
 */
export const TTL_MS = 120_000;

/**
 * How long a cached answer survives a restart.
 *
 * Longer than {@link TTL_MS} on purpose, and this is the one number here worth
 * arguing about. A running daemon re-reads every two minutes because somebody
 * is watching; a daemon that has just started has a person waiting on a window,
 * and rows that are an hour old with `read at 09:14` under them are worth far
 * more than five seconds of nothing. The refresh button is one press away, and
 * the ordinary two-minute lifetime takes over from the first read onward.
 */
export const DISK_TTL_MS = 3_600_000;

/**
 * How long the `gh` login is believed.
 *
 * A day, because it changes when a person runs `gh auth login` and not
 * otherwise — and the refresh button re-reads it, which is the escape hatch for
 * the one moment it is wrong.
 */
export const VIEWER_TTL_MS = 86_400_000;

/**
 * What shape the stored payloads are in.
 *
 * ── why a cache needs a version and not just a TTL ─────────────────────────
 *
 * The payload is this daemon's *projection* of gh's answer, and that shape
 * changes whenever `github-parse.ts` does — a field added, a field renamed. A
 * row written by the previous shape parses as JSON perfectly well and comes back
 * missing whatever is new, which then travels to a client and fails there
 * instead:
 *
 *   Missing key at ["value"]["remarks"][0]["verdict"]
 *
 * — a decode error about the wire, several layers from a cache row nobody
 * suspected. The TTL cannot help: the row is fresh, it is simply the wrong
 * shape.
 *
 * So every row carries this, and a row stamped with anything else is a miss. It
 * is a hand-turned number and that is the honest cost: **bump it when the shape
 * of `PullRequest` or `PullRequestDetail` changes.** Forgetting shows up as the
 * error above rather than as silence, which is the least bad way to be reminded.
 */
export const PROJECTION = 2;

/** What was last read for one repository. */
interface Cached {
  readonly at: number;
  readonly prs: ReadonlyArray<PullRequest>;
  /**
   * This repository has no remote on a host `gh` knows, so it is not a source
   * of pull requests at all.
   *
   * Not a failure, and the distinction is the whole of it: a vault of notes and
   * a scratch repository with no remote are working exactly as intended and
   * have no pull requests to have. They are left out of `sources` entirely, so
   * the panel says nothing about them — a red sentence on every refresh about a
   * repository nobody can act on is a warning that teaches the eye to skip
   * warnings, which costs the one project whose token really has expired.
   */
  readonly offGithub: boolean;
  /** `gh`'s sentence, when the read failed. The rows are then whatever the
   * previous successful read held — stale rows plus a sentence beat no rows. */
  readonly failure: string | undefined;
  /**
   * What had to be given up to read them at all, if anything.
   *
   * A repository big enough that GitHub refuses to compute mergeability for a
   * hundred pull requests still gets its inbox — with conflicts and
   * behind-base unknown, and this sentence saying so. See `github-cli.ts`.
   */
  readonly degraded: string | undefined;
}

export class InboxFeed extends Context.Service<
  InboxFeed,
  {
    /**
     * Every open pull request across these projects, sectioned and ordered.
     *
     * The projects are the caller's, not this service's: the daemon's list is
     * the imported rows merged with what the running sessions imply, and that
     * merge belongs where it already is rather than in a second copy here.
     */
    readonly read: (options: {
      readonly projects: ReadonlyArray<Project>;
      /** Ask GitHub again rather than answering from what was last read. */
      readonly refresh: boolean;
      readonly claimed: Claim;
      /**
       * Whether a workspace contains a pull request's head commit.
       *
       * A callback rather than something this service works out, because the two
       * halves live on opposite sides: the head commit is in the listing, which
       * is here, and answering the question means asking jj about a workspace,
       * which is the caller's. Asked only for rows that have a workspace — on a
       * real machine a handful of the forty-five.
       */
      readonly contains: (
        member: { readonly project: string; readonly workspace: string },
        headOid: string,
      ) => Effect.Effect<boolean>;
    }) => Effect.Effect<Inbox>;

    /**
     * One pull request, from what was last read or by asking.
     *
     * For the moment somebody acts on a row: the row came out of a listing, so
     * the answer is nearly always already here, and the fallback exists because
     * "nearly always" is not a thing to build a refusal on.
     */
    readonly find: (
      repo: string,
      number: number,
    ) => Effect.Effect<PullRequest | undefined, GithubError>;

    /**
     * Who `gh` is signed in as, from the cache.
     *
     * Exposed because the repair prompt's *tone* depends on it — an owner is
     * asked to fix, a reviewer to look — and the alternative is a second place
     * that reads the login and a second answer to "who is this". Absent when
     * nobody is signed in, which makes every viewer-relative question false.
     */
    readonly who: () => Effect.Effect<Viewer | undefined>;

    /**
     * One pull request in full, cached the same way the listing is.
     *
     * Cached because of how the panel is mounted, not to save a call in the
     * abstract: Base UI unmounts a hidden tab, so switching to the diff and back
     * remounts this panel — and without a cache every switch is a `gh pr view`,
     * which is a second of nothing at a moment somebody is navigating.
     */
    readonly detail: (
      repo: string,
      number: number,
      /** Go to GitHub rather than answering from the cache. */
      refresh?: boolean,
    ) => Effect.Effect<PullRequestDetail | undefined, GithubError>;
  }
>()("awp/InboxFeed") {}

const make = Effect.gen(function* () {
  const gh = yield* Github;
  const db = yield* Db;
  const cache = yield* Ref.make(new Map<string, Cached>());
  const details = yield* Ref.make(new Map<string, { at: number; detail: PullRequestDetail }>());

  // The version is a *predicate*, not a column to read back: a row of another
  // shape is not a row this code can do anything with, so it is filtered out in
  // sqlite rather than fetched and discarded here.
  const readList = db.prepare(
    "select fetched_at, payload from pr_lists where repo = ? and projection = ?",
  );
  const writeList = db.prepare(
    `insert into pr_lists (repo, fetched_at, payload, projection) values (?, ?, ?, ?)
     on conflict (repo) do update set
       fetched_at = excluded.fetched_at,
       payload = excluded.payload,
       projection = excluded.projection`,
  );
  const readViewer = db.prepare(
    "select login, teams, fetched_at from gh_viewer order by fetched_at desc limit 1",
  );
  const writeViewer = db.prepare(
    `insert into gh_viewer (login, teams, fetched_at) values (?, ?, ?)
     on conflict (login) do update set
       teams = excluded.teams, fetched_at = excluded.fetched_at`,
  );
  const readDetail = db.prepare(
    "select fetched_at, payload from pr_details where repo = ? and number = ? and projection = ?",
  );
  const writeDetail = db.prepare(
    `insert into pr_details (repo, number, fetched_at, payload, projection) values (?, ?, ?, ?, ?)
     on conflict (repo, number) do update set
       fetched_at = excluded.fetched_at,
       payload = excluded.payload,
       projection = excluded.projection`,
  );

  /**
   * A stored row, or nothing.
   *
   * Every failure is a miss rather than an error: a payload written by a version
   * whose projection has since changed is exactly as useful as no payload, and
   * refusing to serve an inbox because a cache row is stale-shaped would be the
   * cache making things worse than not having one.
   */
  const stored = <A>(
    read: () => ReadonlyArray<Record<string, unknown>>,
    ttl: number,
    now: number,
  ) =>
    attempt("read the pull request cache", () => {
      const [row] = read();
      if (row === undefined) {
        return undefined;
      }
      const at = Number(row["fetched_at"]);
      if (!Number.isFinite(at) || now - at > ttl) {
        return undefined;
      }
      try {
        return { at, value: JSON.parse(String(row["payload"])) as A };
      } catch {
        return undefined;
      }
    }).pipe(
      Effect.mapError((error) => new InboxStoreError({ reason: error.reason, cause: error })),
      Effect.orElseSucceed(() => undefined),
    );
  // The viewer is one answer for the whole machine, not one per repository, and
  // it changes when somebody runs `gh auth login` — which is rare enough that
  // it is read once and kept. A window open across a login gets it on the next
  // refresh, which is the button that exists for exactly that.
  const who = yield* Ref.make<Viewer | undefined>(undefined);
  // Which repositories have a background re-read in flight. See `behind`.
  const running = yield* Ref.make(new Set<string>());

  const viewer = (refresh: boolean) =>
    Effect.gen(function* () {
      const known = yield* Ref.get(who);
      if (known !== undefined && !refresh) {
        return known;
      }
      if (!refresh) {
        // Off disk before `gh` is asked. Two `gh api` calls, one paginated,
        // measured about 1.7s — which was most of what a restarted daemon's
        // "warm" inbox still cost.
        //
        // Read here rather than through `stored`, and that is not a style
        // choice: `stored` parses a column called `payload`, this table keeps
        // the teams in a column called `teams`, and reusing it meant every read
        // threw on `JSON.parse("undefined")`, missed, and asked `gh` again. It
        // cost nothing visible except the two seconds it was added to remove —
        // the cache was simply never hit, and nothing said so.
        const now = yield* Clock.currentTimeMillis;
        const kept = yield* attempt("read the gh login", () => {
          const [row] = readViewer.all();
          const login = row?.["login"];
          const at = Number(row?.["fetched_at"]);
          if (typeof login !== "string" || !Number.isFinite(at) || now - at > VIEWER_TTL_MS) {
            return undefined;
          }
          try {
            const teams: unknown = JSON.parse(String(row?.["teams"]));
            return {
              login,
              teams: Array.isArray(teams) ? (teams as ReadonlyArray<string>) : [],
            } satisfies Viewer;
          } catch {
            return undefined;
          }
        }).pipe(Effect.orElseSucceed(() => undefined));
        if (kept !== undefined) {
          yield* Ref.set(who, kept);
          return kept;
        }
      }

      const found = yield* gh.viewer().pipe(Effect.orElseSucceed(() => undefined));
      if (found !== undefined) {
        yield* Ref.set(who, found);
        const at = yield* Clock.currentTimeMillis;
        yield* attempt("write the gh login", () =>
          writeViewer.run(found.login, JSON.stringify(found.teams), at),
        ).pipe(Effect.ignore);
      }
      // A failed refresh keeps whoever was known. Losing the login because one
      // call timed out would empty every bucket that names them.
      return found ?? known;
    });

  const fresh = (repo: string) =>
    Effect.gen(function* () {
      const at = yield* Clock.currentTimeMillis;
      // Asked first, and locally: `git remote -v` costs milliseconds and
      // answers definitively, where `gh` can only report "no GitHub remote" as
      // a failure. A repository that cannot be read at all counts as off
      // GitHub, which is the honest answer for a directory that is not a git
      // repository.
      const on = yield* gh.isGithub(repo).pipe(Effect.orElseSucceed(() => false));
      if (!on) {
        const entry: Cached = {
          at,
          prs: [],
          failure: undefined,
          degraded: undefined,
          offGithub: true,
        };
        yield* Ref.update(cache, (all) => new Map(all).set(repo, entry));
        return entry;
      }
      const answer = yield* Effect.result(gh.pullRequests(repo));
      const previous = (yield* Ref.get(cache)).get(repo);
      // Stale rows plus a sentence, rather than an empty section: a token that
      // expired an hour ago should not make a project's pull requests look like
      // they were merged.
      const entry: Cached = Result.isSuccess(answer)
        ? {
            at,
            prs: answer.success.prs,
            failure: undefined,
            degraded: answer.success.degraded,
            offGithub: false,
          }
        : {
            at,
            prs: previous?.prs ?? [],
            failure: answer.failure.reason,
            degraded: previous?.degraded,
            offGithub: false,
          };
      yield* Ref.update(cache, (all) => new Map(all).set(repo, entry));
      // Only a real answer is written down. Persisting a failure would mean a
      // repository whose token expired once served an empty list from disk for
      // an hour after it was fixed.
      if (Result.isSuccess(answer)) {
        yield* attempt("write the pull request cache", () =>
          writeList.run(repo, at, JSON.stringify(answer.success.prs), PROJECTION),
        ).pipe(Effect.ignore);
      }
      return entry;
    });

  /**
   * Fetch behind the answer, at most once per repository at a time.
   *
   * `forkDetach` and not `fork`: the fiber must outlive the request that started
   * it, because the whole point is that the request has already answered — a
   * child fiber would be interrupted the moment the handler returns, which is
   * a background refresh that never happens and never says so. The guard is
   * what stops a person switching tabs three times from starting three
   * identical `gh` calls.
   */
  const behind = (repo: string) =>
    Effect.gen(function* () {
      const busy = yield* Ref.get(running);
      if (busy.has(repo)) {
        return;
      }
      yield* Ref.update(running, (all) => new Set(all).add(repo));
      yield* fresh(repo).pipe(
        Effect.ignore,
        Effect.ensuring(
          Ref.update(running, (all) => {
            const next = new Set(all);
            next.delete(repo);
            return next;
          }),
        ),
        Effect.forkDetach,
      );
    });

  /**
   * What to answer with, and what to do about it being old.
   *
   * ── one answer, two lifetimes, and they were fighting ─────────────────────
   *
   * The first version had `TTL_MS` decide whether to re-fetch and `DISK_TTL_MS`
   * decide whether a stored row was worth loading — and those disagree by
   * construction. A row read off disk carries the moment it was *fetched*, so
   * loading a twenty-minute-old row and then asking "is it younger than two
   * minutes" answers no, immediately, and the read pays the full `gh` call
   * anyway. Measured, and it is why the numbers made no sense:
   *
   *   warm disk, first read     2.6s
   *   warm disk, second read    7.9s   ← re-fetched everything
   *
   * So the two lifetimes now mean different things rather than competing:
   *
   *   is there anything to say      DISK_TTL_MS. An hour-old inbox with
   *                                 `read at 09:14` under it beats a spinner
   *   is it worth re-reading        TTL_MS, and the re-read happens **behind**
   *                                 the answer rather than in front of it
   *
   * Which makes every read after the first one instant, and keeps the rows
   * moving: a person who opens the tab sees what was there and a fresher answer
   * lands a few seconds later, without a spinner in between. `refresh` is still
   * the synchronous path, because somebody pressing it is asking to wait.
   */
  const entryFor = (repo: string, refresh: boolean) =>
    Effect.gen(function* () {
      if (refresh) {
        return yield* fresh(repo);
      }
      const now = yield* Clock.currentTimeMillis;
      const held = (yield* Ref.get(cache)).get(repo);
      if (held !== undefined) {
        if (now - held.at > TTL_MS) {
          yield* behind(repo);
        }
        return held;
      }
      // Nothing in memory: the first read since the daemon started, and the row
      // on disk is what stops it costing seconds per project.
      const kept = yield* stored<ReadonlyArray<PullRequest>>(
        () => readList.all(repo, PROJECTION),
        DISK_TTL_MS,
        now,
      );
      if (kept === undefined) {
        return yield* fresh(repo);
      }
      const entry: Cached = {
        at: kept.at,
        prs: kept.value,
        failure: undefined,
        // Not stored: a cached row says what was read, and whether the *next*
        // read has to give something up is a question about that read.
        degraded: undefined,
        offGithub: false,
      };
      yield* Ref.update(cache, (all) => new Map(all).set(repo, entry));
      if (now - entry.at > TTL_MS) {
        yield* behind(repo);
      }
      return entry;
    });

  return {
    read: ({
      projects,
      refresh,
      claimed,
      contains,
    }: {
      readonly projects: ReadonlyArray<Project>;
      readonly refresh: boolean;
      readonly claimed: Claim;
      readonly contains: (
        member: { readonly project: string; readonly workspace: string },
        headOid: string,
      ) => Effect.Effect<boolean>;
    }) =>
      Effect.gen(function* () {
        const login = yield* viewer(refresh);
        // Concurrently, because these are independent network calls and a
        // person with four projects would otherwise wait for the sum of them.
        // Bounded, because `gh` is a process per call and GitHub rate-limits.
        const read = yield* Effect.forEach(
          projects,
          (project) =>
            entryFor(project.root, refresh).pipe(
              Effect.map((entry) => ({ project, entry }) as const),
            ),
          { concurrency: 4 },
        );

        const sources: Array<InboxSource> = [];
        const feed: Array<Source> = [];
        for (const { project, entry } of read) {
          if (entry.offGithub) {
            // Silently. See `Cached.offGithub` — there is nothing here for a
            // person to do, and nothing for the panel to say.
            continue;
          }
          sources.push({
            project: project.name,
            root: project.root,
            fetchedAt: entry.failure === undefined ? new Date(entry.at) : undefined,
            failure: entry.failure,
            degraded: entry.degraded,
          });
          feed.push({ project: project.name, repo: project.root, prs: entry.prs });
        }

        // ── which checkouts no longer contain their pull request ───────────
        //
        // Here rather than in `inbox.ts` because it is the one part of a row
        // that cannot be computed from the listing alone, and here rather than
        // in the handler because this is where the head commits are. Only rows
        // with a workspace are asked about, concurrently, and a claim that has
        // none is left alone.
        const moved = new Map<string, boolean>();
        yield* Effect.forEach(
          feed.flatMap((source) => source.prs.map((pr) => ({ project: source.project, pr }))),
          ({ project, pr }) =>
            Effect.gen(function* () {
              const claim = claimed(project, pr.number);
              if (claim?.workspace === undefined || pr.headOid === "") {
                return;
              }
              const has = yield* contains({ project, workspace: claim.workspace }, pr.headOid);
              moved.set(`${project}:${pr.number}`, !has);
            }),
          { concurrency: 4 },
        );

        /** The caller's claim, with the answer above folded into it. */
        const settled: Claim = (project, number) => {
          const claim = claimed(project, number);
          return claim === undefined
            ? undefined
            : { ...claim, moved: moved.get(`${project}:${number}`) ?? false };
        };

        return {
          items: inboxItems(feed, login, settled),
          sources,
          viewer: login?.login,
        };
      }),

    detail: (repo: string, number: number, refresh = false) =>
      Effect.gen(function* () {
        const now = yield* Clock.currentTimeMillis;
        const key = `${repo}#${number}`;
        const held = (yield* Ref.get(details)).get(key);
        if (!refresh && held !== undefined && now - held.at <= TTL_MS) {
          return held.detail;
        }
        if (!refresh && held === undefined) {
          const kept = yield* stored<PullRequestDetail>(
            () => readDetail.all(repo, number, PROJECTION),
            DISK_TTL_MS,
            now,
          );
          if (kept !== undefined) {
            yield* Ref.update(details, (all) =>
              new Map(all).set(key, { at: kept.at, detail: kept.value }),
            );
            return kept.value;
          }
        }
        const answer = yield* gh.pullRequest(repo, number);
        if (answer === undefined) {
          // Not cached: "gh has no such pull request" is usually a number typed
          // wrongly, and remembering it for an hour would outlive the typo.
          return undefined;
        }
        yield* Ref.update(details, (all) => new Map(all).set(key, { at: now, detail: answer }));
        yield* attempt("write the pull request cache", () =>
          writeDetail.run(repo, number, now, JSON.stringify(answer), PROJECTION),
        ).pipe(Effect.ignore);
        return answer;
      }),

    who: () => viewer(false),

    find: (repo: string, number: number) =>
      Effect.gen(function* () {
        const held = (yield* Ref.get(cache)).get(repo);
        const found = held?.prs.find((pr) => pr.number === number);
        if (found !== undefined) {
          return found;
        }
        const entry = yield* fresh(repo);
        return entry.prs.find((pr) => pr.number === number);
      }),
  };
});

export const layer: Layer.Layer<InboxFeed, never, Github | Db> = Layer.effect(InboxFeed)(make);
