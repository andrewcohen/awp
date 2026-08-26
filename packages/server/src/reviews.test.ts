import { layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeAll, describe, expect, it } from "vitest";
import { Reviews, commentId, layer as reviewsLayer, migrations } from "./reviews";

// The store, against real sqlite. There is no memory arm to compare it with —
// unlike the jobs store, a review *is* rows, so a second implementation would
// be a fake of the only thing under test.

let scratch = "";
let files = 0;

beforeAll(() => {
  scratch = mkdtempSync(join(tmpdir(), "awp-reviews-"));
});

const run = <A>(body: Effect.Effect<A, unknown, Reviews>) =>
  Effect.runPromise(
    body.pipe(
      Effect.provide(
        reviewsLayer.pipe(
          Layer.provide(
            Layer.orDie(dbLayer(join(scratch, `r-${(files += 1)}.sqlite`), migrations)),
          ),
        ),
      ),
    ) as Effect.Effect<A, unknown, never>,
  );

const draft = (over: {
  readonly id?: string;
  readonly line?: number;
  readonly body?: string;
  readonly workspace?: string;
  readonly side?: "additions" | "deletions";
  readonly createdAt?: Date;
}) => ({
  id: over.id ?? "20260827-0001",
  project: "thicket",
  workspace: over.workspace ?? "lantern",
  revision: "vtknsnwv",
  path: "src/router.ts",
  side: over.side ?? ("additions" as const),
  line: over.line ?? 42,
  body: over.body ?? "this branch never runs",
  createdAt: over.createdAt ?? new Date("2026-08-27T09:00:00.000Z"),
  sentAt: undefined,
});

/** A fixed instant, varied only by milliseconds, so ordering is the variable. */
const at = (ms: number) => new Date(Date.UTC(2026, 7, 27, 9, 0, 0, ms));

describe("the review store", () => {
  it("keeps a comment and reads it back whole", async () => {
    // Every field, not a spot check. The row goes through sqlite's type
    // affinity and `side` is narrowed on the way out of a text column, so a
    // partial assertion here is a partial assertion about the mapping.
    const written = draft({});
    const [got] = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(written);
        return yield* reviews.list("thicket", "lantern");
      }),
    );
    expect(got).toStrictEqual(written);
  });

  it("keeps workspaces apart", async () => {
    // The read is always per workspace, and the index is on the pair. A store
    // that answered with everything would put another branch's review in this
    // one's panel, which is worse than showing none.
    const got = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(draft({ id: "a", workspace: "lantern" }));
        yield* reviews.add(draft({ id: "b", workspace: "orchard" }));
        return yield* reviews.list("thicket", "lantern");
      }),
    );
    expect(got.map((one) => one.id)).toStrictEqual(["a"]);
  });

  it("orders by when it was written, then by id", async () => {
    // The panel reads this order and so does the prompt. The id tiebreak
    // matters because two comments written in the same millisecond are the
    // ordinary case when someone is typing fast, and Map order is not an order.
    const got = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(draft({ id: "c", createdAt: at(5) }));
        yield* reviews.add(draft({ id: "b", createdAt: at(1) }));
        yield* reviews.add(draft({ id: "a", createdAt: at(1) }));
        return yield* reviews.list("thicket", "lantern");
      }),
    );
    expect(got.map((one) => one.id)).toStrictEqual(["a", "b", "c"]);
  });

  it("says whether there was anything to remove", async () => {
    // Asking twice is not an error — a window whose list is a moment stale
    // will do exactly that. The second answer is `false`, not a failure.
    const got = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(draft({ id: "a" }));
        return [yield* reviews.remove("a"), yield* reviews.remove("a")];
      }),
    );
    expect(got).toStrictEqual([true, false]);
  });

  it("marks every draft sent, and returns exactly what it marked", async () => {
    // The pair that makes batching safe: the caller composes a prompt out of
    // the rows this returns, so anything marked and not returned is a comment
    // the agent was never told about but which now looks delivered.
    const when = new Date("2026-08-27T10:00:00.000Z");
    const [sent, after] = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(draft({ id: "a" }));
        yield* reviews.add(draft({ id: "b" }));
        const marked = yield* reviews.markSent("thicket", "lantern", when);
        return [marked, yield* reviews.list("thicket", "lantern")] as const;
      }),
    );
    expect(sent.map((one) => one.id)).toStrictEqual(["a", "b"]);
    expect(sent.every((one) => one.sentAt?.getTime() === when.getTime())).toBe(true);
    // Kept, not deleted. A review is the record of what was asked for.
    expect(after.map((one) => one.sentAt?.getTime())).toStrictEqual([
      when.getTime(),
      when.getTime(),
    ]);
  });

  it("does not send a comment twice", async () => {
    // The second batch must be empty. Without the `sent_at is null` predicate
    // this would re-deliver the whole history every time the button was
    // pressed, which is the failure batching exists to avoid at scale.
    const second = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        yield* reviews.add(draft({ id: "a" }));
        yield* reviews.markSent("thicket", "lantern", new Date(1));
        return yield* reviews.markSent("thicket", "lantern", new Date(2));
      }),
    );
    expect(second).toStrictEqual([]);
  });

  it("marks nothing when a workspace has nothing to say", async () => {
    const got = await run(
      Effect.gen(function* () {
        const reviews = yield* Reviews;
        return yield* reviews.markSent("thicket", "lantern", new Date(1));
      }),
    );
    expect(got).toStrictEqual([]);
  });
});

describe("commentId", () => {
  it("is the day and four characters", () => {
    // The same spelling as a job id and a thread id, on purpose: three id
    // formats in one system is three things to recognise for no gain.
    expect(commentId(new Date("2026-08-27T09:00:00.000Z"), 0)).toBe("20260827-0000");
    expect(commentId(new Date("2026-08-27T09:00:00.000Z"), 0.5)).toMatch(/^20260827-[0-9a-z]{4}$/u);
  });
});
