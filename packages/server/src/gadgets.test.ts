import { Effect, Result } from "effect";
import { describe, expect, test } from "vitest";
import { Gadgets, layer } from "./gadgets";

// What a gadget service owes its two callers, and they want opposite things:
// the agent wants to be told exactly what is wrong with the document it just
// wrote, and the window wants either a document that runs or a sentence it can
// print. So every test here is about a refusal or about what survives a round
// trip — there is nothing in between.

const on = <A>(program: (gadgets: Gadgets["Service"]) => Effect.Effect<A, unknown>): Promise<A> =>
  Effect.gen(function* () {
    const gadgets = yield* Gadgets;
    return yield* program(gadgets);
  }).pipe(Effect.provide(layer), Effect.scoped, Effect.orDie, Effect.runPromise);

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
    // Nothing is on disk, so the daemon's own restart is this case — and the
    // window remembers the address across launches. The sentence is what the
    // panel prints, so it has to name the repair.
    const reason = await on((gadgets) => refusalIn(gadgets.read("gadget://loose/nothing")));
    expect(reason).toContain("no gadget at");
    expect(reason).toContain("restart");
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
      Effect.gen(function* () {
        const first = yield* gadgets.show("20260917-ab3d", "first", "# First\n");
        const second = yield* gadgets.show("20260917-ab3d", "second", "# Second\n");
        const mine = yield* gadgets.list("20260917-ab3d");
        expect(second.at).toBe(first.at);
        expect(mine.map((one) => one.title)).toEqual(["Second", "First"]);
      }),
    ));

  test("a rewrite moves to the front of its own tie", () =>
    on((gadgets) =>
      Effect.gen(function* () {
        yield* gadgets.show("20260917-ab3d", "first", "# First\n");
        yield* gadgets.show("20260917-ab3d", "second", "# Second\n");
        yield* gadgets.show("20260917-ab3d", "first", "# First, revised\n");
        const mine = yield* gadgets.list("20260917-ab3d");
        expect(mine.map((one) => one.title)).toEqual(["First, revised", "Second"]);
      }),
    ));
});
