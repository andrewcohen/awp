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
} from "./naming.js";

// These prove properties rather than re-asserting what the Go implementation
// did. The Go code is evidence that a rule was once right, not proof that it
// is — so what is checked here is the behaviour the rest of the system relies
// on, which would still have to hold if the implementation were rewritten.
//
// What CANNOT be proved here is anything that is a claim about zmx itself:
// that 46 is really its limit, that `zmx ls` really marks the caller's own
// session, that it really refuses a slash silently. Those are inherited and
// unverified — see the probe.

// A corpus with the shapes that actually occur, including the pair the Go
// comment names as the reason shortening cannot be plain truncation.
const PROJECTS = ["awp", "thicket", "orchard", "harbor-works", "typed-router"];
const WORKSPACES = [
  "default",
  "awp-kit-amoeba",
  "pr-2336-dev-mlwzqyrmxslo",
  "pr-2336-dev-qqtnvbdlrxzz",
  "effect-ts-tiered-discount-timemachine",
  "pr-2357-lantern-lantern-email-link-identity",
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

  test("replaces a slash — zmx is claimed to refuse a name containing one", () => {
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
    const workspace = "pr-2357-lantern-lantern-email-link-identity";
    const name = sessionName("thicket", workspace, "agent");
    expect(parseSessionName(name)?.workspace).not.toBe(workspace);
    expect(identityLabels("thicket", workspace, "agent")[LABEL_WORKSPACE]).toBe(workspace);
  });
});
