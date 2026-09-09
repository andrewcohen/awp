import { Effect, Fiber, Option, Result, Stream } from "effect";
import { describe, expect, test } from "vitest";
import { Pages, layer, pageAddress } from "./pages";

// Two properties, and they are the two things this service is for: the rule
// about what a url may be, and the fact that setting one reaches a subscriber
// who is not the caller.

const on = <A>(program: (pages: Pages["Service"]) => Effect.Effect<A, unknown>): Promise<A> =>
  Effect.gen(function* () {
    const pages = yield* Pages;
    return yield* program(pages);
  }).pipe(Effect.provide(layer), Effect.scoped, Effect.orDie, Effect.runPromise);

const refusalOf = (typed: string): string => {
  const found = Effect.runSync(Effect.result(pageAddress(typed)));
  if (Result.isSuccess(found)) {
    throw new Error(`${typed} was accepted, and should not have been`);
  }
  return found.failure.reason;
};

describe("the url", () => {
  test("an absolute http or https url is taken as it stands", () => {
    expect(Effect.runSync(pageAddress("https://example.invalid/build/412"))).toBe(
      "https://example.invalid/build/412",
    );
    expect(Effect.runSync(pageAddress("http://localhost:5273/#/"))).toBe(
      "http://localhost:5273/#/",
    );
  });

  test("a bare host is refused rather than guessed at", () => {
    // The address bar guesses, because a person is watching the result. This
    // is called by an agent that is not, so a guess would be a navigation
    // nobody could check.
    expect(refusalOf("example.invalid/build")).toContain("not a url");
  });

  test("prose is refused rather than searched for", () => {
    expect(refusalOf("effect schema v4")).toContain("not a url");
  });

  test("a scheme that is not the web is named in the refusal", () => {
    // The panel is a real browser view with a preload in it. Pointing it at
    // the local disk is a larger decision than a navigation call should make,
    // and the sentence has to say which scheme was the problem.
    expect(refusalOf("file:///etc/passwd")).toContain("file:");
  });

  test("nothing at all is its own sentence", () => {
    expect(refusalOf("   ")).toContain("no url");
  });
});

describe("open", () => {
  test("what is published is what the caller was told", () =>
    on((pages) =>
      Effect.gen(function* () {
        // Subscribed first, because nothing is replayed — see the test below.
        const heard = yield* Effect.forkChild(Stream.runHead(pages.changes()));
        // The fork above has to reach its subscribe before the publish, and
        // `Stream.runHead` yields on its way there. One yield is enough on
        // this runtime and a sleep would be a slower way to say the same.
        yield* Effect.yieldNow;
        const said = yield* pages.open("th-1", "https://example.invalid/build/412");
        const arrived = yield* Fiber.join(heard);
        expect(Option.getOrUndefined(arrived)).toEqual(said);
        expect(said.thread).toBe("th-1");
        expect(said.url).toBe("https://example.invalid/build/412");
      }),
    ));

  test("a listener that joins afterwards hears nothing", () =>
    on((pages) =>
      Effect.gen(function* () {
        // Deliberate, and the reason is on the service: a navigation is an
        // event. Replaying the last one to a window that has just connected
        // would move the page somebody is reading, for a request answered
        // before they opened the window.
        yield* pages.open("th-1", "https://example.invalid/one");
        // A timeout rather than an interrupt, because the assertion has to be
        // able to fail: a stream that *did* replay would answer inside the
        // window, and an interrupted fiber would have looked like a pass
        // either way.
        const found = yield* Stream.runHead(pages.changes()).pipe(
          Effect.timeoutOption("50 millis"),
        );
        expect(Option.isNone(found)).toBe(true);
      }),
    ));

  test("a workspace no thread claims carries no thread rather than a placeholder", () =>
    on((pages) =>
      Effect.gen(function* () {
        const said = yield* pages.open(undefined, "https://example.invalid/");
        expect(said.thread).toBeUndefined();
      }),
    ));

  test("the same url twice is two events, told apart by `at`", () =>
    on((pages) =>
      Effect.gen(function* () {
        // Asking for the page already showing is a real request — it means
        // reload — so the two answers must not be value-equal, or the window
        // cannot tell a second ask from no ask at all.
        const first = yield* pages.open("th-1", "https://example.invalid/");
        const again = yield* pages.open("th-1", "https://example.invalid/");
        expect(again.at).toBeGreaterThanOrEqual(first.at);
        expect(again).not.toBe(first);
      }),
    ));
});
