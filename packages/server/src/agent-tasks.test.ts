import { mkdirSync, mkdtempSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { type AgentTask, projectSlug, readTasks, taskPrompt } from "./agent-tasks";

// Reading somebody else's files, against a tree shaped like theirs.
//
// A real `~/.claude` is not usable here — it is a person's machine and it
// changes while the suite runs — so `home` is an argument and every test
// builds the three directories the reader walks.

const scratch = mkdtempSync(join(tmpdir(), "awp-tasks-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let homes = 0;

interface Written {
  readonly home: string;
  readonly dir: string;
}

/** A home with one workspace directory in it, and nothing else. */
const home = (dir = "/work/thicket/lantern"): Written => {
  const at = join(scratch, `home-${(homes += 1)}`);
  mkdirSync(join(at, ".claude", "projects", projectSlug(dir)), { recursive: true });
  return { home: at, dir };
};

/** A session that ran in that directory, with a list of its own. */
const session = (
  written: Written,
  id: string,
  tasks: ReadonlyArray<Partial<AgentTask> & { readonly id: string }>,
  at?: number,
): void => {
  writeFileSync(
    join(written.home, ".claude", "projects", projectSlug(written.dir), `${id}.jsonl`),
    "",
  );
  const dir = join(written.home, ".claude", "tasks", id);
  mkdirSync(dir, { recursive: true });
  for (const task of tasks) {
    writeFileSync(
      join(dir, `${task.id}.json`),
      JSON.stringify({ subject: `task ${task.id}`, description: "", status: "pending", ...task }),
    );
  }
  if (at !== undefined) {
    utimesSync(dir, at, at);
  }
};

const read = (written: Written): Promise<ReadonlyArray<AgentTask>> =>
  Effect.runPromise(readTasks(written.dir, written.home));

describe("projectSlug", () => {
  test("every non-alphanumeric byte becomes a dash", () => {
    expect(projectSlug("/work/thicket/lantern")).toBe("-work-thicket-lantern");
  });

  test("a dot is a dash, so a hidden directory keeps its place", () => {
    expect(projectSlug("/home/one/.awp/workspaces/thicket")).toBe(
      "-home-one--awp-workspaces-thicket",
    );
  });

  test("an underscore is a dash too", () => {
    // The case that told the three candidate rules apart. Nothing in this tree
    // has an underscore in a path, and `sanitize` can produce one — so this was
    // settled against a real transcript elsewhere on the machine rather than
    // guessed. See the note in `agent-tasks.ts`.
    expect(projectSlug("/work/ses_ABC")).toBe("-work-ses-ABC");
  });

  test("a relative path is resolved first", () => {
    // Otherwise the slug has no leading dash, matches nothing, and reads as
    // "no tasks" rather than as a mistake.
    expect(projectSlug("./somewhere").startsWith("-")).toBe(true);
  });
});

describe("readTasks", () => {
  test("reads the list a session kept", async () => {
    const written = home();
    session(written, "s1", [{ id: "1" }, { id: "2" }]);
    expect((await read(written)).map((task) => task.id)).toEqual(["1", "2"]);
  });

  test("orders by number, not as text", async () => {
    // `10.json` sorts before `2.json` as a string, which puts a counted list in
    // an order nobody wrote.
    const written = home();
    session(written, "s1", [{ id: "10" }, { id: "2" }, { id: "1" }]);
    expect((await read(written)).map((task) => task.id)).toEqual(["1", "2", "10"]);
  });

  test("what is being done comes before what is next", async () => {
    const written = home();
    session(written, "s1", [
      { id: "1", status: "pending" },
      { id: "9", status: "in_progress" },
    ]);
    expect((await read(written)).map((task) => task.id)).toEqual(["9", "1"]);
  });

  test("completed tasks are read, not dropped", async () => {
    // The panel hides them and shows a count. Dropping them here would make
    // that count impossible, and this module's job is to report the file.
    const written = home();
    session(written, "s1", [{ id: "1", status: "completed" }]);
    expect(await read(written)).toHaveLength(1);
  });

  test("the newest list wins, not the newest transcript", async () => {
    // A workspace accumulates a transcript per launch and one per subagent, so
    // the newest transcript is often a subagent that ran a minute ago. The
    // question being asked is whose *list* is live.
    //
    // The session ids are chosen so the stale one is read *first*: `readdir`
    // answers in directory order, which here is alphabetical, so ids of "old"
    // and "new" would have this test passing on the wrong reason — the right
    // answer arriving first by luck. Removing the comparison then changes
    // nothing, which was checked.
    const written = home();
    session(written, "aaa-stale", [{ id: "1", subject: "the old one" }], 1_000_000);
    session(written, "zzz-live", [{ id: "1", subject: "the live one" }], 2_000_000);
    expect((await read(written)).at(0)?.subject).toBe("the live one");
  });

  test("a session that kept no list is skipped, not preferred", async () => {
    // The one that would have been picked by "newest transcript": most
    // sessions never keep a list at all, and an empty directory beating a full
    // one is the whole list disappearing.
    const written = home();
    session(written, "zzz-kept", [{ id: "1", subject: "held" }], 1_000_000);
    session(written, "aaa-empty", [], 2_000_000);
    expect((await read(written)).map((task) => task.subject)).toEqual(["held"]);
  });

  test("a directory no agent has run in is empty, not an error", async () => {
    expect(await Effect.runPromise(readTasks("/nowhere/at/all", scratch))).toEqual([]);
  });

  test("a torn file costs that task and not the list", async () => {
    // The agent appends to this directory while the panel reads it, so half a
    // file is an ordinary event rather than a fault.
    const written = home();
    session(written, "s1", [{ id: "1" }, { id: "2" }]);
    writeFileSync(join(written.home, ".claude", "tasks", "s1", "2.json"), "{ half writ");
    expect((await read(written)).map((task) => task.id)).toEqual(["1"]);
  });

  test("a task with no subject is skipped", async () => {
    const written = home();
    session(written, "s1", [{ id: "1", subject: "  " }, { id: "2" }]);
    expect((await read(written)).map((task) => task.id)).toEqual(["2"]);
  });

  test("an unfamiliar status is kept rather than refused", async () => {
    // Somebody else's field. A status this does not know sorts last and is
    // still shown; refusing it would lose the task over a word.
    const written = home();
    session(written, "s1", [{ id: "1", status: "blocked" }]);
    expect((await read(written))[0]?.status).toBe("blocked");
  });
});

describe("taskPrompt", () => {
  const task: AgentTask = {
    id: "61",
    subject: "Archive a thread",
    description: "Threads accumulate and there is no way to put a finished one away.",
    status: "pending",
  };

  test("names the task by the agent's own id", () => {
    // The agent's list is keyed by it; a task named only by its subject is one
    // the agent has to find again by reading.
    expect(taskPrompt(task)).toContain("task 61: Archive a thread");
  });

  test("the instruction is last", () => {
    // Same rule as `notePrompt`: everything above it is address, and an agent
    // reads top to bottom.
    expect(taskPrompt(task).split("\n").at(-1)).toBe("Please pick this up now.");
  });

  test("the description is quoted whole", () => {
    // Not capped, unlike a page note's element text. A description is written
    // on purpose and the cut half is usually the half saying what done means.
    const long = { ...task, description: "word ".repeat(400).trim() };
    expect(taskPrompt(long)).toContain("word ".repeat(400).trim());
  });

  test("a task with no description says nothing about one", () => {
    expect(
      taskPrompt({ ...task, description: "  " })
        .split("\n")
        .filter((one) => one === ""),
    ).toHaveLength(1);
  });
});
