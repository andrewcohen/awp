import type { SessionInfo } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import { shellsOf } from "./shells";

// `zmx ls` lists every session on the machine, so the whole of this function is
// deciding which of them are *this* workspace's shells. Each test below is a
// session that was, at some point, drawn as a tab it should not have been.

const session = (over: Partial<SessionInfo>): SessionInfo => ({
  name: "awp.thicket.lantern.shell_1",
  pid: 4242,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  exitCode: 0,
  created: undefined,
  cmd: "bash",
  labels: {},
  identity: { project: "thicket", workspace: "lantern", kind: "shell_1", label: undefined },
  refusal: undefined,
  ...over,
});

const names = (found: ReturnType<typeof shellsOf>): ReadonlyArray<string> =>
  found.map((one) => one.session.name);

describe("a workspace's shells", () => {
  it("takes the ones whose identity names this workspace", () => {
    const found = shellsOf(
      [
        session({}),
        session({
          name: "awp.orchard.lantern.shell_1",
          identity: {
            project: "orchard",
            workspace: "lantern",
            kind: "shell_1",
            label: undefined,
          },
        }),
      ],
      "thicket",
      "lantern",
    );

    expect(names(found)).toEqual(["awp.thicket.lantern.shell_1"]);
  });

  it("leaves out the agent, which is the session the stage is already drawing", () => {
    const found = shellsOf(
      [
        session({
          name: "awp.thicket.lantern.agent",
          identity: { project: "thicket", workspace: "lantern", kind: "agent", label: undefined },
        }),
      ],
      "thicket",
      "lantern",
    );

    expect(found).toEqual([]);
  });

  // zmx keeps a session listed after its command exits so the output can still
  // be read. A tab for one attaches to a dead process, draws its last screen and
  // takes no keys — which reads as the pane being broken rather than as a shell
  // somebody closed.
  it("leaves out a shell that has ended", () => {
    const found = shellsOf([session({ ended: true })], "thicket", "lantern");
    expect(found).toEqual([]);
  });

  // A session awp did not create has no identity at all, and there are always
  // some: this is a repository developed from inside a zmx session.
  it("leaves out a session that is not awp's", () => {
    const found = shellsOf(
      [session({ name: "someone-elses-work", identity: undefined })],
      "thicket",
      "lantern",
    );
    expect(found).toEqual([]);
  });

  it("orders by number rather than by the order zmx listed them", () => {
    const shell = (n: number): SessionInfo =>
      session({
        name: `awp.thicket.lantern.shell_${String(n)}`,
        identity: {
          project: "thicket",
          workspace: "lantern",
          kind: `shell_${String(n)}`,
          label: undefined,
        },
      });

    const found = shellsOf([shell(10), shell(2), shell(1)], "thicket", "lantern");
    expect(found.map((one) => one.n)).toEqual([1, 2, 10]);
  });

  // The ordinary state of the window before anything is selected. An empty list
  // is the answer; the panel says so itself.
  it("has nothing to show with no workspace open", () => {
    expect(shellsOf([session({})], undefined, undefined)).toEqual([]);
  });
});
