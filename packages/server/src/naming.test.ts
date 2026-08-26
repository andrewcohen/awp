import { describe, expect, test } from "vitest";
import {
  LABEL_KIND,
  LABEL_PROJECT,
  LABEL_WORKSPACE,
  MAX_SESSION_NAME,
  identityLabels,
  parseSessionName,
  sanitize,
  sessionKind,
  sessionName,
  sessionStem,
  splitSessionName,
  stemMatches,
} from "./naming";

// These prove properties rather than re-asserting what the Go implementation
// did. The Go code is evidence that a rule was once right, not proof that it
// is — so what is checked here is the behaviour the rest of the system relies
// on, which would still have to hold if the implementation were rewritten.
//
// What CANNOT be proved here is anything that is a claim about zmx itself.
// Those went to `bun run probe:claims`, which measured them on 2026-08-25: the
// 46-character limit holds (47 is refused, and zmx names its own max), and a
// name containing a slash is refused *silently* — no error, no session, nothing
// to notice. Only the arrow marking the caller's own session is still untested,
// because observing it means running inside a session and the probe refuses to.

// A corpus with the shapes that actually occur, including the pair the Go
// comment names as the reason shortening cannot be plain truncation.
const PROJECTS = ["awp", "thicket", "orchard", "harbor-works", "typed-router"];
const WORKSPACES = [
  "default",
  "awp-kit-amoeba",
  "pr-2336-dev-mlwzqyrmxslo",
  "pr-2336-dev-qqtnvbdlrxzz",
  "effect-ts-tiered-discount-timemachine",
  "pr-2357-lantern-email-link-identity",
];
const KINDS = ["agent", "captain", "action_dev", "action_verylongactionname"];

const everyCombination = PROJECTS.flatMap((project) =>
  WORKSPACES.flatMap((workspace) => KINDS.map((kind) => ({ project, workspace, kind }))),
);

describe("sanitize", () => {
  test("emits only characters that survive a session name", () => {
    for (const input of ["a b", "a/b", "a.b", "héllo", "🙂", "UPPER_case-9"]) {
      expect(sanitize(input)).toMatch(/^[A-Za-z0-9_-]+$/u);
    }
  });

  test("never returns empty, so a segment always exists", () => {
    // An empty segment would collapse `awp..agent` into something that cannot
    // be split back apart.
    expect(sanitize("")).not.toBe("");
    expect(sanitize("🙂")).not.toBe("");
  });

  test("replaces a dot, which is what makes splitting a name safe", () => {
    expect(sanitize("a.b")).not.toContain(".");
  });

  test("replaces a slash — zmx refuses such a name, and says nothing", () => {
    // Measured: no error, no session, no exit code to notice. A workspace named
    // after a branch would silently never get one.
    expect(sanitize("feature/thing")).not.toContain("/");
  });
});

describe("sessionName", () => {
  test("never exceeds the budget", () => {
    for (const { project, workspace, kind } of everyCombination) {
      expect(sessionName(project, workspace, kind).length).toBeLessThanOrEqual(MAX_SESSION_NAME);
    }
  });

  test("is deterministic — a name is an address", () => {
    for (const { project, workspace, kind } of everyCombination) {
      expect(sessionName(project, workspace, kind)).toBe(sessionName(project, workspace, kind));
    }
  });

  test("distinct inputs never collide", () => {
    // The property plain truncation fails: two workspaces named after the same
    // PR share every character the budget has room for.
    const names = new Set(everyCombination.map((c) => sessionName(c.project, c.workspace, c.kind)));
    expect(names.size).toBe(everyCombination.length);
  });

  test("the pair that motivated the fingerprint stays distinct once shortened", () => {
    // A longer project than the Go comment's example, so both names genuinely
    // exceed the budget. With "alpha" they do not: that name is 45, not the 47
    // the comment claims, so nothing is shortened and the test would pass
    // without exercising the fingerprint at all.
    const workspaceA = "pr-2336-dev-mlwzqyrmxslo";
    const workspaceB = "pr-2336-dev-qqtnvbdlrxzz";
    const a = sessionName("harbor-works", workspaceA, "action_dev");
    const b = sessionName("harbor-works", workspaceB, "action_dev");

    // Prove the premise before the conclusion: both were actually shortened.
    expect(splitSessionName(a)?.stem).not.toBe(sessionStem("harbor-works", workspaceA));
    expect(splitSessionName(b)?.stem).not.toBe(sessionStem("harbor-works", workspaceB));

    // Plain truncation would collapse these two onto one session, and one
    // workspace would open the other's agent.
    expect(a).not.toBe(b);
  });

  test("keeps the kind intact so a pane can resolve its action", () => {
    for (const { project, workspace, kind } of everyCombination) {
      const split = splitSessionName(sessionName(project, workspace, kind));
      expect(split?.kind).toBe(sessionKind(kind));
    }
  });
});

