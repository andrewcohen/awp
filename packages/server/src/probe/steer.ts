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
// ── and the second scenario, which is about the QUEUED mark ────────────────
//
// An ordinary send is not a steer — `ChatSend.interrupt` defaults to false, so
// a message typed mid-turn goes as a plain `session/prompt` and the adapter's
// own queue holds it at priority `next`. The panel marks that message `queued`
// and clears the mark when a turn ends, under a comment claiming the turn that
// ended is the one it was waiting behind.
//
// That claim is a guess, and this measures it. Two messages are sent behind
// one slow turn, so the questions have answers a single message cannot give:
//
//   is a queued prompt ACCEPTED immediately, or held until the turn is over?
//     — which decides whether it can be taken back at all, and the answer
//       decides how much of "Up pops the queued message back" is even honest
//   does anything on the stream mark the moment the adapter DEQUEUES one?
//     — the adapter's `activateTurn` is internal and notifies nobody, so the
//       expectation is no. A `turn started` is emitted by the daemon when it
//       SENDS, not when the agent gets to it
//   do the two turns end in the order they were sent?
//     — the release rule can only be exact if they do
//
// ── and the third, which is about an end that never came ───────────────────
//
// Reported as "this thread is thinking but its not". Every ordinary end of a
// turn arrives as the reply to `session/prompt`; a killed adapter sends no
// reply, and `request` is an `Effect.callback` with no timeout, so the fiber
// holding it simply waits. The transcript then holds a `turn started` with no
// end after it — for the life of the conversation, and for every client that
// replays it, including one opened tomorrow.
//
//   does a `turn ended` arrive when the adapter is killed mid-turn?
//     — the only question here whose old answer was NO, and the only one a
//       fake cannot ask: the absence being measured is the absence of a
//       process
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
    // The key is the whole reason a second message's mark can be right: it
    // says WHOSE turn this edge is, which two ends in the same millisecond
    // cannot say for themselves.
    return `${when}  TURN     ${String(update.status).padEnd(8)} ${String(update.id ?? "—").padEnd(16)} ${update.stopReason ?? ""}`;
  }
  return `${when}  ${update.kind}`;
};

/** Two messages behind one slow turn, so "which turn ended" has an answer. */
const QUEUED = ["Say only the word lantern.", "Say only the word orchard."] as const;

/**
 * Drive one conversation and hand back every update, stamped.
 *
 * One conversation per scenario, never two scenarios in one — the second would
 * read the turns the first left running. The same rule as one page per gesture.
 */
const drive = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  dir: string,
  act: (chat: {
    readonly send: (
      text: string,
      key: string,
      interrupt: boolean,
    ) => Effect.Effect<string, unknown>;
    /** SIGKILL the adapter. See the third scenario. */
    readonly stop: Effect.Effect<void>;
  }) => Effect.Effect<void, unknown>,
) =>
  Effect.scoped(
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
      yield* Effect.ignore(chat.set("mode", "bypassPermissions"));
      yield* Effect.orDie(act(chat as never) as Effect.Effect<void>);
      return yield* Ref.get(collected);
    }),
  );

/** The turn edges as one string — `started → started → ended → ended`. */
const turnOrder = (seen: ReadonlyArray<Stamped>): string =>
  seen
    .filter((one) => one.update.kind === "turn")
    .map((one) => (one.update as { status?: string }).status)
    .join(" → ");

