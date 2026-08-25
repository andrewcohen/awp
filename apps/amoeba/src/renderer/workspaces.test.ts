import type { SessionInfo } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import { groupByWorkspace, openable } from "./workspaces";

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
      awp("thicket", "default", "agent"),
      awp("thicket", "default", "editor"),
    ]);
    expect(got).toHaveLength(1);
    expect(got[0]?.label).toBe("thicket.default");
    expect(got[0]?.sessions).toHaveLength(2);
  });

  // The agent is what the workspace is for. Whatever order zmx listed them in,
  // clicking the row must not open the editor.
  it("puts the agent first, whatever order they arrived in", () => {
    const got = groupByWorkspace([
      awp("thicket", "default", "editor"),
      awp("thicket", "default", "agent"),
    ]);
    expect(got[0]?.sessions.map((s) => s.identity?.kind)).toEqual(["agent", "editor"]);
  });

  // A sidebar is read by scanning it. Order that depends on what the daemon
  // happened to return is order that changes between two polls.
  it("orders workspaces by name, not by arrival", () => {
    const got = groupByWorkspace([
      awp("thicket", "default", "agent"),
      awp("orchard", "effect-v4-poc", "agent"),
      awp("mossy", "default", "agent"),
    ]);
    expect(got.map((w) => w.label)).toEqual([
      "orchard.effect-v4-poc",
      "mossy.default",
      "thicket.default",
    ]);
  });

  // `zmx ls` reports every session on the machine. Someone else's is shown —
  // hiding what the daemon can see would be a lie — but it is not a workspace
  // and does not get a project it never had.
  it("keeps a session awp did not create out of the workspaces, and after them", () => {
    const got = groupByWorkspace([
      session("some-other-tool", undefined),
      awp("thicket", "default", "agent"),
    ]);
    expect(got.map((w) => [w.label, w.foreign])).toEqual([
      ["thicket.default", false],
      ["some-other-tool", true],
    ]);
  });

  // Two workspaces of the same name in different projects are two workspaces.
  // The key is the pair, and a label that showed only the workspace would merge
  // them — which is the same class of bug as the one this module fixes.
  it("does not merge the same workspace name across projects", () => {
    const got = groupByWorkspace([
      awp("thicket", "default", "agent"),
      awp("mossy", "default", "agent"),
    ]);
    expect(got).toHaveLength(2);
  });

  it("does not mutate what it was given", () => {
    const input = [awp("thicket", "default", "editor"), awp("thicket", "default", "agent")];
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
