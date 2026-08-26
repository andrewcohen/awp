import { type Thread, type ThreadMember, ThreadNotFound } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer } from "effect";

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
];

export class Threads extends Context.Service<
  Threads,
  {
    /** Every thread, newest first. Archived ones included — the caller filters. */
    readonly list: () => Effect.Effect<ReadonlyArray<Thread>, ThreadStoreError>;

    readonly create: (title: string) => Effect.Effect<Thread, ThreadStoreError>;

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

export const make = Effect.gen(function* () {
  const db = yield* Db;

  const insertThread = db.prepare("insert into threads values (?, ?, ?, ?)");
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

  /** Every thread with its members, in one pair of reads rather than N + 1. */
  const readAll = (): ReadonlyArray<Thread> => {
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
        members: members.get(id) ?? [],
      };
    });
  };

  const one = (thread: string): Thread | undefined =>
    readAll().find((entry) => entry.id === thread);

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
    );

  return {
    list: () => ask("cannot list threads", readAll),

    create: (title: string) =>
      Effect.sync(() => threadId(new Date(), Math.random())).pipe(
        Effect.flatMap((id) => {
          const made: Thread = {
            id,
            title: title.trim(),
            createdAt: new Date(),
            archivedAt: undefined,
            members: [],
          };
          return ask(`cannot create thread ${id}`, () => {
            insertThread.run(made.id, made.title, made.createdAt.getTime(), null);
            return made;
          });
        }),
      ),

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

    detach: (thread: string, member: ThreadMember) =>
      change(
        thread,
        `cannot detach ${member.project}/${member.workspace} from ${thread}`,
        () => void release.run(thread, member.project, member.workspace),
      ),
  };
});

/** Threads over whichever connection was provided. */
export const layer: Layer.Layer<Threads, never, Db> = Layer.effect(Threads)(make);
