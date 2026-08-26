import type { SessionInfo, Thread } from "@awp-kit/protocol";
import { describe, expect, it, test } from "vitest";
import { groupByThread, groupByWorkspace, openable } from "./workspaces";

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
