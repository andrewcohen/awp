import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { type Incoming, Tasks, layer, migrations, taskId } from "./tasks";

// Proved against a real file, because two of the three properties here are
// claims about one: that ingest run twice changes nothing, and that a task
// removed from its source leaves the table.

const scratch = mkdtempSync(join(tmpdir(), "awp-tasks-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `tasks-${(files += 1)}.sqlite`);

const at = (path: string) => layer.pipe(Layer.provide(dbLayer(path, migrations)));

type Service = { readonly [K in keyof Tasks["Service"]]: Tasks["Service"][K] };

const on = <A>(path: string, program: (tasks: Service) => Effect.Effect<A, unknown>): Promise<A> =>
  Effect.gen(function* () {
    const tasks = yield* Tasks;
    return yield* program(tasks);
  }).pipe(Effect.provide(at(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

const incoming = (over: Partial<Incoming> = {}): Incoming => ({
  source: "todo",
  sourceKey: "thicket#1",
  sourceSeq: 1,
  subject: "paginate the tabular exports",
  description: "the list is unbounded",
  status: "pending",
  tags: ["project:thicket"],
  ...over,
});

describe("ingest", () => {
  test("puts a source's tasks in, with their tags", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        const counted = yield* tasks.ingest("todo", "thicket#", [incoming()]);
        expect(counted).toEqual({ added: 1, changed: 0, removed: 0 });
        return yield* tasks.list();
      }),
    );
    expect(held).toHaveLength(1);
    expect(held[0]?.subject).toBe("paginate the tabular exports");
    expect(held[0]?.tags).toEqual(["project:thicket"]);
    expect(held[0]?.id).toBe(taskId("todo", "thicket#1"));
  });

  test("run twice changes nothing", async () => {
    // The property the UNIQUE exists for. Ingest is what a forked refresh
    // calls, so it runs on every read of a panel somebody leaves open.
    const path = file();
    const twice = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        const again = yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return { again, held: yield* tasks.list() };
      }),
    );
    expect(twice.again).toEqual({ added: 0, changed: 0, removed: 0 });
    expect(twice.held).toHaveLength(1);
  });

  test("survives a reopen", async () => {
    // A store is only worth having if a task outlives the process that read
    // it, which is the whole complaint against the per-session lists.
    const path = file();
    await on(path, (tasks) => tasks.ingest("todo", "thicket#", [incoming()]));
    expect(await on(path, (tasks) => tasks.list())).toHaveLength(1);
  });

  test("takes out what the source no longer has", async () => {
    // How a task finishes in this repository: the entry leaves TODO.md. There
    // is no record whose absence a per-task write could notice, which is why
    // ingest takes the whole set.
    const path = file();
    const after = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [
          incoming(),
          incoming({ sourceKey: "thicket#2", sourceSeq: 2, subject: "second" }),
        ]);
        const swept = yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return { swept, held: yield* tasks.list() };
      }),
    );
    expect(after.swept).toEqual({ added: 0, changed: 0, removed: 1 });
    expect(after.held.map((task) => task.subject)).toEqual(["paginate the tabular exports"]);
  });

  test("one project's sweep does not touch another's", async () => {
    // The reason ingest is scoped by a key prefix rather than by source alone.
    // Without it, reading one repository's TODO.md would delete every other
    // repository's tasks — and the panel would show whichever was read last.
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        yield* tasks.ingest("todo", "orchard#", [
          incoming({ sourceKey: "orchard#1", subject: "orchard's own", tags: ["project:orchard"] }),
        ]);
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return yield* tasks.list();
      }),
    );
    expect(held.map((task) => task.subject).toSorted()).toEqual([
      "orchard's own",
      "paginate the tabular exports",
    ]);
  });

  test("reports a task whose text moved", async () => {
    const path = file();
    const counted = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return yield* tasks.ingest("todo", "thicket#", [incoming({ status: "in_progress" })]);
      }),
    );
    expect(counted).toEqual({ added: 0, changed: 1, removed: 0 });
  });

  test("a tag the source stopped implying goes away", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming({ tags: ["project:thicket", "ui"] })]);
        yield* tasks.ingest("todo", "thicket#", [incoming({ tags: ["project:thicket"] })]);
        return yield* tasks.list();
      }),
    );
    expect(held[0]?.tags).toEqual(["project:thicket"]);
  });
});

describe("list", () => {
  const three: ReadonlyArray<Incoming> = [
    incoming({ sourceKey: "thicket#1", sourceSeq: 1, subject: "one", status: "pending" }),
    incoming({ sourceKey: "thicket#2", sourceSeq: 2, subject: "two", status: "in_progress" }),
    incoming({
      sourceKey: "thicket#10",
      sourceSeq: 10,
      subject: "ten",
      status: "pending",
      tags: ["project:thicket", "thread:20260908-ab12"],
    }),
  ];

  test("reads as a queue: underway first, then by the source's own number", async () => {
    // Numeric, because a source that counts its tasks writes 10 after 2 and
    // text order puts them the other way round.
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", three);
        return yield* tasks.list();
      }),
    );
    expect(held.map((task) => task.subject)).toEqual(["two", "one", "ten"]);
  });

  test("a tag filter is an AND, not an OR", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", three);
        return yield* tasks.list({ tags: ["project:thicket", "thread:20260908-ab12"] });
      }),
    );
    expect(held.map((task) => task.subject)).toEqual(["ten"]);
  });

  test("a status filter narrows to what was asked for", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", three);
        return yield* tasks.list({ statuses: ["in_progress"] });
      }),
    );
    expect(held.map((task) => task.subject)).toEqual(["two"]);
  });

  test("no filter is everything", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", three);
        return yield* tasks.list({});
      }),
    );
    expect(held).toHaveLength(3);
  });
});

