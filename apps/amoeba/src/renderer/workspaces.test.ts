import type { SessionInfo, Thread, WorkspaceStatus } from "@awp-kit/protocol";
import { describe, expect, it, test } from "vitest";
import { type Workspace, groupByThread, groupByWorkspace, openable } from "./workspaces";

// The grouping is where the sidebar's one real decision lives, so it is tested
// away from the markup. Every fixture below is shaped like something `zmx ls`
// actually returned on 2026-08-25.

const session = (
  name: string,
  identity: SessionInfo["identity"],
  extra: Partial<SessionInfo> = {},
): SessionInfo => ({
  name,
  pid: 1,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  exitCode: 0,
  created: undefined,
  cmd: "claude",
  labels: {},
  identity,
  refusal: undefined,
  ...extra,
});

const awp = (project: string, workspace: string, kind: string, extra?: Partial<SessionInfo>) =>
  session(
    `awp.${project}.${workspace}.${kind}`,
    { project, workspace, kind, label: undefined },
    extra,
  );

describe("grouping sessions into workspaces", () => {
  // The whole reason this module exists. Two sessions of one workspace used to
  // be two rows with the same text, because the label came from splitting the
  // name and the kind was the part being dropped.
  it("puts a workspace's sessions on one row", () => {
    const got = groupByWorkspace([
      awp("rowan", "default", "agent"),
      awp("rowan", "default", "editor"),
    ]);
    expect(got).toHaveLength(1);
    expect(got[0]?.address).toBe("rowan.default");
    expect(got[0]?.sessions).toHaveLength(2);
  });

  // The agent is what the workspace is for. Whatever order zmx listed them in,
  // clicking the row must not open the editor.
  it("puts the agent first, whatever order they arrived in", () => {
    const got = groupByWorkspace([
      awp("rowan", "default", "editor"),
      awp("rowan", "default", "agent"),
    ]);
    expect(got[0]?.sessions.map((s) => s.identity?.kind)).toEqual(["agent", "editor"]);
  });

  // A sidebar is read by scanning it. Order that depends on what the daemon
  // happened to return is order that changes between two polls.
  it("orders workspaces by name, not by arrival", () => {
    const got = groupByWorkspace([
      awp("rowan", "default", "agent"),
      awp("orchard", "effect-v4-poc", "agent"),
      awp("mossy", "default", "agent"),
    ]);
    expect(got.map((w) => w.address)).toEqual([
      "mossy.default",
      "orchard.effect-v4-poc",
      "rowan.default",
    ]);
  });

  // `zmx ls` reports every session on the machine. Someone else's is shown —
  // hiding what the daemon can see would be a lie — but it is not a workspace
  // and does not get a project it never had.
  it("keeps a session awp did not create out of the workspaces, and after them", () => {
    const got = groupByWorkspace([
      session("some-other-tool", undefined),
      awp("rowan", "default", "agent"),
    ]);
    expect(got.map((w) => [w.address, w.foreign])).toEqual([
      ["rowan.default", false],
      ["some-other-tool", true],
    ]);
  });

  // Two workspaces of the same name in different projects are two workspaces.
  // The key is the pair, and a label that showed only the workspace would merge
  // them — which is the same class of bug as the one this module fixes.
  it("does not merge the same workspace name across projects", () => {
    const got = groupByWorkspace([
      awp("rowan", "default", "agent"),
      awp("mossy", "default", "agent"),
    ]);
    expect(got).toHaveLength(2);
  });

  it("does not mutate what it was given", () => {
    const input = [awp("rowan", "default", "editor"), awp("rowan", "default", "agent")];
    const before = input.map((s) => s.name);
    groupByWorkspace(input);
    expect(input.map((s) => s.name)).toEqual(before);
  });
});

