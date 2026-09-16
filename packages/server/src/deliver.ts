import type { Message } from "@awp-kit/protocol";
import { Effect, Option, Stream } from "effect";
import { Chat } from "./chat";
import { Messages } from "./messages";

// The push half of messaging, and the only part of it that runs on its own.
//
// ── why anything is pushed at all ──────────────────────────────────────────
// An inbox an agent has to remember to check is an inbox nobody checks: a model
// calls a tool when it decides to, and an agent deep in a task decides nothing.
// So something has to arrive. What arrives is a *nudge* — how many messages are
// waiting and the tool that fetches them — never the bodies.
//
// That split is the whole design and it is about authority, not size. ACP has
// one `user` role, so a body injected as a turn wears the operator's face, and
// the recipient cannot tell a peer's "run the migration" from the same words
// typed by the person running the window. Fetched through `awp_messages` the
// bodies arrive as tool output, which every model already reads as data from a
// system. See `Message` in the contract.
//
// ── waiting, not interrupting ──────────────────────────────────────────────
// A message never cuts a turn short. Decided directly: interrupting is faster
// and can wreck a long edit that had nothing to do with the message, and the
// cost of waiting — an agent that finds out thirty minutes late — is a delay
// rather than damage. `Chat.statuses()` is the idle signal, and its shape is
// what makes this cheap: a workspace with nothing running is *absent* from the
// map rather than present with an idle status.
//
// ── two triggers, one sweep ────────────────────────────────────────────────
// A status change is not enough on its own. A workspace that is already idle
// when a message arrives never transitions, so it would wait for an unrelated
// edge — which on a quiet machine is never. And a send is not enough either,
// because the usual case is a recipient that is busy at that moment.
//
//   a status changed   somebody may have just gone idle
//   a message was sent somebody may already be idle
//
// Both run the same sweep, and the sweep is idempotent: it reads what is
// waiting, nudges, and marks. Running it twice over the same message nudges
// once, because `notified` is what the second read filters on.

/**
 * What the recipient is told.
 *
 * The count and the tool, and nothing else. No sender, no subject, no first
 * line — every one of those is a piece of the body arriving through the channel
 * this design exists to keep the body out of, and a summary composed here is
 * also the lossy copy the thread's own store already holds properly.
 *
 * The tool name is spelled out because an agent that has not called it yet has
 * only the tool list to find it in, and an instruction it can act on without
 * searching is the difference between a nudge and an interruption.
 */
export const nudge = (waiting: number): string =>
  waiting === 1
    ? "You have 1 new message from another checkout in this thread. Call awp_messages to read it."
    : `You have ${waiting} new messages from other checkouts in this thread. Call awp_messages to read them.`;

/** The waiting messages, grouped by who they are for, oldest recipient first. */
export const byRecipient = (
  waiting: ReadonlyArray<Message>,
): ReadonlyArray<{
  readonly project: string;
  readonly workspace: string;
  readonly ids: ReadonlyArray<string>;
}> => {
  const groups = new Map<string, { project: string; workspace: string; ids: string[] }>();
  for (const message of waiting) {
    const key = `${message.to.project}/${message.to.workspace}`;
    const found = groups.get(key);
    if (found === undefined) {
      groups.set(key, {
        project: message.to.project,
        workspace: message.to.workspace,
        ids: [message.id],
      });
    } else {
      found.ids.push(message.id);
    }
  }
  return [...groups.values()];
};

/**
 * One pass: nudge everybody who is idle and has mail they have not heard about.
 *
 * ── marked before the turn is waited on ────────────────────────────────────
 *
 * `Chat.brief` holds until the agent has finished answering, which for a nudge
 * means until the agent has read its inbox and acted — minutes. Marking after
 * that would leave the message un-nudged for the whole of it, and the next
 * sweep would nudge again, and the one after that. Marking first costs a
 * message that is never re-nudged if the brief fails outright, which is the
 * lesser of the two: a failed brief leaves the row unread and visible in the
 * viewer, and the next message to that workspace nudges for all of them.
 *
 * Failures are swallowed per recipient rather than ending the sweep. One
 * workspace whose adapter will not start must not stop another's mail.
 */
export const sweep = Effect.gen(function* () {
  const messages = yield* Messages;
  const chat = yield* Chat;

  const waiting = yield* messages.waiting().pipe(Effect.orElseSucceed(() => []));
  if (waiting.length === 0) {
    return;
  }

  // Absent means idle — see the note at the top. Taken once for the whole
  // sweep, so every recipient is judged against one reading rather than
  // against a map that moves underneath the loop.
  const busy = yield* Stream.runHead(chat.statuses()).pipe(
    Effect.map(Option.getOrElse(() => new Map<string, unknown>() as ReadonlyMap<string, unknown>)),
    Effect.orElseSucceed(() => new Map<string, unknown>() as ReadonlyMap<string, unknown>),
  );

  for (const group of byRecipient(waiting)) {
    if (busy.has(`${group.project}/${group.workspace}`)) {
      continue;
    }
    yield* messages.notified(group.ids).pipe(Effect.ignore);
    yield* chat.brief(group.project, group.workspace, nudge(group.ids.length)).pipe(Effect.ignore);
  }
});

/**
 * Run the sweep whenever anything might have made it worth running.
 *
 * Forked by the daemon and never awaited. Both streams are already the
 * daemon's own — no polling, and nothing here wakes up on a timer to ask a
 * question whose answer has not changed.
 */
export const deliverer = Effect.gen(function* () {
  const messages = yield* Messages;
  const chat = yield* Chat;

  // Merged rather than two fibers, so two edges arriving together run the
  // sweep twice in sequence instead of twice at once — which matters because
  // `brief` holds a conversation open and a second sweep would open a second
  // one for the same workspace.
  const edges = Stream.merge(
    chat.statuses().pipe(Stream.map(() => undefined)),
    messages.changes().pipe(Stream.map(() => undefined)),
  );
  yield* Stream.runForEach(edges, () => sweep.pipe(Effect.ignore));
});
