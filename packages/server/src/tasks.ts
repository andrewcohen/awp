import { type Migration, Db, DbError, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer } from "effect";

// The tasks awp owns.
//
// ── why a store at all ─────────────────────────────────────────────────────
//
// A task list belonged to a *session*: `agent-tasks.ts` finds one by walking
// from a directory to Claude Code's transcripts to the newest task directory
// under them. That is a good reader and a bad home. A task cannot outlive the
// session that wrote it, cannot be seen from any other workspace, and cannot
// be about anything larger than the checkout it was written in.
//
//   before   one list per Claude Code session, on disk, found by mtime
//   after    one table, with tags, readable from anywhere in the window
//
// ── nothing here is the first writer ───────────────────────────────────────
//
// A store nobody writes to is empty forever, and the panel is deliberately
// read-only for now — so the writer is **ingest**: whatever a source already
// wrote, copied in. `todo-tasks.ts` reads a project's `TODO.md`, which is the
// one task list on this machine that already outlives a session and already
// holds the reasoning. Claude Code's own files are the second source and go
// through the same door.
//
// That keeps the promise `agent-tasks.ts` makes in its own comment — amoeba is
// not a second writer of somebody else's store — while giving tasks a home
// that survives.
//
// ── a tag, not a scope column ──────────────────────────────────────────────
//
// A task is not always about one thing:
//
//   thread     "paginate the tabular exports"
//   project    "this repo still has no integration tests"
//   global     "learn what jj fix actually rewrites"
//
// A field with three values forces every task to pick one and makes the third
// awkward. Tags do not, and they give the cross-cutting view for free: one
// query, filtered by whatever tag is interesting — a project, a thread, or
// nothing at all for everything.
//
// **And a tag is deliberately not a foreign key.** `thread:<id>` is a label
// somebody applied, and it should survive the thread being archived — the same
// argument this repo already makes for recording `parentId` rather than
// re-deriving it from jj. A tag left pointing at a thread that is gone is a
// claim about history, not a broken reference.

