import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { layer as dbLayer } from "@awp-kit/store";
import { Clock, Effect, Layer, Result } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { Gadgets, layer, migrations } from "./gadgets";

// What a gadget service owes its two callers, and they want opposite things:
// the agent wants to be told exactly what is wrong with the document it just
// wrote, and the window wants either a document that runs or a sentence it can
// print. So every test here is about a refusal or about what survives a round
// trip — there is nothing in between.

const scratch = mkdtempSync(join(tmpdir(), "awp-gadget-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;

/**
 * The service, over a store of its own.
 *
 * `file` is taken rather than always generated because of the one property
 * that cannot be written down inside a single instance: what survives a
 * restart. Two services over one file is the only honest way to say it — the
 * map of compiled documents goes with the process, and the row does not.
 */
const on = <A>(
  program: (gadgets: Gadgets["Service"]) => Effect.Effect<A, unknown>,
  file = `gadgets-${(files += 1)}.sqlite`,
): Promise<A> =>
  Effect.gen(function* () {
    const gadgets = yield* Gadgets;
    return yield* program(gadgets);
  }).pipe(
    Effect.provide(
      layer.pipe(Layer.provide(Layer.orDie(dbLayer(join(scratch, file), migrations)))),
    ),
    Effect.scoped,
    Effect.orDie,
    Effect.runPromise,
  );

/**
 * Every clock read inside this answers with the same millisecond.
 *
 * The tie is the point of the test below, and a real clock only produces one
 * by luck — less of it the more work sits between the two writes, and writing
 * a row is more work than it used to be. A test whose premise is an accident
 * is a test that fails on a slow machine and teaches everybody to re-run it.
 */
const atOneInstant = <A, E, R>(program: Effect.Effect<A, E, R>): Effect.Effect<A, E, R> =>
  Clock.clockWith((real) => {
    const at = real.currentTimeMillisUnsafe();
    return Effect.provideService(program, Clock.Clock, {
      ...real,
      currentTimeMillisUnsafe: () => at,
      currentTimeMillis: Effect.succeed(at),
    });
  });

/** What a call refused with, or the word `accepted` when it did not refuse. */
const refusalIn = <A>(effect: Effect.Effect<A, { readonly reason: string }>) =>
  Effect.result(effect).pipe(
    Effect.map((found) => (Result.isSuccess(found) ? "accepted" : found.failure.reason)),
  );

describe("writing one", () => {
  test("markdown compiles, and what comes back is not markdown", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const made = yield* gadgets.show("20260917-ab3d", "cost-table", "# Costs\n\nOne line.\n");
        expect(made.address).toBe("gadget://20260917-ab3d/cost-table");
        expect(made.thread).toBe("20260917-ab3d");
        // The compile happened here. The window is handed JavaScript, and the
        // whole argument for that is in `Gadget.code` — an author that has
        // moved on cannot fix a syntax error somebody sees an hour later.
        expect(made.code).toContain("function MDXContent");
        // The contract with `gadget-run.ts`, which builds a function around
        // this and therefore decides what the document can see. MDX reads its
        // runtime out of `arguments[0]`, so the window's first parameter is
        // not a spelling either side may change alone.
        expect(made.code).toContain("arguments[0]");
        expect(made.code).not.toContain("# Costs");
      }),
    ));

  test("a document that does not compile is refused in the compiler's words", async () => {
    const reason = await on((gadgets) =>
      refusalIn(gadgets.show(undefined, "broken", "# Hi\n\n<Unclosed\n")),
    );
    // The place, because "unexpected end of file" against forty lines is a
    // sentence an author has to guess at.
    expect(reason).toContain("line");
    expect(reason).not.toBe("accepted");
  });

  test("import is refused, and the refusal says what to do instead", async () => {
    const reason = await on((gadgets) =>
      refusalIn(gadgets.show(undefined, "imports", "import { Chart } from 'somewhere'\n\n# Hi\n")),
    );
    // MDX itself does not refuse this: it compiles to a dynamic import and a
    // check for an option, which throws in the window, about an option the
    // author has never heard of. The sentence has to arrive where the source is.
    expect(reason).toContain("import is not available");
    expect(reason).toContain("React");
  });

  test("a fenced block full of imports is prose about imports", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        // The estree and not the source text — the guard has to know the
        // difference between a statement and a code sample of one.
        const made = yield* gadgets.show(
          undefined,
          "prose",
          "# How it works\n\n```ts\nimport { Chart } from 'somewhere'\n```\n",
        );
        expect(made.name).toBe("prose");
      }),
    ));

  test("a name that is not a url segment is refused where it was typed", async () => {
    for (const bad of ["Cost Table", "cost_table", "", "-leading"]) {
      const reason = await on((gadgets) => refusalIn(gadgets.show(undefined, bad, "# Hi\n")));
      expect(reason).not.toBe("accepted");
    }
  });

  test("a workspace no thread claims gets the bucket, not a refusal", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        // Most checkouts on a real machine predate threads. Refusing there
        // would refuse the ordinary case — the same answer `Pages` gives.
        const made = yield* gadgets.show(undefined, "loose-one", "# Hi\n");
        expect(made.address).toBe("gadget://loose/loose-one");
        expect(made.thread).toBeUndefined();
      }),
    ));
});

