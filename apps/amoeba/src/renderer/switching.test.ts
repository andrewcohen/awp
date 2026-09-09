import type { Thread } from "@awp-kit/protocol";
import { describe, expect, test } from "vitest";
import { KEPT, filtered, ordered, visitedWith } from "./switching";

// The one property this has to have: cmd+P then Return goes back to where you
// were. Everything else about the ordering follows from it.

const thread = (id: string, title: string, projects: ReadonlyArray<string> = ["thicket"]) =>
  ({
    id,
    title,
    createdAt: new Date(0),
    archivedAt: undefined,
    parentId: undefined,
    members: projects.map((project) => ({ project, workspace: `w-${id}` })),
    prs: [],
  }) as unknown as Thread;

describe("the order", () => {
  const threads = [
    thread("a", "tabular exports"),
    thread("b", "the flaky login test"),
    thread("c", "paginate the inbox"),
  ];

  test("the previous thread is first, and the current one is not", () => {
    // The gesture being paid for. `a` is where the window is now.
    expect(ordered(threads, ["a", "b"])[0]?.id).toBe("b");
    expect(ordered(threads, ["a", "b"]).at(-1)?.id).toBe("a");
  });

  test("the current thread is kept, and is last of everything", () => {
    // Not removed: going to where you already are is a thing somebody may
    // choose on purpose, and a missing row reads as a bug in the list. Last,
    // because it is the one thread nobody needs a switcher to reach.
    expect(ordered(threads, ["a", "b"]).map((row) => row.id)).toEqual(["b", "c", "a"]);
    expect(ordered(threads, ["a", "c"]).map((row) => row.id)).toEqual(["c", "b", "a"]);
  });

  test("nothing visited leaves the daemon's own order", () => {
    expect(ordered(threads, []).map((row) => row.id)).toEqual(["a", "b", "c"]);
    expect(ordered(threads, []).every((row) => !row.visited)).toBe(true);
  });

  test("an id that no longer names a thread is ignored, not fatal", () => {
    // Archived in another window, or from a previous version of the app.
    expect(ordered(threads, ["gone", "b"]).map((row) => row.id)).toEqual(["b", "a", "c"]);
  });

  test("only one visit means there is nowhere to flip back to", () => {
    // The honest answer is the list, with the current thread demoted — not the
    // current thread under the cursor, which would make Return a no-op that
    // looks like a broken shortcut.
    expect(ordered(threads, ["a"]).map((row) => row.id)).toEqual(["b", "c", "a"]);
  });
});

describe("filtering", () => {
  const rows = ordered([thread("a", "tabular exports", ["thicket", "orchard"])], []);

  test("a substring of the title, case insensitively", () => {
    expect(filtered(rows, "EXPORT")).toHaveLength(1);
    expect(filtered(rows, "nothing here")).toHaveLength(0);
  });

  test("the projects it holds, because two titles can read alike", () => {
    expect(filtered(rows, "orchard")).toHaveLength(1);
  });

  test("an empty query is every row, not none", () => {
    expect(filtered(rows, "   ")).toEqual(rows);
  });
});

describe("the history", () => {
  test("a visit goes to the front and does not repeat", () => {
    expect(visitedWith(["b", "c"], "c")).toEqual(["c", "b"]);
  });

  test("it is capped", () => {
    const many = Array.from({ length: 60 }, (_, at) => `t${String(at)}`);
    expect(visitedWith(many, "new")).toHaveLength(KEPT);
  });
});
