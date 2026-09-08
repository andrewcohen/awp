import { describe, expect, it } from "vitest";
import type { AgentTask, Task } from "@awp-kit/protocol";
import { finished, merge } from "./tasklist";

const session = (over: Partial<AgentTask> = {}): AgentTask => ({
  id: "3",
  subject: "wire the panel",
  description: "read the board",
  status: "pending",
  ...over,
});

const board = (over: Partial<Task> = {}): Task => ({
  id: "todo:thicket#91",
  subject: "run the agent under ACP",
  description: "## why\n\na terminal is a picture of a conversation",
  status: "pending",
  source: "todo",
  tags: ["project:thicket"],
  seq: 91,
  ...over,
});

describe("merge", () => {
  it("draws both sources as one list", () => {
    const { rows } = merge([session()], [board()]);
    expect(rows.map((row) => row.source)).toEqual(["session", "board"]);
  });

  it("floats what is underway to the top, whichever source it came from", () => {
    const { rows } = merge([session()], [board({ status: "in_progress" })]);
    expect(rows.map((row) => row.subject)).toEqual(["run the agent under ACP", "wire the panel"]);
  });

  it("keeps the session's own queue above the board within one status", () => {
    // The list that describes what is happening right now goes first.
    const { rows } = merge([session()], [board()]);
    expect(rows[0]?.source).toBe("session");
  });

  it("counts finished tasks instead of drawing them", () => {
    // Eighty completed against a handful outstanding is the real shape here,
    // and showing them all buries the four that matter.
    const { rows, done } = merge(
      [session({ status: "completed" }), session({ id: "4" })],
      [board({ status: "completed" })],
    );
    expect(rows).toHaveLength(1);
    expect(done).toBe(2);
  });

  it("labels a board task by its number, not its whole id", () => {
    // `todo:thicket#91` is what awp_task wants and what nobody would read in
    // a 280px column.
    expect(merge([], [board()]).rows[0]?.label).toBe("#91");
  });

  it("keys the two sources apart", () => {
    // A session task with the id "3" and a board task with the id "3" would
    // otherwise collide as react keys and one row would vanish.
    const { rows } = merge([session({ id: "3" })], [board({ id: "3", seq: undefined })]);
    expect(new Set(rows.map((row) => row.key)).size).toBe(2);
  });

  it("does not deduplicate the same work appearing twice", () => {
    // Deliberate. The two entries are not the same object — different ids and
    // different statuses — and merging them would have to pick a status.
    const { rows } = merge(
      [session({ subject: "run the agent under ACP", status: "in_progress" })],
      [board({ subject: "run the agent under ACP" })],
    );
    expect(rows).toHaveLength(2);
  });

  it("hands a board task over in the shape TaskSend takes", () => {
    // What makes one send work for both sources without a second call.
    expect(merge([], [board()]).rows[0]?.task).toEqual({
      id: "todo:thicket#91",
      subject: "run the agent under ACP",
      description: "## why\n\na terminal is a picture of a conversation",
      status: "pending",
    });
  });
});

describe("finished", () => {
  it("knows the spellings a source might use", () => {
    // The status is free text on purpose — a source's set can grow — so this
    // is a named set rather than a comparison with one string.
    expect(finished("completed")).toBe(true);
    expect(finished("done")).toBe(true);
    expect(finished("pending")).toBe(false);
    expect(finished("in_progress")).toBe(false);
  });
});
