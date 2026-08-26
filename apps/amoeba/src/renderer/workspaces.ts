import type { SessionInfo, Thread } from "@awp-kit/protocol";

// The sidebar lists workspaces. zmx lists sessions. This is the difference.
//
// A workspace has one session per kind — an agent, an editor, whatever user
// actions are configured — and every one of them is a separate zmx session with
// its own name. Listing those names and labelling each with its workspace is
// how three rows read `rowan.default` and two of them were the same place:
//
//   awp.rowan.default.agent   ┐
//   awp.rowan.default.editor  ┘ →  "rowan.default", twice
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

/** The workspace a repository has before anyone makes another one. */
const DEFAULT = "default";

export type Workspace = {
  /** `project.workspace`, or the session name for one of someone else's. */
  readonly key: string;
  /**
   * The whole address — `project.workspace` — for a tooltip and for ordering.
   *
   * Named for what it is rather than `label`, which it used to be called and
   * which now means the human one below. Two fields called nearly the same
   * thing is how a tooltip ends up showing a sentence and a heading a slug.
   */
  readonly address: string;
  /**
   * What the row is called.
   *
   * A workspace named `default` is the repository's own, and the word says
   * nothing about it — six projects with one workspace each would be six rows
   * reading `default`. So a default workspace is named for its project.
   */
  readonly name: string;
  /**
   * What a person called this work, or absent.
   *
   * The slug is a slug because it has to be a directory, a jj workspace and
   * half a bookmark: `effect-ts-tiered-discount-timemachine`. What was asked
   * for was a sentence, and until the `awp_label` session label existed it
   * survived only as the title of a thread — so a workspace no thread claimed
   * had nowhere to keep it.
   *
   * Absent for every workspace made before that label, which today is nearly
   * all of them. A row falls back to {@link Workspace.name} rather than going
   * blank, and that fallback is why this could be added without a migration.
   */
  readonly label: string | undefined;
  /**
   * The half of project/workspace that {@link Workspace.name} did not use, for
   * the row's second line. Taken from the Go deck, whose rule this is: the two
   * halves are one fact between them, and repeating either is a column of
   * identical words where the distinguishing one should be.
   */
  readonly otherIdent: string | undefined;
  /** Ordered: the primary kind first, then the rest by name. */
  readonly sessions: ReadonlyArray<SessionInfo>;
  /**
   * When the oldest of its sessions was started, or 0 if zmx did not say.
   *
   * The workspace's own age, near enough. Used for ordering, and taken from the
   * *oldest* rather than the newest deliberately: opening an editor beside an
   * agent that has been running all week should not move that week-old
   * workspace to the top of the strip.
   */
  readonly since: number;
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
      foreign.push({
        key: session.name,
        address: session.name,
        label: undefined,
        name: session.name,
        otherIdent: undefined,
        sessions: [session],
        since: startedAt([session]),
        foreign: true,
      });
      continue;
    }
    const key = `${id.project}.${id.workspace}`;
    grouped.set(key, [...(grouped.get(key) ?? []), session]);
  }

  const workspaces: Workspace[] = [...grouped].map(([key, found]) => {
    const id = found[0]?.identity;
    const project = id?.project ?? "";
    const workspace = id?.workspace ?? "";
    const isDefault = workspace === DEFAULT;
    return {
      key,
      address: key,
      // Taken from whichever session carries one. Only the agent is labelled
      // today — the job writes it when it starts that session — so an editor
      // opened later beside it must not decide the workspace has no label.
      label: found.map((session) => session.identity?.label).find((one) => one !== undefined),
      name: isDefault ? project : workspace,
      otherIdent: isDefault ? DEFAULT : project,
      sessions: found.toSorted(byKind),
      since: startedAt(found),
      foreign: false,
    };
  });

  // awp's own first, then anyone else's. Two different things in one list, and
  // the boundary is worth being able to see without reading either.
  return [...workspaces.toSorted(newestFirst), ...foreign.toSorted(newestFirst)];
};

/**
 * When a workspace's oldest session started, or 0.
 *
 * `created` is absent when zmx did not say, and a workspace with no answer
 * sorts last rather than first — an unknown age is not a claim to be new.
 */
const startedAt = (sessions: ReadonlyArray<SessionInfo>): number => {
  const times = sessions
    .map((session) => session.created?.getTime())
    .filter((time): time is number => time !== undefined);
  return times.length === 0 ? 0 : Math.min(...times);
};

/**
 * Newest workspace first, ties broken by address.
 *
 * ── this used to be alphabetical, and the reason it changed ────────────────
 * The old order was by address, with a note saying a list that reorders itself
 * between two polls is a list nobody can scan. That reasoning is still right;
 * it just does not apply to *this* key. `since` is when a session was started,
 * which never changes — so the order is as stable as the alphabetical one was,
 * and it puts the work someone is actually doing at the top instead of
 * whichever project begins with an early letter.
 *
 * The alphabetical tiebreak matters more than it looks. A workspace whose
 * sessions predate zmx reporting `created` has `since: 0`, and without it every
 * one of those would sit in whatever order a Map iterated.
 */