export class TaskStoreError extends Data.TaggedError("TaskStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/** Where a task came from. The half of its key that says who may change it. */
export type TaskSource = "todo" | "claude" | "awp";

export interface Task {
  readonly id: string;
  readonly subject: string;
  readonly description: string;
  /**
   * Free text, and deliberately not a constrained set.
   *
   * `agent-tasks.ts` says of Claude Code's own statuses "or whatever else it
   * gains", and a `check` here would turn an upstream addition into a daemon
   * that will not start. The ordering in {@link rank} is what treats the
   * values this window knows about specially; everything else sorts last.
   */
  readonly status: string;
  readonly source: TaskSource;
  /** Unique within its source, so ingest is safe to run twice. */
  readonly sourceKey: string | undefined;
  /** The source's own ordering number, where it has one. */
  readonly sourceSeq: number | undefined;
  readonly tags: ReadonlyArray<string>;
  readonly createdAt: Date;
  readonly updatedAt: Date;
}

/**
 * The tasks tables.
 *
 * `unique (source, source_key)` is what makes ingest idempotent, and it does
 * one more thing worth stating: sqlite treats NULLs as distinct in a UNIQUE,
 * so every task with no source key — one typed here, when there is somewhere
 * to type it — is still its own row. One index gives both.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "tasks.001-initial",
    up: [
      `create table tasks (
         id          text primary key,
         subject     text not null,
         description text not null,
         status      text not null,
         source      text not null,
         source_key  text,
         source_seq  integer,
         created_at  integer not null,
         updated_at  integer not null,
         unique (source, source_key)
       ) strict`,
      `create table task_tags (
         task_id text not null references tasks (id) on delete cascade,
         tag     text not null,
         unique (task_id, tag)
       ) strict`,
      // By tag, because that is the only filter there is: the panel asks for a
      // project's tasks or a thread's, and without this every such read is a
      // scan of the whole table joined to the whole tag table.
      `create index task_tags_tag on task_tags (tag)`,
    ],
  },
];

/** What a caller may narrow a listing to. Both absent means everything. */
export interface TaskFilter {
  /** Every tag named has to be present — an AND, not an OR. */
  readonly tags?: ReadonlyArray<string> | undefined;
  /** Absent means every status, done ones included. */
  readonly statuses?: ReadonlyArray<string> | undefined;
}

/** One task as a source has it, before this store has given it an id. */
export interface Incoming {
  readonly source: TaskSource;
  readonly sourceKey: string;
  readonly sourceSeq?: number;
  readonly subject: string;
  readonly description: string;
  readonly status: string;
  readonly tags: ReadonlyArray<string>;
}

export class Tasks extends Context.Service<
  Tasks,
  {
    /** Tasks, filtered, in reading order — what is underway, then what is next. */
    readonly list: (filter?: TaskFilter) => Effect.Effect<ReadonlyArray<Task>, TaskStoreError>;

    /**
     * Put a source's tasks in, and take out the ones it no longer has.
     *
     * One call rather than an upsert per task, because the deletion half needs
     * to know the whole set: a task removed from a `TODO.md` — which is how
     * this repository marks one finished — has to leave the table, and there
     * is no record whose absence a per-task write could notice. The same shape
     * as `useJobs`' refresh, and for the same reason.
     *
     * Scoped by `(source, keyPrefix)` so one project's ingest cannot delete
     * another's: every key from a project is prefixed with it.
     *
     * Answers what changed, so a caller can say whether a sweep was worth it.
     */
    readonly ingest: (
      source: TaskSource,
      keyPrefix: string,
      tasks: ReadonlyArray<Incoming>,
    ) => Effect.Effect<
      { readonly added: number; readonly changed: number; readonly removed: number },
      TaskStoreError
    >;
  }
>()("awp/Tasks") {}

/**
 * Reading order: what is being done, then what is next, then everything else.
 *
 * The same ranking `agent-tasks.ts` applies to Claude Code's list, so the two
 * sources read the same way round in one panel.
 */
const RANK: Record<string, number> = { in_progress: 0, blocked: 1, pending: 2 };

const rank = (status: string): number => RANK[status] ?? 9;

/**
 * Sort key. Within a status, by the source's own number.
 *
 * Numeric, because a source that counts its tasks writes `10` after `2` and
 * text order puts them the other way round — which is a task list in an order
 * nobody wrote.
 */
export const order = (a: Task, b: Task): number =>
  rank(a.status) - rank(b.status) ||
  (a.sourceSeq ?? Number.MAX_SAFE_INTEGER) - (b.sourceSeq ?? Number.MAX_SAFE_INTEGER) ||
  a.createdAt.getTime() - b.createdAt.getTime() ||
  a.id.localeCompare(b.id);

const ask = <A>(reason: string, run: () => A): Effect.Effect<A, TaskStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error: DbError) => new TaskStoreError({ reason, cause: error.cause })),
  );

const text = (value: unknown): string => (typeof value === "string" ? value : "");

const count = (value: unknown): number | undefined =>
  typeof value === "number" ? value : undefined;

/**
 * A task's id: its source and its key, joined.
 *
 * Derived rather than minted, and that is what makes ingest a single upsert
 * with nothing to look up first. It also means the id of an ingested task is
 * stable across a wipe of the table, so a tag applied to one — the next thing
 * this store will want — is not lost by a re-read of the file it came from.
 */
export const taskId = (source: TaskSource, key: string): string => `${source}:${key}`;

