import type { Message } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import { endLabel, endsOf, halfFor } from "./messaging";

// Which half of a pair a row draws, and it is a rule rather than a rendering —
// which is why it is here and not inside the component.

const end = (project: string, workspace: string) => ({ project, workspace });

const said = (from: [string, string], to: [string, string]): Message => ({
  id: `m-${from[0]}-${to[0]}`,
  thread: "th-1",
  from: end(...from),
  to: end(...to),
  body: "try it",
  sentAt: 0,
  notifiedAt: undefined,
  readAt: undefined,
});

describe("halfFor", () => {
  it("names the project when every checkout shares a workspace name", () => {
    // The ordinary thread, and the case this was written for: adding a project
    // to a thread takes the sibling's workspace name, so `Testing Multi` is
    // three rows all called `testing-multi`. Drawn as workspaces, every row
    // read `testing-multi → testing-multi`.
    expect(halfFor(endsOf([said(["grove", "testing-multi"], ["redwood", "testing-multi"])]))).toBe(
      "project",
    );
  });

  it("names the workspace when every checkout is in one project", () => {
    expect(halfFor(endsOf([said(["rowan", "the-api"], ["rowan", "exports-ui"])]))).toBe(
      "workspace",
    );
  });

  it("names the pair when neither half tells them apart on its own", () => {
    expect(halfFor(endsOf([said(["grove", "the-api"], ["redwood", "exports-ui"])]))).toBe("pair");
  });

  it("is decided over the whole group, not per row", () => {
    // Two messages that would each read as `workspace` alone: `rowan → rowan`
    // for the first and `grove → grove` for the second. Together the projects
    // differ, so the group is a `pair` — and a rule applied per row would draw
    // two different kinds of arrow in one list.
    const half = halfFor(
      endsOf([
        said(["rowan", "the-api"], ["rowan", "exports-ui"]),
        said(["grove", "the-api"], ["grove", "exports-ui"]),
      ]),
    );
    expect(half).toBe("pair");
  });

  it("answers something for a group with nothing in it", () => {
    // Not reachable from the panel — a group exists because a message put it
    // there — but a total function has no branch a caller has to avoid.
    expect(halfFor([])).toBe("project");
  });
});

describe("endLabel", () => {
  it("draws the half it is given", () => {
    const one = end("redwood", "testing-multi");
    expect(endLabel(one, "project")).toBe("redwood");
    expect(endLabel(one, "workspace")).toBe("testing-multi");
    // The pair is what the daemon's inbox hands an agent back as the way to
    // reply, so what is on screen is also what somebody could paste.
    expect(endLabel(one, "pair")).toBe("redwood/testing-multi");
  });
});
