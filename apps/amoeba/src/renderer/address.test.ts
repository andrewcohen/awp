import type { SessionInfo, Thread } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import {
  type Address,
  addressFrom,
  addressOf,
  nowhere,
  pathOf,
  placeAt,
  sessionAt,
} from "./address";

// An address is what selection *is* now, so these are the tests that used to be
// impossible: the old selection was a session name compared for equality, and
// there was nothing to assert about it beyond that.
//
// The property worth pinning is the round trip. A name goes out to the router
// as a path and comes back as loose params, and the two halves are written in
// different files — `pathOf` composes and the route patterns parse. A test that
// only checked one of them would pass while they disagreed.

const session = (
  name: string,
  identity?: { readonly project: string; readonly workspace: string; readonly kind: string },
  refusal?: string,
): SessionInfo =>
  ({
    name,
    identity,
    refusal,
    ended: false,
    cmd: "",
    startDir: "/tmp",
  }) as unknown as SessionInfo;

const ours = session("awp.thicket.lantern-f500.agent", {
  project: "thicket",
  workspace: "lantern",
  kind: "agent",
});

describe("addressOf", () => {
  it("takes the unshortened identity, not the session name", () => {
    // The whole argument for routing. The name has been shortened to fit a
    // socket path and cannot be split back into these three fields.
    expect(addressOf(ours)).toStrictEqual({
      at: "workspace",
      project: "thicket",
      workspace: "lantern",
      kind: "agent",
    });
  });

  it("falls back to the name for a session awp did not create", () => {
    expect(addressOf(session("someone-elses-thing"))).toStrictEqual({
      at: "session",
      name: "someone-elses-thing",
    });
  });
});

// The parse side is the route patterns, which only exist inside the router.
// Splitting the path here is a second implementation of them — and a
// deliberately dumb one, so that a change to the patterns this does not follow
// shows up as a failure rather than as a route nothing matches.
const params = (path: string): Record<string, string | undefined> => {
  const parts = path.split("/").slice(1).map(decodeURIComponent);
  if (parts[0] === "s" && parts[1] !== undefined) {
    return { name: parts[1] };
  }
  if (parts[0] === "w") {
    return { project: parts[1], workspace: parts[2], kind: parts[3] };
  }
  return {};
};

const roundTrip = (address: Address) => addressFrom(params(pathOf(address)));

describe("pathOf and addressFrom", () => {
  it("survives the round trip for all three shapes", () => {
    const all: ReadonlyArray<Address> = [
      nowhere,
      { at: "workspace", project: "thicket", workspace: "lantern", kind: "agent" },
      { at: "session", name: "someone-elses-thing" },
    ];
    for (const address of all) {
      expect(roundTrip(address)).toStrictEqual(address);
    }
  });

  it("survives a name a path would otherwise take apart", () => {
    // Not expected from `sanitize`, which is exactly why it is asserted: the
    // address is the one place a daemon-supplied name becomes structure again,
    // and an unescaped slash would resolve to a different route rather than to
    // none.
    const odd: Address = {
      at: "workspace",
      project: "harbor-works",
      workspace: "pr-2340/header allowlist",
      kind: "agent",
    };
    // Four segments after the leading slash, whatever is in the names. A raw
    // slash would make five, and the fifth would be read as the kind.
    expect(pathOf(odd).split("/")).toHaveLength(5);
    expect(roundTrip(odd)).toStrictEqual(odd);
  });

  it("is nowhere when the params name nothing", () => {
    expect(addressFrom({})).toStrictEqual(nowhere);
    // Two of the three is not a workspace. Half an address is not half open.
    expect(addressFrom({ project: "thicket", workspace: "lantern" })).toStrictEqual(nowhere);
  });
});