const newestFirst = (a: Workspace, b: Workspace): number =>
  b.since - a.since || a.address.localeCompare(b.address);

/** The session a workspace opens to, or undefined if none of them can be. */
export const openable = (workspace: Workspace): SessionInfo | undefined =>
  workspace.sessions.find((session) => session.refusal === undefined);

// ── threads ────────────────────────────────────────────────────────────────

/**
 * A thread and the workspaces it has claimed, or the group for everything it
 * has not.
 *
 * The loose group is not a thread with a blank name, and the difference shows
 * up everywhere downstream: it cannot be renamed, archived or claimed into, and
 * a row inside it offers to *join* a thread where a row inside a real one
 * offers to leave. `thread: undefined` is what makes that a type error rather
 * than a runtime check.
 */
export type ThreadGroup = {
  readonly key: string;
  readonly title: string;
  readonly thread: Thread | undefined;
  readonly workspaces: ReadonlyArray<Workspace>;
};

/** Where a workspace sits, if any thread has claimed it. */
const claimant = (threads: ReadonlyArray<Thread>, workspace: Workspace): Thread | undefined => {
  const id = workspace.sessions[0]?.identity;
  if (id === undefined || workspace.foreign) {
    return undefined;
  }
  return threads.find((thread) =>
    thread.members.some(
      (member) => member.project === id.project && member.workspace === id.workspace,
    ),
  );
};

/**
 * Workspaces, as the threads that claimed them.
 *
 * Every thread appears, including one that has claimed nothing — a thread made
 * a moment ago with no workspace in it yet is the single most important row on
 * the strip, because it is the one waiting to be filled.
 *
 * Archived threads are dropped. They are still on the wire, because the record
 * that a set of workspaces were once one job is worth more after the fact than
 * during; they are simply not what the sidebar is for.
 *
 * Everything unclaimed goes in one group at the end, and **nothing is guessed
 * into a thread**. That is the same rule `identities` follows for a workspace
 * whose sessions carry no labels: a group that is honestly unknown beats a
 * group that is confidently wrong.
 */
export const groupByThread = (
  threads: ReadonlyArray<Thread>,
  workspaces: ReadonlyArray<Workspace>,
): ReadonlyArray<ThreadGroup> => {
  const live = threads.filter((thread) => thread.archivedAt === undefined);
  const claimed = new Map<string, Workspace[]>();
  const loose: Workspace[] = [];

  for (const workspace of workspaces) {
    const thread = claimant(live, workspace);
    if (thread === undefined) {
      loose.push(workspace);
      continue;
    }
    claimed.set(thread.id, [...(claimed.get(thread.id) ?? []), workspace]);
  }

  const groups: ThreadGroup[] = live.map((thread) => ({
    key: thread.id,
    title: thread.title === "" ? "untitled" : thread.title,
    thread,
    workspaces: claimed.get(thread.id) ?? [],
  }));

  // Newest thread first — a thread is made when work starts, so the one at the
  // top is the one being worked on. Workspaces inside stay in the order
  // `groupByWorkspace` put them, which is alphabetical and does not move.
  groups.sort(
    (a, b) => (b.thread?.createdAt.getTime() ?? 0) - (a.thread?.createdAt.getTime() ?? 0),
  );

  return loose.length === 0
    ? groups
    : [
        ...groups,
        { key: "\u0000loose", title: "not in a thread", thread: undefined, workspaces: loose },
      ];
};

// ── what the new-thread modal needs to know ────────────────────────────────

/**
 * A project the window knows about, and a directory inside it.
 *
 * Derived from the sessions rather than from a configured list of roots, which
 * is a real limit and is stated here rather than hidden: a project awp has
 * never opened a session in does not appear, so the very first thread on a
 * machine still cannot be started from this window. The config's
 * `deck.project_roots` is what fixes that, and it is deliberately not read yet.
 *
 * What this does buy is the thing the inline box got wrong. There, the project
 * came from whichever row was *selected*, so starting a thread meant first
 * clicking a workspace in a different project than the one you wanted. A picker
 * over everything known is a list a person can choose from, and a selected row
 * merely decides which entry it opens on.
 */
export type Project = {
  readonly name: string;
  /**
   * A directory inside the project. The daemon turns it into the repository
   * root with `Jj.sourceRoot` — a client cannot, because `jj root` inside a
   * secondary workspace answers with the workspace.
   */
  readonly from: string;
};

/**
 * Every project the sessions name, each with a directory to resolve it from.
 *
 * A session with no `startDir` cannot answer "which repository", so it names a
 * project and contributes nothing else; a project whose sessions *all* lack one
 * is dropped rather than offered as an entry that cannot work.
 */
export const projectsOf = (sessions: ReadonlyArray<SessionInfo>): ReadonlyArray<Project> => {
  const found = new Map<string, string>();
  for (const session of sessions) {
    const project = session.identity?.project;
    const from = session.startDir;
    if (project === undefined || project === "" || from === undefined || from === "") {
      continue;
    }
    if (!found.has(project)) {
      found.set(project, from);
    }
  }
  return [...found]
    .map(([name, from]) => ({ name, from }))
    .toSorted((a, b) => a.name.localeCompare(b.name));
};
