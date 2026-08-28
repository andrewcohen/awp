import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Db, layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { Threads, layer, migrations, threadId } from "./threads";

// What a thread is allowed to be, proved against a real file.
//
// A temp directory rather than a fake filesystem: the two properties worth
// having here are that the file survives a restart and that a workspace cannot
// be in two threads, and the first of those is only a claim about a real file.

const scratch = mkdtempSync(join(tmpdir(), "awp-threads-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `threads-${(files += 1)}.sqlite`);

/** Threads over a database of their own, migrated with only their tables. */
const at = (path: string) => layer.pipe(Layer.provide(dbLayer(path, migrations)));

type Service = { readonly [K in keyof Threads["Service"]]: Threads["Service"][K] };

const on = <A>(
  path: string,
  program: (threads: Service) => Effect.Effect<A, unknown>,
): Promise<A> =>
  Effect.gen(function* () {
    const threads = yield* Threads;
    return yield* program(threads);
  }).pipe(Effect.provide(at(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

const pair = (project: string, workspace: string) => ({ project, workspace });

describe("threadId", () => {
  test("is the day it was made and four characters", () => {
    expect(threadId(new Date("2026-08-26T10:00:00Z"), 0.5)).toMatch(/^20260826-[0-9a-z]{4}$/u);
  });

  test("differs for two threads made the same day", () => {
    const day = new Date("2026-08-26T10:00:00Z");
    expect(threadId(day, 0.1)).not.toBe(threadId(day, 0.9));
  });
});

describe("threads", () => {
  test("a fresh machine has no threads and no file", async () => {
    const path = file();
    const all = await on(path, (threads) => threads.list());
    expect(all).toEqual([]);
  });

  test("a thread comes back with the title it was given", async () => {
    const made = await on(file(), (threads) => threads.create("  tiered discounts  "));
    expect(made.title).toBe("tiered discounts");
    expect(made.members).toEqual([]);
    expect(made.archivedAt).toBeUndefined();
  });

  test("a thread written by one daemon is there for the next", async () => {
    const path = file();
    const made = await on(path, (threads) => threads.create("tiered discounts"));

    // A second layer over the same path: a different Ref, the way a restarted
    // daemon is. This is the whole reason there is a file at all.
    const all = await on(path, (threads) => threads.list());
    expect(all.map((entry) => entry.id)).toEqual([made.id]);
    expect(all[0]?.title).toBe("tiered discounts");
    // JSON has no Date, so this is the assertion that the revival works.
    expect(all[0]?.createdAt).toBeInstanceOf(Date);
  });

  // The relationship is recorded rather than re-derived. It *could* be
  // recovered later by asking jj which revision each workspace descends from,
  // but that answers a question about commits — and jj's answer changes as
  // branches are rebased and deleted, while the claim "this follows from that"
  // does not.
  test("a thread remembers the thread it branched from, across a restart", async () => {
    const path = file();
    const [parent, child] = await on(path, (threads) =>
      Effect.gen(function* () {
        const first = yield* threads.create("the first thing");
        const second = yield* threads.create("the follow-on", first.id);
        return [first, second] as const;
      }),
    );

    expect(child.parentId).toBe(parent.id);
    expect(parent.parentId).toBeUndefined();

    const all = await on(path, (threads) => threads.list());
    expect(all.find((entry) => entry.id === child.id)?.parentId).toBe(parent.id);
    expect(all.find((entry) => entry.id === parent.id)?.parentId).toBeUndefined();
  });

  // The case that actually happens. Every other test here migrates a fresh
  // file, where 001 and 002 run back to back — but the database on a machine
  // that has already run awp has 001 recorded and 002 never seen, and
  // `alter table add column` is a different operation from `create table`.
  //
  // It is also the shape sqlite is fussiest about: a REFERENCES clause may
  // only be added when the new column defaults to NULL, which this one does.
  // Getting that wrong would fail on a real database and on none of the
  // others.
  //
  // The old schema is written with raw SQL rather than through `Threads`,
  // because the service is only ever run against the *whole* list — pointing
  // today's statements at yesterday's tables is a situation the daemon does
  // not have and a test of it would be testing nothing.
  test("a database with only the first migration takes the second", async () => {
    const path = file();
    const first = migrations.slice(0, 1);
    expect(first).toHaveLength(1);

    await Effect.gen(function* () {
      const db = yield* Db;
      db.prepare("insert into threads (id, title, created_at) values (?, ?, ?)").run(
        "20260101-old",
        "made before the column existed",
        1_700_000_000_000,
      );
    }).pipe(Effect.provide(dbLayer(path, first)), Effect.scoped, Effect.orDie, Effect.runPromise);

    // The same file, now migrated with both. 002 has to run and 001 has to be
    // left alone — it is recorded, and re-running `create table` would throw.
    const all = await on(path, (threads) =>
      Effect.gen(function* () {
        yield* threads.create("made after", "20260101-old");
        return yield* threads.list();
      }),
    );

    // The row that predates the column reads back with no parent, rather than
    // failing to read at all.
    expect(all.find((entry) => entry.id === "20260101-old")?.parentId).toBeUndefined();
    expect(all.find((entry) => entry.title === "made after")?.parentId).toBe("20260101-old");
  });

  test("listing is newest first", async () => {
    const path = file();
    const ids = await on(path, (threads) =>
      Effect.gen(function* () {
        const first = yield* threads.create("first");
        const second = yield* threads.create("second");
        return [first.id, second.id] as const;
      }),
    );
    const listed = await on(path, (threads) => threads.list());
    // Both were made in the same millisecond as often as not, so the assertion
    // is on the set rather than the order — what matters is that nothing was
    // lost between the two calls.
    expect(new Set(listed.map((entry) => entry.id))).toEqual(new Set(ids));
  });

  test("a workspace can only be in one thread", async () => {
    const path = file();
    const listed = await on(path, (threads) =>
      Effect.gen(function* () {
        const a = yield* threads.create("a");
        const b = yield* threads.create("b");
        yield* threads.attach(a.id, pair("rowan", "discounts"));
        // The second claim wins, and the first thread lets go without being
        // asked. Resolving this on read instead has no rendering: the sidebar
        // would have to draw the workspace twice.
        yield* threads.attach(b.id, pair("rowan", "discounts"));
        return yield* threads.list();
      }),
    );

    const holding = listed.filter((entry) => entry.members.length > 0);
    expect(holding).toHaveLength(1);
    expect(holding[0]?.title).toBe("b");
  });

  test("attaching the same workspace twice changes nothing", async () => {
    const path = file();
    const found = await on(path, (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("a");
        yield* threads.attach(made.id, pair("rowan", "discounts"));
        // A job step re-runs after a retry, so every write a step makes has to
        // be safe to make twice.
        return yield* threads.attach(made.id, pair("rowan", "discounts"));
      }),
    );

    expect(found.members).toEqual([pair("rowan", "discounts")]);
  });

  test("detaching a workspace that is not there is not an error", async () => {
    const found = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("a");
        return yield* threads.detach(made.id, pair("rowan", "never-attached"));
      }),
    );

    expect(found.members).toEqual([]);
  });

  test("a thread holds two workspaces from different projects", async () => {
    const found = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("tiered discounts");
        yield* threads.attach(made.id, pair("rowan", "discounts"));
        return yield* threads.attach(made.id, pair("beta", "discounts"));
      }),
    );

    expect(found.members).toEqual([pair("rowan", "discounts"), pair("beta", "discounts")]);
  });

  test("archiving is reversible", async () => {
    const path = file();
    const [archived, restored] = await on(path, (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("done with this");
        const off = yield* threads.archive(made.id, true);
        const back = yield* threads.archive(made.id, false);
        return [off, back] as const;
      }),
    );

    expect(archived.archivedAt).toBeInstanceOf(Date);
    expect(restored.archivedAt).toBeUndefined();
  });

  test("an archived thread is still listed — the caller decides", async () => {
    const path = file();
    const listed = await on(path, (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("done with this");
        yield* threads.archive(made.id, true);
        return yield* threads.list();
      }),
    );

    expect(listed).toHaveLength(1);
  });

  test("renaming keeps everything else", async () => {
    const found = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("wrong name");
        yield* threads.attach(made.id, pair("rowan", "discounts"));
        return yield* threads.rename(made.id, "right name");
      }),
    );

    expect(found.title).toBe("right name");
    expect(found.members).toEqual([pair("rowan", "discounts")]);
  });

  test("a thread that is not there fails as ThreadNotFound", async () => {
    const outcome = await Effect.gen(function* () {
      const threads = yield* Threads;
      return yield* threads.rename("nope", "x");
    }).pipe(
      Effect.provide(at(file())),
      Effect.scoped,
      Effect.flip,
      Effect.orDie,
      Effect.runPromise,
    );

    expect(outcome).toMatchObject({ thread: "nope" });
  });
});

