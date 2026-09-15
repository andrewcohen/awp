import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TaskChange, WorkspaceStatus } from "@awp-kit/protocol";
import { layer as dbLayer } from "@awp-kit/store";
import { Duration, Effect, Fiber, Layer, Stream } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import {
  type Source,
  type TaskFeed,
  claudeKey,
  make,
  projectPrefix,
  settled,
  todoKey,
  workspaceTag,
} from "./task-feed";
import { projectSlug } from "./agent-tasks";
import { Tasks, layer as tasksLayer, migrations } from "./tasks";

// Against a real directory, because most of what is claimed here is a claim
// about one: which checkouts are read, what a key made of a workspace keeps
// apart, and what a sweep that changed nothing says.

const scratch = mkdtempSync(join(tmpdir(), "awp-task-feed-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let made = 0;
const file = (): string => join(scratch, `feed-${(made += 1)}.sqlite`);

const at = (path: string) => tasksLayer.pipe(Layer.provide(dbLayer(path, migrations)));

/** A project root with a `TODO.md` in it. */
const project = (name: string, todo: string): Source => {
  const root = join(scratch, `${name}-${(made += 1)}`);
  mkdirSync(root, { recursive: true });
  writeFileSync(join(root, "TODO.md"), todo);
  return { name, root };
};

const run = <A>(
  sources: ReadonlyArray<Source>,
  program: (feed: TaskFeed) => Effect.Effect<A, unknown>,
  home: string = scratch,
): Promise<A> =>
  Effect.gen(function* () {
    const tasks = yield* Tasks;
    const feed = yield* make({ tasks, projects: () => Effect.succeed(sources), home });
    return yield* program(feed);
  }).pipe(Effect.provide(at(file())), Effect.scoped, Effect.orDie, Effect.runPromise);

/**
 * Subscribe, let the subscription land, then do the thing.
 *
 * `changes()` is a PubSub and the subscribe happens when the stream runs, so a
 * fork followed immediately by a publish is a race the publisher usually wins —
 * which would make every one of these tests pass for the wrong reason.
 */
const watching = <A>(
  feed: TaskFeed,
  act: Effect.Effect<A>,
): Effect.Effect<ReadonlyArray<TaskChange>> =>
  Effect.gen(function* () {
    const seen = yield* Effect.forkDetach(Stream.runCollect(Stream.take(feed.changes(), 1)));
    yield* Effect.sleep(Duration.millis(50));
    yield* act;
    // A timeout and not an interrupt: a check that cannot fail reads as a
    // pass, and "nothing was pushed" is exactly the assertion that needs one.
    return yield* Fiber.join(seen).pipe(
      Effect.timeout(Duration.millis(300)),
      Effect.orElseSucceed(() => []),
    );
  });

describe("keys", () => {
  test("a project's two sources share a prefix, so one sweep scopes both", () => {
    expect(todoKey("thicket", 91).startsWith(projectPrefix("thicket"))).toBe(true);
    expect(claudeKey("thicket", "lantern", "3").startsWith(projectPrefix("thicket"))).toBe(true);
  });

  test("two checkouts numbering their tasks from one keep their own rows", () => {
    expect(claudeKey("thicket", "lantern", "1")).not.toBe(claudeKey("thicket", "orchard", "1"));
  });

  test("a checkout's tag names the project as well, so two projects' workspaces cannot collide", () => {
    expect(workspaceTag("thicket", "lantern")).toBe("workspace:thicket/lantern");
  });
});

const map = (entries: ReadonlyArray<readonly [string, WorkspaceStatus]>) =>
  new Map<string, WorkspaceStatus>(entries);

describe("settled", () => {
  test("a workspace that stopped working is an edge", () => {
    expect(settled(map([["a", "working"]]), map([["a", "idle"]]))).toBe(true);
  });

  test("a workspace that stopped to ask is one too — the files have settled either way", () => {
    expect(settled(map([["a", "working"]]), map([["a", "waiting"]]))).toBe(true);
  });

  test("a conversation released mid-turn is an edge, and it is the one an absence hides", () => {
    expect(settled(map([["a", "working"]]), map([]))).toBe(true);
  });

  test("still working is not", () => {
    expect(settled(map([["a", "working"]]), map([["a", "working"]]))).toBe(false);
  });

  test("a turn starting is not", () => {
    expect(settled(map([["a", "idle"]]), map([["a", "working"]]))).toBe(false);
  });

  test("two readings of nothing are not", () => {
    expect(settled(map([]), map([]))).toBe(false);
  });
});

describe("sweep", () => {
  test("reads a project's TODO.md into the store", async () => {
    const one = project("thicket", "# TODO\n\n## 1. paginate the exports\n\nunbounded\n");
    const held = await run([one], (feed) =>
      Effect.gen(function* () {
        yield* feed.refresh();
        return yield* feed.read();
      }),
    );
    expect(held.map((task) => task.subject)).toEqual(["paginate the exports"]);
    expect(held[0]?.source).toBe("todo");
  });

  test("a sweep that found something pushes what it moved", async () => {
    const one = project("thicket", "## 1. paginate the exports\n\nbody\n");
    const seen = await run([one], (feed) => watching(feed, feed.refresh()));
    expect(seen).toHaveLength(1);
    expect(seen[0]?.added).toBe(1);
    expect(seen[0]?.at).toBeGreaterThan(0);
  });

  test("a sweep that changed nothing says nothing", async () => {
    const one = project("thicket", "## 1. paginate the exports\n\nbody\n");
    const seen = await run([one], (feed) =>
      Effect.gen(function* () {
        // The news is in the first sweep, and it is spent before anything is
        // watching — so what the stream is asked here is whether an unchanged
        // file produces a push, which is the whole point of counting.
        yield* feed.refresh();
        return yield* watching(feed, feed.refresh());
      }),
    );
    expect(seen).toEqual([]);
  });
});

// A write moves no file, so no sweep can report it — and the panel only ever
// re-reads on a push. Without these the agent's own `awp_task_add` lands in a
// store nothing looking at it is told about, which was measured in a browser
// before it was written down here.
describe("a write says so too", () => {
  const nothing = project("thicket", "");

  test("a task written through the daemon is pushed", async () => {
    const seen = await run([nothing], (feed) => watching(feed, feed.wrote("added")));
    expect(seen).toHaveLength(1);
    expect(seen[0]?.added).toBe(1);
    expect(seen[0]?.at).toBeGreaterThan(0);
  });

  test("one row, in the column that moved", async () => {
    const seen = await run([nothing], (feed) => watching(feed, feed.wrote("removed")));
    expect(seen[0]).toMatchObject({ added: 0, changed: 0, removed: 1 });
  });

  test("a move is a change rather than an arrival, so a count stays a count", async () => {
    const seen = await run([nothing], (feed) => watching(feed, feed.wrote("changed")));
    expect(seen[0]).toMatchObject({ added: 0, changed: 1, removed: 0 });
  });
});

/**
 * A workspace with an agent's task list behind it.
 *
 * Written where `agent-tasks.ts` looks: the transcript names the session, and
 * the session names the task directory. Both halves are needed — a task
 * directory with no transcript pointing at it is not reachable, which is the
 * rule that file measured rather than guessed.
 */
const withList = (
  home: string,
  dir: string,
  session: string,
  tasks: ReadonlyArray<{
    readonly id: string;
    readonly subject: string;
    readonly status: string;
  }>,
): void => {
  mkdirSync(dir, { recursive: true });
  const under = join(home, ".claude", "projects", projectSlug(dir));
  mkdirSync(under, { recursive: true });
  writeFileSync(join(under, `${session}.jsonl`), "");
  const held = join(home, ".claude", "tasks", session);
  mkdirSync(held, { recursive: true });
  for (const task of tasks) {
    writeFileSync(
      join(held, `${task.id}.json`),
      JSON.stringify({ ...task, description: "what it is for" }),
    );
  }
};

describe("claude's own lists", () => {
  test("a checkout's agent list is read, tagged with the checkout it was found in", async () => {
    const home = join(scratch, `home-${(made += 1)}`);
    const root = join(home, "repos", "thicket");
    mkdirSync(root, { recursive: true });
    withList(home, join(home, ".awp", "workspaces", "thicket", "lantern"), "s-lantern", [
      { id: "1", subject: "read the adapter", status: "in_progress" },
    ]);

    const held = await run(
      [{ name: "thicket", root }],
      (feed) =>
        Effect.gen(function* () {
          yield* feed.refresh();
          return yield* feed.read();
        }),
      home,
    );
    expect(held.map((task) => task.subject)).toEqual(["read the adapter"]);
    expect(held[0]?.source).toBe("claude");
    expect(held[0]?.tags).toContain(workspaceTag("thicket", "lantern"));
  });

  test("two checkouts of one project each keep their own task 1", async () => {
    const home = join(scratch, `home-${(made += 1)}`);
    const root = join(home, "repos", "thicket");
    mkdirSync(root, { recursive: true });
    withList(home, join(home, ".awp", "workspaces", "thicket", "lantern"), "s-a", [
      { id: "1", subject: "read the adapter", status: "pending" },
    ]);
    withList(home, join(home, ".awp", "workspaces", "thicket", "orchard"), "s-b", [
      { id: "1", subject: "measure the sweep", status: "pending" },
    ]);

    const held = await run(
      [{ name: "thicket", root }],
      (feed) =>
        Effect.gen(function* () {
          yield* feed.refresh();
          return yield* feed.read();
        }),
      home,
    );
    expect(held.map((task) => task.subject).toSorted()).toEqual([
      "measure the sweep",
      "read the adapter",
    ]);
  });

  test("a project with no checkouts under ~/.awp is still read at its root", async () => {
    const home = join(scratch, `home-${(made += 1)}`);
    const root = join(home, "repos", "orchard");
    withList(home, root, "s-root", [{ id: "2", subject: "trust the repo", status: "pending" }]);

    const held = await run(
      [{ name: "orchard", root }],
      (feed) =>
        Effect.gen(function* () {
          yield* feed.refresh();
          return yield* feed.read();
        }),
      home,
    );
    expect(held.map((task) => task.subject)).toEqual(["trust the repo"]);
    // No workspace tag: the root is the project, not one of its checkouts.
    expect(held[0]?.tags).toEqual(["project:orchard"]);
  });
});
