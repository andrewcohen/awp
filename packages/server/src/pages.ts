import type { Page } from "@awp-kit/protocol";
import { PageRefused } from "@awp-kit/protocol";
import { Clock, Context, Effect, Layer, PubSub, Stream } from "effect";

// Where the web panel is pointed, as something more than one thing can move.
//
// ── why the daemon holds this at all ──────────────────────────────────────
//
// The address was the window's own: typed into a box, remembered per thread in
// localStorage, read by nothing else. That is the right home for a preference
// and the wrong one for a *place*, because the panel is one of the few things
// beside an agent that the agent has an opinion about — "look at the failing
// build" is a sentence with a URL in it, and the agent is the half of the
// conversation holding the URL.
//
// So this is the crossing point, and it is deliberately small: a claim about
// where each thread's page is, and a feed of the claims as they are made. What
// it is not is a store. Nothing here is written to disk, and that is not an
// omission — the window remembers the page it was last shown (see
// `remembered.ts`), and what a daemon that has just started knows about a
// navigation somebody asked for yesterday is nothing at all. Answering from a
// stale table would be worse than answering nothing: it would move a person's
// page on launch.
//
// ── a PubSub, not a SubscriptionRef ───────────────────────────────────────
//
// `WorkspaceState` uses a `SubscriptionRef` because what a late subscriber
// wants there is the value *now* and then the changes — a status light with
// nothing in it is a lie about a running agent. Here the opposite holds: a
// navigation is an event. Replaying the last one to a window that has just
// connected would move the page somebody was reading, for a request that was
// answered before they opened the window.
//
// The consequence is stated where it is felt: a subscriber that was not
// listening missed it, and `Page.at` is what makes two identical urls two
// events rather than one.

export class Pages extends Context.Service<
  Pages,
  {
    /**
     * Say where a thread's page should be, and answer with what was recorded.
     *
     * The thread is the caller's to resolve — a directory is not a thread, and
     * only the handler holds the tables that turn one into the other. What
     * this owns is the rule about the url and the fact of publishing it.
     */
    readonly open: (thread: string | undefined, url: string) => Effect.Effect<Page, PageRefused>;
    /** Every page set from now on. Nothing is replayed — see the note above. */
    readonly changes: () => Stream.Stream<Page>;
  }
>()("awp/Pages") {}

/**
 * The url, as something a native webview can be sent to — or a refusal.
 *
 * ── the window guesses and this does not ──────────────────────────────────
 *
 * `addressFor` in the renderer turns `localhost:5173` into a URL and `effect
 * schema v4` into a search, which is right for an address bar: a person is
 * watching the result and can retype it. Nobody is watching this one. A
 * mistyped path silently becoming a search is a call that reports success for
 * a navigation to a search engine, and the agent then reads the wrong page and
 * says something about it.
 *
 * Two schemes and no others. `file://` is the one worth naming as excluded:
 * the panel is a real browser view with a preload in it, and pointing it at
 * the local disk is a larger decision than a navigation call should be able to
 * make.
 */
export const pageAddress = (typed: string): Effect.Effect<string, PageRefused> => {
  const said = typed.trim();
  if (said === "") {
    return Effect.fail(new PageRefused({ reason: "no url — pass one to navigate to" }));
  }
  let parsed: URL;
  try {
    parsed = new URL(said);
  } catch {
    return Effect.fail(
      new PageRefused({
        reason: `${said} is not a url — pass an absolute one, http:// or https://`,
      }),
    );
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return Effect.fail(
      new PageRefused({
        reason: `${said} is ${parsed.protocol} — the web panel takes http:// and https:// only`,
      }),
    );
  }
  // `parsed.href` and not `said`: a url that came back normalised is the one
  // the window will report having navigated to, and two spellings of one page
  // would read as the panel refusing to go where it was sent.
  return Effect.succeed(parsed.href);
};

export const make = Effect.gen(function* () {
  // Dropping rather than blocking, and the number is small on purpose. A
  // subscriber is a socket; one that has stopped reading is a window that has
  // gone away, and a navigation nobody could receive is not worth holding a
  // publisher open for. `PageOpen` answers the caller from its own return
  // value, so a dropped event costs the panel a navigation and costs the agent
  // nothing it was told about.
  const hub = yield* PubSub.dropping<Page>(16);

  return {
    open: (thread: string | undefined, url: string) =>
      Effect.gen(function* () {
        const address = yield* pageAddress(url);
        // The clock, not `Date.now()`: a test that could not control this
        // would have to assert on a real timestamp, and `at` is the field two
        // identical navigations are told apart by.
        const at = yield* Clock.currentTimeMillis;
        const page: Page = { ...(thread === undefined ? {} : { thread }), url: address, at };
        yield* PubSub.publish(hub, page);
        return page;
      }),

    changes: () => Stream.fromPubSub(hub),
  };
});

export const layer: Layer.Layer<Pages> = Layer.effect(Pages)(make);
