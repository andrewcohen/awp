import { describe, expect, it } from "vitest";
import type { Task } from "@awp-kit/protocol";
import { finished, merge } from "./tasklist";

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

const claude = (over: Partial<Task> = {}): Task =>
  board({
    id: "claude:thicket#lantern/3",
    subject: "wire the panel",
    description: "read the board",
    source: "claude",
    seq: 3,
    tags: ["project:thicket", "workspace:thicket/lantern"],
    ...over,
  });

describe("merge", () => {
  it("draws both sources as one list", () => {
    const { rows } = merge([claude(), board()]);
    expect(rows.map((row) => row.source)).toEqual(["claude", "todo"]);
  });

  it("floats what is underway to the top, whichever source it came from", () => {
    const { rows } = merge([claude(), board({ status: "in_progress" })]);
    expect(rows.map((row) => row.subject)).toEqual(["run the agent under ACP", "wire the panel"]);
  });

  it("keeps an agent's own queue above what is merely written down", () => {
    // The list that describes what is happening right now goes first, which is
    // the order the two reads used to give for free.
    const { rows } = merge([board(), claude()]);
    expect(rows[0]?.source).toBe("claude");
  });

  it("counts finished tasks instead of drawing them", () => {
    // Eighty completed against a handful outstanding is the real shape here,
    // and showing them all buries the four that matter.
    const { rows, done } = merge([
      claude({ status: "completed" }),
      claude({ id: "claude:thicket#lantern/4" }),
      board({ status: "completed" }),
    ]);
    expect(rows).toHaveLength(1);
    expect(done).toBe(2);
  });

  it("labels a task by its number, not its whole id", () => {
    // `todo:thicket#91` is what awp_task wants and what nobody would read in
    // a 280px column.
    expect(merge([board()]).rows[0]?.label).toBe("#91");
  });

  it("falls back to the id when a source counts nothing", () => {
    expect(merge([board({ seq: undefined })]).rows[0]?.label).toBe("todo:thicket#91");
  });

  it("does not deduplicate the same work appearing in two sources", () => {
    // Deliberate. The two entries are not the same object — different ids and
    // different statuses — and merging them would have to pick a status.
    const { rows } = merge([
      claude({ subject: "run the agent under ACP", status: "in_progress" }),
      board({ subject: "run the agent under ACP" }),
    ]);
    expect(rows).toHaveLength(2);
  });

  it("hands a task over in the shape TaskSend takes", () => {
    // What makes one send work for every source without a second call.
    expect(merge([board()]).rows[0]?.task).toEqual({
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