const report = (label: string, seen: ReadonlyArray<Stamped>) => {
  const started = seen[0]?.at ?? Date.now();
  console.log(`\n  ── ${label} ${"─".repeat(Math.max(0, 60 - label.length))}\n`);
  for (const one of seen) {
    console.log(`  ${line(started, one)}`);
  }
};

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  const dir = mkdtempSync(join(tmpdir(), "awp-steer-"));
  writeFileSync(join(dir, "notes.txt"), "the word is: heron\n");
  console.log(`\n  cwd         ${dir}\n`);

  // ── one: the steer, which is what this probe was written for ────────────
  const steered = yield* drive(spawner, dir, (chat) =>
    Effect.gen(function* () {
      yield* chat.send(SLOW, "probe-slow", false);
      // Long enough that the turn is certainly underway and the tool call is
      // running — a steer sent before the agent has started is not a steer.
      yield* Effect.sleep("12 seconds");
      console.log("  steering now\n");
      // `send` forks the prompt, so its own failure never reaches here — the
      // first version of this probe printed "accepted" for a request nobody
      // had waited on. The answer is in the updates: a refused steer ends its
      // turn with a reason, and a second `turn started` with no `turn ended`
      // after it is a window that says "working" for the rest of the session.
      // `true`, because this probe exists to measure what an interrupt does.
      const how = yield* chat.send(STEER, "probe-steer", true);
      console.log(`  delivered as  ${how}\n`);
      yield* Effect.sleep("60 seconds");
    }),
  );
  report("a steer, interrupt: true", steered);

  const echoes = steered.filter(
    (one) => one.update.kind === "message" && one.update.role === "user",
  );
  const order = turnOrder(steered);
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

  // ── two: two ordinary sends behind a slow turn ──────────────────────────
  //
  // This is the shape the `queued` mark is about, and the one nothing had ever
  // measured. A fresh conversation, because the one above has turns in it.
  const delivered: Array<string> = [];
  const queued = yield* drive(spawner, dir, (chat) =>
    Effect.gen(function* () {
      yield* chat.send(SLOW, "probe-slow", false);
      yield* Effect.sleep("12 seconds");
      console.log("  two ordinary sends now\n");
      for (const [index, text] of QUEUED.entries()) {
        const at = Date.now();
        const how = yield* chat.send(text, `probe-queued-${String(index)}`, false);
        // How long `send` took is the whole of the first question: a call that
        // returns at once has handed the message to the adapter, and a message
        // the adapter is holding cannot be taken back by this window.
        delivered.push(`${String(how)} in ${String(Date.now() - at)}ms`);
      }
      yield* Effect.sleep("90 seconds");
    }),
  );
  report("two ordinary sends, interrupt: false", queued);

  const queuedOrder = turnOrder(queued);
  const userEchoes = queued.filter(
    (one) => one.update.kind === "message" && one.update.role === "user",
  );
  // Every update between the second `turn started` and the first `turn ended`.
  // If anything in here named the queued message, that would be the dequeue
  // edge the release rule could be built on.
  const turns = queued.filter((one) => one.update.kind === "turn");
  const secondStart = turns[1]?.at ?? 0;
  const firstEnd =
    turns.find((one, i) => i > 0 && (one.update as { status?: string }).status === "ended")?.at ??
    0;
  const between = queued.filter((one) => one.at > secondStart && one.at < firstEnd);

  console.log(
    `\n  send answered             ${delivered.join(" · ")}` +
      `\n  turns                     ${queuedOrder}` +
      `\n  user chunks on the wire   ${String(userEchoes.length)} (the daemon's own echo of each send)` +
      `\n  updates between the 2nd start and the 1st end   ${String(between.length)}` +
      `\n    ${between.map((one) => one.update.kind).join(", ") || "(none)"}` +
      `\n\n  a dequeue edge            ${
        between.some((one) => one.update.kind === "turn")
          ? "SOMETHING — read the trace above"
          : "NONE"
      }` +
      `\n  which means               ${
        queuedOrder.startsWith("started → started → started")
          ? "all three turns are started at SEND time, so `turn started` says " +
            "nothing about when the agent got to a message. Ends are the only " +
            "readable edge, and the release rule has to be built on their ORDER"
          : queuedOrder
      }\n`,
  );
  // ── three: the adapter is killed while a turn is in flight ──────────────
  //
  // The one scenario here whose subject is not the adapter's behaviour but
  // this daemon's. Nothing comes back from a killed process, so the edge —
  // if there is one — is entirely the daemon's own.
  const killed = yield* drive(spawner, dir, (chat) =>
    Effect.gen(function* () {
      yield* chat.send(SLOW, "probe-killed", false);
      yield* Effect.sleep("8 seconds");
      console.log("  killing the adapter now\n");
      yield* chat.stop;
      // Generous: the edge is emitted from the reader's own finalizer, which
      // runs when stdout closes, and a pipe closing is not instantaneous.
      yield* Effect.sleep("10 seconds");
    }),
  );
  report("the adapter killed mid-turn", killed);

  const killedOrder = turnOrder(killed);
  const end = killed.find(
    (one) => one.update.kind === "turn" && (one.update as { status?: string }).status === "ended",
  );
  console.log(
    `\n  turns                     ${killedOrder}` +
      `\n  stopReason                ${
        (end?.update as { stopReason?: string } | undefined)?.stopReason ?? "(no end arrived)"
      }` +
      `\n  which means               ${
        killedOrder === "started → ended"
          ? "the turn ends when the process answering it does. A client folding " +
            "this transcript stops drawing work in progress"
          : "NO END — every client replaying this transcript says `working` " +
            "for the rest of the conversation, and a window opened tomorrow " +
            "says it too"
      }\n`,
  );

  rmSync(dir, { recursive: true, force: true });
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
