// What the task board actually holds, against a real daemon.
//
// The one thing `tasks.test.ts` structurally cannot say: whether anything
// ingested. It proves ingest over a temp file with a list handed to it, and
// the sweep's whole job is to find that list on a real machine — a project's
// root, a `TODO.md` that may not be there, and a `read` that starts the sweep
// *behind* the answer, so a cold first read is legitimately empty and looks
// exactly like a failure.
//
// Two sources now, and the count per source is the line worth reading: a
// machine where Claude Code keeps no list anywhere and a reader that finds
// none of them produce exactly the same board.
//
//     bun run probe:tasks                      the default daemon
//     bun run probe:tasks ws://127.0.0.1:5284  a second instance
//
// Every call is a question. The sweep it starts writes to the tasks tables and
// to nothing else, and what it writes is whatever the files already say.

import { Effect, Result } from "effect";
import * as client from "@awp-kit/protocol/client";

const url = process.argv[2] ?? client.DEFAULT_DAEMON_URL;

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;
  const projects = yield* rpc.ProjectList();

  console.log(url);
  // The roots are what the sweep reads from, so they are the first thing to
  // look at: a derived project whose root is a *workspace* rather than a
  // repository has no TODO.md and reports nothing, which reads as an empty
  // list rather than as the wrong directory.
  for (const project of projects) {
    console.log(`  project       ${project.name}  ${project.root}`);
  }

  // Twice, deliberately. The first read answers from the store and forks the
  // sweep; the second is the one that can say whether the sweep did anything.
  const cold = yield* rpc.TaskBoard({});
  yield* Effect.sleep("2 seconds");
  const warm = yield* rpc.TaskBoard({});

  console.log(`  cold          ${cold.length} task(s)`);
  console.log(`  warm          ${warm.length} task(s)`);
  for (const task of warm.slice(0, 5)) {
    console.log(`    ${task.id}  [${task.status}]  ${task.subject.slice(0, 60)}`);
  }

  // Per source, because `todo` working and `claude` finding nothing is a
  // board that looks entirely healthy. Counted on this machine when the second
  // source landed: 59 workspaces swept, four of them keeping a list.
  const bySource = new Map<string, number>();
  for (const task of warm) {
    bySource.set(task.source, (bySource.get(task.source) ?? 0) + 1);
  }
  console.log(
    `  by source     ${[...bySource].map(([source, n]) => `${source} ${n}`).join(" · ") || "none"}`,
  );

  const tagged = yield* rpc.TaskBoard({ tags: ["project:awp"] });
  console.log(`  project:awp   ${tagged.length} task(s)`);

  const underway = yield* rpc.TaskBoard({ statuses: ["in_progress"] });
  console.log(
    `  in progress   ${underway.map((task) => `#${task.seq ?? "?"} ${task.subject}`).join(", ") || "none"}`,
  );

  // The description is the reason to have any of this: a task here is an
  // argument rather than a ticket, and a store that kept only subjects would
  // have thrown the useful half away.
  const longest = warm.toSorted((a, b) => b.description.length - a.description.length)[0];
  console.log(
    `  longest body  ${longest === undefined ? "none" : `${longest.description.length} chars on ${longest.id}`}`,
  );

  // The writing half, end to end and cleaned up after. Written, moved, tagged,
  // then forgotten — so running this twice does not leave a row behind in
  // somebody's board, which is the rule `probe:mcp` follows for the finding it
  // files.
  const made = yield* rpc.TaskAdd({
    subject: "probe: written by probe:tasks",
    description: "removed again before this probe exits",
    status: "pending",
    tags: ["project:awp"],
  });
  const moved = yield* rpc.TaskStatus({ id: made.id, status: "in_progress" });
  const held = yield* rpc.TaskTag({ id: made.id, tag: "probe", on: true });
  console.log(`  wrote         ${made.id}  [${moved.status}]  tags ${held.tags.join(", ")}`);

  // The refusal, against a row that came from a file. It is the whole reason
  // the panel's dot is a mark rather than a button for those, so a probe that
  // never saw it would be checking the easy half.
  const copied = warm.find((task) => task.source !== "awp");
  if (copied !== undefined) {
    const refused = yield* Effect.result(rpc.TaskStatus({ id: copied.id, status: "completed" }));
    console.log(
      `  refused       ${Result.isSuccess(refused) ? "NOT REFUSED — a copied task moved" : refused.failure.reason}`,
    );
  }

  yield* rpc.TaskForget({ id: made.id });
  const after = yield* rpc.TaskBoard({ tags: ["probe"] });
  console.log(`  cleaned up    ${after.length === 0 ? "yes" : `NO — ${after.length} left`}`);
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