describe("which session a workspace opens to", () => {
  it("skips a session that cannot be attached to", () => {
    const [workspace] = groupByWorkspace([
      awp("awp", "awp-kit-amoeba", "agent", { refusal: "this is the session awp is running in" }),
      awp("awp", "awp-kit-amoeba", "editor"),
    ]);
    expect(openable(workspace!)?.identity?.kind).toBe("editor");
  });

  it("says so when none of them can be", () => {
    const [workspace] = groupByWorkspace([
      awp("awp", "awp-kit-amoeba", "agent", { refusal: "no" }),
      awp("awp", "awp-kit-amoeba", "editor", { refusal: "also no" }),
    ]);
    expect(openable(workspace!)).toBeUndefined();
  });
});

describe("what a row calls itself", () => {
  // `default` is the repository's own workspace and the word says nothing about
  // it. Six projects with one workspace each would be six rows reading
  // "default", which is a column of one repeated word where the distinguishing
  // one should be.
  it("names a default workspace for its project", () => {
    const [w] = groupByWorkspace([awp("dotfiles", "default", "agent")]);
    expect(w?.name).toBe("dotfiles");
    expect(w?.otherIdent).toBe("default");
  });

  it("names any other workspace for itself, and puts the project below", () => {
    const [w] = groupByWorkspace([awp("rowan", "pr-2340-lantern-header", "agent")]);
    expect(w?.name).toBe("pr-2340-lantern-header");
    expect(w?.otherIdent).toBe("rowan");
  });

  // The full address is still what orders the list and what the tooltip shows,
  // so shortening the visible name cannot make two rows indistinguishable.
  it("keeps the whole address whatever it calls itself", () => {
    const got = groupByWorkspace([
      awp("rowan", "default", "agent"),
      awp("mossy", "default", "agent"),
    ]);
    expect(got.map((w) => w.name)).toEqual(["mossy", "rowan"]);
    expect(got.map((w) => w.address)).toEqual(["mossy.default", "rowan.default"]);
  });
});

// ── threads ────────────────────────────────────────────────────────────────

/**
 * A thread, made just now unless a test says otherwise.
 *
 * The clock rather than a fixed stamp, because an empty thread is only shown
 * while it is new — see `PENDING_FOR`. A fixture stamped in 1970 would age out
 * of every test that did not mean to be about ageing, which is what happened
 * when that rule landed.
 */
/**
 * A status read off the workspace's own name, so a test can state the one it
 * means in the fixture rather than in a second table beside it.
 */
const byAddress = (workspace: Workspace): WorkspaceStatus | undefined => {
  if (workspace.address.endsWith("asking")) return "waiting";
  if (workspace.address.endsWith("busy")) return "working";
  if (workspace.address.endsWith("finished") || workspace.address.endsWith("quiet")) return "idle";
  return undefined;
};

const thread = (over: Partial<Thread> = {}): Thread => ({
  id: "t1",
  title: "tiered discounts",
  createdAt: new Date(),
  archivedAt: undefined,
  parentId: undefined,
  members: [],
  ...over,
});

const inProject = (project: string, workspace: string): SessionInfo =>
  session(`awp.${project}.${workspace}.agent`, {
    project,
    workspace,
    kind: "agent",
    label: undefined,
  });

