import type { SessionInfo } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import { type Address, addressFrom, addressOf, nowhere, pathOf, sessionAt } from "./address";

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

  it("answers nothing for nowhere, whatever is listed", () => {
    expect(sessionAt(nowhere, listing)).toBeUndefined();
  });
});
