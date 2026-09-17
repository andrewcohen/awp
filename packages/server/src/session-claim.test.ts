import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Db, layer } from "@awp-kit/store";
import { Effect, Result } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import {
  type Claims,
  STALE_AFTER,
  claimMigration,
  claims as claimsOn,
  holdersIn,
} from "./session-claim";

// Two agents on one transcript is the failure this file is about, and it has
// happened: two processes wrote two implementations of one task into one
// working copy, interleaved a second apart. Everything below is a property of
// the thing that would have refused the second one.

const scratch = mkdtempSync(join(tmpdir(), "awp-claim-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;

/**
 * A claim table, plus a clock and a liveness answer the test decides.
 *
 * Both are injected for the same reason: what the guard turns on is *time* and
 * *whether a process is there*, and a test that could control neither could
 * only assert the happy path. `alive` defaults to true — the interesting cases
 * are a holder that is there and one that is not, and both must be stated.
 */
const on = <A>(
  program: (
    claims: (as: { owner: string; pid: number }) => Claims,
    clock: { now: number },
  ) => A | Promise<A>,
  alive: (pid: number) => boolean = () => true,
): Promise<A> => {
  const clock = { now: 1_787_000_000_000 };
  return Effect.gen(function* () {
    const db = yield* Db;
    // Awaited *inside* the scope. Returning the promise instead closes the
    // connection the moment the effect succeeds, and every statement after
    // that fails with "database is not open" — which reads as a fault in the
    // claim rather than in the harness around it.
    return yield* Effect.promise(async () =>
      program((as) => claimsOn(db, as, () => clock.now, alive), clock),
    );
  }).pipe(
    Effect.provide(layer(join(scratch, `claims-${(files += 1)}.sqlite`), [claimMigration])),
    Effect.scoped,
    Effect.orDie,
    Effect.runPromise,
  );
};

/** What a call refused with, or `taken` when it did not refuse. */
const reasonOf = <A>(effect: Effect.Effect<A, { readonly reason: string }>): Promise<string> =>
  Effect.runPromise(
    Effect.map(Effect.result(effect), (found) =>
      Result.isSuccess(found) ? "taken" : found.failure.reason,
    ),
  );

const ONE = "20260917-aaaa-one";

describe("the claim", () => {
  test("a free session is taken, and is not reported as already ours", async () => {
    const answer = await on((claims) =>
      Effect.runPromise(claims({ owner: "port 5274", pid: 100 }).take(ONE)),
    );
    // False is what sends the caller on to the process check: nothing of ours
    // was in this session, so something that is not ours might be.
    expect(answer).toBe(false);
  });

  test("the same process taking it twice is told it already had it", async () => {
    const answer = await on(async (claims) => {
      const mine = claims({ owner: "port 5274", pid: 100 });
      await Effect.runPromise(mine.take(ONE));
      return Effect.runPromise(mine.take(ONE));
    });
    // Which is what stops `/new` — invalidate, then immediately re-open —
    // paying for a `ps` scan that would find this daemon's own adapter.
    expect(answer).toBe(true);
  });

  test("a second live process is refused, and the sentence says where to go", async () => {
    const said = await on(async (claims) => {
      await Effect.runPromise(claims({ owner: "the awp daemon on port 5274", pid: 100 }).take(ONE));
      return reasonOf(claims({ owner: "the awp daemon on port 5284", pid: 200 }).take(ONE));
    });

    expect(said).not.toBe("taken");
    // Every part of it is there to be acted on: which daemon, which process,
    // which session, and what to do instead.
    expect(said).toContain("port 5274");
    expect(said).toContain("pid 100");
    expect(said).toContain(ONE);
    expect(said).toContain("/new");
  });

  test("a holder that has died is taken over at once, not after a minute", async () => {
    // The two-instance workflow restarts daemons several times an hour. A
    // guard that made every restart wait out `STALE_AFTER` would be a guard
    // everybody learned to ignore.
    const answer = await on(
      async (claims) => {
        await Effect.runPromise(claims({ owner: "port 5274", pid: 100 }).take(ONE));
        return reasonOf(claims({ owner: "port 5274", pid: 101 }).take(ONE));
      },
      (pid) => pid !== 100,
    );

    expect(answer).toBe("taken");
  });

  test("a holder that is alive but has stopped beating decays", async () => {
    const answer = await on(async (claims, clock) => {
      await Effect.runPromise(claims({ owner: "port 5274", pid: 100 }).take(ONE));
      // A pid that still exists but is not this conversation any more — a
      // reused number, or a process wedged past saving.
      clock.now += STALE_AFTER + 1;
      return reasonOf(claims({ owner: "port 5284", pid: 200 }).take(ONE));
    });

    expect(answer).toBe("taken");
  });

  test("a beat is what keeps it", async () => {
    const answer = await on(async (claims, clock) => {
      const mine = claims({ owner: "port 5274", pid: 100 });
      await Effect.runPromise(mine.take(ONE));
      clock.now += STALE_AFTER - 1000;
      await Effect.runPromise(mine.beat(ONE));
      clock.now += STALE_AFTER - 1000;
      return reasonOf(claims({ owner: "port 5284", pid: 200 }).take(ONE));
    });

    // Without the beat this window is past `STALE_AFTER` twice over.
    expect(answer).not.toBe("taken");
  });

  test("releasing gives up only what is still ours", async () => {
    const holder = await on(
      async (claims) => {
        const first = claims({ owner: "port 5274", pid: 100 });
        await Effect.runPromise(first.take(ONE));
        // The first daemon dies, the second takes the session over, and only
        // then does the first one's finalizer run. It must not delete a row
        // that is no longer its own — the second daemon is holding it.
        await Effect.runPromise(claims({ owner: "port 5284", pid: 200 }).take(ONE));
        await Effect.runPromise(first.release(ONE));
        return Effect.runPromise(claims({ owner: "port 5284", pid: 200 }).holder(ONE));
      },
      (pid) => pid !== 100,
    );

    expect(holder?.pid).toBe(200);
  });
});

describe("who else is resuming it, out of the process table", () => {
  // `ps -Ao pid=,command=` pads the pid column, so every line begins with
  // spaces. Parsing has to survive that; the first attempt at this read the
  // whole line as a number and found nothing at all.
  const listing = [
    "  501 /Users/x/.local/bin/claude --resume=20260917-aaaa-one --model opus",
    "  502 bun /Users/x/.awp/tools/claude-agent-acp/dist/index.js",
    "  503 grep -r 20260917-aaaa-one /Users/x/.claude/projects",
    "  504 tail -f /Users/x/.claude/projects/20260917-aaaa-one.jsonl",
  ].join("\n");

  test("a process resuming the session is found", () => {
    expect(holdersIn(listing, ONE, new Set())).toEqual([
      {
        pid: 501,
        command: "/Users/x/.local/bin/claude --resume=20260917-aaaa-one --model opus",
      },
    ]);
  });

  test("mentioning the id is not holding it", () => {
    // The check itself greps for the id, and so does anybody diagnosing this.
    // Matching the bare id would make the diagnosis refuse the conversation.
    const found = holdersIn(listing, ONE, new Set());
    expect(found.map((one) => one.pid)).not.toContain(503);
    expect(found.map((one) => one.pid)).not.toContain(504);
  });

  test("our own process is not somebody else", () => {
    expect(holdersIn(listing, ONE, new Set([501]))).toEqual([]);
  });

  test("nothing at all is an empty list, not a refusal", () => {
    expect(holdersIn("", ONE, new Set())).toEqual([]);
    expect(holdersIn(listing, "20260917-bbbb-two", new Set())).toEqual([]);
  });
});