describe("groupByThread", () => {
  test("a thread shows the workspaces it claimed", () => {
    const workspaces = groupByWorkspace([
      inProject("rowan", "discounts"),
      inProject("beta", "discounts"),
    ]);
    const groups = groupByThread(
      [
        thread({
          members: [
            { project: "rowan", workspace: "discounts" },
            { project: "beta", workspace: "discounts" },
          ],
        }),
      ],
      workspaces,
    );

    // One piece of work, two checkouts — the whole reason threads exist.
    expect(groups).toHaveLength(1);
    expect(groups[0]?.workspaces.map((w) => w.address)).toEqual([
      "beta.discounts",
      "rowan.discounts",
    ]);
  });

  test("a thread with nothing in it shows while it is new, and not after", () => {
    // Both halves of one rule, and the second half reversed an earlier one.
    //
    // A thread is created before the job that fills it, so for the length of
    // that job it genuinely is the most important row on the strip. After the
    // job has failed and rolled back it is litter — and measured on a real
    // machine that was eighteen empty headers above one bucket of twenty-five
    // rows, which is the shape that made the sidebar unreadable.
    const made = new Date(1_787_000_000_000);
    const soon = made.getTime() + 60_000;
    const later = made.getTime() + 60 * 60_000;

    expect(groupByThread([thread({ createdAt: made })], [], undefined, soon)).toHaveLength(1);
    expect(groupByThread([thread({ createdAt: made })], [], undefined, later)).toHaveLength(0);
  });

  test("a thread holding a workspace shows however old it is", () => {
    // The window is about a thread with *nothing* in it. A thread that has
    // claimed work is a record of that work, and ageing it out would hide the
    // thing the sidebar exists to show.
    const workspaces = groupByWorkspace([inProject("rowan", "discounts")]);
    const old = thread({
      createdAt: new Date(0),
      members: [{ project: "rowan", workspace: "discounts" }],
    });

    expect(groupByThread([old], workspaces, undefined, Date.now())).toHaveLength(1);
  });

  test("the unclaimed group is ordered by what needs attention", () => {
    // Only this group. A thread's own workspaces stay in the order it holds
    // them — that is a person's arrangement of their work, and reordering it
    // underneath them would move rows while they were looking at them.
    const workspaces = groupByWorkspace([
      inProject("orchard", "quiet"),
      inProject("orchard", "busy"),
      inProject("orchard", "asking"),
    ]);
    const groups = groupByThread([], workspaces, byAddress, Date.now());

    expect(groups.at(-1)?.workspaces.map((w) => w.address)).toEqual([
      "orchard.asking",
      "orchard.busy",
      "orchard.quiet",
    ]);
  });

  test("a workspace nothing has reported on sorts after one that is idle", () => {
    // Unknown is not idle. A workspace whose status nothing has ever written
    // is a different thing from one an agent has finished in, and sorting them
    // together would put a row that has never run above one waiting to be read.
    const workspaces = groupByWorkspace([
      inProject("orchard", "never"),
      inProject("orchard", "finished"),
    ]);
    const groups = groupByThread([], workspaces, byAddress, Date.now());

    expect(groups.at(-1)?.workspaces.map((w) => w.address)).toEqual([
      "orchard.finished",
      "orchard.never",
    ]);
  });

  test("what no thread claimed goes in one group, at the end", () => {
    const workspaces = groupByWorkspace([inProject("orchard", "main")]);
    const groups = groupByThread([thread()], workspaces);

    expect(groups).toHaveLength(2);
    expect(groups.at(-1)?.thread).toBeUndefined();
    expect(groups.at(-1)?.workspaces.map((w) => w.address)).toEqual(["orchard.main"]);
  });

  test("nothing is guessed into a thread by name", () => {
    // A thread called "discounts" and a workspace called "discounts" are not
    // related. Only a claim relates them — the same rule identities() follows
    // for an unlabelled session: honestly unknown beats confidently wrong.
    const workspaces = groupByWorkspace([inProject("rowan", "discounts")]);
    const groups = groupByThread([thread({ title: "discounts", members: [] })], workspaces);

    expect(groups[0]?.workspaces).toEqual([]);
    expect(groups.at(-1)?.thread).toBeUndefined();
  });

  test("no loose group at all when everything is claimed", () => {
    // A heading reading "not in a thread" over nothing is a heading that
    // teaches the eye to skip the strip.
    const workspaces = groupByWorkspace([inProject("rowan", "discounts")]);
    const groups = groupByThread(
      [thread({ members: [{ project: "rowan", workspace: "discounts" }] })],
      workspaces,
    );

    expect(groups).toHaveLength(1);
    expect(groups.every((group) => group.thread !== undefined)).toBe(true);
  });

  test("an archived thread is not on the strip", () => {
    const groups = groupByThread([thread({ archivedAt: new Date(3000) })], []);
    expect(groups).toEqual([]);
  });

  test("a session awp did not create is never claimed", () => {
    // A foreign session has no project or workspace to match on, and inventing
    // one would put somebody's unrelated terminal inside a thread.
    const workspaces = groupByWorkspace([session("someone-elses-shell", undefined)]);
    const groups = groupByThread([thread()], workspaces);

    expect(groups.at(-1)?.thread).toBeUndefined();
    expect(groups.at(-1)?.workspaces[0]?.foreign).toBe(true);
  });

  test("newest thread first", () => {
    const groups = groupByThread(
      [
        // Both recent, so the ordering is what is under test rather than the
        // pending window above it.
        thread({ id: "old", title: "old", createdAt: new Date(Date.now() - 60_000) }),
        thread({ id: "new", title: "new", createdAt: new Date() }),
      ],
      [],
    );

    expect(groups.map((group) => group.key)).toEqual(["new", "old"]);
  });
});

