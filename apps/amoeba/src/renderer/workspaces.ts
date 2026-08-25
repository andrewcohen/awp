import type { SessionInfo } from "@awp-kit/protocol";

// The sidebar lists workspaces. zmx lists sessions. This is the difference.
//
// A workspace has one session per kind — an agent, an editor, whatever user
// actions are configured — and every one of them is a separate zmx session with
// its own name. Listing those names and labelling each with its workspace is
// how three rows read `thicket.default` and two of them were the same place:
//
//   awp.thicket.default.agent   ┐
//   awp.thicket.default.editor  ┘ →  "thicket.default", twice
//
// The identity comes off the wire rather than out of the name. Splitting a name
// on dots looks like it answers this and does not: a name is shortened when it
// would exceed what zmx accepts, and a dot inside a real project name comes back
// as an underscore. The daemon has the labels awp wrote, which are the
// unshortened truth — see SessionIdentity in the protocol.

/**
 * The kind a workspace opens to when nothing more specific was asked for.
 *
 * The agent is the point of the workspace; the editor is beside it. A row with
 * one session opens that one whatever it is, so this only decides between them.
 */
export const PRIMARY = "agent";

export type Workspace = {
  /** `project.workspace`, or the session name for one of someone else's. */
  readonly key: string;
  readonly label: string;
  /** Ordered: the primary kind first, then the rest by name. */
  readonly sessions: ReadonlyArray<SessionInfo>;
  /**
   * True for a session awp did not create. `zmx ls` reports every session on
   * the machine, and someone else's is not a workspace — it is shown, because
   * hiding a session the daemon can see would be a lie, but it is not grouped
   * and it does not pretend to a project.
   */
  readonly foreign: boolean;
};

const byKind = (a: SessionInfo, b: SessionInfo): number => {
  const ak = a.identity?.kind ?? "";
  const bk = b.identity?.kind ?? "";
  if (ak === bk) {
    return 0;
  }
  if (ak === PRIMARY) {
    return -1;
  }
  if (bk === PRIMARY) {
    return 1;
  }
  return ak.localeCompare(bk);
};

/**
 * Sessions, as the workspaces they belong to.
 *
 * Order is by label rather than whatever order the daemon listed in. A sidebar
 * is read by scanning it, and a list that reorders itself between two polls
 * because a session ended is a list nobody can scan.
 */
export const groupByWorkspace = (
  sessions: ReadonlyArray<SessionInfo>,
): ReadonlyArray<Workspace> => {
  const grouped = new Map<string, SessionInfo[]>();
  const foreign: Workspace[] = [];

  for (const session of sessions) {
    const id = session.identity;
    if (id === undefined) {
      foreign.push({ key: session.name, label: session.name, sessions: [session], foreign: true });
      continue;
    }
    const key = `${id.project}.${id.workspace}`;
    grouped.set(key, [...(grouped.get(key) ?? []), session]);
  }

  const workspaces: Workspace[] = [...grouped].map(([key, found]) => ({
    key,
    label: key,
    sessions: found.toSorted(byKind),
    foreign: false,
  }));

  // awp's own first, then anyone else's. Two different things in one list, and
  // the boundary is worth being able to see without reading either.
  return [
    ...workspaces.toSorted((a, b) => a.label.localeCompare(b.label)),
    ...foreign.toSorted((a, b) => a.label.localeCompare(b.label)),
  ];
};

/** The session a workspace opens to, or undefined if none of them can be. */
export const openable = (workspace: Workspace): SessionInfo | undefined =>
  workspace.sessions.find((session) => session.refusal === undefined);