export const make = Effect.gen(function* () {
  const db = yield* Db;

  const upsert = db.prepare(
    `insert into tasks (id, subject, description, status, source, source_key, source_seq,
                        created_at, updated_at)
     values (?, ?, ?, ?, ?, ?, ?, ?, ?)
     on conflict (source, source_key) do update set
       subject     = excluded.subject,
       description = excluded.description,
       status      = excluded.status,
       source_seq  = excluded.source_seq,
       updated_at  = excluded.updated_at
     where subject     is not excluded.subject
        or description is not excluded.description
        or status      is not excluded.status
        or source_seq  is not excluded.source_seq`,
  );
  const addTag = db.prepare(
    "insert into task_tags (task_id, tag) values (?, ?) on conflict do nothing",
  );
  const dropTags = db.prepare("delete from task_tags where task_id = ?");
  const drop = db.prepare("delete from tasks where id = ?");
  const keysOf = db.prepare(
    "select id, subject, description, status, source_seq from tasks where source = ? and source_key like ?",
  );
  const allTags = db.prepare("select task_id, tag from task_tags");

  const rows = db.prepare(
    `select id, subject, description, status, source, source_key, source_seq,
            created_at, updated_at
       from tasks`,
  );

  return {
    list: (filter?: TaskFilter) =>
      ask("read the tasks", () => {
        // Read whole and filtered here rather than composed into SQL. The
        // table is a person's task list — tens of rows, not thousands — and a
        // dynamic `where` built from a caller's tag array is the one shape in
        // this file that could take a value straight into a statement.
        const tags = new Map<string, string[]>();
        for (const row of allTags.all()) {
          const id = text(row["task_id"]);
          const held = tags.get(id) ?? [];
          held.push(text(row["tag"]));
          tags.set(id, held);
        }
        const wanted = filter?.tags ?? [];
        const statuses = filter?.statuses;
        return rows
          .all()
          .map((row): Task => {
            const id = text(row["id"]);
            return {
              id,
              subject: text(row["subject"]),
              description: text(row["description"]),
              status: text(row["status"]),
              source: text(row["source"]) as TaskSource,
              sourceKey: typeof row["source_key"] === "string" ? row["source_key"] : undefined,
              sourceSeq: count(row["source_seq"]),
              tags: (tags.get(id) ?? []).toSorted(),
              createdAt: new Date(count(row["created_at"]) ?? 0),
              updatedAt: new Date(count(row["updated_at"]) ?? 0),
            };
          })
          .filter(
            (task) =>
              wanted.every((tag) => task.tags.includes(tag)) &&
              (statuses === undefined || statuses.includes(task.status)),
          )
          .toSorted(order);
      }),

    ingest: (source: TaskSource, keyPrefix: string, tasks: ReadonlyArray<Incoming>) =>
      ask(`ingest ${tasks.length} tasks from ${source}`, () => {
        const now = Date.now();
        const held = new Map(
          keysOf.all(source, `${keyPrefix}%`).map((row) => [text(row["id"]), row] as const),
        );
        let added = 0;
        let changed = 0;
        for (const task of tasks) {
          const id = taskId(source, task.sourceKey);
          const before = held.get(id);
          held.delete(id);
          upsert.run(
            id,
            task.subject,
            task.description,
            task.status,
            source,
            task.sourceKey,
            task.sourceSeq ?? null,
            now,
            now,
          );
          if (before === undefined) {
            added += 1;
          } else if (
            text(before["subject"]) !== task.subject ||
            text(before["description"]) !== task.description ||
            text(before["status"]) !== task.status
          ) {
            changed += 1;
          }
          // Replaced wholesale rather than merged: the tags an ingest applies
          // are derived from the source, so a source that stops implying one
          // must stop carrying it. A tag a *person* applies will need its own
          // table or a marked kind — see the note above the migration.
          dropTags.run(id);
          for (const tag of task.tags) {
            addTag.run(id, tag);
          }
        }
        // Whatever the source no longer has. This is how a task finishes in
        // this repository: the entry leaves `TODO.md`.
        for (const id of held.keys()) {
          drop.run(id);
        }
        return { added, changed, removed: held.size };
      }),
  };
});

export const layer: Layer.Layer<Tasks, never, Db> = Layer.effect(Tasks)(make);