// ── the display label ──────────────────────────────────────────────────────
//
// A slug is what a workspace has to be — a directory, a jj workspace and half a
// bookmark. The label is what someone typed, and until `awp_label` existed it
// survived only as a thread's title, so a workspace no thread claimed lost it.

const labelled = (project: string, workspace: string, kind: string, label: string | undefined) =>
  session(`awp.${project}.${workspace}.${kind}`, { project, workspace, kind, label });

describe("the display label", () => {
  it("is taken from whichever session carries one", () => {
    // Only the agent is labelled — the job writes it when it starts that
    // session. An editor opened later beside it must not decide the workspace
    // has no label, which is what reading only the first session would do.
    const got = groupByWorkspace([
      labelled("thicket", "tiered-discounts", "editor", undefined),
      labelled("thicket", "tiered-discounts", "agent", "Tiered discounts"),
    ]);
    expect(got[0]?.label).toBe("Tiered discounts");
  });

  it("is absent when no session carries one", () => {
    // Every workspace made before the label exists looks like this, which is
    // why a row falls back to the slug rather than going blank.
    const got = groupByWorkspace([labelled("thicket", "lantern", "agent", undefined)]);
    expect(got[0]?.label).toBeUndefined();
    expect(got[0]?.name).toBe("lantern");
  });
});

describe("the order workspaces come back in", () => {
  const at = (project: string, workspace: string, when: string | undefined) =>
    session(
      `awp.${project}.${workspace}.agent`,
      { project, workspace, kind: "agent", label: undefined },
      { created: when === undefined ? undefined : new Date(when) },
    );

  it("puts the newest workspace first", () => {
    const got = groupByWorkspace([
      at("thicket", "lantern", "2026-08-01T00:00:00.000Z"),
      at("thicket", "orchard", "2026-08-20T00:00:00.000Z"),
    ]);
    expect(got.map((w) => w.address)).toEqual(["thicket.orchard", "thicket.lantern"]);
  });

  it("ages a workspace by its oldest session, not its newest", () => {
    // Opening an editor beside a week-old agent should not move that workspace
    // to the top of the strip. `since` is the minimum for exactly this.
    const got = groupByWorkspace([
      session(
        "awp.thicket.lantern.agent",
        { project: "thicket", workspace: "lantern", kind: "agent", label: undefined },
        { created: new Date("2026-08-01T00:00:00.000Z") },
      ),
      session(
        "awp.thicket.lantern.editor",
        { project: "thicket", workspace: "lantern", kind: "editor", label: undefined },
        { created: new Date("2026-08-25T00:00:00.000Z") },
      ),
      at("thicket", "orchard", "2026-08-20T00:00:00.000Z"),
    ]);
    expect(got.map((w) => w.address)).toEqual(["thicket.orchard", "thicket.lantern"]);
  });

  it("sorts a workspace zmx gave no time for last, and alphabetically", () => {
    // An unknown age is not a claim to be new. The alphabetical tiebreak is
    // what stops every such workspace sitting in Map iteration order.
    const got = groupByWorkspace([
      at("thicket", "orchard", undefined),
      at("thicket", "lantern", undefined),
      at("thicket", "harbor", "2026-08-20T00:00:00.000Z"),
    ]);
    expect(got.map((w) => w.address)).toEqual([
      "thicket.harbor",
      "thicket.lantern",
      "thicket.orchard",
    ]);
  });
});
