import { readdir } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import type { TaskChange, WorkspaceStatus } from "@awp-kit/protocol";
import { Clock, Duration, Effect, Fiber, PubSub, Ref, Stream } from "effect";
import { readTasks } from "./agent-tasks";
import { type Incoming, type Task, type TaskFilter, type Tasks } from "./tasks";
import { readTodo } from "./todo-tasks";

// The tasks, answered from the store, re-read from the sources behind the
// answer.
//
// ── why not read the files on every ask ────────────────────────────────────
//
// The panel is mounted every time its tab is opened — Base UI unmounts a
// hidden tab — so "read every source" would be a disk sweep per glance. And a
// question that writes is the shape `--ignore-working-copy` exists to prevent,
// so the ingest is deliberately a *separate* step rather than part of
// answering.
//
//   read      the store, immediately          ← what the panel gets
//   behind    every source, forked            ← what makes it current
//
// The same two-part shape as the pull request cache, and for the same reason
// its own note gives: `forkDetach` and not `fork`, because the fiber has to
// outlive the request that started it — the whole point is that the request
// has already answered.
//
// ── two sources, and neither of them is this process ───────────────────────
//
//   todo     a project's `TODO.md`, in whichever checkout wrote it last
//   claude   Claude Code's own per-session lists, one per workspace
//
// Both are files somebody else writes, which is what makes the trigger below a
// problem rather than a detail: there is no event to subscribe to, so the only
// way to learn that a list changed is to look again.
//
// ── so the sweep runs while somebody is watching ───────────────────────────
//
// It used to run only behind a read, and the panel polled every four seconds to
// produce the reads — which put a timer in the window for a question the daemon
// is better placed to ask, and made every client that opened the panel its own
// poller. `changes()` moves it: the daemon sweeps on a slow timer for as long
// as anybody holds the stream, and pushes only when a sweep actually changed
// something. A window with the panel closed costs nothing at all.
//
//   nobody watching   no timer, and a read is still swept behind
//   one watching      a sweep every SWEEP_MS, for every client at once
//   a turn ends       `nudge()`, because the files have just settled

/** Where a project's tasks are read from, and what they get tagged with. */
export interface Source {
  readonly name: string;
  readonly root: string;
}

export interface TaskFeed {
  /** The tasks, from the store. Starts a re-read behind the answer. */
  readonly read: (filter?: TaskFilter) => Effect.Effect<ReadonlyArray<Task>>;
  /** Re-read now and wait for it. What a person pressing a button asks for. */
  readonly refresh: () => Effect.Effect<void>;
  /**
   * Re-read soon, because something that writes a task list has settled.
   *
   * Forked, so a caller on the hot path of a turn ending pays nothing for it.
   */
  readonly nudge: () => Effect.Effect<void>;
  /**
   * A write made through this daemon, announced to whoever is watching.
   *
   * The sweep publishes what a *file* said; this is the other writer. A task
   * written by `awp_task_add`, or moved by another window, changes no file and
   * therefore no sweep — so without this a panel sitting open beside the agent
   * that wrote it learns nothing until some unrelated list happens to move.
   *
   * One row, because every one of these calls is one row. The count is what
   * crosses the wire and a client re-reads on any of them, so the kind is here
   * to keep the figure honest rather than because anything branches on it.
   */
  readonly wrote: (kind: "added" | "changed" | "removed") => Effect.Effect<void>;
  /**
   * Every sweep that changed something, from now.
   *
   * Holding this is what keeps the timer running — see the note above. Nothing
   * is replayed: a subscriber that has just arrived wants the listing, and it
   * has a call for that.
   */
  readonly changes: () => Stream.Stream<TaskChange>;
}

/** How often the sources are re-read while at least one client is watching. */
const SWEEP_MS = 10_000;

/**
 * A project's tag, and the prefix its keys carry.
 *
 * Namespaced rather than bare, so `project:thicket` cannot collide with a tag
 * somebody applies by hand — and so one query can ask for a project's tasks
 * without a second column to filter on.
 */
export const projectTag = (project: string): string => `project:${project}`;

/**
 * The checkout a task was read in, as a tag.
 *
 * Only the `claude` source carries one: its lists are per workspace, and the
 * panel beside a checkout is asking about that checkout. A `TODO.md` is read
 * from whichever checkout wrote it last, so tagging one with a workspace would
 * be recording an accident of modification time as though it meant something.
 */
