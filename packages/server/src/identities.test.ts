import { describe, expect, it } from "vitest";
import { type Session, identities, identity } from "./multiplexer";
import { LABEL_KIND, LABEL_PROJECT, LABEL_WORKSPACE, sessionName } from "./naming";

// One workspace's sessions do not share a stem, and that is the whole subject
// of this file.
//
// `sessionName` gives the stem whatever budget the kind does not need, so a
// long workspace produces a differently shortened stem per kind. Read one name
// at a time they are different workspaces — which is what the sidebar showed
// until 2026-08-25. The names below are generated rather than typed out, so the
// test cannot go on passing if the shortening changes.

const session = (
  name: string,
  labels: Readonly<Record<string, string>> = {},
  startDir = "/tmp",
): Session => ({
  name,
  pid: 1,
  clients: 0,
  startDir,
  ended: false,
  busy: true,
  taskEnded: false,
  exitCode: 0,
  created: undefined,
  cmd: "claude",
  labels,
});

const PROJECT = "thicket";
const WORKSPACE = "effect-ts-tiered-discount-timemachine";

const named = (kind: string, labelled = false) =>
  session(
    sessionName(PROJECT, WORKSPACE, kind),
    labelled ? { [LABEL_PROJECT]: PROJECT, [LABEL_WORKSPACE]: WORKSPACE, [LABEL_KIND]: kind } : {},
  );

describe("a long workspace's names", () => {
  // The premise. If this ever stops holding the repair below is unnecessary,
  // and a test that silently became vacuous is worse than one that fails.
  it("differ by kind, because the kind is what the stem is shortened against", () => {
    const stems = ["agent", "editor", "action_dev"].map((kind) => {
      const name = sessionName(PROJECT, WORKSPACE, kind);
      return name.slice(0, name.lastIndexOf("."));
    });
    expect(new Set(stems).size).toBe(3);
  });

  it("cannot be grouped by reading one of them", () => {
    const found = ["agent", "editor"].map((kind) => identity(named(kind))?.workspace);
    expect(found[0]).not.toBe(found[1]);
    expect(found[0]).not.toBe(WORKSPACE);
  });
});

describe("repairing a listing against its labelled sessions", () => {
  it("recovers every sibling from one labelled session", () => {
    const all = [named("agent", true), named("editor"), named("action_dev")];
    const found = identities(all);
    for (const s of all) {
      expect(found.get(s.name)?.workspace).toBe(WORKSPACE);
      expect(found.get(s.name)?.project).toBe(PROJECT);
    }
    expect(new Set(all.map((s) => found.get(s.name)?.kind))).toEqual(
      new Set(["agent", "editor", "action_dev"]),
    );
  });

  // Not repairable, and it must not be guessed at. A workspace with no labelled
  // session anywhere in the listing keeps the shortened name off its own name —
  // wrong-looking, but honestly wrong rather than invented.
  it("leaves a workspace alone when none of its sessions is labelled", () => {
    const all = [named("agent"), named("editor")];
    const found = identities(all);
    expect(found.get(all[0]!.name)?.workspace).not.toBe(WORKSPACE);
    expect(found.get(all[1]!.name)?.workspace).not.toBe(found.get(all[0]!.name)?.workspace);
  });

  // stemMatches is asked per workspace precisely so that a near-miss cannot
  // borrow an identity it has no claim to.
  it("does not lend an identity to another workspace", () => {
    const other = session(sessionName(PROJECT, "something-else-entirely", "agent"));
    const found = identities([named("agent", true), other]);
    expect(found.get(other.name)?.workspace).toBe("something-else-entirely");
  });

  it("leaves a session awp did not create without one", () => {
    const found = identities([named("agent", true), session("some-other-tool")]);
    expect(found.get("some-other-tool")).toBeUndefined();
  });

  // A short workspace is not shortened at all, so the name is already exact and
  // the repair has nothing to do. Worth pinning: most workspaces are this.
  it("needs no repair for a name that fits", () => {
    const all = [session("awp.dotfiles.default.agent"), session("awp.dotfiles.default.editor")];
    const found = identities(all);
    expect(all.map((s) => found.get(s.name)?.workspace)).toEqual(["default", "default"]);
  });
});

describe("repairing against the working directory", () => {
  // The last resort, and the reason it is safe: a path is put to stemMatches
  // like any other candidate, so it is believed only when it reproduces the
  // name that was actually observed. These four rows on this machine had no
  // labelled session anywhere in the listing and were showing their shortened
  // names — orchard.pr-557-lantern-header-head-bc47 and friends.
  it("recovers a name from ~/.awp/workspaces/<project>/<workspace>", () => {
    const name = sessionName("orchard", "pr-557-lantern-header-head-refactor", "agent");
    const found = identities([
      session(
        name,
        {},
        "/Users/someone/.awp/workspaces/orchard/pr-557-lantern-header-head-refactor",
      ),
    ]);
    expect(found.get(name)?.workspace).toBe("pr-557-lantern-header-head-refactor");
  });

  // A default workspace is the repository itself, so its directory names only
  // the project.
  it("reads a default workspace off the repository directory", () => {
    const name = sessionName("dotfiles", "default", "agent");
    const found = identities([session(name, {}, "/Users/someone/dotfiles")]);
    expect(found.get(name)?.project).toBe("dotfiles");
    expect(found.get(name)?.workspace).toBe("default");
  });

  // The property that makes guessing acceptable. A directory that does not
  // reproduce the observed name contributes nothing, and the session keeps the
  // shortened name it came with.
  it("ignores a directory that does not reproduce the name", () => {
    const name = sessionName("orchard", "the-real-one", "agent");
    const found = identities([
      session(name, {}, "/Users/someone/.awp/workspaces/orchard/something-else"),
    ]);
    expect(found.get(name)?.workspace).not.toBe("something-else");
  });
});
