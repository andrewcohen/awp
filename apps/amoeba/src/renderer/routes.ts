import {
  createHashHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { App } from "./App";
import { STYLE_GUIDE } from "./address";

// Where the window is, as an address rather than a variable.
//
// ── what this replaced, and why that was wrong ─────────────────────────────
// Selection used to be one string of React state — the *session name* — kept
// across reloads by hand in localStorage. Three things were wrong with it, and
// only the last one is really about routing.
//
//   1. There was no back. Opening a workspace threw away which one you had
//      been looking at, and the window had no memory of it.
//   2. `remembered.ts` was persistence hand-rolled for exactly the thing a
//      history already keeps.
//   3. A session name is a SHORTENED address, and cannot be split back into
//      its parts.
//
// The third is the argument. AGENTS.md states the rule at length: `sessionName`
// gives the stem whatever budget the kind does not need, so two sessions of one
// workspace are shortened to different stems, and reading a name back is
// guessing. The daemon already sends the unshortened truth as `SessionIdentity`
// — and the old selection threw it away, storing the shortened form and then
// searching the listing for a name equal to it.
//
// A route stores the truth instead:
//
//   before   selected = "awp.thicket.effect-ts-tabular-expor-ca90.agent"
//   after    /w/thicket/effect-ts-tabular-export-timemachine/agent
//
// The second survives a session being restarted under a different shortening.
// The first does not, and that is a real failure rather than a tidiness point.
//
// ── the shape of the tree, and why it is flat ──────────────────────────────
// One route level, and no `Outlet`. The window's layout does not change with
// the address — the same two bars and three columns are on screen whatever is
// selected, and only the pane's contents differ. A nested route rendering a
// different component tree would be modelling a screen change that does not
// happen, and would then have to hand the session list back down through it.
//
// So the root renders the whole window and reads the address; the leaf routes
// exist to *type and parse* it. That is the whole job, and it is enough of one.
//
// ── hash history ───────────────────────────────────────────────────────────
// The window loads the renderer from a dev server in development and from the
// app's own `app://` scheme in a build, and only one of those is a server that
// would rewrite a deep path back to index.html. A hash needs nothing to agree
// with it, and still pushes real entries onto the session history, so back and
// forward work.

const rootRoute = createRootRoute({ component: App });

const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/" });

/** One of ours: the unshortened project, workspace and kind. */
const workspaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/w/$project/$workspace/$kind",
});

/**
 * The style guide: every colour, every type step, every control, on one page.
 *
 * ── a route, and not a panel in the accessory column ──────────────────────
 *
 * The panels are all about *the work* — this workspace's diff, this thread's
 * pull request, this project's tasks — and a page of swatches is about the
 * application itself. Put in the strip it would be a permanent empty room in
 * the column somebody switches most, on every workspace, for a thing looked at
 * while iterating on the design and at no other time.
 *
 * It is also the one screen in this window whose whole point is that nothing
 * else is on it. Colour is judged against its neighbours, so a palette drawn
 * beside a terminal, a sidebar and somebody's diff is a palette judged against
 * those instead of against itself.
 *
 * `#/styleguide`, reachable by typing it, which is what a development surface
 * wants: no control on screen for it, nothing to explain in the chrome, and no
 * keyboard chord taken away from the work.
 */
const styleGuideRoute = createRoute({ getParentRoute: () => rootRoute, path: STYLE_GUIDE });

/**
 * A thread, which is a link rather than a screen.
 *
 * There is nothing for the columns to draw for a thread — it holds several
 * checkouts — so `App` swaps this for the workspace it names and replaces the
 * entry. See the `thread` variant in address.ts for why the id is the thing
 * worth pasting.
 */
const threadRoute = createRoute({ getParentRoute: () => rootRoute, path: "/t/$thread" });

/** Someone else's: the session name, because there is nothing else to hold. */
const sessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$name",
});

export const routeTree = rootRoute.addChildren([
  homeRoute,
  workspaceRoute,
  sessionRoute,
  threadRoute,
  styleGuideRoute,
]);

export const router = createRouter({ routeTree, history: createHashHistory() });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
