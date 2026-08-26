import { mkdtempSync, rmSync } from "node:fs";
// The store picks its driver at open time; these tests reach for Node's
// directly, because what they need is to write the database the *wrong* way.
import { DatabaseSync } from "node:sqlite";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { DbError } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import type { Job } from "./job";
import { layerSqliteAt } from "./sqlite";
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
  readonly layer: () => Layer.Layer<JobStore, StoreError | DbError>;
}> = [
  { name: "memory", layer: () => layerMemory },
  { name: "sqlite", layer: () => layerSqliteAt(file()) },
];

type Service = { readonly [K in keyof JobStore["Service"]]: JobStore["Service"][K] };

const on = <A>(
  layer: Layer.Layer<JobStore, StoreError | DbError>,
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
    // Clearing is the one operation that has to be *selective*, and every case
    // below is a record it must refuse to touch. Written once and run against
    // both stores, because the memory one filters in JS and the sqlite one in
    // SQL — two entirely separate spellings of the same rule, which is exactly
    // the shape that drifts.
    test("forgetting the finished keeps everything still to come", async () => {
      const forgotten = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          for (const written of [
            job({ id: "20260101-done", status: "succeeded" }),
            job({ id: "20260101-fail", status: "failed", error: "nope" }),
            job({ id: "20260101-gone", status: "cancelled" }),
            // Still to come. The runner holds a fiber for each of these, and
            // deleting the row would only mean the next save writes it back
            // without its log.
            job({ id: "20260101-wait", status: "queued" }),
            job({ id: "20260101-live", status: "running" }),
            // Over, but its rollback left something behind. The only outcome
            // the package cannot put right by itself, so it is the one that
            // most needs to still be there tomorrow.
            job({ id: "20260101-mess", status: "failed", cleanup: "dirty" }),
          ]) {
            yield* jobs.put(written);
          }
          yield* jobs.append("20260101-done", ["a line"]);
          yield* jobs.append("20260101-wait", ["another"]);

          const gone = yield* jobs.forgetFinished();
          return { gone, left: (yield* jobs.list()).map((entry) => entry.id) };
        }),
      );

      expect(forgotten.gone).toBe(3);
      expect(forgotten.left.toSorted()).toEqual([
        "20260101-live",
        "20260101-mess",
        "20260101-wait",
      ]);
    });

    // The log is a separate table in sqlite with no foreign key back to the
    // job, so nothing deletes these for us. A row left behind here is invisible
    // — it belongs to an id nothing lists — and would accumulate forever.
    test("forgetting a job takes its log with it", async () => {
      const after = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job({ id: "20260101-done", status: "succeeded" }));
          yield* jobs.append("20260101-done", ["one", "two"]);
          yield* jobs.forgetFinished();
          // Written again under the same id, which is what makes this a real
          // question rather than a reading of nothing: if the lines survived
          // the delete, they come back attached to a job that never wrote them.
          yield* jobs.put(job({ id: "20260101-done", status: "queued" }));
          return yield* jobs.log("20260101-done");
        }),
      );

      expect(after).toEqual([]);
    });

    test("forgetting nothing is not an error", async () => {
      const gone = await on(store.layer(), (jobs) =>
        Effect.gen(function* () {
          yield* jobs.put(job({ status: "running" }));
          return yield* jobs.forgetFinished();
        }),
      );
      expect(gone).toBe(0);
    });

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

    await on(layerSqliteAt(path), (jobs) =>
      jobs
        .put(job({ status: "running", done: ["one"] }))
        .pipe(Effect.flatMap(() => jobs.append("20260101-aaaa", ["got as far as one"]))),
    );

    // A second layer over the same file: a different connection, the way a
    // restarted daemon is. This is the whole reason the store is not a Map.
    const [read, log] = await on(layerSqliteAt(path), (jobs) =>
      Effect.gen(function* () {
        return [yield* jobs.get("20260101-aaaa"), yield* jobs.log("20260101-aaaa")] as const;
      }),
    );

    expect(read?.status).toBe("running");
    expect(read?.done).toEqual(["one"]);
    expect(log).toEqual(["got as far as one"]);
  });

  test("a migration already applied is not applied again", async () => {
    const path = file();
    await on(layerSqliteAt(path), (jobs) => jobs.put(job()));

    // Opening again re-runs `migrate`, which is the whole point of recording
    // names: `create table jobs` is not `if not exists`, so a second run that
    // did not consult the record would fail outright — which is how this was
    // caught, by the test below failing on its first run.
    const listed = await on(layerSqliteAt(path), (jobs) => jobs.list());
    expect(listed.map((entry) => entry.id)).toEqual(["20260101-aaaa"]);
  });

  test("a strict table refuses a value of the wrong type", async () => {
    const path = file();
    await on(layerSqliteAt(path), (jobs) => jobs.put(job()));

    // `strict` is why this throws instead of storing a word in an integer
    // column and handing it back later. Reached directly, because the store's
    // own types make it unreachable through the service — which is the point:
    // the table is the second line of defence, not the first.
    //
    // A *lossless* conversion is still allowed — `kind = 7` becomes the text
    // "7" and raises nothing. `strict` rejects what cannot be converted, which
    // is a narrower promise than it first reads as.
    const raw = new DatabaseSync(path);
    const wrong = raw.prepare("update jobs set attempt = 'many' where id = ?");
    expect(() => wrong.run("20260101-aaaa")).toThrow();
    raw.close();
  });

  test("rows survive a close and reopen", async () => {
    const path = file();
    await on(layerSqliteAt(path), (jobs) => jobs.put(job()));
    const listed = await on(layerSqliteAt(path), (jobs) => jobs.list());
    expect(listed).toHaveLength(1);
  });
});
