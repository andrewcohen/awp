import { afterAll, describe, expect, it } from "vitest";
import { mkdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import { candidates, parseTodo, readTodo, subjectOf } from "./todo-tasks";

// The parse rule, and the one thing it has to get right: an unnumbered `##`
// belongs to the task above it. This repository's own file has four of them —
// three under #91 — and a rule that took every heading would have reported
// them as tasks with no bodies.

describe("subjectOf", () => {
  it("reads a status marker off the end of a heading", () => {
    expect(subjectOf("Run the agent under ACP, not only in a terminal · in progress")).toEqual({
      subject: "Run the agent under ACP, not only in a terminal",
      status: "in_progress",
    });
  });

  it("keeps a separator that is part of the title", () => {
    // Losing a word off a title is worse than an unread marker, so anything
    // after the separator that is not recognised leaves the subject whole.
    expect(subjectOf("Own the agent's terminals · so a long command is watchable")).toEqual({
      subject: "Own the agent's terminals · so a long command is watchable",
      status: "pending",
    });
  });

  it("is pending when the heading says nothing", () => {
    expect(subjectOf("Rename a thread from the header").status).toBe("pending");
  });
});

describe("parseTodo", () => {
  const file = [
    "# TODO",
    "",
    "46 open, 75 finished.",
    "",
    "---",
    "",
    "## 91. Run the agent under ACP · in progress",
    "",
    "A terminal is a picture of a conversation.",
    "",
    "## The spike, 2026-08-28",
    "",
    "Four throwaway sessions.",
    "",
    "## 113. Dragging a divider near the top moves the window",
    "",
    "The cause is almost certainly the drag region.",
    "",
  ].join("\n");

  it("takes a numbered heading and not an unnumbered one", () => {
    expect(parseTodo(file).map((task) => task.number)).toEqual([91, 113]);
  });

  it("keeps a sub-heading in the body of the task above it", () => {
    const [first] = parseTodo(file);
    expect(first?.description).toContain("## The spike, 2026-08-28");
    expect(first?.description).toContain("Four throwaway sessions.");
  });

  it("carries the status off the heading", () => {
    expect(parseTodo(file)[0]?.status).toBe("in_progress");
    expect(parseTodo(file)[1]?.status).toBe("pending");
  });

  it("drops the preamble above the first task", () => {
    // It is the file's note to its own reader — how many are open, what was
    // hand-edited — and not a task.
    expect(parseTodo(file).some((task) => task.description.includes("46 open"))).toBe(false);
  });

  it("yields nothing for a file with no numbered heading", () => {
    // Rather than one task made of the whole document, which is what a parser
    // that fell back to the preamble would produce.
    expect(parseTodo("# Notes\n\nsome prose\n\n## Landed\n\nmore prose")).toEqual([]);
  });

  it("reads this repository's own file", async () => {
    // The corpus that matters. A rule that passes on a fixture and misses on
    // the real file is the failure this whole module is one guess away from.
    const { readFile } = await import("node:fs/promises");
    const tasks = parseTodo(await readFile("TODO.md", "utf8"));
    expect(tasks.length).toBeGreaterThan(40);
    expect(tasks.every((task) => task.subject !== "")).toBe(true);
    expect(tasks.every((task) => Number.isInteger(task.number))).toBe(true);
    // Every number appears once. Two tasks sharing one would collide on their
    // key and the second would overwrite the first in the store.
    expect(new Set(tasks.map((task) => task.number)).size).toBe(tasks.length);
  });
});

describe("candidates", () => {
  it("offers the root and the project's awp workspaces", () => {
    // The root alone is not enough, and this repository is what showed it: a
    // project's root is its *default* jj workspace, so it holds whatever
    // revision that one checkout is parked on.
    expect(candidates("/repos/thicket", "thicket", "/home/x")).toEqual([
      "/repos/thicket",
      "/home/x/.awp/workspaces/thicket",
    ]);
  });
});

/** A `TODO.md` at a path, with its modification time set so a pick is deterministic. */
const write = (path: string, body: string, when?: number): void => {
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, body);
  if (when !== undefined) {
    utimesSync(path, when, when);
  }
};

const run = (root: string, project: string, home: string) =>
  Effect.runPromise(readTodo({ root, project, home }));

describe("readTodo", () => {
  const scratch = mkdtemp();
  afterAll(() => rmSync(scratch, { recursive: true, force: true }));

  it("says nothing for a project that keeps no such file", async () => {
    // Most repositories on a real machine have none. A refusal here would be
    // a sentence that is true and unactionable for every one of them.
    expect(await run(join(scratch, "empty"), "empty", scratch)).toEqual([]);
  });

  it("reads the root when that is the only one", async () => {
    const root = join(scratch, "solo");
    write(join(root, "TODO.md"), "## 1. only here\n\nbody\n");
    expect((await run(root, "solo", scratch)).map((task) => task.subject)).toEqual(["only here"]);
  });

  it("prefers the NEWEST file, not the root", async () => {
    // The whole reason this is a pick rather than a read. Measured against
    // this repository: the root was on an old commit with no TODO.md at all,
    // and the checkout being worked in had 46 tasks.
    const root = join(scratch, "picky");
    write(join(root, "TODO.md"), "## 1. the stale one\n", 1_000_000);
    write(
      join(scratch, ".awp", "workspaces", "picky", "branch", "TODO.md"),
      "## 1. the live one\n",
      2_000_000,
    );
    expect((await run(root, "picky", scratch)).map((task) => task.subject)).toEqual([
      "the live one",
    ]);
  });

  it("reads one list, not one per checkout", async () => {
    // Taking all of them would put a project's task list in the store several
    // times over, at several revisions, with nothing able to say which row
    // was true.
    const root = join(scratch, "many");
    write(join(root, "TODO.md"), "## 1. a\n", 1_000_000);
    write(join(scratch, ".awp", "workspaces", "many", "one", "TODO.md"), "## 1. b\n", 2_000_000);
    write(join(scratch, ".awp", "workspaces", "many", "two", "TODO.md"), "## 1. c\n", 3_000_000);
    expect(await run(root, "many", scratch)).toHaveLength(1);
  });
});

function mkdtemp(): string {
  const path = join(tmpdir(), `awp-todo-${Date.now().toString(36)}`);
  mkdirSync(path, { recursive: true });
  return path;
}
