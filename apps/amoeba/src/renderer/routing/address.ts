import type { SessionInfo, Thread } from "@awp-kit/protocol";

// An address, and the two questions asked of one.
//
// Separate from routes.ts on purpose, and the reason is the import graph rather
// than tidiness: the route tree names App as the root's component, and App has
// to read the address. Left in one file that is a cycle — App to routes to App
// — which ES modules happen to tolerate here and which nothing guarantees will
// keep being true. Nothing below imports the router, so nothing below can
// participate in one.
//
// See routes.ts for what the three shapes are and why selection is an address
// at all.

/**
 * The three things the window can be looking at.
 *
 * `session` is for a session awp did not create: it has no identity, so its
 * opaque name is the only address it has. Keeping that a separate case rather
 * than a workspace with blank fields is what stops a foreign session being
 * looked up as though it were one of ours.
 */
export type Address =
  | { readonly at: "nothing" }
  | {
      readonly at: "workspace";
      readonly project: string;
      readonly workspace: string;
      readonly kind: string;
    }
  | { readonly at: "session"; readonly name: string }
  /**
   * A thread, which is not a place the window can draw.
   *
   * ── it exists to be a link, and it resolves to a workspace ─────────────
   *
   * A thread holds several checkouts, so there is nothing for the three
   * columns to show for one — the agent column needs a session and the panels
   * need a directory. What a thread address is *for* is being pasted: "copy
   * link" in the sidebar's menu produces one, and opening it lands on the
   * thread's first checkout.
   *
   * So this is the one address that is resolved rather than rendered. `App`
   * swaps it for the workspace it names, with `replace`, so the history holds
   * the place and not the redirect — pressing back from it leaves for wherever
   * you were, not for a link that would resolve again.
   *
   * Why an address at all, rather than the workspace's own: a thread survives
   * its checkouts being renamed, added and removed, and a link that named one
   * of them would go stale the first time somebody reorganised the work. The
   * id is the thing that does not move.
   */
  | { readonly at: "thread"; readonly id: string };

export const nowhere: Address = { at: "nothing" };

/**
 * A thread's address as something pasteable.
 *
 * The origin and path come from the window rather than being composed: the
 * renderer is served by a dev server in development and over `app://` in a
 * build, and only the window knows which. The hash is the whole of the
 * address — see routes.ts on why the history is a hash history.
 *
 * Not a `awp://` scheme link. Nothing registers one, so it would be a link
 * that only looks clickable — and what this is for is pasting into a message,
 * a note or another window of this app.
 *
 * @param at  where the window is. A parameter with a default rather than a
 *            read, because this module is deliberately free of the router and
 *            has to be free of the document for the same reason: it is the
 *            pure half, imported by tests and by anything that must not reach
 *            for a browser. With no location — under vitest, on Node — the
 *            hash alone is answered, which is still a usable address inside
 *            the app rather than a throw.
 */
export const threadLink = (
  id: string,
  at: { readonly origin: string; readonly pathname: string } | undefined = globalThis.location,
): string => {
  const hash = `#${pathOf({ at: "thread", id })}`;
  return at === undefined ? hash : `${at.origin}${at.pathname}${hash}`;
};

/**
 * The style guide's path.
 *
 * Here rather than in routes.ts, and for the reason stated at the top of this
 * file: `App` has to read it, routes.ts names `App` as the root's component,
 * and `import/no-cycle` is on repo-wide. Nothing in this file imports the
 * router, so nothing in it can be part of a cycle — which is the whole reason
 * the two are separate modules.
 *
 * Not an `Address`. The union above is what the window can be *looking at* —
 * a session, a workspace, or nothing — and every reader of it is asking a
 * question about work. The style guide is a different screen rather than a
 * different selection, so making it a fourth case would put a value through
 * `sessionAt` and `placeAt` that neither has an answer for.
 */
export const STYLE_GUIDE = "/styleguide";

/** The address a session lives at. */
export const addressOf = (session: SessionInfo): Address => {
  const id = session.identity;
  return id === undefined
    ? { at: "session", name: session.name }
    : { at: "workspace", project: id.project, workspace: id.workspace, kind: id.kind };
};

/**
 * A path segment.
 *
 * A project or workspace name is `sanitize`d before it ever becomes a session
 * label, so a slash is not expected — but the address is the one place a name
 * from the daemon is turned back into structure, and a name that did carry one
 * would silently become a different route rather than an unfound one.
 */
const part = (value: string): string => encodeURIComponent(value);

