import { mkdtempSync, rmSync } from "node:fs";
// The store picks its driver at open time; the test reaches for Node's
// directly, because what it needs is a file written the *wrong* way.
import { DatabaseSync } from "node:sqlite";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import type { Job } from "./job";
import { layerSqlite } from "./sqlite";
import { JobStore, LOG_LINES, type StoreError, layerMemory } from "./store";

// One suite, both stores.
//
// The memory store is what every runner test runs against and the sqlite one is
// what the daemon actually uses, so the second is the one that matters and the
// first is the one that gets exercised. Written once and run twice for exactly
// that reason: a contract with two implementations always drifts in the copy
// nobody looks at.

const scratch = mkdtempSync(join(tmpdir(), "awp-jobs-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `jobs-${(files += 1)}.sqlite`);

const stores: ReadonlyArray<{
  readonly name: string;
  readonly layer: () => Layer.Layer<JobStore, StoreError>;
}> = [
  { name: "memory", layer: () => layerMemory },
  { name: "sqlite", layer: () => layerSqlite(file()) },
];

type Service = { readonly [K in keyof JobStore["Service"]]: JobStore["Service"][K] };

const on = <A>(
  layer: Layer.Layer<JobStore, StoreError>,
  program: (store: Service) => Effect.Effect<A, StoreError>,
): Promise<A> =>
  Effect.gen(function* () {
    const store = yield* JobStore;
    return yield* program(store);
  }).pipe(Effect.provide(layer), Effect.scoped, Effect.orDie, Effect.runPromise);

const job = (over: Partial<Job> = {}): Job => ({
  id: "20260101-aaaa",
  kind: "three",
  title: "a job",
  key: undefined,
  input: { name: "a" },
  status: "queued",
  attempt: 0,
  attempts: 3,
  steps: ["one", "two", "three"],
  done: [],
  step: undefined,
  error: undefined,
  cleanup: undefined,
  createdAt: new Date(1_700_000_000_000),
  startedAt: undefined,
  endedAt: undefined,
  ...over,
});

for (const store of stores) {
  describe(store.name, () => {
    test("a record comes back as it went in", async () => {
      const written = job({
        key: "k",
        status: "failed",
        attempt: 2,
        done: ["one", "two"],
        step: "three",
        error: "nope",
        cleanup: "dirty",
        startedAt: new Date(1_700_000_001_000),
        endedAt: new Date(1_700_000_002_000),
      });

      const read = await on(store.layer(), (jobs) =>
        jobs.put(written).pipe(Effect.flatMap(() => jobs.get(written.id))),
      );

      expect(read).toEqual(written);
    });

    test("absent fields stay absent rather than becoming null", async () => {
      const read = await on(store.layer(), (jobs) =>
        jobs.put(job()).pipe(Effect.flatMap(() => jobs.get("20260101-aaaa"))),
      );

      expect(read?.key).toBeUndefined();
      expect(read?.step).toBeUndefined();
      expect(read?.cleanup).toBeUndefined();
      expect(read?.endedAt).toBeUndefined();
    });

    test("a job that is not there is undefined, not an error", async () => {
      const read = await on(store.layer(), (jobs) => jobs.get("20260101-zzzz"));
      expect(read).toBeUndefined();
    });

    test("byKey finds the job holding a key", async () => {
      const [found, missing] = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job({ key: "make-a" }));
          return [yield* jobs.byKey("make-a"), yield* jobs.byKey("make-b")] as const;
        }),
      );

      expect(found?.id).toBe("20260101-aaaa");
      expect(missing).toBeUndefined();
    });

    test("listing is newest first", async () => {
      const ids = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job({ id: "20260101-aaaa", createdAt: new Date(1000) }));
          yield* jobs.put(job({ id: "20260103-cccc", createdAt: new Date(3000) }));
          yield* jobs.put(job({ id: "20260102-bbbb", createdAt: new Date(2000) }));
          return (yield* jobs.list()).map((entry) => entry.id);
        }),
      );

      expect(ids).toEqual(["20260103-cccc", "20260102-bbbb", "20260101-aaaa"]);
    });

    test("the log keeps its order across appends", async () => {
      const lines = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job());
          yield* jobs.append("20260101-aaaa", ["first", "second"]);
          yield* jobs.append("20260101-aaaa", ["third"]);
          return yield* jobs.log("20260101-aaaa");
        }),
      );

      expect(lines).toEqual(["first", "second", "third"]);
    });

    test("the log is capped, keeping the end", async () => {
      const lines = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job());
          yield* jobs.append(
            "20260101-aaaa",
            Array.from({ length: LOG_LINES + 50 }, (_, index) => `line ${index}`),
          );
          return yield* jobs.log("20260101-aaaa");
        }),
      );

      expect(lines).toHaveLength(LOG_LINES);
      // The end, because that is where a failure explains itself.
      expect(lines.at(-1)).toBe(`line ${LOG_LINES + 49}`);
      expect(lines[0]).toBe("line 50");
    });

    test("rewriting a record does not erase its log", async () => {
      const lines = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job());
          yield* jobs.append("20260101-aaaa", ["something happened"]);
          yield* jobs.put(job({ status: "running", attempt: 1 }));
          return yield* jobs.log("20260101-aaaa");
        }),
      );

      expect(lines).toEqual(["something happened"]);
    });
  });
}

describe("sqlite, specifically", () => {
  test("a job written by one process is there for the next", async () => {
    const path = file();

    await on(layerSqlite(path), (jobs) =>
      jobs
        .put(job({ status: "running", done: ["one"] }))
        .pipe(Effect.flatMap(() => jobs.append("20260101-aaaa", ["got as far as one"]))),
    );

    // A second layer over the same file: a different connection, the way a
    // restarted daemon is. This is the whole reason the store is not a Map.
    const [read, log] = await on(layerSqlite(path), (jobs) =>
      Effect.gen(function* () {
        return [yield* jobs.get("20260101-aaaa"), yield* jobs.log("20260101-aaaa")] as const;
      }),
    );

    expect(read?.status).toBe("running");
    expect(read?.done).toEqual(["one"]);
    expect(log).toEqual(["got as far as one"]);
  });

  test("a file from an older schema is rebuilt rather than left broken", async () => {
    const path = file();

    // A jobs table as an earlier version had it — one column, and the wrong
    // one. `create table if not exists` leaves this alone, so every insert
    // afterwards fails on the column count. That is what happened when `steps`
    // was added to the record, and is the whole reason the file carries a
    // version.
    const older = new DatabaseSync(path);
    older.exec("pragma user_version = 0");
    older.exec("create table jobs (id text primary key)");
    older.exec("insert into jobs values ('20250101-old')");
    older.close();

    const [written, listed] = await on(layerSqlite(path), (jobs) =>
      Effect.gen(function* () {
        yield* jobs.put(job());
        return [true, yield* jobs.list()] as const;
      }),
    );

    expect(written).toBe(true);
    // The old row is gone with the old table, and the new one is writable.
    expect(listed.map((entry) => entry.id)).toEqual(["20260101-aaaa"]);
  });

  test("a file at the current version keeps its rows", async () => {
    const path = file();
    await on(layerSqlite(path), (jobs) => jobs.put(job()));
    const listed = await on(layerSqlite(path), (jobs) => jobs.list());
    expect(listed).toHaveLength(1);
  });
});
