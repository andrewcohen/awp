// The durable store: one sqlite file, two runtimes.
//
// The reason a job survives a restart at all. Everything the runner needs to
// resume — the completed steps, the attempt it was on, the input — is a row
// here, so a daemon that dies mid-workspace-create comes back and carries on
// from step four rather than from step one.
//
// ── why not bun:sqlite, plainly ────────────────────────────────────────────
// The daemon runs under Bun, which has `bun:sqlite` and does not have
// `node:sqlite`. The tests run under vitest on Node, which is the other way
// round. A static `import "bun:sqlite"` therefore makes this file unloadable by
// the only thing that tests it, which is the worst of both.
//
// So the import is dynamic and chosen at open time. The surface used below is
// deliberately the intersection of the two — positional `?` parameters,
// `exec`, `prepare().run()`, `prepare().all()` — and nothing else, because
// every additional call is another place the two can disagree without saying
// so. Named parameters in particular are spelled differently by each and are
// not used here for that reason alone.
//
// `store.test.ts` runs one suite against both stores, which is what keeps this
// honest: an interface with two implementations is only as good as the one
// nobody runs, and that is always the one written second.

import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { Effect, Layer } from "effect";
import type { Cleanup, Job, JobId, JobStatus } from "./job";
import { JobStore, LOG_LINES, StoreError } from "./store";

type Value = string | number | null;

interface Statement {
  run: (...parameters: ReadonlyArray<Value>) => unknown;
  all: (...parameters: ReadonlyArray<Value>) => ReadonlyArray<Record<string, unknown>>;
}

interface Db {
  exec: (sql: string) => unknown;
  prepare: (sql: string) => Statement;
  close: () => unknown;
}

/** True under Bun, which needs the other module name. */
const underBun = (): boolean => (globalThis as { readonly Bun?: unknown }).Bun !== undefined;

const openDb = (path: string): Effect.Effect<Db, StoreError> =>
  Effect.tryPromise({
    try: async () => {
      if (path !== ":memory:") {
        mkdirSync(dirname(path), { recursive: true });
      }
      if (underBun()) {
        const { Database } = (await import("bun:sqlite")) as {
          readonly Database: new (path: string) => Db;
        };
        return new Database(path);
      }
      const { DatabaseSync } = (await import("node:sqlite")) as {
        readonly DatabaseSync: new (path: string) => Db;
      };
      return new DatabaseSync(path);
    },
    catch: (cause) => new StoreError({ reason: `cannot open ${path}`, cause }),
  });

// One statement per line so a migration is a diff rather than a rewrite.
//
// `key` is UNIQUE rather than merely indexed: idempotency is the property being
// claimed, and a claim the database will not enforce is a claim two concurrent
// enqueues can break. The runner checks first; this is what makes the check
// true rather than likely.
/**
 * Bump when anything in `SCHEMA` changes shape.
 *
 * There is no migration machinery here yet, and pretending otherwise is what
 * actually broke: adding `steps` to the record left `create table if not
 * exists` looking at a table without the column, so every write failed and the
 * daemon was dead on arrival with an error about a column count. A version that
 * does not match the file is therefore handled by **discarding the table and
 * making it again** — which is a real loss and is only defensible while nothing
 * in awp enqueues real work. The moment it does, this needs to become a list of
 * migrations keyed by this same number.
 */
const SCHEMA_VERSION = 1;

const SCHEMA = [
  `create table if not exists jobs (
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
   )`,
  `create index if not exists jobs_created_at on jobs (created_at desc, id desc)`,
  // Its own table rather than a column, because a log is appended to a great
  // many times and a job row rewritten per line is the whole record rewritten
  // per line.
  `create table if not exists job_logs (
     seq    integer primary key autoincrement,
     job_id text not null,
     line   text not null
   )`,
  `create index if not exists job_logs_job on job_logs (job_id, seq)`,
];

// Every call into sqlite goes through here, so a failure carries which one it
// was rather than a bare exception from three frames down.
const attempt = <A>(reason: string, run: () => A): Effect.Effect<A, StoreError> =>
  Effect.try({ try: run, catch: (cause) => new StoreError({ reason, cause }) });

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

export const makeSqlite = (path: string) =>
  Effect.gen(function* () {
    const db = yield* Effect.acquireRelease(openDb(path), (open) =>
      Effect.sync(() => void open.close()),
    );

    yield* Effect.try({
      try: () => {
        // WAL so a reader — a probe, or a second window's daemon — is not
        // locked out by the runner writing a step transition.
        db.exec("pragma journal_mode = wal");
        db.exec("pragma foreign_keys = on");

        // A brand new file reads 0, which is not the current version either —
        // the drops are no-ops there and the effect is the same.
        const found = db.prepare("pragma user_version").all()[0]?.["user_version"];
        if (Number(found ?? 0) !== SCHEMA_VERSION) {
          db.exec("drop table if exists job_logs");
          db.exec("drop table if exists jobs");
        }
        for (const statement of SCHEMA) {
          db.exec(statement);
        }
        // Not a bound parameter: sqlite will not take one in a pragma. Safe
        // because the value is a literal in this file rather than anything a
        // caller supplies.
        db.exec(`pragma user_version = ${SCHEMA_VERSION}`);
      },
      catch: (cause) => new StoreError({ reason: "cannot prepare the schema", cause }),
    });

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
    };
  });

/** A store in a file, kept for as long as the layer is. */
export const layerSqlite = (path: string): Layer.Layer<JobStore, StoreError> =>
  Layer.effect(JobStore)(makeSqlite(path));
