import { type Thread, type ThreadMember, ThreadNotFound, type ThreadPr } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer, PubSub, Stream } from "effect";

// Where threads are kept, and the rules about what may be in them.
//
// ── one database, with jobs ────────────────────────────────────────────────
// This was a JSON file for about an hour, and the argument that moved it is the
// first real job: creating a workspace makes a jj workspace, starts sessions,
// *and* claims the workspace for a thread. Two stores means that job can record
// its own success and fail to record the thread, and nothing afterwards can say
// which happened. One connection makes it one transaction.
//
// What the file lost was being readable in an editor, which mattered while the
// shape of a thread was still being argued about and matters less than the
// above.
//
// ── two tables, not a JSON column ──────────────────────────────────────────
// Members are rows with a foreign key, so "a workspace belongs to at most one
// thread" is a UNIQUE constraint the database enforces rather than a rule this
// file remembers to apply. The alternative — a JSON array on the thread row —
// makes that rule a loop, and a loop is a thing that can be skipped.
//
// Enforced on write rather than resolved on read, for a reason that is about
// rendering: two threads claiming one workspace has no drawing. The sidebar
// would show it twice and a person would have to decide which claim was lying.
// The second claim wins outright and the first thread lets go.

/** The database would not answer. */
export class ThreadStoreError extends Data.TaggedError("ThreadStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The threads tables, as a list that only ever grows.
 *
 * Named rather than numbered, so appending one here cannot renumber the jobs
 * migrations sharing the same table of applied names. See `@awp-kit/store`.
 *
 * The UNIQUE on `(project, workspace)` in `thread_members` **is** the
 * one-thread rule. `on delete cascade` means deleting a thread takes its claims
 * with it rather than leaving rows pointing at nothing — which is only true
 * because the connection turns `foreign_keys` on, since sqlite does not.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "threads.001-initial",
    up: [
      `create table threads (
         id          text primary key,
         title       text not null,
         created_at  integer not null,
         archived_at integer
       ) strict`,
      `create table thread_members (
         thread_id text not null references threads (id) on delete cascade,
         project   text not null,
         workspace text not null,
         seq       integer not null,
         unique (project, workspace)
       ) strict`,
      `create index thread_members_thread on thread_members (thread_id, seq)`,
    ],
  },
  {
    // Appended rather than folded into 001, because 001 has run on this
    // machine: a migration's name is fixed the moment it has run anywhere, and
    // editing its statements makes the record a lie about what the file holds.
    //
    // `alter table add column` is the only shape sqlite offers, and it is the
    // reason this cannot be `not null` — an added column takes a constant
    // default and every existing row gets it. Null is the honest value here
    // anyway: a thread made before this migration did not branch from
    // anything, and saying so is different from guessing which.
    name: "threads.002-parent",
    up: [`alter table threads add column parent_id text references threads (id)`],
  },
  {
    // Which pull requests a thread is about.
    //
    // A table rather than a column, because a thread may be about several — see
    // `ThreadPr` in the contract. The UNIQUE is on `(project, number)` and not
    // on `(thread_id, project, number)`, which is the whole rule: a pull request
    // belongs to at most one thread, the same way a workspace does, so the
    // reviewQueue row pointing at a thread always has one answer.
    //
    // `on delete cascade` for the reason `thread_members` has it: deleting a
    // thread must not leave rows pointing at nothing. It also means the
    // rollback of a failed review takes its link with it.
    name: "threads.003-prs",
    up: [
      `create table thread_prs (
         thread_id text not null references threads (id) on delete cascade,
         project   text not null,
         number    integer not null,
         unique (project, number)
       ) strict`,
      `create index thread_prs_thread on thread_prs (thread_id)`,
    ],
  },
  {
    // Which *checkout* the pull request is about, and not merely which thread.
    //
    // A thread holds several workspaces and 003 recorded only the project, so
    // the read had no way to tell two members of one thread apart and guessed.
    // Seen live: a thread across three repositories with one pull request drew
    // that number on all three rows, two of them pointing into a repository
    // the row has nothing to do with.
    //
    // The table is emptied rather than back-filled. Every link is re-made from
    // the pull request list the next time it is read, so a guessed answer would
    // be a guess with a lifetime of minutes — and back-filling a `not null`
    // column is exactly the pressure that produces an empty string standing in
    // for an absent value, which this repository has paid for three times.
    //
    // Recreated rather than altered for the same reason: `alter table add
    // column` cannot add a `not null` one without a constant default, and the
    // only constant available is `''`.
    name: "threads.004-pr-workspace",
    up: [
      `drop table thread_prs`,
      `create table thread_prs (
         thread_id text not null references threads (id) on delete cascade,
         project   text not null,
         workspace text not null,
         number    integer not null,
         unique (project, number)
       ) strict`,
      `create index thread_prs_thread on thread_prs (thread_id)`,
    ],
  },
];