describe("compatibility with sessions that already exist", () => {
  // Every one of these was read off `zmx ls` on 2026-08-25 and every one had
  // been shortened, so each exercises the sha256 fingerprint and the budget
  // arithmetic together.
  //
  // This is the test that matters most in the file. A name is an address: if
  // the port disagreed with the Go implementation by a single character, every
  // shortened session already running would stop being found and awp would
  // start a second of each.
  //
  // These were real names once, read off a live `zmx ls`, and they are not any
  // more — see the naming rule in AGENTS.md. What that costs is worth being
  // honest about: the expectations below were computed by the very code they
  // check, so they no longer prove agreement with a hash some other program
  // wrote months ago. What they still do is pin it. Any future change to the
  // shortening breaks all ten at once, which is the property that matters,
  // because a name is an address and one character of disagreement leaves
  // every shortened session unfindable.
  //
  // The `thicket` pair is the interesting one: the same workspace with two
  // kinds produces the same fingerprint at two different keep-lengths, which is
  // the budget split doing its job.
  const observed: ReadonlyArray<readonly [string, string, string, string]> = [
    [
      "harbor",
      "typed-router-endpoint-proof-of-concept",
      "agent",
      "awp.harbor.typed-router-endpoint-pr-9f6c.agent",
    ],
    [
      "orchard",
      "pr-557-lantern-header-allowlist",
      "agent",
      "awp.orchard.pr-557-lantern-header-a-8f11.agent",
    ],
    [
      "orchard",
      "pr-558-lantern-identity-hasher",
      "agent",
      "awp.orchard.pr-558-lantern-identity-5992.agent",
    ],
    [
      "thicket",
      "effect-tiered-discount-timemachine",
      "action_dev",
      "awp.thicket.effect-tiered-disc-f488.action_dev",
    ],
    [
      "thicket",
      "effect-tiered-discount-timemachine",
      "agent",
      "awp.thicket.effect-tiered-discount-f488.agent",
    ],
    [
      "thicket",
      "pr-2320-survey-slot-collapse-metrics",
      "agent",
      "awp.thicket.pr-2320-survey-slot-col-84b1.agent",
    ],
    [
      "thicket",
      "pr-2340-lantern-header-allowlist",
      "agent",
      "awp.thicket.pr-2340-lantern-header-c1d2.agent",
    ],
    [
      "thicket",
      "pr-2357-lantern-email-link-identity",
      "agent",
      "awp.thicket.pr-2357-lantern-email-l-1bba.agent",
    ],
    [
      "thicket",
      "pr-2359-lantern-identity-resolve",
      "agent",
      "awp.thicket.pr-2359-lantern-identit-20e3.agent",
    ],
    [
      "thicket",
      "quick-2nd-pick-v2-rollback-ship",
      "agent",
      "awp.thicket.quick-2nd-pick-v2-rollb-15a0.agent",
    ],
  ];

  test.each(observed)("%s / %s / %s → %s", (project, workspace, kind, expected) => {
    expect(sessionName(project, workspace, kind)).toBe(expected);
  });

  test("each one really was shortened, or the fingerprint is untested", () => {
    for (const [project, workspace, kind] of observed) {
      const stem = splitSessionName(sessionName(project, workspace, kind))?.stem;
      expect(stem).not.toBe(sessionStem(project, workspace));
    }
  });
});

describe("stemMatches", () => {
  test("recognises the stem of a name it generated, shortened or not", () => {
    for (const { project, workspace, kind } of everyCombination) {
      const split = splitSessionName(sessionName(project, workspace, kind));
      expect(split).toBeDefined();
      expect(stemMatches(project, workspace, split?.stem ?? "")).toBe(true);
    }
  });

  test("does not recognise another workspace's stem", () => {
    const mine = splitSessionName(sessionName("alpha", "pr-2336-dev-mlwzqyrmxslo", "agent"));
    expect(stemMatches("alpha", "pr-2336-dev-qqtnvbdlrxzz", mine?.stem ?? "")).toBe(false);
  });
});

describe("splitSessionName", () => {
  test("rejects a name that is not ours", () => {
    expect(splitSessionName("some.other.session")).toBeUndefined();
    expect(splitSessionName("awp")).toBeUndefined();
    expect(splitSessionName("")).toBeUndefined();
  });
});

describe("parseSessionName", () => {
  test("round-trips a name short enough not to be shortened", () => {
    expect(parseSessionName(sessionName("awp", "default", "agent"))).toEqual({
      project: "awp",
      workspace: "default",
      kind: "agent",
    });
  });

  test("rejects a session awp did not create — zmx lists every session", () => {
    expect(parseSessionName("mysession")).toBeUndefined();
    expect(parseSessionName("a.b.c.d")).toBeUndefined();
    expect(parseSessionName("awp.only.three")).toBeUndefined();
  });

  test("is lossy where the docs say it is, which is why labels exist", () => {
    // A workspace whose real name held a dot comes back with an underscore.
    const parsed = parseSessionName(sessionName("awp", "has.dot", "agent"));
    expect(parsed?.workspace).toBe("has_dot");
    expect(parsed?.workspace).not.toBe("has.dot");
  });
});

describe("identityLabels", () => {
  test("keeps the real name, unsanitized — a label is data, not an address", () => {
    expect(identityLabels("awp", "has.dot", "agent")).toEqual({
      [LABEL_PROJECT]: "awp",
      [LABEL_WORKSPACE]: "has.dot",
      [LABEL_KIND]: "agent",
    });
  });

  test("carries what a shortened name loses", () => {
    const workspace = "pr-2357-lantern-email-link-identity";
    const name = sessionName("thicket", workspace, "agent");
    expect(parseSessionName(name)?.workspace).not.toBe(workspace);
    expect(identityLabels("thicket", workspace, "agent")[LABEL_WORKSPACE]).toBe(workspace);
  });
});