describe("reading one back", () => {
  test("the document survives the round trip", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const made = yield* gadgets.show("20260917-ab3d", "cost-table", "# Costs\n");
        const found = yield* gadgets.read(made.address);
        expect(found.code).toBe(made.code);
      }),
    ));

  test("writing a name twice replaces it", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const first = yield* gadgets.show("20260917-ab3d", "cost-table", "# One\n");
        const second = yield* gadgets.show("20260917-ab3d", "cost-table", "# Two\n");
        expect(second.address).toBe(first.address);
        const found = yield* gadgets.read(first.address);
        expect(found.code).toBe(second.code);
      }),
    ));

  test("two threads may each have a cost-table", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        // The address carries the thread for this reason, and it is the same
        // reason the page is keyed by one: a gadget belongs to a piece of
        // work, and two pieces of work name things alike.
        const mine = yield* gadgets.show("20260917-ab3d", "cost-table", "# Mine\n");
        const theirs = yield* gadgets.show("20260918-9f21", "cost-table", "# Theirs\n");
        expect(mine.address).not.toBe(theirs.address);
        expect((yield* gadgets.read(mine.address)).code).not.toBe(
          (yield* gadgets.read(theirs.address)).code,
        );
      }),
    ));

  test("a gadget nobody wrote refuses with what to do about it", async () => {
    // The sentence used to carry a restart, because a restart was the common
    // way to reach it: the window remembers an address across launches and the
    // daemon remembered nothing. The table is what took that cause away, and
    // the sentence stopped naming it in the same change — a refusal that
    // blames something that can no longer happen sends its reader to look in
    // the wrong place.
    const reason = await on((gadgets) => refusalIn(gadgets.read("gadget://loose/nothing")));
    expect(reason).toContain("no gadget at");
    expect(reason).toContain("nothing has been written under that name");
  });
});

// ── the title, and the strip it is drawn in ────────────────────────────────
//
// Both exist because a thread accumulates gadgets. The first version pointed
// the web panel at one address per thread, so the second gadget an agent wrote
// took the first one's place — and the one somebody asks about is rarely the
// most recent. So: a list, ordered, and a name for each tab that the author
// did not have to be asked for.

describe("what the tab says", () => {
  test("the title is the document's first heading", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const made = yield* gadgets.show("20260917-ab3d", "costs", "# What a run costs\n\nHi.\n");
        expect(made.title).toBe("What a run costs");
      }),
    ));

  test("a heading further down is still the first one", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        // A document that opens with a sentence and gets to its heading after.
        // Taking `children[0]` rather than the first heading would leave this
        // titled after the file.
        const made = yield* gadgets.show("20260917-ab3d", "costs", "Before.\n\n## Latency\n");
        expect(made.title).toBe("Latency");
      }),
    ));

  test("the words in a heading, not its markup", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const made = yield* gadgets.show("20260917-ab3d", "costs", "# `zmx` and **the pty**\n");
        expect(made.title).toBe("zmx and the pty");
      }),
    ));

  test("a document with no heading is titled after itself", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        // The name is never empty and is already a phrase. A tab reading
        // "untitled" is a word nobody wrote about a document somebody did.
        const made = yield* gadgets.show("20260917-ab3d", "run-3-latency", "Just a line.\n");
        expect(made.title).toBe("run-3-latency");
      }),
    ));

  test("a heading inside a fence is prose about a heading", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        const made = yield* gadgets.show("20260917-ab3d", "costs", "```\n# not a heading\n```\n");
        expect(made.title).toBe("costs");
      }),
    ));
});