describe("sessionAt", () => {
  const listing = [
    ours,
    session("awp.thicket.orchard-f500.agent", {
      project: "thicket",
      workspace: "orchard",
      kind: "agent",
    }),
  ];

  it("finds a session by its identity and not by its name", () => {
    expect(sessionAt(addressOf(ours), listing)).toBe(ours);
  });

  it("answers nothing when the address names a session that has gone", () => {
    const gone: Address = {
      at: "workspace",
      project: "thicket",
      workspace: "vanished",
      kind: "agent",
    };
    expect(sessionAt(gone, listing)).toBeUndefined();
  });

  it("answers nothing for a session the daemon refuses", () => {
    // Present in the listing and not openable — the session the daemon is
    // itself running in. Both halves matter, and a check of only presence
    // would attach to it.
    const refused = session(
      "awp.thicket.lantern-f500.agent",
      { project: "thicket", workspace: "lantern", kind: "agent" },
      "that is the session this daemon is running in",
    );
    expect(sessionAt(addressOf(refused), [refused])).toBeUndefined();
  });

  // Found in the wild, on the very workspace #122 was reported about. zmx
  // keeps an ended session in `zmx ls`, so the check on presence and refusal
  // answered with one, and the pane attached to a process that had exited:
  //
  //   name=awp.awp.test.agent  ended=1788891181  exit_code=127
  //
  // A blank terminal is also what a terminal that failed to draw looks like,
  // which is why this is a test rather than a note. `placeAt` still resolves
  // the workspace, so answering nothing here is what puts the start control
  // on screen instead of a dead pane.
  it("answers nothing for a session whose process has exited", () => {
    const over = {
      ...session("awp.awp.test.agent", { project: "awp", workspace: "test", kind: "agent" }),
      ended: true,
    } as SessionInfo;
    expect(sessionAt(addressOf(over), [over])).toBeUndefined();
    expect(placeAt(addressOf(over), [over], [])?.workspace).toBe("test");
  });

  it("answers nothing for nowhere, whatever is listed", () => {
    expect(sessionAt(nowhere, listing)).toBeUndefined();
  });
});

// ── #122: the address is a workspace, and `sessionAt` is the narrow question ──
//
// Reported as "i cant get into the chat" for a thread whose zmx session had
// been killed. `sessionAt` answering undefined is right for the pane and was
// wrong for every panel beside it, all of which are questions about a
// checkout rather than about a terminal.
const held = (project: string, workspace: string, over: Partial<Thread> = {}): Thread =>
  ({
    id: "t1",
    title: "tabular exports",
    createdAt: new Date(0),
    archivedAt: undefined,
    parentId: undefined,
    members: [{ project, workspace }],
    prs: [],
    ...over,
  }) as unknown as Thread;

describe("placeAt", () => {
  const at: Address = { at: "workspace", project: "thicket", workspace: "lantern", kind: "agent" };

  it("resolves a workspace a live thread holds, with nothing running in it", () => {
    expect(placeAt(at, [], [held("thicket", "lantern")])).toEqual({
      project: "thicket",
      workspace: "lantern",
      kind: "agent",
    });
    // And the pane's question still answers no, which is the honest half:
    // there is genuinely no terminal to attach to.
    expect(sessionAt(at, [])).toBeUndefined();
  });

  it("resolves a workspace a session is in, thread or no thread", () => {
    expect(placeAt(at, [ours], [])?.workspace).toBe("lantern");
  });

  it("resolves a workspace whose only session the daemon refuses", () => {
    // The daemon's own session must not be *attached* to — and its workspace
    // is still a workspace. A single gate for both questions would have made
    // the chat unreachable for exactly one row, which is worse than either
    // answer, because nothing on screen would say why.
    const refused = session(
      "awp.thicket.lantern-f500.agent",
      { project: "thicket", workspace: "lantern", kind: "agent" },
      "that is the session this daemon is running in",
    );
    expect(placeAt(at, [refused], [])?.workspace).toBe("lantern");
    expect(sessionAt(at, [refused])).toBeUndefined();
  });

  it("answers nothing for a pair neither source has heard of", () => {
    // A remembered address survives a quit, and the workspace it named may
    // not. Opening panels onto a directory nothing knows about would report
    // a broken repository where the answer is "that is gone".
    expect(placeAt(at, [], [held("thicket", "orchard")])).toBeUndefined();
  });

  it("answers nothing for a workspace only an archived thread holds", () => {
    expect(
      placeAt(at, [], [held("thicket", "lantern", { archivedAt: new Date(3000) })]),
    ).toBeUndefined();
  });

  it("answers nothing for someone else's session", () => {
    // `/s/<name>` is not a workspace. It has no identity, so there is nothing
    // to hold a conversation in and the pane is the only honest face for it.
    expect(placeAt({ at: "session", name: "someone-elses-shell" }, [], [])).toBeUndefined();
    expect(placeAt(nowhere, [], [])).toBeUndefined();
  });
});