export const workspaceTag = (project: string, workspace: string): string =>
  `workspace:${project}/${workspace}`;

/**
 * The prefix every one of a project's keys carries, and the scope of its sweep.
 *
 * Composed in one place and parsed nowhere, for the reason `reviewKey` and
 * `reviewOf` sit together: a format written in one file and read in another
 * drifts by a character, and this one decides which rows an ingest deletes.
 * `ProjectForget` is the second reader — it hands this an empty set, once per
 * source, to let a forgotten project's rows go.
 */
export const projectPrefix = (project: string): string => `${project}#`;

/** The key a project's `TODO.md` task is stored under. */
export const todoKey = (project: string, number: number): string =>
  `${projectPrefix(project)}${number}`;

/**
 * The key one of Claude Code's own tasks is stored under.
 *
 * The workspace is in it because a project has several checkouts and each has
 * its own agent keeping its own list — two of them numbering their tasks from
 * one. The project is still first, because that is what the sweep is scoped by.
 */
export const claudeKey = (project: string, workspace: string, id: string): string =>
  `${projectPrefix(project)}${workspace}/${id}`;

/**
 * Every checkout of a project that could hold an agent's task list.
 *
 * The root, and each directory under `~/.awp/workspaces/<project>` — the same
 * convention `todo-tasks.ts` reads candidates by, and the one this repo already
 * relies on to recover a session's identity when it carries no labels. Asked of
 * `readdir` rather than of jj, because a subprocess per project per sweep is a
 * cost paid for an answer the directory already has.
 *
 * Measured across 59 workspaces on this machine: 11ms serially, 2ms
 * concurrently, with four of them keeping a list at all. That is what makes
 * sweeping every checkout affordable rather than something to scope down.
 */
export const checkouts = async (
  source: Source,
  home: string,
): Promise<ReadonlyArray<{ readonly workspace: string; readonly dir: string }>> => {
  const under = join(home, ".awp", "workspaces", source.name);
  let found: ReadonlyArray<string> = [];
  try {
    found = (await readdir(under, { withFileTypes: true }))
      .filter((one) => one.isDirectory())
      .map((one) => one.name);
  } catch {
    // A project with no awp workspaces at all, which is every project somebody
    // has only imported. Its root is still a checkout.
  }
  return [
    { workspace: "", dir: source.root },
    ...found.map((name) => ({ workspace: name, dir: join(under, name) })),
  ];
};

/**
 * Whether a turn just finished somewhere between two readings.
 *
 * A workspace that was `working` and is now anything else — a turn that ended,
 * a turn that stopped to ask, or a conversation that was released. All three
 * mean the files an agent was writing have settled, which is the only property
 * this is asked for.
 *
 * Pure, and separate from the fold that uses it, because the interesting cases
 * are the two that do not look like an edge: a workspace that was working and
 * is *absent* now, and a reading that is equal to the one before it.
 */
export const settled = (
  before: ReadonlyMap<string, WorkspaceStatus>,
  after: ReadonlyMap<string, WorkspaceStatus>,
): boolean => {
  for (const [key, was] of before) {
    if (was === "working" && after.get(key) !== "working") {
      return true;
    }
  }
  return false;
};

