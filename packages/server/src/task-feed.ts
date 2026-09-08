import { homedir } from "node:os";
import { Effect, Ref } from "effect";
import { type Task, type TaskFilter, type Tasks } from "./tasks";
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
//   behind    every project's TODO.md, forked ← what makes it current
//
// The same two-part shape as the pull request cache, and for the same reason
// its own note gives: `forkDetach` and not `fork`, because the fiber has to
// outlive the request that started it — the whole point is that the request
// has already answered.

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
}

/**
 * A project's tag, and the prefix its keys carry.
 *
 * Namespaced rather than bare, so `project:thicket` cannot collide with a tag
 * somebody applies by hand — and so one query can ask for a project's tasks
 * without a second column to filter on.
 */
export const projectTag = (project: string): string => `project:${project}`;

/**
 * The key a project's task is stored under.
 *
 * The project first, because ingest sweeps by prefix: a read of one project's
 * file must not delete another's rows, and the prefix is what scopes it.
 */
export const todoKey = (project: string, number: number): string => `${project}#${number}`;

export const make = (options: {
  readonly tasks: Tasks["Service"];
  readonly projects: () => Effect.Effect<ReadonlyArray<Source>>;
}): Effect.Effect<TaskFeed> =>
  Effect.gen(function* () {
    const running = yield* Ref.make(false);
    const home = homedir();

    /** Read every project's file and put what it says into the store. */
    const sweep = Effect.gen(function* () {
      const sources = yield* options.projects();
      for (const source of sources) {
        const found = yield* readTodo({ root: source.root, project: source.name, home });
        // Ingested even when empty, because empty is an answer: a project
        // whose TODO.md was deleted has no tasks, and leaving the last read
        // in the table would show a list nothing on disk agrees with.
        yield* options.tasks
          .ingest(
            "todo",
            `${source.name}#`,
            found.map((task) => ({
              source: "todo" as const,
              sourceKey: todoKey(source.name, task.number),
              sourceSeq: task.number,
              subject: task.subject,
              description: task.description,
              status: task.status,
              tags: [projectTag(source.name)],
            })),
          )
          .pipe(Effect.ignore);
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

    return {
      read: (filter?: TaskFilter) =>
        Effect.gen(function* () {
          const held = yield* options.tasks.list(filter).pipe(Effect.orElseSucceed(() => []));
          yield* behind;
          return held;
        }),
      refresh: () => sweep.pipe(Effect.ignore),
    };
  });
