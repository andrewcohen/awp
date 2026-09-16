import { describe, expect, it } from "vitest";
import type { Task } from "@awp-kit/protocol";
import { filter, finished, merge } from "./tasklist";

const board = (over: Partial<Task> = {}): Task => ({
  id: "todo:thicket#91",
  subject: "run the agent under ACP",
  description: "## why\n\na terminal is a picture of a conversation",
  status: "pending",
  source: "todo",
  tags: ["project:thicket"],
  seq: 91,
  updatedAt: 0,
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

  it("keeps finished tasks out of the list, and hands them back separately", () => {
    // Eighty completed against a handful outstanding is the real shape here,
    // and showing them all buries the four that matter. They are still
    // answered, because the panel's count is a control now.
    const { rows, done } = merge([
      claude({ status: "completed" }),
      claude({ id: "claude:thicket#lantern/4" }),
      board({ status: "completed" }),
    ]);
    expect(rows).toHaveLength(1);
    expect(done).toHaveLength(2);
  });

  it("orders the finished ones newest first", () => {
    // The other half is ordered for reading — what is underway, then what is
    // written down. This half answers "what got done while I was away", which
    // is a question about when.
    const { done } = merge([
      claude({ id: "claude:thicket#lantern/1", status: "completed", updatedAt: 10 }),
      claude({ id: "claude:thicket#lantern/2", status: "completed", updatedAt: 30 }),
      claude({ id: "claude:thicket#lantern/3", status: "completed", updatedAt: 20 }),
    ]);
    expect(done.map((row) => row.at)).toEqual([30, 20, 10]);
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

describe("filter", () => {
  const rows = merge([
    board({ id: "a", seq: 89, subject: "Fuzzy search over the tasks panel", description: "" }),
    board({
      id: "b",
      seq: 63,
      subject: "Run a workspace's services",
      description: "The port is the feature.\n\n    A service nobody can reach is a process.",
    }),
    board({ id: "c", seq: 21, subject: "Design one theme", description: "" }),
  ]).rows;

  const subjects = (query: string): ReadonlyArray<string> =>
    filter(rows, query).map((one) => one.task.subject);

  it("an empty query keeps everything, and marks nothing", () => {
    const all = filter(rows, "  ");
    expect(all).toHaveLength(3);
    expect(all.every((one) => one.subject.length === 0 && one.excerpt === undefined)).toBe(true);
  });

  it("matches a subject fuzzily", () => {
    expect(subjects("fzp")).toStrictEqual(["Fuzzy search over the tasks panel"]);
  });

  it("matches the label as an address, not a subsequence", () => {
    expect(subjects("#63")).toStrictEqual(["Run a workspace's services"]);
    expect(subjects("#69")).toStrictEqual([]);
  });

  // The reason the description is matched at all: half of what a person
  // remembers about a task is a phrase from its body.
  it("matches a description, and says where", () => {
    const [only, ...rest] = filter(rows, "nobody can reach");
    expect(rest).toStrictEqual([]);
    expect(only?.task.subject).toBe("Run a workspace's services");
    expect(only?.excerpt?.text).toContain("nobody can reach");
  });

  it("a subject hit carries no excerpt, because the title is the evidence", () => {
    expect(filter(rows, "services")[0]?.excerpt).toBeUndefined();
  });

  it("keeps the order it was given rather than ranking by the match", () => {
    expect(subjects("e")).toStrictEqual(rows.map((one) => one.subject));
  });
});