export const make = (options: {
  readonly tasks: Tasks["Service"];
  readonly projects: () => Effect.Effect<ReadonlyArray<Source>>;
  /**
   * Where `~/.awp/workspaces` and `~/.claude` are.
   *
   * Taken rather than read, for the one reason `readTodo` and `readTasks` both
   * take it: the second source is somebody else's directory tree, and a test
   * that could not put one somewhere would be a test of a mock instead.
   */
  readonly home?: string;
}): Effect.Effect<TaskFeed> =>
  Effect.gen(function* () {
    const running = yield* Ref.make(false);
    const watchers = yield* Ref.make(0);
    const ticker = yield* Ref.make<Fiber.Fiber<void> | undefined>(undefined);
    const home = options.home ?? homedir();
    // Dropping, and small. A subscriber is a socket, one that has stopped
    // reading is a window that has gone away, and a nudge nobody could receive
    // costs a client one re-read it will make on its next sweep anyway.
    const hub = yield* PubSub.dropping<TaskChange>(16);

    /** What a project's `TODO.md` says, in the store's shape. */
    const todoOf = (source: Source) =>
      readTodo({ root: source.root, project: source.name, home }).pipe(
        Effect.map((found) =>
          found.map((task): Incoming => ({
            source: "todo",
            sourceKey: todoKey(source.name, task.number),
            sourceSeq: task.number,
            subject: task.subject,
            description: task.description,
            status: task.status,
            tags: [projectTag(source.name)],
          })),
        ),
      );

    /** What every checkout's agent has written down for itself. */
    const claudeOf = (source: Source) =>
      Effect.gen(function* () {
        const where = yield* Effect.promise(() => checkouts(source, home));
        const all: Incoming[] = [];
        for (const one of where) {
          const found = yield* readTasks(one.dir, home);
          for (const task of found) {
            all.push({
              source: "claude",
              sourceKey: claudeKey(source.name, one.workspace, task.id),
              // The agent's own number, which is what orders its queue.
              ...(Number.isFinite(Number(task.id)) ? { sourceSeq: Number(task.id) } : {}),
              subject: task.subject,
              description: task.description,
              status: task.status,
              tags:
                one.workspace === ""
                  ? [projectTag(source.name)]
                  : [projectTag(source.name), workspaceTag(source.name, one.workspace)],
            });
          }
        }
        return all as ReadonlyArray<Incoming>;
      });

    /** Read every source and put what it says into the store. */
    const sweep = Effect.gen(function* () {
      const sources = yield* options.projects();
      let added = 0;
      let changed = 0;
      let removed = 0;
      for (const source of sources) {
        for (const [kind, found] of [
          ["todo", yield* todoOf(source)],
          ["claude", yield* claudeOf(source)],
        ] as const) {
          // Ingested even when empty, because empty is an answer: a project
          // whose TODO.md was deleted has no tasks, and leaving the last read
          // in the table would show a list nothing on disk agrees with.
          const moved = yield* options.tasks
            .ingest(kind, projectPrefix(source.name), found)
            .pipe(Effect.orElseSucceed(() => ({ added: 0, changed: 0, removed: 0 })));
          added += moved.added;
          changed += moved.changed;
          removed += moved.removed;
        }
      }
      if (added + changed + removed > 0) {
        const at = yield* Clock.currentTimeMillis;
        yield* PubSub.publish(hub, { added, changed, removed, at });
      }
    });

    /**
     * At most one sweep at a time.
     *
     * Not per project, unlike the pull request cache's guard: a sweep is a few
     * file reads rather than a `gh` call per repository, so the whole thing is
     * one unit and there is nothing to gain by letting two overlap.
     */
    const behind = Effect.gen(function* () {
      if (yield* Ref.get(running)) {
        return;
      }
      yield* Ref.set(running, true);
      yield* sweep.pipe(Effect.ignore, Effect.ensuring(Ref.set(running, false)), Effect.forkDetach);
    });

    /** Sweep forever, slowly. Started by the first watcher and stopped by the last. */
    const tick = Effect.gen(function* () {
      while (true) {
        yield* Effect.sleep(Duration.millis(SWEEP_MS));
        yield* sweep.pipe(Effect.ignore);
      }
    });

    const joined = Effect.gen(function* () {
      const was = yield* Ref.getAndUpdate(watchers, (n) => n + 1);
      if (was === 0) {
        yield* Ref.set(ticker, yield* Effect.forkDetach(tick));
      }
    });

    const left = Effect.gen(function* () {
      const was = yield* Ref.getAndUpdate(watchers, (n) => Math.max(0, n - 1));
      if (was <= 1) {
        const held = yield* Ref.getAndSet(ticker, undefined);
        if (held !== undefined) {
          yield* Fiber.interrupt(held);
        }
      }
    });

    return {
      wrote: (kind: "added" | "changed" | "removed") =>
        Effect.gen(function* () {
          const at = yield* Clock.currentTimeMillis;
          yield* PubSub.publish(hub, {
            added: kind === "added" ? 1 : 0,
            changed: kind === "changed" ? 1 : 0,
            removed: kind === "removed" ? 1 : 0,
            at,
          });
        }),
      read: (filter?: TaskFilter) =>
        Effect.gen(function* () {
          const held = yield* options.tasks.list(filter).pipe(Effect.orElseSucceed(() => []));
          yield* behind;
          return held;
        }),
      refresh: () => sweep.pipe(Effect.ignore),
      nudge: () => behind,
      changes: () =>
        Stream.unwrap(
          Effect.gen(function* () {
            yield* joined;
            return Stream.fromPubSub(hub).pipe(Stream.ensuring(left));
          }),
        ),
    };
  });