// ── writing ────────────────────────────────────────────────────────────────
//
// The store had one writer, ingest, and everything about it is shaped by the
// source owning the row. These are the calls where this window owns it — plus
// the one that reaches across, which is a tag.

describe("add", () => {
  test("writes a task nothing sweeps", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        const made = yield* tasks.add({
          subject: "measure the sweep",
          description: "over every checkout",
          status: "pending",
          tags: ["project:thicket"],
        });
        // The sweep that would delete an ingested row under this prefix. An
        // awp task is not reachable by one, and that is the whole property.
        yield* tasks.ingest("todo", "thicket#", []);
        yield* tasks.ingest("claude", "thicket#", []);
        return { made, left: yield* tasks.list() };
      }),
    );
    expect(held.made.source).toBe("awp");
    expect(held.left.map((task) => task.id)).toEqual([held.made.id]);
  });

  test("answers the row it wrote, so nobody has to ask again", async () => {
    const path = file();
    const made = await on(path, (tasks) =>
      tasks.add({ subject: "trust the repo", description: "", status: "pending", tags: [] }),
    );
    expect(made.subject).toBe("trust the repo");
    expect(made.id.startsWith("awp:")).toBe(true);
  });
});

describe("setStatus", () => {
  test("moves a task awp owns", async () => {
    const path = file();
    const moved = await on(path, (tasks) =>
      Effect.gen(function* () {
        const made = yield* tasks.add({
          subject: "measure the sweep",
          description: "",
          status: "pending",
          tags: [],
        });
        return yield* tasks.setStatus(made.id, "in_progress");
      }),
    );
    expect(moved.status).toBe("in_progress");
  });

  test("refuses an ingested one, and says where it is written", async () => {
    const path = file();
    const refused = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return yield* Effect.flip(tasks.setStatus(taskId("todo", "thicket#1"), "completed"));
      }),
    );
    // The sentence is the interface — what reads it is as often a model as a
    // person, and "it did not work" would send them nowhere.
    expect(String(refused.reason)).toContain("came from todo");
  });

  test("refuses a task that is not there at all", async () => {
    const path = file();
    const refused = await on(path, (tasks) => Effect.flip(tasks.setStatus("awp:nope", "pending")));
    expect(String(refused.reason)).toContain("no task");
  });
});

describe("tag", () => {
  test("a person's tag survives the sweep that rebuilt the task", async () => {
    // The property the `applied` column exists for, and the one a second table
    // would have had to invent a rule for. Without it a thread tag is written
    // and then silently undone by the next reading of the file it came from.
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        const id = taskId("todo", "thicket#1");
        yield* tasks.tag(id, "thread:20260915-ab12", true);
        yield* tasks.ingest("todo", "thicket#", [incoming({ subject: "reworded" })]);
        return yield* tasks.list();
      }),
    );
    expect(held[0]?.tags).toContain("thread:20260915-ab12");
    // And the source's own tag is still there: the sweep replaced what it
    // wrote, which is both halves of the rule.
    expect(held[0]?.tags).toContain("project:thicket");
  });

  test("untagging one the source implies is honest about being temporary", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        const id = taskId("todo", "thicket#1");
        const after = yield* tasks.tag(id, "project:thicket", false);
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return { after, swept: yield* tasks.list() };
      }),
    );
    expect(held.after.tags).not.toContain("project:thicket");
    expect(held.swept[0]?.tags).toContain("project:thicket");
  });

  test("applying one the source already implies keeps it through a sweep that stops", async () => {
    const path = file();
    const held = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        const id = taskId("todo", "thicket#1");
        yield* tasks.tag(id, "project:thicket", true);
        yield* tasks.ingest("todo", "thicket#", [incoming({ tags: [] })]);
        return yield* tasks.list();
      }),
    );
    expect(held[0]?.tags).toContain("project:thicket");
  });
});

describe("remove", () => {
  test("forgets a task awp owns, tags and all", async () => {
    const path = file();
    const left = await on(path, (tasks) =>
      Effect.gen(function* () {
        const made = yield* tasks.add({
          subject: "measure the sweep",
          description: "",
          status: "pending",
          tags: ["project:thicket"],
        });
        yield* tasks.remove(made.id);
        return yield* tasks.list();
      }),
    );
    expect(left).toEqual([]);
  });

  test("refuses an ingested one", async () => {
    const path = file();
    const refused = await on(path, (tasks) =>
      Effect.gen(function* () {
        yield* tasks.ingest("todo", "thicket#", [incoming()]);
        return yield* Effect.flip(tasks.remove(taskId("todo", "thicket#1")));
      }),
    );
    expect(String(refused.reason)).toContain("only copied here");
  });
});