describe("restore", () => {
  // The counterpart of deleteIfEmpty, and the reason it had to exist: without
  // it, `create-workspace`'s first step asserted a thread that its own last
  // undo removed, so every rolled-back create was permanently unretryable.
  test("puts a deleted thread back under the same id", async () => {
    const path = file();
    const id = await on(path, (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("tiered discounts");
        yield* threads.deleteIfEmpty(made.id);
        return made.id;
      }),
    );

    // A second connection, because the point of the id surviving is that
    // something holding a reference to it later can still resolve it.
    const back = await on(path, (threads) =>
      Effect.gen(function* () {
        const did = yield* threads.restore(id, "Tiered discounts");
        return { did, all: yield* threads.list() };
      }),
    );

    expect(back.did).toBe(true);
    expect(back.all.map((one) => one.id)).toEqual([id]);
    expect(back.all[0]?.title).toBe("Tiered discounts");
  });

  test("keeps the lineage it is given", async () => {
    const seen = await on(file(), (threads) =>
      Effect.gen(function* () {
        const parent = yield* threads.create("the work before");
        yield* threads.restore("20260828-zzzz", "the work after", parent.id);
        return { parent: parent.id, all: yield* threads.list() };
      }),
    );
    expect(seen.all.find((one) => one.id === "20260828-zzzz")?.parentId).toBe(seen.parent);
  });

  test("a thread already there is left exactly as it is", async () => {
    // Idempotent, because the runner re-enters a step — and, more than that,
    // it must never overwrite a title somebody has since edited. A job resumed
    // after a daemon restart still names its thread, and that thread may have
    // been renamed twice since.
    const seen = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("what was typed");
        yield* threads.rename(made.id, "what a person called it");
        const did = yield* threads.restore(made.id, "what was typed");
        return { did, all: yield* threads.list() };
      }),
    );
    expect(seen.did).toBe(false);
    expect(seen.all[0]?.title).toBe("what a person called it");
  });
});
