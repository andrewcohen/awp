// The durable store: jobs as rows in the shared awp database.
//
// The reason a job survives a restart at all. Everything the runner needs to
// resume — the completed steps, the attempt it was on, the input — is a row
// here, so a daemon that dies mid-workspace-create comes back and carries on
// from step four rather than from step one.
//
// The connection is not opened here. It comes from `@awp-kit/store`, which is
// what puts jobs and threads in one file — so a job that claims a workspace for
// a thread writes both or neither. See that package for why the file is one
// file and why migrations are named rather than numbered.
//
// `store.test.ts` runs one suite against this and the memory store together,
// which is what keeps them honest: an interface with two implementations is
// only as good as the one nobody runs, and that is always the one written
// second.

import {
  Db,
  type DbError,
  type Migration,
  type Value,
  attempt as dbAttempt,
  layer as dbLayer,
} from "@awp-kit/store";
import { Effect, Layer } from "effect";
import type { Cleanup, Job, JobId, JobStatus } from "./job";
import { JobStore, LOG_LINES, StoreError } from "./store";

/**
 * The jobs tables, as a list that only ever grows.
 *
 * One statement per line so a migration is a diff rather than a rewrite. A
 * name, once it has run anywhere, is fixed — renaming one makes it run again.
 *
 * `key` is UNIQUE rather than merely indexed: idempotency is the property being
 * claimed, and a claim the database will not enforce is a claim two concurrent
 * enqueues can break. The runner checks first; this is what makes the check
 * true rather than likely.
 *
 * `strict` on both tables. Without it sqlite will put a number in a text column
 * and hand it back as a number, which is exactly the class of bug the JSON
 * round trip in `enqueue` exists to catch one layer up.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "jobs.001-initial",
    up: [
      `create table jobs (
         id         text primary key,
         kind       text not null,
         title      text not null,
         key        text unique,
         input      text not null,
         status     text not null,
         attempt    integer not null,
         attempts   integer not null,
         steps      text not null,
         done       text not null,
         step       text,
         error      text,
         cleanup    text,
         created_at integer not null,
         started_at integer,
         ended_at   integer
       ) strict`,
      `create index jobs_created_at on jobs (created_at desc, id desc)`,
      // Its own table rather than a column, because a log is appended to a
      // great many times and a job row rewritten per line is the whole record
      // rewritten per line.
      `create table job_logs (
         seq    integer primary key autoincrement,
         job_id text not null,
         line   text not null
       ) strict`,
      `create index job_logs_job on job_logs (job_id, seq)`,
    ],
  },
];

// Every call into sqlite goes through here, so a failure carries which one it
// was rather than a bare exception from three frames down. The store's own
// error type rather than the database's, because a caller catching this is
// reasoning about jobs and not about sqlite.
const attempt = <A>(reason: string, run: () => A): Effect.Effect<A, StoreError> =>
  dbAttempt(reason, run).pipe(
    Effect.mapError((error) => new StoreError({ reason, cause: error.cause })),
  );

const millis = (date: Date | undefined): number | null => date?.getTime() ?? null;
const date = (value: unknown): Date | undefined =>
  typeof value === "number" ? new Date(value) : undefined;
const text = (value: unknown): string | undefined =>
  typeof value === "string" ? value : undefined;

const toRow = (job: Job): ReadonlyArray<Value> => [
  job.id,
  job.kind,
  job.title,
  job.key ?? null,
  JSON.stringify(job.input ?? null),
  job.status,
  job.attempt,
  job.attempts,
  JSON.stringify(job.steps),
  JSON.stringify(job.done),
  job.step ?? null,
  job.error ?? null,
  job.cleanup ?? null,
  job.createdAt.getTime(),
  millis(job.startedAt),
  millis(job.endedAt),
];

const fromRow = (row: Record<string, unknown>): Job => ({
  id: String(row["id"]),
  kind: String(row["kind"]),
  title: String(row["title"]),
  key: text(row["key"]),
  input: JSON.parse(String(row["input"])) as unknown,
  status: String(row["status"]) as JobStatus,
  attempt: Number(row["attempt"]),
  attempts: Number(row["attempts"]),
  steps: JSON.parse(String(row["steps"])) as ReadonlyArray<string>,
  done: JSON.parse(String(row["done"])) as ReadonlyArray<string>,
  step: text(row["step"]),
  error: text(row["error"]),
  cleanup: text(row["cleanup"]) as Cleanup | undefined,
  createdAt: new Date(Number(row["created_at"])),
  startedAt: date(row["started_at"]),
  endedAt: date(row["ended_at"]),
});

export const makeSqlite = Effect.gen(function* () {
  const db = yield* Db;

  // Prepared once and kept, which is the point of preparing them.
  const put = db.prepare(
    `insert into jobs values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
       on conflict(id) do update set
         kind=excluded.kind, title=excluded.title, key=excluded.key,
         input=excluded.input, status=excluded.status, attempt=excluded.attempt,
         attempts=excluded.attempts, steps=excluded.steps, done=excluded.done,
         step=excluded.step,
         error=excluded.error, cleanup=excluded.cleanup,
         created_at=excluded.created_at, started_at=excluded.started_at,
         ended_at=excluded.ended_at`,
  );
  const byId = db.prepare("select * from jobs where id = ?");
  const byKeyStatement = db.prepare("select * from jobs where key = ?");
  const listAll = db.prepare("select * from jobs order by created_at desc, id desc");
  const insertLine = db.prepare("insert into job_logs (job_id, line) values (?, ?)");
  const readLog = db.prepare("select line from job_logs where job_id = ? order by seq");
  // Keeps only the newest LOG_LINES for a job. The useful part of a log a
  // person reads after a failure is its end.
  const trimLog = db.prepare(
    `delete from job_logs where job_id = ? and seq <= (
         select seq from job_logs where job_id = ? order by seq desc limit 1 offset ?
       )`,
  );

  // Over, and safe to forget. `dirty` is excluded even though its status is
  // terminal: compensation stopped partway, something is left behind that only
  // a person can put right, and this record is the only thing that says what.
  const FINISHED = `status in ('succeeded', 'failed', 'cancelled')
       and (cleanup is null or cleanup <> 'dirty')`;
  const countFinished = db.prepare(`select id from jobs where ${FINISHED}`);
  const dropFinishedLogs = db.prepare(
    `delete from job_logs where job_id in (select id from jobs where ${FINISHED})`,
  );
  const dropFinished = db.prepare(`delete from jobs where ${FINISHED}`);

  const one = (rows: ReadonlyArray<Record<string, unknown>>): Job | undefined =>
    rows.length === 0 ? undefined : fromRow(rows[0] as Record<string, unknown>);

  return {
    put: (job: Job) => attempt(`cannot write job ${job.id}`, () => void put.run(...toRow(job))),

    get: (id: JobId) => attempt(`cannot read job ${id}`, () => one(byId.all(id))),

    byKey: (key: string) =>
      attempt(`cannot read the job for key ${key}`, () => one(byKeyStatement.all(key))),

    list: () => attempt("cannot list jobs", () => listAll.all().map((row) => fromRow(row))),

    append: (id: JobId, lines: ReadonlyArray<string>) =>
      attempt(`cannot append to the log of ${id}`, () => {
        for (const line of lines) {
          insertLine.run(id, line);
        }
        trimLog.run(id, id, LOG_LINES);
      }),

    log: (id: JobId) =>
      attempt(`cannot read the log of ${id}`, () =>
        readLog.all(id).map((row) => String(row["line"])),
      ),

    // Logs first, then the jobs. `job_logs` has no foreign key to `jobs` — it
    // is written far more often than anything reads it, and a constraint check
    // per appended line is a cost paid on every line for a guarantee this is
    // the only place that needs. So the order here is the guarantee.
    forgetFinished: () =>
      attempt("cannot forget finished jobs", () => {
        const doomed = countFinished.all().length;
        dropFinishedLogs.run();
        dropFinished.run();
        return doomed;
      }),
  };
});

/**
 * Jobs over whichever connection was provided.
 *
 * The daemon provides one shared with threads. See `@awp-kit/store`.
 */
export const layerSqlite: Layer.Layer<JobStore, StoreError, Db> =
  Layer.effect(JobStore)(makeSqlite);

/**
 * Jobs over a connection of their own, migrated with only the jobs tables.
 *
 * For tests and for the probe. The daemon does not use it — a second connection
 * to the same file is the thing combining the stores was meant to remove.
 */
export const layerSqliteAt = (path: string): Layer.Layer<JobStore, StoreError | DbError> =>
  layerSqlite.pipe(Layer.provide(dbLayer(path, migrations)));
