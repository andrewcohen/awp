// When the adapter is killed, what else dies with it?
//
// ── the report this answers ────────────────────────────────────────────────
//
// An agent asked to start something long-lived — a dev server, a watcher —
// starts it inside its own turn, and it dies. `chat.ts` already explains why
// the *adapter* goes: `RcMap` releases a conversation two minutes after its
// last reference, the reference is the chat panel's subscription, and Base UI
// unmounts a hidden tab. What none of that says is how far the killing
// reaches, and the answer decides which of two problems is being looked at:
//
//   the kill reaches down      anything an agent starts is doomed the moment
//                              the turn it was started in ends. The cure is a
//                              process the daemon owns — a zmx session — and
//                              no amount of holding the adapter open helps
//   the kill stops at the      the grandchild is orphaned and keeps running,
//   direct child               so a dying dev server is somebody else's doing
//                              — Claude Code's own command timeout, most
//                              likely — and the cure is somewhere else
//
// Guessing between those two is how an afternoon goes missing, so this asks.
//
// ── why sh and not the adapter ─────────────────────────────────────────────
//
// The question is about `ChildProcessSpawner`, which is what `conversation`
// spawns the adapter with, and `sh` exercises exactly the same kill. Spawning
// a real adapter would add a model, a workspace and several seconds to a
// measurement that would still be about this one call — and would answer less
// clearly, because a real agent's children come and go for their own reasons.
//
// The shape mirrors what an agent does: a child that backgrounds something and
// then keeps running, which is `bun run dev &` inside a turn.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// Two `sh` processes and a `ps`. It never invokes zmx, never attaches, never
// names a session, and every process it starts is one it started itself.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { execFileSync } from "node:child_process";

/** How long the planted processes would live if nothing killed them. */
const LINGER = 300;

/** Long enough for a signal to be delivered and reaped, short enough to watch. */
const SETTLE = 500;

/**
 * A process's group, or nothing if it is gone.
 *
 * Asked *before* the kill, because it is what turns the outcome into a reason:
 * POSIX does not kill an orphan when its parent dies, so a backgrounded
 * grandchild going with the adapter means the signal went to a **group**. And
 * if the child's group is its own pid, the spawner made that group when it
 * spawned — which is the thing a caller would have to opt out of.
 */
const groupOf = (pid: number): string => {
  try {
    return execFileSync("ps", ["-o", "pgid=", "-p", String(pid)], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
};

const alive = (pid: number): boolean => {
  try {
    // `ps -p` rather than `kill -0`: a zombie answers to the signal and is not
    // running, and what is being asked here is whether the thing kept going.
    const out = execFileSync("ps", ["-o", "stat=", "-p", String(pid)], {
      encoding: "utf8",
    }).trim();
    return out !== "" && !out.startsWith("Z");
  } catch {
    return false;
  }
};

const pause = (ms: number): Promise<void> => new Promise((done) => setTimeout(done, ms));

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  // The scope is the process, exactly as it is for the adapter: leaving it is
  // what kills the child. Held in a variable so the grandchild's pid can be
  // read out before the scope closes.
  const pids = yield* Effect.scoped(
    Effect.gen(function* () {
      const handle = yield* spawner.spawn(
        ChildProcess.make("/bin/sh", [
          "-c",
          // Backgrounded and then waited on, which is the shape that matters:
          // the child is still running when the kill arrives, and the
          // grandchild is not its foreground job.
          `/bin/sh -c 'sleep ${String(LINGER)}' & echo grand:$!; echo child:$$; sleep ${String(LINGER)}`,
        ]),
      );

      // Into an array rather than a fold. The child prints two lines and then
      // sleeps, so the stream never ends on its own and something has to cut it
      // short — and `Effect.timeout` around a fold **interrupts** it, which
      // throws the accumulator away and reports nothing arriving. The array is
      // outside the effect, so what arrived is still here.
      const chunks: Array<string> = [];
      yield* Stream.runForEach(Stream.decodeText(handle.stdout), (chunk) =>
        Effect.sync(() => chunks.push(chunk)),
      ).pipe(Effect.timeout("3 seconds"), Effect.ignore);
      const seen = chunks.join("");

      const child = /child:(?<pid>\d+)/u.exec(seen)?.groups?.["pid"];
      const grand = /grand:(?<pid>\d+)/u.exec(seen)?.groups?.["pid"];
      return {
        child: Number(child),
        grand: Number(grand),
        seen,
        // Read inside the scope: after it closes there is nothing left to ask.
        childGroup: groupOf(Number(child)),
        grandGroup: groupOf(Number(grand)),
      };
    }),
  );

  if (!Number.isFinite(pids.child) || !Number.isFinite(pids.grand)) {
    console.log(`\n  the child said nothing usable: ${JSON.stringify(pids.seen)}\n`);
    return 1;
  }

  // The scope has closed, so the child has been killed. Everything after this
  // is about what the kill did *not* reach.
  yield* Effect.promise(() => pause(SETTLE));

  const childLives = alive(pids.child);
  const grandLives = alive(pids.grand);

  console.log("");
  console.log(`  this probe   pid ${String(process.pid).padEnd(8)} group ${groupOf(process.pid)}`);
  console.log(
    `  child        pid ${String(pids.child).padEnd(8)} group ${pids.childGroup.padEnd(8)} ${childLives ? "STILL RUNNING" : "gone"}`,
  );
  console.log(
    `  grand        pid ${String(pids.grand).padEnd(8)} group ${pids.grandGroup.padEnd(8)} ${grandLives ? "STILL RUNNING" : "gone"}`,
  );
  console.log("");

  if (childLives) {
    // Nothing else here is meaningful if the direct child survived: the scope
    // did not kill what it owns, which is a bigger finding than the one being
    // measured.
    console.log("  the scope did not kill its own child. Nothing below follows from this.\n");
    if (grandLives) {
      execFileSync("kill", ["-9", String(pids.grand)], { stdio: "ignore" });
    }
    execFileSync("kill", ["-9", String(pids.child)], { stdio: "ignore" });
    return 1;
  }

  if (grandLives) {
    console.log("  the kill stops at the direct child — a grandchild is orphaned, not killed.");
    console.log("  So a process an agent backgrounds outlives the adapter, and a dev server");
    console.log("  that dies is dying of something else.\n");
    // Left running would be a probe that litters, which is the one thing a
    // probe about stray processes must not do.
    execFileSync("kill", ["-9", String(pids.grand)], { stdio: "ignore" });
    return 0;
  }

  console.log("  the kill reaches the whole tree — a grandchild goes with the adapter.");
  console.log("  So anything an agent starts is doomed when the conversation is released,");
  console.log("  and only a process the daemon owns outright survives it.");
  if (pids.childGroup !== "" && pids.childGroup === String(pids.child)) {
    console.log("");
    console.log("  And the reason is the group: the child leads one of its own, so the");
    console.log("  signal went to every process in it. An orphan is not killed by its");
    console.log("  parent dying — a group is.");
  }
  console.log("");
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
