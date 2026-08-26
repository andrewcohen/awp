// One sqlite file, opened once, shared by everything that writes to it.
//
// ── why one file ───────────────────────────────────────────────────────────
// Jobs and threads were separate stores, and the thing that settled it is the
// first real job: creating a workspace makes a jj workspace, starts sessions,
// *and* claims the workspace for a thread. Two files means that job can record
// its own success and fail to record the thread, and nothing afterwards can say
// which of the two happened. One file makes it one transaction.
//
// ── why not a version number ───────────────────────────────────────────────
// The jobs store used `pragma user_version` and rebuilt the tables when it
// disagreed, which is a real loss of data and was only ever defensible while
// nothing enqueued real work. A counter also cannot survive two owners: jobs
// appending a migration would renumber threads'.
//
// So migrations are named and recorded, and a name is applied or it is not.
// Each package exports its own list, the daemon hands over all of them, and
// appending to one list cannot disturb the other.
//
// ── why the imports are dynamic ────────────────────────────────────────────
// The daemon runs under Bun, which has `bun:sqlite` and not `node:sqlite`.
// vitest runs on Node, which is the other way round. A static import therefore
// makes this file unloadable by the only thing that tests it.
//
// The surface used is the intersection of the two drivers — positional `?`
// parameters, `exec`, `prepare().run()`, `prepare().all()` — and nothing else,
// because every extra call is another place they can disagree without saying
// so. Named parameters in particular are spelled differently by each. vitest
// can only ever exercise the Node arm, which is what `probe:jobs-store` is for.

import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { Context, Data, Effect, Layer } from "effect";

/** What a positional `?` will take. Everything else is JSON on the way in. */
export type Value = string | number | null;

export interface Statement {
  readonly run: (...parameters: ReadonlyArray<Value>) => unknown;
  readonly all: (...parameters: ReadonlyArray<Value>) => ReadonlyArray<Record<string, unknown>>;
}

/** The intersection of `bun:sqlite` and `node:sqlite`, and nothing more. */
export interface Connection {
  readonly exec: (sql: string) => unknown;
  readonly prepare: (sql: string) => Statement;
  readonly close: () => unknown;
}

export class DbError extends Data.TaggedError("DbError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The open connection.
 *
 * A service so that both stores get the *same* one — which is the entire point
 * of combining the files, and would be undone by each of them opening the path
 * for itself.
 */
export class Db extends Context.Service<Db, Connection>()("awp/Db") {}

/**
 * One step in a schema's history.
 *
 * `name` is the identity and must never change once it has run anywhere: it is
 * what the file records, so renaming one makes it run a second time. Prefix it
 * with the package — `jobs.001-initial` — because the names of two packages'
 * migrations share one table.
 */
export interface Migration {
  readonly name: string;
  readonly up: ReadonlyArray<string>;
}

/**
 * The pragmas, and why each one is here.
 *
 * `journal_mode` is stored in the file and survives; the rest are per
 * connection and have to be set every time, which is a thing to know before
 * wondering why one of them stopped applying.
 */
const PRAGMAS = [
  // A reader — a probe, or a second window — is not locked out while the
  // runner writes a step transition.
  "pragma journal_mode = wal",
  // Off by default in sqlite, which surprises everybody exactly once. A
  // declared reference that is not enforced is a comment.
  "pragma foreign_keys = on",
  // Wait for a writer rather than failing SQLITE_BUSY immediately. The daemon
  // is one process, but a probe run beside it is a second connection.
  "pragma busy_timeout = 5000",
  // Safe under WAL — a crash cannot corrupt the database, it can only lose the
  // last transaction or two — and much faster than `full`.
  "pragma synchronous = normal",
];

const MIGRATIONS_TABLE = `create table if not exists schema_migrations (
   name       text primary key,
   applied_at integer not null
 ) strict`;

const underBun = (): boolean => (globalThis as { readonly Bun?: unknown }).Bun !== undefined;

const open = (path: string): Effect.Effect<Connection, DbError> =>
  Effect.tryPromise({
    try: async () => {
      if (path !== ":memory:") {
        mkdirSync(dirname(path), { recursive: true });
      }
      if (underBun()) {
        const { Database } = (await import("bun:sqlite")) as {
          readonly Database: new (path: string) => Connection;
        };
        return new Database(path);
      }
      const { DatabaseSync } = (await import("node:sqlite")) as {
        readonly DatabaseSync: new (path: string) => Connection;
      };
      return new DatabaseSync(path);
    },
    catch: (cause) => new DbError({ reason: `cannot open ${path}`, cause }),
  });

/**
 * Apply whatever has not been applied, in the order given.
 *
 * Each migration runs inside its own transaction together with the row that
 * records it, so a statement that fails leaves neither the change nor the
 * claim to have made it. Failing halfway with the name already written is the
 * one outcome that cannot be recovered from by running again.
 */
export const migrate = (
  db: Connection,
  migrations: ReadonlyArray<Migration>,
): Effect.Effect<ReadonlyArray<string>, DbError> =>
  Effect.try({
    try: () => {
      db.exec(MIGRATIONS_TABLE);
      const applied = new Set(
        db
          .prepare("select name from schema_migrations")
          .all()
          .map((row) => String(row["name"])),
      );

      const ran: string[] = [];
      const record = db.prepare("insert into schema_migrations values (?, ?)");
      const now = Date.now();

      for (const migration of migrations) {
        if (applied.has(migration.name)) {
          continue;
        }
        db.exec("begin");
        try {
          for (const statement of migration.up) {
            db.exec(statement);
          }
          record.run(migration.name, now);
          db.exec("commit");
        } catch (cause) {
          db.exec("rollback");
          throw new Error(`migration ${migration.name} failed: ${String(cause)}`, { cause });
        }
        ran.push(migration.name);
      }
      return ran;
    },
    catch: (cause) => new DbError({ reason: "cannot migrate the database", cause }),
  });

export const make = (path: string, migrations: ReadonlyArray<Migration>) =>
  Effect.gen(function* () {
    const db = yield* Effect.acquireRelease(open(path), (connection) =>
      Effect.sync(() => void connection.close()),
    );

    yield* Effect.try({
      try: () => {
        for (const pragma of PRAGMAS) {
          db.exec(pragma);
        }
      },
      catch: (cause) => new DbError({ reason: "cannot set the pragmas", cause }),
    });

    yield* migrate(db, migrations);
    return db;
  });

/** The connection, open for as long as the layer is. */
export const layer = (
  path: string,
  migrations: ReadonlyArray<Migration>,
): Layer.Layer<Db, DbError> => Layer.effect(Db)(make(path, migrations));

/**
 * Every call into sqlite goes through here, so a failure says which one it was
 * rather than arriving as a bare exception from three frames down.
 */
export const attempt = <A>(reason: string, run: () => A): Effect.Effect<A, DbError> =>
  Effect.try({ try: run, catch: (cause) => new DbError({ reason, cause }) });
