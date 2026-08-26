import type { SessionInfo, Thread } from "@awp-kit/protocol";
import { describe, expect, it, test } from "vitest";
import {
  branchable,
  groupByThread,
  groupByWorkspace,
  openable,
  projectsOf,
  threadOf,
  titleOf,
} from "./workspaces";

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
  session(`awp.${project}.${workspace}.${kind}`, { project, workspace, kind }, extra);

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
    expect(got[0]?.label).toBe("rowan.default");
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
    expect(got.map((w) => w.label)).toEqual([
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
    expect(got.map((w) => [w.label, w.foreign])).toEqual([
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
    expect(got.map((w) => w.label)).toEqual(["mossy.default", "rowan.default"]);
  });
});

// ── threads ────────────────────────────────────────────────────────────────

const thread = (over: Partial<Thread> = {}): Thread => ({
  id: "t1",
  title: "tiered discounts",
  createdAt: new Date(2000),
  archivedAt: undefined,
  parentId: undefined,
  members: [],
  ...over,
});

const inProject = (project: string, workspace: string): SessionInfo =>
  session(`awp.${project}.${workspace}.agent`, { project, workspace, kind: "agent" });

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
    expect(groups[0]?.workspaces.map((w) => w.label)).toEqual([
      "beta.discounts",
      "rowan.discounts",
    ]);
  });

  test("a thread with nothing in it still shows", () => {
    // The most important row on the strip, not the least: it is the one
    // waiting to be filled. Dropping empty threads would make a thread made a
    // moment ago invisible until it had a workspace.
    const groups = groupByThread([thread()], []);

    expect(groups).toHaveLength(1);
    expect(groups[0]?.workspaces).toEqual([]);
  });

  test("what no thread claimed goes in one group, at the end", () => {
    const workspaces = groupByWorkspace([inProject("orchard", "main")]);
    const groups = groupByThread([thread()], workspaces);

    expect(groups).toHaveLength(2);
    expect(groups.at(-1)?.thread).toBeUndefined();
    expect(groups.at(-1)?.workspaces.map((w) => w.label)).toEqual(["orchard.main"]);
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
        thread({ id: "old", title: "old", createdAt: new Date(1000) }),
        thread({ id: "new", title: "new", createdAt: new Date(9000) }),
      ],
      [],
    );

    expect(groups.map((group) => group.key)).toEqual(["new", "old"]);
  });
});

// ── what the new-thread modal reads ────────────────────────────────────────

describe("the projects a window knows", () => {
  it("names each one once, with a directory to resolve it from", () => {
    expect(
      projectsOf([
        awp("thicket", "default", "agent", { startDir: "/w/thicket" }),
        awp("thicket", "lantern", "agent", { startDir: "/w/thicket/lantern" }),
        awp("orchard", "default", "agent", { startDir: "/w/orchard" }),
      ]),
    ).toEqual([
      { name: "orchard", from: "/w/orchard" },
      { name: "thicket", from: "/w/thicket" },
    ]);
  });

  // A session someone else started has no identity, so it names no project.
  // Listing it as one would offer a repository awp cannot resolve.
  it("ignores a session awp did not create", () => {
    expect(projectsOf([session("zsh", undefined, { startDir: "/w/elsewhere" })])).toEqual([]);
  });

  // The directory is the whole point of the entry: without one the daemon has
  // nothing to turn into a repository root, so the project would be an option
  // that cannot work. A sibling with a directory rescues it.
  it("drops a project whose sessions have no directory, and keeps one a sibling saves", () => {
    expect(projectsOf([awp("thicket", "default", "agent", { startDir: "" })])).toEqual([]);
    expect(
      projectsOf([
        awp("thicket", "default", "agent", { startDir: "" }),
        awp("thicket", "lantern", "agent", { startDir: "/w/thicket/lantern" }),
      ]),
    ).toEqual([{ name: "thicket", from: "/w/thicket/lantern" }]);
  });
});

describe("the thread a workspace belongs to", () => {
  // What cmd+shift+N branches from. A thread, deliberately: the obvious answer
  // was `<name>@`, the workspace's working-copy commit, which carries whatever
  // is uncommitted in it right now. Only the daemon can turn a thread into the
  // bookmark, because the prefix is in its config.
  const claimed = thread({
    id: "t9",
    members: [{ project: "thicket", workspace: "lantern" }],
  });

  it("is the thread that claimed it", () => {
    expect(threadOf([claimed], awp("thicket", "lantern", "agent"))).toBe("t9");
  });

  it("is nothing when no thread has claimed it", () => {
    expect(threadOf([claimed], awp("thicket", "orchard", "agent"))).toBeUndefined();
    expect(threadOf([claimed], session("zsh", undefined))).toBeUndefined();
    expect(threadOf([claimed], undefined)).toBeUndefined();
  });

  // An archived thread is a record rather than a place to work, so branching
  // from it would put new work under something already finished.
  it("ignores an archived thread", () => {
    const done = thread({ ...claimed, archivedAt: new Date(1) });
    expect(threadOf([done], awp("thicket", "lantern", "agent"))).toBeUndefined();
  });
});

describe("the threads a new one can branch from", () => {
  const here = thread({
    id: "t1",
    title: "in this project",
    members: [{ project: "thicket", workspace: "lantern" }],
  });
  const elsewhere = thread({
    id: "t2",
    title: "somewhere else",
    members: [{ project: "orchard", workspace: "harbor" }],
  });
  // A thread made a moment ago. It has a title and no work behind it, so there
  // is no bookmark to branch from — offering it would be offering a base that
  // does not exist.
  const fresh = thread({ id: "t3", title: "nothing in it", members: [] });

  it("offers only the ones with a workspace in this project", () => {
    expect(branchable([here, elsewhere, fresh], "thicket").map((entry) => entry.id)).toEqual([
      "t1",
    ]);
    expect(branchable([here, elsewhere, fresh], "orchard").map((entry) => entry.id)).toEqual([
      "t2",
    ]);
  });

  // The daemon refuses a cross-project parent outright — a revision is only
  // meaningful inside one repository. This is the same rule stated where a
  // person can see it, so the refusal is unreachable from the window rather
  // than merely survivable.
  it("offers nothing in a project with no threads", () => {
    expect(branchable([here, elsewhere, fresh], "lantern")).toEqual([]);
  });

  it("drops an archived thread", () => {
    expect(branchable([{ ...here, archivedAt: new Date(1) }], "thicket")).toEqual([]);
  });

  it("names a thread for the chip, and says untitled rather than nothing", () => {
    expect(titleOf([here], "t1")).toBe("in this project");
    expect(titleOf([thread({ id: "t4", title: "" })], "t4")).toBe("untitled");
    expect(titleOf([here], "nope")).toBeUndefined();
  });
});
