// What happens to a message typed while the agent is already working?
//
// Steering is the most valuable thing a person does in this window — a "no,
// not that file" while a turn is in flight — and it was reported as arriving in
// the wrong place. Every candidate cause is a fact about a real adapter that no
// fake can answer:
//
//   does a second session/prompt mid-turn succeed, or is it refused?
//   does the adapter echo the steer back as a user_message_chunk?
//   how many `turn` updates arrive, and does the FIRST turn's end arrive
//   while the second is still running?
//
// The last one is the one that decides whether `running` can be a boolean.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// A temporary directory and one file in it. It never invokes zmx, never
// attaches and never names a session.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Ref, Stream } from "effect";
import { ChildProcessSpawner } from "effect/unstable/process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ChatUpdate } from "@awp-kit/protocol";
import { conversation } from "../chat";

/** Long enough to still be running when the steer is sent. */
const SLOW =
  "Using Bash, run `for i in 1 2 3 4 5; do echo $i; sleep 3; done` and then say what it printed.";

const STEER = "Actually stop — forget the counting and just say the word heron.";

interface Stamped {
  readonly at: number;
  readonly update: ChatUpdate;
}

/** One update as a line, in arrival order, with the offset it arrived at. */
const line = (started: number, { at, update }: Stamped): string => {
  const when = `${String(Math.round((at - started) / 100) / 10).padStart(6)}s`;
  if (update.kind === "message") {
    const text = (update.text ?? "").replaceAll("\n", " ").trim().slice(0, 48);
    return `${when}  ${String(update.role).padEnd(8)} "${text}"`;
  }
  if (update.kind === "tool") {
    return `${when}  tool     ${String(update.status ?? "").padEnd(11)} ${String(update.title ?? "").slice(0, 40)}`;
  }
  if (update.kind === "turn") {
    return `${when}  TURN     ${update.status} ${update.stopReason ?? ""}`;
  }
  return `${when}  ${update.kind}`;
};

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  const dir = mkdtempSync(join(tmpdir(), "awp-steer-"));
  writeFileSync(join(dir, "notes.txt"), "the word is: heron\n");
  console.log(`\n  cwd         ${dir}\n`);

  const seen = yield* Effect.scoped(
    Effect.gen(function* () {
      const chat = yield* conversation(spawner, { cwd: dir, model: "sonnet" });
      const updates = yield* chat.updates;
      const collected = yield* Ref.make<ReadonlyArray<Stamped>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) =>
            Ref.update(collected, (all) => [...all, { at: Date.now(), update }]),
          ),
        ),
      );

      // A mode that does not stop to ask. The first run of this probe sat on a
      // permission request in Manual mode for the whole sixty seconds and
      // measured nothing at all — a turn that is waiting for a person is not a
      // turn that is working, and steering is about the second one.
      const before = yield* chat.config;
      console.log(
        `  modes       ${before
          .find((one) => one.id === "mode")
          ?.values.map((one) => one.value)
          .join(" · ")}\n`,
      );
      yield* Effect.ignore(chat.set("mode", "bypassPermissions"));

      yield* chat.send(SLOW, "probe-slow");
      // Long enough that the turn is certainly underway and the tool call is
      // running — a steer sent before the agent has started is not a steer.
      yield* Effect.sleep("12 seconds");
      console.log("  steering now\n");
      // `send` forks the prompt, so its own failure never reaches here — the
      // first version of this probe printed "accepted" for a request nobody
      // had waited on. The answer is in the updates: a refused steer ends its
      // turn with a reason, and a second `turn started` with no `turn ended`
      // after it is a window that says "working" for the rest of the session.
      const how = yield* chat.send(STEER, "probe-steer");
      console.log(`  delivered as  ${how}\n`);
      yield* Effect.sleep("60 seconds");
      return yield* Ref.get(collected);
    }),
  );

  rmSync(dir, { recursive: true, force: true });

  const started = seen[0]?.at ?? Date.now();
  for (const one of seen) {
    console.log(`  ${line(started, one)}`);
  }

  const echoes = seen.filter((one) => one.update.kind === "message" && one.update.role === "user");
  const turns = seen.filter((one) => one.update.kind === "turn");
  // What the turn sequence says, and it is the whole point of the probe: only
  // a real adapter decides whether a mid-turn message can be steered into the
  // turn already running, and it decides atomically — its own comment says the
  // check and the push "stay in one synchronous section so the turn cannot
  // settle in the gap between deciding to inject and enqueueing".
  const order = turns.map((one) => (one.update as { status?: string }).status).join(" → ");
  console.log(
    `\n  user chunks echoed back   ${String(echoes.length)}` +
      `\n  turns                     ${order}` +
      `\n  which means               ${
        order === "started → ended"
          ? "ONE turn — the steer went into the one already running"
          : order.startsWith("started → started")
            ? "two turns — the steer queued behind the first, so the panel " +
              "has to say so and keep the reply above it"
            : order
      }\n`,
  );
  return 0;
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