export class Threads extends Context.Service<
  Threads,
  {
    /** Every thread, newest first. Archived ones included — the caller filters. */
    readonly list: () => Effect.Effect<ReadonlyArray<Thread>, ThreadStoreError>;

    /**
     * Every thread again, each time any of them changes.
     *
     * Here rather than beside the handlers, because the handlers are not the
     * only writer: a job step claims a workspace, the reviewQueue join adopts a pull
     * request, an agent's own tool renames a thread. A feed fed by the calls a
     * window happens to make is a feed that is right about the window's own
     * edits and silent about everything else — which is the state three
     * separate re-read workarounds in the renderer existed to paper over.
     *
     * The whole list each time, for the reason `WorkspaceFactsChanges` sends
     * the whole table: it is a few kilobytes, and a delta would be machinery in
     * service of an economy nobody can measure. It is also the only shape that
     * can say a thread is *gone* — `deleteIfEmpty` removes one, and there is no
     * record left to send.
     *
     * Nothing is replayed, and the rule this repo already records is why: a
     * subscription answers what changes and a question answers what is, so a
     * client subscribes *and* asks, here and on every reconnect.
     */
    readonly changes: () => Stream.Stream<ReadonlyArray<Thread>>;

    /**
     * A thread, optionally branched from another.
     *
     * The parent is not checked to exist. It is a claim about intent recorded
     * at the moment it was made, and the caller has already looked the thread
     * up to resolve a base revision from it — checking again here would be a
     * second read answering a question already answered, and the answer that
     * matters was true then rather than now.
     */
    readonly create: (title: string, parent?: string) => Effect.Effect<Thread, ThreadStoreError>;

    /**
     * Put a thread back under an id it already had. Answers whether it did.
     *
     * The counterpart of {@link deleteIfEmpty}, and it exists because that one
     * left the system in a state nothing could re-enter. `create-workspace`'s
     * first step *checks* the thread is there and its undo *removes* it, so a
     * rollback destroyed the precondition of the step that would run first on
     * the retry — every rolled-back create was permanently unretryable, and
     * said so in a message about a thread that had been there a moment ago.
     *
     * Separate from `create` rather than an optional id on it, because the two
     * are different acts. `create` mints an id nothing has referred to yet;
     * this one satisfies references that already exist — a job record naming
     * the thread it was enqueued for.
     *
     * Idempotent: a thread already there is left exactly as it is and the
     * answer is `false`. It never overwrites a title somebody has since
     * changed.
     */
    readonly restore: (
      thread: string,
      title: string,
      parent?: string,
      /**
       * The pull request the thread was about, put back with it.
       *
       * Here rather than left to a later step because this is the one place a
       * rolled-back thread is rebuilt, and the link is part of what it was. A
       * retry that came back without it would leave the reviewQueue row unable to
       * find the thread that is being built for it, which is exactly the state
       * this function exists to prevent for the thread itself.
       */
      pr?: ThreadPr,
    ) => Effect.Effect<boolean, ThreadStoreError>;

    readonly rename: (
      thread: string,
      title: string,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /** Archive, or bring back — `archived: false` undoes it. */
    readonly archive: (
      thread: string,
      archived: boolean,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /**
     * Claim a workspace for this thread, releasing it from any other.
     *
     * Idempotent: claiming a pair the thread already holds changes nothing and
     * is not an error, which is what lets a job step call it twice.
     */
    readonly attach: (
      thread: string,
      member: ThreadMember,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /** Release a workspace. Also idempotent, and for the same reason. */
    readonly detach: (
      thread: string,
      member: ThreadMember,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /**
     * Record that this thread is about a pull request, taking it from whichever
     * thread held it before.
     *
     * The same shape as {@link attach}, and idempotent for the same reason: a
     * job step calls it, and a step is re-entered.
     */
    readonly link: (
      thread: string,
      pr: ThreadPr,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    readonly unlink: (
      thread: string,
      pr: ThreadPr,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /**
     * Remove a thread, but only while it holds nothing. Answers whether it did.
     *
     * The only deletion there is, and the emptiness check is the whole reason
     * it is safe to have one. A thread holding a workspace is the only record
     * that those checkouts were one piece of work — that is what `archive` is
     * for. A thread holding *nothing* is not a record of anything.
     *
     * It exists for the rollback of a create that failed. The thread is made by
     * the handler before the job is enqueued, so the job cannot create it —
     * but it can be the thing that takes it away, and an empty thread left in
     * the sidebar by a failure a person already saw is litter with no way to
     * sweep it.
     *
     * Idempotent both ways: a thread that is not there is already gone, and one
     * with members is left alone rather than refused. The runner re-enters an
     * undo, so neither may be an error.
     */
    readonly deleteIfEmpty: (thread: string) => Effect.Effect<boolean, ThreadStoreError>;
  }
>()("awp/Threads") {}
/**
 * A thread's id: the day it was made, and four characters to tell it from the
 * others made that day.
 *
 * The same spelling as a job id, deliberately. Both are things a person may end
 * up reading in a log line, and two id formats in one system is two things to
 * recognise for no gain.
 */
export const threadId = (now: Date, random: number): string => {
  const day = now.toISOString().slice(0, 10).replaceAll("-", "");
  const tail = Math.floor(random * 36 ** 4)
    .toString(36)
    .padStart(4, "0");
  return `${day}-${tail}`;
};

/** The store's own error, so a caller catching it reasons about threads. */
const ask = <A>(reason: string, run: () => A): Effect.Effect<A, ThreadStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error) => new ThreadStoreError({ reason, cause: error.cause })),
  );

const date = (value: unknown): Date | undefined =>
  typeof value === "number" ? new Date(value) : undefined;

const text = (value: unknown): string | undefined =>
  typeof value === "string" && value !== "" ? value : undefined;

export const make = Effect.gen(function* () {
  const db = yield* Db;

  // Columns named rather than positional. `insert into threads values (…)`
  // depends on the column order, and the order is now a function of which
  // migrations have run — a third one would silently shift every value along.
  const insertThread = db.prepare(
    "insert into threads (id, title, created_at, archived_at, parent_id) values (?, ?, ?, ?, ?)",
  );
  const setTitle = db.prepare("update threads set title = ? where id = ?");
  const setArchived = db.prepare("update threads set archived_at = ? where id = ?");
  const readThread = db.prepare("select * from threads where id = ?");
  const readThreads = db.prepare("select * from threads order by created_at desc, id desc");
  const readMembers = db.prepare(
    "select thread_id, project, workspace from thread_members order by thread_id, seq",
  );
  // The one-thread rule, as one statement. `on conflict do update` rather than
  // a delete-then-insert: the UNIQUE is on the pair, so the row already there
  // for another thread is the row this rewrites — which is both the release and
  // the claim, and cannot half happen.
  const claim = db.prepare(
    `insert into thread_members values (?, ?, ?,
       (select coalesce(max(seq), 0) + 1 from thread_members where thread_id = ?))
     on conflict (project, workspace) do update set
       thread_id = excluded.thread_id, seq = excluded.seq`,
  );
  const release = db.prepare(
    "delete from thread_members where thread_id = ? and project = ? and workspace = ?",
  );
  // The same shape as `claim`, and the same reason: the UNIQUE is on
  // `(project, number)`, so the row already there for another thread is the row
  // this rewrites — the release and the claim in one statement.
  const linkPr = db.prepare(
    `insert into thread_prs (thread_id, project, workspace, number) values (?, ?, ?, ?)
     on conflict (project, number) do update set
       thread_id = excluded.thread_id, workspace = excluded.workspace`,
  );
  const unlinkPr = db.prepare(
    "delete from thread_prs where thread_id = ? and project = ? and number = ?",
  );
  const readPrs = db.prepare(
    "select thread_id, project, workspace, number from thread_prs order by number",
  );
  // One statement, so the emptiness check and the delete cannot disagree. A
  // thread that gains a workspace between a read and a write is exactly the
  // race this shape removes.
  const dropEmpty = db.prepare(
    `delete from threads
       where id = ? and not exists (select 1 from thread_members where thread_id = ?)`,
  );

  /** Every thread with its members and pull requests, in three reads not N + 1. */
  const readAll = (): ReadonlyArray<Thread> => {
    const prs = new Map<string, ThreadPr[]>();
    for (const row of readPrs.all()) {
      const id = String(row["thread_id"]);
      prs.set(id, [
        ...(prs.get(id) ?? []),
        {
          project: String(row["project"]),
          workspace: String(row["workspace"]),
          number: Number(row["number"]),
        },
      ]);
    }
    const members = new Map<string, ThreadMember[]>();
    for (const row of readMembers.all()) {
      const id = String(row["thread_id"]);
      members.set(id, [
        ...(members.get(id) ?? []),
        { project: String(row["project"]), workspace: String(row["workspace"]) },
      ]);
    }
    return readThreads.all().map((row) => {
      const id = String(row["id"]);
      return {
        id,
        title: String(row["title"]),
        createdAt: new Date(Number(row["created_at"])),
        archivedAt: date(row["archived_at"]),
        parentId: text(row["parent_id"]),
        members: members.get(id) ?? [],
        prs: prs.get(id) ?? [],
      };
    });
  };

  const one = (thread: string): Thread | undefined =>
    readAll().find((entry) => entry.id === thread);

  // Sliding rather than dropping, which is the difference between an event and
  // a state. `Pages` drops, because a navigation nobody could receive is spent;
  // here the newest list is the only one that is *true*, so a full buffer must
  // lose the oldest snapshot and never the newest.
  const hub = yield* PubSub.sliding<ReadonlyArray<Thread>>(16);

  /**
   * Say that something changed, having already changed it.
   *
   * Failure is swallowed on purpose, and it is the one place in this file that
   * is. The write has happened by the time this runs, so a read that could not
   * answer must not turn a successful rename into a refusal — the caller would
   * report a failure for work that is on disk. What it costs is a stale window
   * until the next question, which is what every client is already built to
   * survive.
   */
  const announce = ask("cannot read threads", readAll).pipe(
    Effect.flatMap((all) => PubSub.publish(hub, all)),
    Effect.ignore,
  );

  /**
   * Run a write, but only for a thread that exists.
   *
   * The existence check and the write are not in a transaction together, and do
   * not need to be: the daemon is the only writer, and a thread cannot be
   * deleted at all yet. When deletion arrives this becomes the place that has
   * to change.
   */
  const change = (
    thread: string,
    reason: string,
    run: () => void,
  ): Effect.Effect<Thread, ThreadStoreError | ThreadNotFound> =>
    ask(reason, () => readThread.all(thread).length > 0).pipe(
      Effect.flatMap((exists) =>
        exists ? Effect.void : Effect.fail(new ThreadNotFound({ thread })),
      ),
      Effect.flatMap(() => ask(reason, run)),
      Effect.flatMap(() =>
        ask(reason, () => one(thread)).pipe(
          Effect.flatMap((found) =>
            found === undefined
              ? Effect.fail(new ThreadNotFound({ thread }))
              : Effect.succeed(found),
          ),
        ),
      ),
      // Every write that goes through here announces itself — rename, archive,
      // attach, detach, link, unlink — so a writer added later cannot forget.
      Effect.tap(() => announce),
    );

  return {
    list: () => ask("cannot list threads", readAll),

    changes: () => Stream.fromPubSub(hub),

    create: (title: string, parent?: string) =>
      Effect.sync(() => threadId(new Date(), Math.random())).pipe(
        Effect.flatMap((id) => {
          const made: Thread = {
            id,
            title: title.trim(),
            createdAt: new Date(),
            archivedAt: undefined,
            parentId: parent,
            members: [],
            // A new thread is about nothing yet. What links a pull request to
            // one is a separate act — `link` — because a thread is usually
            // named before anybody knows whether it will have a PR at all.
            prs: [],
          };
          return ask(`cannot create thread ${id}`, () => {
            insertThread.run(
              made.id,
              made.title,
              made.createdAt.getTime(),
              null,
              made.parentId ?? null,
            );
            return made;
          });
        }),
        Effect.tap(() => announce),
      ),

    restore: (thread: string, title: string, parent?: string, pr?: ThreadPr) =>
      ask(`cannot restore thread ${thread}`, () => {
        // Asked first rather than caught: an insert that conflicts is a normal
        // outcome here — a retry of a job whose rollback never got as far as
        // the thread — and turning it into an exception to swallow would hide
        // a real primary-key collision alongside it.
        // `.all`, not `.get`: only the intersection of bun:sqlite and
        // node:sqlite is used here, and the daemon and vitest are on different
        // ones.
        if (readThread.all(thread).length > 0) {
          return false;
        }
        // `createdAt` is now, and honestly so. The original moment is gone
        // with the row, and inventing one from the id's date prefix would put
        // a thread in the sidebar's ordering where the evidence does not.
        insertThread.run(thread, title.trim(), Date.now(), null, parent ?? null);
        // Part of what the thread was, so it goes back with it. A retry whose
        // thread came back without its pull request would leave the reviewQueue row
        // unable to find the thread being built for it.
        if (pr !== undefined) {
          linkPr.run(thread, pr.project, pr.workspace, pr.number);
        }
        return true;
        // Only when it actually put one back. A restore that found the thread
        // already there changed nothing, and a snapshot identical to the last
        // one is a re-render every client pays for and nobody asked for.
      }).pipe(Effect.tap((made) => (made ? announce : Effect.void))),

    rename: (thread: string, title: string) =>
      change(
        thread,
        `cannot rename thread ${thread}`,
        () => void setTitle.run(title.trim(), thread),
      ),

    archive: (thread: string, archived: boolean) =>
      change(
        thread,
        `cannot archive thread ${thread}`,
        () => void setArchived.run(archived ? Date.now() : null, thread),
      ),

    attach: (thread: string, member: ThreadMember) =>
      change(
        thread,
        `cannot attach ${member.project}/${member.workspace} to ${thread}`,
        () => void claim.run(thread, member.project, member.workspace, thread),
      ),

    deleteIfEmpty: (thread: string) =>
      ask(`cannot delete thread ${thread}`, () => {
        const before = readThread.all(thread).length;
        dropEmpty.run(thread, thread);
        return before > 0 && readThread.all(thread).length === 0;
        // Same rule as `restore`: only a deletion that happened is news. This
        // is the one change no per-record feed could carry — the record is what
        // went away.
      }).pipe(Effect.tap((gone) => (gone ? announce : Effect.void))),

    detach: (thread: string, member: ThreadMember) =>
      change(
        thread,
        `cannot detach ${member.project}/${member.workspace} from ${thread}`,
        () => void release.run(thread, member.project, member.workspace),
      ),

    link: (thread: string, pr: ThreadPr) =>
      change(
        thread,
        `cannot link ${pr.project}#${pr.number} to ${thread}`,
        () => void linkPr.run(thread, pr.project, pr.workspace, pr.number),
      ),

    unlink: (thread: string, pr: ThreadPr) =>
      change(
        thread,
        `cannot unlink ${pr.project}#${pr.number} from ${thread}`,
        () => void unlinkPr.run(thread, pr.project, pr.number),
      ),
  };
});

/** Threads over whichever connection was provided. */
export const layer: Layer.Layer<Threads, never, Db> = Layer.effect(Threads)(make);
