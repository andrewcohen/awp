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