/** The path an address is written as. The one place the shapes above are spelled. */
export const pathOf = (address: Address): string => {
  switch (address.at) {
    case "nothing":
      return "/";
    case "session":
      return `/s/${part(address.name)}`;
    case "thread":
      return `/t/${part(address.id)}`;
    case "workspace":
      return `/w/${part(address.project)}/${part(address.workspace)}/${part(address.kind)}`;
  }
};

/**
 * The address the router is currently at.
 *
 * Read off loose params rather than a matched route's typed ones, because the
 * caller is the *root* component and is therefore above every match. The three
 * shapes are told apart by which keys are present, which is exactly what the
 * paths above guarantee.
 */
export const addressFrom = (params: Record<string, string | undefined>): Address => {
  const { project, workspace, kind, name, thread } = params;
  if (thread !== undefined) {
    return { at: "thread", id: thread };
  }
  if (name !== undefined) {
    return { at: "session", name };
  }
  if (project !== undefined && workspace !== undefined && kind !== undefined) {
    return { at: "workspace", project, workspace, kind };
  }
  return nowhere;
};

/**
 * The session an address names, if it is here and can be attached to.
 *
 * Three halves now, and the third was found in the wild. A remembered address
 * may name a session that has since gone from the listing; one the daemon
 * refuses — because it is the session the daemon itself is running in — is
 * present and must not be opened; and one whose process has **exited** is
 * present, unrefused, and cannot be attached to either:
 *
 *   name=awp.awp.test.agent  ended=1788891181  exit_code=127
 *
 * zmx keeps an ended session in `zmx ls`, so the old test — a name in the
 * listing with no refusal — answered with it, and the pane attached to a
 * process that was not running. What that looks like is a blank terminal,
 * which is also what a terminal that failed to draw looks like. `placeAt`
 * still resolves the workspace from it, so the answer here being nothing is
 * what puts the start control on screen rather than a dead pane.
 *
 * Nothing is written back when this answers undefined. The address is what was
 * asked for and the listing is what is true; correcting one from the other
 * would be keeping a second copy of something already known, and would race
 * the first listing on launch.
 */
export const sessionAt = (
  address: Address,
  sessions: ReadonlyArray<SessionInfo>,
): SessionInfo | undefined => {
  // A thread is resolved to a workspace before anything asks this — see the
  // note on the variant — so it answers nothing rather than being a fourth
  // case to reason about here.
  if (address.at === "nothing" || address.at === "thread") {
    return undefined;
  }
  const found = sessions.find((session) =>
    address.at === "session"
      ? session.name === address.name
      : session.identity?.project === address.project &&
        session.identity.workspace === address.workspace &&
        session.identity.kind === address.kind,
  );
  return found?.refusal === undefined && found?.ended !== true ? found : undefined;
};

/**
 * The workspace an address names, whether or not anything is running in it.
 *
 * ── the address is a workspace, and `sessionAt` is the narrower question ────
 *
 * Everything beside the terminal used to be drawn from `sessionAt`, which
 * meant a workspace whose agent had exited had no chat, no diff and no pull
 * request — though its directory, its bookmark, its thread and its
 * conversation were all still there. It was reported as lost work, and from
 * the outside that is exactly what it was:
 *
 *   a session exists   →  the row resolves  →  everything opens
 *   nothing running    →  nothing resolves  →  the conversation is unreachable
 *
 * So the two questions are separate, and each caller asks the one it means.
 * `sessionAt` is for attaching — the pane, and nothing else. This is for
 * everything that is a question about the *checkout*, none of which needs a
 * pty at all.
 *
 * Gated on the workspace being *known*, and by two sources rather than one. A
 * session carrying the identity is the ordinary case; a live thread holding
 * the pair is what covers the one this exists for. A remembered address that
 * names neither answers undefined rather than opening panels onto a directory
 * nothing has ever heard of.
 *
 * A foreign session — `/s/<name>` — is not a workspace and never resolves
 * here. It has no identity, so there is nothing to hold a conversation in and
 * the pane is the only honest answer for it.
 */
export const placeAt = (
  address: Address,
  sessions: ReadonlyArray<SessionInfo>,
  threads: ReadonlyArray<Thread>,
): { readonly project: string; readonly workspace: string; readonly kind: string } | undefined => {
  if (address.at !== "workspace") {
    return undefined;
  }
  const known =
    sessions.some(
      (session) =>
        session.identity?.project === address.project &&
        session.identity.workspace === address.workspace,
    ) ||
    threads.some(
      (thread) =>
        thread.archivedAt === undefined &&
        thread.members.some(
          (member) => member.project === address.project && member.workspace === address.workspace,
        ),
    );
  return known
    ? { project: address.project, workspace: address.workspace, kind: address.kind }
    : undefined;
};
