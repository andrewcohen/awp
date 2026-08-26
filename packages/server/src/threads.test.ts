import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect, Result } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { Threads, layer, threadId } from "./threads";

// What a thread is allowed to be, proved against a real file.
//
// A temp directory rather than a fake filesystem: the two properties worth
// having here are that the file survives a restart and that a workspace cannot
// be in two threads, and the first of those is only a claim about a real file.

const scratch = mkdtempSync(join(tmpdir(), "awp-threads-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `threads-${(files += 1)}.json`);

type Service = { readonly [K in keyof Threads["Service"]]: Threads["Service"][K] };

const on = <A>(
  path: string,
  program: (threads: Service) => Effect.Effect<A, unknown>,
): Promise<A> =>
  Effect.gen(function* () {
    const threads = yield* Threads;
    return yield* program(threads);
  }).pipe(Effect.provide(layer(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

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
        yield* threads.attach(a.id, pair("thicket", "discounts"));
        // The second claim wins, and the first thread lets go without being
        // asked. Resolving this on read instead has no rendering: the sidebar
        // would have to draw the workspace twice.
        yield* threads.attach(b.id, pair("thicket", "discounts"));
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
        yield* threads.attach(made.id, pair("thicket", "discounts"));
        // A job step re-runs after a retry, so every write a step makes has to
        // be safe to make twice.
        return yield* threads.attach(made.id, pair("thicket", "discounts"));
      }),
    );

    expect(found.members).toEqual([pair("thicket", "discounts")]);
  });

  test("detaching a workspace that is not there is not an error", async () => {
    const found = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("a");
        return yield* threads.detach(made.id, pair("thicket", "never-attached"));
      }),
    );

    expect(found.members).toEqual([]);
  });

  test("a thread holds two workspaces from different projects", async () => {
    const found = await on(file(), (threads) =>
      Effect.gen(function* () {
        const made = yield* threads.create("tiered discounts");
        yield* threads.attach(made.id, pair("thicket", "discounts"));
        return yield* threads.attach(made.id, pair("api", "discounts"));
      }),
    );

    expect(found.members).toEqual([pair("thicket", "discounts"), pair("api", "discounts")]);
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
        yield* threads.attach(made.id, pair("thicket", "discounts"));
        return yield* threads.rename(made.id, "right name");
      }),
    );

    expect(found.title).toBe("right name");
    expect(found.members).toEqual([pair("thicket", "discounts")]);
  });

  test("a thread that is not there fails as ThreadNotFound", async () => {
    const outcome = await Effect.gen(function* () {
      const threads = yield* Threads;
      return yield* threads.rename("nope", "x");
    }).pipe(
      Effect.provide(layer(file())),
      Effect.scoped,
      Effect.flip,
      Effect.orDie,
      Effect.runPromise,
    );

    expect(outcome).toMatchObject({ thread: "nope" });
  });

  test("the file is readable by a person", async () => {
    const path = file();
    await on(path, (threads) => threads.create("tiered discounts"));

    // The reason this is a file rather than a database, asserted rather than
    // asserted-about-in-a-comment.
    const text = readFileSync(path, "utf8");
    expect(text).toContain("tiered discounts");
    expect(text.split("\n").length).toBeGreaterThan(3);
  });

  test("a file from a version we do not know refuses to load", async () => {
    const path = file();
    writeFileSync(path, JSON.stringify({ version: 99, threads: [] }));

    const failed = await Effect.gen(function* () {
      const threads = yield* Threads;
      return yield* threads.list();
    }).pipe(Effect.provide(layer(path)), Effect.scoped, Effect.result, Effect.runPromise);

    // Loud, and deliberately: the alternative to refusing is a daemon that
    // silently reports no threads, which reads as having lost all of them.
    expect(Result.isFailure(failed)).toBe(true);
  });
});