describe("a thread's strip", () => {
  test("newest first, and each thread sees only its own", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        yield* gadgets.show("20260917-ab3d", "first", "# First\n");
        yield* gadgets.show("20260917-ab3d", "second", "# Second\n");
        yield* gadgets.show("20260917-cc11", "elsewhere", "# Elsewhere\n");
        yield* gadgets.show(undefined, "loose", "# Loose\n");

        const mine = yield* gadgets.list("20260917-ab3d");
        // Written second, listed first: the strip is read left to right and
        // the newest is the one somebody was just told about.
        expect(mine.map((one) => one.title)).toEqual(["Second", "First"]);
        // The head and not the gadget — a strip of documents is the panel
        // fetching every one of them to draw a row of tabs.
        expect(mine.every((one) => !("code" in one))).toBe(true);

        const nobodys = yield* gadgets.list(undefined);
        expect(nobodys.map((one) => one.name)).toEqual(["loose"]);
      }),
    ));

  test("a name written twice is one tab, not two", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        yield* gadgets.show("20260917-ab3d", "costs", "# Costs\n");
        yield* gadgets.show("20260917-ab3d", "costs", "# Costs, again\n");
        const mine = yield* gadgets.list("20260917-ab3d");
        expect(mine.map((one) => one.title)).toEqual(["Costs, again"]);
      }),
    ));
});

// Effect's test clock does not advance on its own, so every gadget written by
// the block above shares one `at` — which makes this the ordinary case here
// and, for an agent writing a pair of them in a loop, out there too.
describe("two in the same millisecond", () => {
  test("the second written is the first listed", () =>
    on((gadgets) =>
      atOneInstant(
        Effect.gen(function* () {
          const first = yield* gadgets.show("20260917-ab3d", "first", "# First\n");
          const second = yield* gadgets.show("20260917-ab3d", "second", "# Second\n");
          const mine = yield* gadgets.list("20260917-ab3d");
          expect(second.at).toBe(first.at);
          expect(mine.map((one) => one.title)).toEqual(["Second", "First"]);
        }),
      ),
    ));

  test("a rewrite moves to the front of its own tie", () =>
    on((gadgets) =>
      atOneInstant(
        Effect.gen(function* () {
          yield* gadgets.show("20260917-ab3d", "first", "# First\n");
          yield* gadgets.show("20260917-ab3d", "second", "# Second\n");
          yield* gadgets.show("20260917-ab3d", "first", "# First, revised\n");
          const mine = yield* gadgets.list("20260917-ab3d");
          expect(mine.map((one) => one.title)).toEqual(["First, revised", "Second"]);
        }),
      ),
    ));
});

describe("across a restart", () => {
  test("a gadget written by one daemon is read by the next", async () => {
    const file = `restart-${(files += 1)}.sqlite`;
    const made = await on(
      (gadgets) => gadgets.show("20260917-ab3d", "cost-table", "# Costs\n\nOne line.\n"),
      file,
    );

    // A second service over the same file and nothing else: the map that held
    // the compiled document went with the process that made it. What is left
    // is a row, and the row is enough.
    const again = await on((gadgets) => gadgets.read(made.address), file);

    expect(again.title).toBe("Costs");
    expect(again.address).toBe(made.address);
    expect(again.thread).toBe("20260917-ab3d");
    // Its own writing, not the first one's: `at` is what tells one showing of
    // a gadget from the next, and a restart is not a new showing.
    expect(again.at).toBe(made.at);
    // Compiled a second time out of the source, rather than restored from a
    // stored artifact. The contract with `gadget-run.ts` is therefore the one
    // this build holds, not the one the build that wrote it held.
    expect(again.code).toContain("arguments[0]");
  });

  test("the strip is there before any document is read", async () => {
    const file = `restart-${(files += 1)}.sqlite`;
    await on((gadgets) => gadgets.show("20260917-ab3d", "first", "# First\n"), file);
    await on((gadgets) => gadgets.show("20260917-ab3d", "second", "# Second\n"), file);

    // `list` compiles nothing, which is the whole reason the table holds
    // source and not output: a thread with forty gadgets opens its strip
    // without building any of them.
    const heads = await on((gadgets) => gadgets.list("20260917-ab3d"), file);

    expect(heads.map((one) => one.title)).toEqual(["Second", "First"]);
  });

  test("a gadget with no thread is found again, where `= null` would lose it", async () => {
    const file = `restart-${(files += 1)}.sqlite`;
    await on((gadgets) => gadgets.show(undefined, "loose", "# Loose\n"), file);

    const heads = await on((gadgets) => gadgets.list(undefined), file);

    expect(heads.map((one) => one.title)).toEqual(["Loose"]);
    expect(heads[0]?.thread).toBeUndefined();
  });

  test("an address nobody wrote is still refused, and the sentence no longer blames a restart", async () => {
    const said = await on((gadgets) =>
      refusalIn(gadgets.read("gadget://20260917-ab3d/never-written")),
    );

    expect(said).toContain("nothing has been written under that name");
    expect(said).not.toContain("restart");
  });
});
