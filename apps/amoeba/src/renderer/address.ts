import type { SessionInfo } from "@awp-kit/protocol";

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
  | { readonly at: "session"; readonly name: string };

export const nowhere: Address = { at: "nothing" };

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
  const { project, workspace, kind, name } = params;
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
 * Both halves matter. A remembered address may name a session that has since
 * ended, and one the daemon refuses — because it is the session the daemon
 * itself is running in — is present in the listing and must not be opened.
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
  if (address.at === "nothing") {
    return undefined;
  }
  const found = sessions.find((session) =>
    address.at === "session"
      ? session.name === address.name
      : session.identity?.project === address.project &&
        session.identity.workspace === address.workspace &&
        session.identity.kind === address.kind,
  );
  return found?.refusal === undefined ? found : undefined;
};
