import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { Effect, Result } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { Db, type Migration, layer, migrate } from "./index";

// The migrator, and the pragmas.
//
// Worth its own suite rather than being covered incidentally by the stores
// above it, because the property that matters — a name that has run does not
// run again — has no visible symptom when it breaks. The jobs store used to
// discard its tables on a version mismatch, and the way that read from the
// outside was a daemon starting normally with nothing in it.

const scratch = mkdtempSync(join(tmpdir(), "awp-store-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `db-${(files += 1)}.sqlite`);

const on = <A>(
  path: string,
  migrations: ReadonlyArray<Migration>,
  program: (db: Db["Service"]) => A,
): Promise<A> =>
  Effect.gen(function* () {
    const db = yield* Db;
    return program(db);
  }).pipe(Effect.provide(layer(path, migrations)), Effect.scoped, Effect.orDie, Effect.runPromise);

const one: Migration = { name: "a.001", up: ["create table a (id text primary key) strict"] };
const two: Migration = { name: "a.002", up: ["alter table a add column note text"] };
const other: Migration = { name: "b.001", up: ["create table b (id text primary key) strict"] };

const tables = (path: string): ReadonlyArray<string> => {
  const db = new DatabaseSync(path);
  const found = db
    .prepare("select name from sqlite_master where type = 'table' order by name")
    .all()
    .map((row) => String(row["name"]));
  db.close();
  return found;
};

const applied = (path: string): ReadonlyArray<string> => {
  const db = new DatabaseSync(path);
  const found = db
    .prepare("select name from schema_migrations order by name")
    .all()
    .map((row) => String(row["name"]));
  db.close();
  return found;
};

describe("migrations", () => {
  test("a fresh database gets everything", async () => {
    const path = file();
    await on(path, [one, other], () => 0);

    expect(applied(path)).toEqual(["a.001", "b.001"]);
    expect(tables(path)).toContain("a");
    expect(tables(path)).toContain("b");
  });

  test("what has already run does not run again", async () => {
    const path = file();
    await on(path, [one], () => 0);
    // `create table` rather than `create table if not exists`, deliberately: a
    // second run that failed to consult the record would fail outright rather
    // than quietly do nothing. Loud is the point.
    await on(path, [one], () => 0);

    expect(applied(path)).toEqual(["a.001"]);
  });

  test("appending a migration runs only the new one", async () => {
    const path = file();
    await on(path, [one], () => 0);
    await on(path, [one, two], () => 0);

    expect(applied(path)).toEqual(["a.001", "a.002"]);
  });

  test("one package's list can grow without disturbing another's", async () => {
    const path = file();
    await on(path, [one, other], () => 0);
    // `a` gains a migration. Under a single version counter this would
    // renumber `b.001` and re-run it; under names it cannot.
    await on(path, [one, two, other], () => 0);

    expect(applied(path)).toEqual(["a.001", "a.002", "b.001"]);
  });

  test("a migration that fails leaves neither the change nor the claim", async () => {
    const path = file();
    const broken: Migration = {
      name: "a.002-broken",
      up: ["create table c (id text primary key) strict", "this is not sql"],
    };

    const outcome = await Effect.gen(function* () {
      yield* Db;
    }).pipe(
      Effect.provide(layer(path, [one, broken])),
      Effect.scoped,
      Effect.result,
      Effect.runPromise,
    );

    expect(Result.isFailure(outcome)).toBe(true);
    // The first migration stands; the broken one is not recorded, and the
    // table its first statement created is gone with the rollback. Recording a
    // name for work that did not finish is the one state nothing can recover
    // from by running again.
    expect(applied(path)).toEqual(["a.001"]);
    expect(tables(path)).not.toContain("c");
  });
});

describe("pragmas", () => {
  test("foreign keys are enforced, which sqlite does not do by default", async () => {
    const path = file();
    const parents: Migration = {
      name: "p.001",
      up: [
        "create table parent (id text primary key) strict",
        `create table child (
           id        text primary key,
           parent_id text not null references parent (id) on delete cascade
         ) strict`,
      ],
    };

    const threw = await on(path, [parents], (db) => {
      try {
        db.prepare("insert into child values (?, ?)").run("c", "nobody");
        return false;
      } catch {
        return true;
      }
    });

    expect(threw).toBe(true);
  });

  test("the journal is wal, which is what lets a probe read while the daemon writes", async () => {
    const path = file();
    const mode = await on(path, [one], (db) =>
      String(db.prepare("pragma journal_mode").all()[0]?.["journal_mode"]),
    );

    expect(mode).toBe("wal");
  });

  test("a strict table refuses what it cannot losslessly convert", async () => {
    const path = file();
    const strict: Migration = {
      name: "s.001",
      up: ["create table counted (id text primary key, n integer not null) strict"],
    };

    const threw = await on(path, [strict], (db) => {
      try {
        db.prepare("insert into counted values (?, ?)").run("a", "many");
        return false;
      } catch {
        return true;
      }
    });

    expect(threw).toBe(true);
  });
});

describe("migrate", () => {
  test("reports which names it ran, and nothing on a second pass", () => {
    const path = file();
    const db = new DatabaseSync(path);

    const first = Effect.runSync(migrate(db, [one, two]).pipe(Effect.orDie));
    const second = Effect.runSync(migrate(db, [one, two]).pipe(Effect.orDie));
    db.close();

    expect(first).toEqual(["a.001", "a.002"]);
    expect(second).toEqual([]);
  });
});
