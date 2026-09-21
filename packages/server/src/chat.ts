// A conversation with an agent, over ACP rather than down a pty.
//
// The terminal reaches an agent by writing bytes at a program that draws a
// picture of a conversation. This reaches the conversation itself: a text
// chunk arrives labelled a text chunk, a tool call arrives as a tool call with
// a status that later changes, and a turn ends with a reason. What the window
// draws is a record rather than a rectangle of ANSI.
//
// ── a session is a file, not a process ────────────────────────────────────
//
// Measured 2026-08-28, and it is the fact the whole design rests on. The
// adapter was SIGKILLed twelve seconds into a running Bash loop; a fresh
// process, given the same session id, replayed the history and answered from
// it:
//
//   descendants of the dead pid   0        nothing was orphaned
//   the loop                      killed with it
//   session/load in a new process replayed, and remembered a word given
//                                 to the process that had died
//
// So there is nothing here to keep alive across a daemon restart, and nothing
// to daemonize under zmx — a pty is the wrong pipe for line-delimited JSON-RPC
// anyway. What a restart costs is the turn that was in flight, which the
// transcript records as a tool call that started and never completed. That is
// the honest account and it is the one the agent itself gives when asked.
//
// ── load, resume, fork ────────────────────────────────────────────────────
//
//   session/load     replays the history as updates, then continues   ← here
//   session/resume   replays nothing, remembers everything
//   session/fork     a new id, the same memory, the original untouched
//
// `load` is what opening a window on a conversation means: the same update
// shape a live turn uses, so one renderer draws the history and the present
// without knowing which it is looking at.
//
// ── the mode is chosen, because the default is not ours to accept ─────────
//
// `session/new` opens in `auto` — "use a model classifier to approve/deny
// permission prompts". Measured in `default` (Manual): reading a file was not
// referred to this client at all, and `rm` was, with reject_once, allow_once
// and allow_always. So a session amoeba opens on somebody's repository is put
// in Manual and the refusals are shown to a person. Leaving it at `auto` would
// mean nobody in this window is ever asked.

import { Db, type Migration, attempt } from "@awp-kit/store";
import {
  BEAT_EVERY,
  type Claims,
  claimMigration,
  claims as claimsOn,
  heldOutside,
  outsideHolders,
} from "./session-claim";
import {
  Context,
  Data,
  Deferred,
  Duration,
  Effect,
  Exit,
  Fiber,
  Layer,
  Option,
  Queue,
  RcMap,
  Ref,
  Result,
  SubscriptionRef,
  type Scope,
  Stream,
} from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { realpathSync } from "node:fs";
import { createTwoFilesPatch } from "diff";
import type {
  ChatCommand,
  ChatConfigOption,
  ChatDelivery,
  ChatDiff,
  ChatUpdate,
  WorkspaceStatus,
} from "@awp-kit/protocol";
import { INSTALL, adapterPath, claudePath, parseMessage } from "./acp";
import { workspacePath } from "./jobs/create-workspace";
import { daemonUrl, mcpEntry, serverSpec } from "./mcp";
import { Settings } from "./settings";
import { childEnv } from "./zmx-session";

/**
 * How long {@link settledWhen} waits, in the daemon.
 *
 *   startsWithin  a turn that never begins would wait forever. An adapter that
 *                 accepted the prompt and did nothing with it is a real thing
 *                 — the whole reason `send` reports how it was delivered — and
 *                 nothing should hang on one
 *   holdsFor      absent, and that is the decision. **A turn in progress is
 *                 never killed.**
 *
 * ── what the bound used to be, and what it cost ────────────────────────────
 *
 * It was 20 minutes, under the reasoning that "a turn that runs for an hour is
 * the agent doing what it was asked, and a job has no business holding a step
 * open that long — giving up is not a failure: the transcript is on disk".
 * The first half is right about the daemon and the second is wrong about the
 * agent, because of *which* transcript is on disk:
 *
 *   disk         the Edit ran. a.ts is changed
 *   transcript   the call that made the change, and no result after it —
 *                the process that would have written one was killed
 *
 * A fresh `claude --resume` reads both and they only reconcile one way: some
 * other writer touched this file. It says so, and every long refactor produces
 * a handful at once, which reads as an agent working alongside it. There is no
 * other writer. There is one agent, killed after it wrote to disk and before
 * it wrote down that it had.
 *
 * So the cap did not trade a held adapter for a lost turn — it traded a held
 * adapter for a **transcript that disagrees with the working copy**, and a
 * model reasoning from the disagreement.
 *
 * The bound this replaces is not a clock: the status is cleared when the
 * adapter's update stream ends, so a turn cannot be "in progress" on a process
 * that has gone. What remains pinnable is a *live* adapter whose `ended` was
 * missed — a leak of one held conversation, which is a thing to fix where the
 * edge is counted rather than to paper over with a timer that corrupts.
 */
export const WAITS = { startsWithin: "30 seconds" } as const;

/**
 * How long a settled conversation is minded for, in case another turn is next.
 *
 * A queued message is promoted by the adapter the instant the turn it arrived
 * during ends, and nothing announces it — so the turn it starts would begin
 * unheld. See `mindUntilSettled`. Three seconds is an adapter's own latency
 * and nothing like a person's: a conversation somebody has actually finished
 * with is released three seconds later than it was, against a TTL of two
 * minutes.
 */
export const PROMOTES_WITHIN = "3 seconds";

/**
 * Hold on until a turn has started and then finished.
 *
 * Takes a reading rather than the ref, which is what makes the two bounds
 * testable at all: the real one is a `SubscriptionRef` fed by an adapter, and
 * there is no adapter in a test.
 *
 * ── polled, deliberately ─────────────────────────────────────────────────
 *
 * `statuses` has a change stream and this does not use it, because what is
 * wanted is a *settled* reading and the stream is a stream of edges. The
 * status is absent both before a turn starts and after it ends, so an
 * edge-driven wait either returns instantly on the reading it began with or
 * has to reason about which absence it is looking at. Two reads a second for a
 * few minutes costs nothing measurable.
 *
 * **Neither bound fails.** This is a keepalive, not a verification — whatever
 * was being said has been said by the time this is called, and a timeout that
 * failed would fail a job whose work is done.
 */
export const settledWhen = (
  busy: Effect.Effect<boolean>,
  waits: { readonly startsWithin: Duration.Input; readonly holdsFor?: Duration.Input },
): Effect.Effect<boolean> =>
  Effect.gen(function* () {
    const until = (wanted: boolean) =>
      Effect.andThen(Effect.sleep("100 millis"), busy).pipe(
        Effect.repeat({ until: (running) => running === wanted }),
      );

    // `Option.isNone` rather than a tag check, and this is the case that
    // matters: no turn ever started, so there is nothing to hold open for and
    // waiting the whole window would be a step asleep for twenty minutes over
    // an adapter that ignored what it was told.
    if (Option.isNone(yield* Effect.timeoutOption(until(true), waits.startsWithin))) {
      return false;
    }
    // No bound unless a caller asks for one. See WAITS: the turn ending is the
    // only thing that ends this, and a clock here kills an agent mid-edit.
    yield* waits.holdsFor === undefined
      ? until(false)
      : Effect.timeoutOption(until(false), waits.holdsFor);
    // Whether there was anything to wait for. The caller minding a live
    // conversation asks again on a `true`, because a queue can hold more than
    // one message — see `mindUntilSettled`.
    return true;
  });

/**
 * Settled, and still settled a moment later.
 *
 * ── a queued message is a SECOND turn, and the hold was armed for the first ─
 *
 * `alone` is right for a **steer**: it joins the turn already running, so the
 * holder minding that turn is minding the steer too. A queued message is the
 * other thing entirely — the adapter keeps it, ends the turn it arrived
 * during, then promotes its queue head and notifies nobody. So {@link
 * settledWhen} returns on a conversation that is about to be busy again, the
 * holder lets the key go on the way out, and that second turn runs held by
 * nothing but whatever window happens to be subscribed.
 *
 * A window is free to look away. `watchChat` is keyed on the workspace, so
 * switching threads tears `ChatOpen` down, and `idleTimeToLive` then kills an
 * adapter that is mid-turn. Measured on this daemon on 2026-09-18: an adapter
 * respawned at the second a tool call died, with no `stranded` warning in the
 * log — released cleanly, with a turn in flight.
 *
 * So the hold ends on a conversation that stays quiet rather than on the first
 * quiet reading. Each pass is the minding of whatever turn it found, and the
 * first pass that finds none is the one that ends it — which is also what
 * makes a queue of several messages one hold rather than a race per message.
 *
 * Exported for its test: the composition is the part that was wrong, and a
 * test that rebuilt it out of the pieces would be testing its own copy.
 */
export const untilQuiet = (
  busy: Effect.Effect<boolean>,
  waits: { readonly startsWithin: Duration.Input; readonly holdsFor?: Duration.Input },
  promotesWithin: Duration.Input,
): Effect.Effect<void> =>
  Effect.gen(function* () {
    yield* settledWhen(busy, waits);
    let again = true;
    yield* Effect.whileLoop({
      while: () => again,
      body: () => settledWhen(busy, { startsWithin: promotesWithin }),
      step: (found) => {
        again = found;
      },
    });
  });

/** Anything that stopped a conversation being had. */
export class ChatError extends Data.TaggedError("ChatError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * Manual, and said out loud rather than left to the default.
 *
 * See the note at the top: the default is a model approving tool calls on this
 * client's behalf, which is not a thing to inherit by saying nothing.
 */
/**
 * The tool calls in a transcript that nothing has finished.
 *
 * A patch keyed by id, folded to the last status each id was given. An id
 * that was mentioned and never given one at all counts as hanging: the
 * adapter opens a call with `status: "pending"` and can simply stop.
 *
 * Pure, and exported, because the Effect around it has a `Ref` and a queue
 * in it and this is the part that can be wrong.
 */
export const hanging = (updates: ReadonlyArray<ChatUpdate>): ReadonlyArray<string> => {
  const last = new Map<string, string>();
  for (const update of updates) {
    if (update.kind !== "tool" || update.id === undefined) continue;
    if (update.status !== undefined) last.set(update.id, update.status);
    else if (!last.has(update.id)) last.set(update.id, "");
  }
  return [...last].filter(([, status]) => !ENDED.has(status)).map(([id]) => id);
};

/**
 * One workspace's status written into the table — **the same table back when
 * nothing moved**, and that identity is the whole economy of the facts feed.
 *
 * `SubscriptionRef` publishes on every write, not on every change, and this is
 * written once per streamed chunk. `Chat.statuses` is `Stream.changes` over
 * those writes, and `Equal.equals` on a plain `Map` is `===`. So a fresh copy
 * here would be correct, would pass every test about what the map contains,
 * and would silently re-send the whole workspace table to every window
 * seventeen times a second. docs/daemon.md has the measurement.
 *
 * Exported for the test that removing the early return has to fail.
 */
export const withStatus = (
  all: ReadonlyMap<string, WorkspaceStatus>,
  key: string,
  status: WorkspaceStatus | undefined,
): ReadonlyMap<string, WorkspaceStatus> => {
  if (all.get(key) === status) {
    return all;
  }
  const next = new Map(all);
  if (status === undefined) {
    next.delete(key);
  } else {
    next.set(key, status);
  }
  return next;
};

/** The statuses that mean a call is over, whatever it did. */
const ENDED = new Set(["completed", "failed", "cancelled"]);

export const MODE = "default";

/** What a turn is asked to run as. */
export interface ChatOptions {
  readonly cwd: string;
  readonly model?: string;
  /**
   * The reasoning effort, when the config names one.
   *
   * Set through the adapter's own config option rather than in the open
   * request's `_meta`, and that is not a preference: `configOptions` is what
   * the reply carries and what the panel draws, so setting it any other way
   * would leave the chips reporting the opposite of the truth — which already
   * happened once with the mode. See the note below.
   */
  readonly effort?: string;
  /**
   * The permission mode, when the config names one.
   *
   * Absent means Manual — see {@link MODE}. This is the field that lets
   * somebody opt out of being asked, which is a decision worth writing down in
   * a file rather than inheriting from an adapter's default.
   */
  readonly mode?: string;
  /**
   * The session to continue, when the daemon has a record of one.
   *
   * **Nothing is loaded that is not named here**, and that is the whole of the
   * rule. `session/list` for a workspace directory answers with every session
   * ever held in it — including the one the *terminal's* `claude` is running
   * right now, which is normally the newest. Taking the newest and loading it
   * makes the ACP side a second writer on a transcript an interactive agent is
   * still appending to, and neither process knows about the other.
   *
   * So an unknown session means a new one, never somebody else's. The list is
   * still asked for, but only to check that this id is really there: a
   * transcript deleted or moved would otherwise fail inside `session/load`
   * with a sentence about a session id, one step after the mistake.
   */
  readonly session?: string;
  /**
   * The last context reading this conversation is known to have had.
   *
   * ── a loaded conversation reports no usage until it is spoken to ─────────
   *
   * Measured against a real adapter: a live turn sends four `usage_update`s,
   * and `session/load` followed by silence sends **none**. So the ordinary
   * case — open a chat, read what the agent said last night, say nothing —
   * has no reading at all, and the composer's context figure was absent
   * exactly when somebody was deciding whether to carry on in it.
   *
   * There is no call that asks, so the daemon remembers instead: the reading
   * is stored per session id and handed back here, and `conversation` emits it
   * as its first update so every subscriber and every replay sees it. Tokens
   * do not change while nobody is talking, which is what makes a stored
   * reading still true.
   */
  readonly usage?: { readonly used: number; readonly size: number };
  /**
   * Start by forking whatever else is going on in this directory.
   *
   * "Open the terminal's conversation in the chat", and it has to happen
   * *here*, inside the process that will hold the conversation. Measured with
   * `probe:chat`: a session forked seconds ago is not in `session/list`, and
   * `session/load` on it from another process fails —
   *
   *   forked to        2392409f-…
   *   in the listing   NO
   *   opened the fork  NO — fell back to 715cd9c7-…
   *
   * — so a design that forked in one place, wrote the id down and opened it
   * somewhere else got a brand new empty session, which from outside is
   * indistinguishable from a fork that carried no memory. That was the first
   * shape and this is the finding that killed it.
   */
  readonly fork?: boolean;
}

interface Pending {
  readonly reply: (message: Record<string, unknown>) => Effect.Effect<void>;
}

/**
 * An update, numbered.
 *
 * The number is what makes a late subscriber correct without a lock. Somebody
 * opening a window takes the transcript so far and then reads the live feed,
 * and an update that landed between those two steps would otherwise arrive
 * twice — once in the history and once down the queue. Registering first and
 * dropping anything already in the snapshot is exact, and needs no mutual
 * exclusion in a runtime that has none to offer.
 */
interface Numbered {
  readonly seq: number;
  readonly update: ChatUpdate;
}

interface Conversation {
  /**
   * The session this conversation is on.
   *
   * Read by the caller and written down, so the next open continues this one
   * rather than starting again beside it — or, worse, joining a session that
   * belongs to something else.
   */
  readonly sessionId: string;
  /**
   * Completes when the adapter has stopped answering, however it stopped.
   *
   * The reader's end is the one place this side can know, and until now all it
   * did with that knowledge was fail the requests that were waiting. The
   * conversation itself stayed in the `RcMap` — so the next message opened a
   * fresh stream against a dead process and the sender was told nothing.
   *
   * Also completes when this scope closes, because a killed adapter is an
   * adapter that stopped. Whoever waits on it has to tell those apart itself;
   * `generations` below is how.
   */
  readonly gone: Effect.Effect<void>;
  /** The history so far, then everything that happens next. */
  readonly updates: Effect.Effect<Stream.Stream<ChatUpdate>, never, Scope.Scope>;
  /**
   * Say something. Returns as soon as it has been delivered, not when the
   * agent has finished — and says which way it went, because the two are a
   * different thing to a person watching:
   *
   *   steer    injected into the turn already running. Being handled now
   *   prompt   a turn of its own, which is what a message sent to an idle
   *            agent is, and what a steer becomes on an agent that cannot
   *            be steered
   *   queued   held, because a compaction is running. Sent when it is over
   */
  readonly send: (
    text: string,
    key: string,
    /** Stop the agent where it is. See `ChatSend.interrupt`. */
    interrupt: boolean,
  ) => Effect.Effect<ChatDelivery, ChatError>;
  /**
   * Every session the adapter sees in this directory.
   *
   * Only the probe asks. It is here because "did the fork appear in the
   * listing" is a question about the adapter that nothing else can answer, and
   * the answer decides whether opening by id loads the fork or quietly starts
   * a new session beside it.
   */
  readonly sessions: () => Effect.Effect<ReadonlyArray<string>, ChatError>;
  /**
   * Kill the adapter process where it stands.
   *
   * Two callers, and they want it for the same reason from opposite ends.
   * `retire` needs it because **the scope is not a way to end this process**:
   * `RcMap.invalidate` leaves a held entry's scope open, so an adapter nobody
   * can reach any longer keeps running unless something signals it. SIGKILL
   * reaches the whole group, which is what makes `/new` take the agent's
   * children with it — `probe:child-tree`.
   *
   * The probe asks the one question no fake can answer: **what does a turn do
   * when the process answering it goes away?** Every ordinary end — a reply, a
   * refusal, a cancel — comes back through `session/prompt`. A killed adapter
   * sends nothing at all, which is an absence rather than an edge, and an
   * absence is what left a conversation saying `working` for the rest of its
   * life.
   *
   * Not `cancel`, which is the adapter being asked nicely and answering. This
   * is the adapter not being there.
   */
  readonly stop: Effect.Effect<void>;
  /** Answer a permission request by the id the update carried. */
  /**
   * Stop the turn that is running, if one is.
   *
   * A notification and not a request: `session/cancel` has no reply, and the
   * turn's own `session/prompt` is what answers — with a `stopReason` saying
   * it was cancelled, through the fork that already emits `turn ended`. So
   * nothing here waits, and nothing here emits: the edge a client draws is
   * the same edge every other ending produces.
   */
  readonly cancel: Effect.Effect<void, ChatError>;

  readonly answer: (request: string, option: string) => Effect.Effect<void, ChatError>;
  /** What this session is running as, and what it could be running as instead. */
  readonly config: Effect.Effect<ReadonlyArray<ChatConfigOption>, ChatError>;
  /** Change one, and get the whole set back as it now stands. */
  readonly set: (
    option: string,
    value: string,
  ) => Effect.Effect<ReadonlyArray<ChatConfigOption>, ChatError>;
}

/** The text of a content block, if it is text. */
const textOf = (content: unknown): string | undefined => {
  const block = content as Record<string, unknown> | undefined;
  return block?.["type"] === "text" && typeof block["text"] === "string"
    ? block["text"]
    : undefined;
};

/** How much unchanged code is drawn either side of a change. */
const CONTEXT = 3;

/**
 * A side of the change, ending in a newline.
 *
 * ── the marker that crashes a renderer ────────────────────────────────────
 *
 * An `Edit`'s two sides are `old_string` and `new_string`, which are a
 * *fragment* of a file and therefore almost never end in a newline. jsdiff
 * says so in the patch, correctly, with the marker git uses:
 *
 *   \ No newline at end of file
 *
 * and `@pierre/diffs` throws on it, from inside its renderer rather than its
 * parser — the patch parses, and then:
 *
 *   DiffHunksRenderer.processDiffResult: deletionLine and additionLine are
 *   null, something is wrong
 *
 * which the agent column's error boundary catches, so the whole column is
 * replaced by a stack trace for an edit that worked. Measured: three of the
 * five shapes an edit takes produce the marker, including every ordinary
 * `Edit`, so this is the common case rather than an edge.
 *
 * A newline is added rather than the marker stripped, because the marker is
 * jsdiff telling the truth about what it was handed. What is wrong is the
 * question: a fragment has no "end of file" to be missing a newline at. An
 * empty side stays empty — that is a `Write`'s absent old text, and giving it
 * a newline would invent a line to delete.
 */
const ends = (text: string): string => (text === "" || text.endsWith("\n") ? text : `${text}\n`);

/**
 * What a tool changed, as a patch rather than as two whole texts.
 *
 * The adapter reports an edit as `{type: "diff", path, oldText, newText}` and
 * reports **one per hunk** — a `MultiEdit` of three places in one file is
 * three of these, each holding only that hunk's before and after. So they are
 * not concatenated: each becomes its own small patch, drawn one under the
 * other, which is what a person reading an edit is looking at anyway.
 *
 * `oldText` is `null` for a `Write`, which is a new file rather than a change
 * to one, and jsdiff produces exactly the right thing for that on its own —
 * every line added. It is the `Read` case that has to be excluded, and it is,
 * by there being no diff block on it at all.
 *
 * Done here and not in a client because there are two of them, and a patch is
 * the one shape both already render.
 */
export const diffsOf = (content: unknown): ReadonlyArray<ChatDiff> =>
  (Array.isArray(content) ? content : [])
    .map((one) => one as Record<string, unknown>)
    .filter((one) => one["type"] === "diff" && typeof one["path"] === "string")
    .flatMap((one) => {
      const path = String(one["path"]);
      const before = typeof one["oldText"] === "string" ? one["oldText"] : "";
      const after = typeof one["newText"] === "string" ? one["newText"] : "";
      // A block saying nothing changed is a block with nothing to draw, and
      // the adapter does send them — a `Write` of a file whose content is
      // already there, and the no-op hunk in a structured patch.
      if (before === after) {
        return [];
      }
      const name = path.split("/").at(-1) ?? path;
      return [
        {
          path,
          // Named on both sides, because the name is where a renderer reads
          // the language from: `x.ts` highlights and `/dev/null` does not.
          patch: createTwoFilesPatch(name, name, ends(before), ends(after), undefined, undefined, {
            context: CONTEXT,
          }),
        },
      ];
    });

/**
 * What a delegated call is, out of the adapter's own `_meta`.
 *
 * A `Task` call is an ordinary tool call — there is no subagent update kind in
 * ACP, which is worth knowing so nobody goes looking for one. The facts ride
 * in `_meta.claudeCode.toolResponse` on the progress updates, and the retry
 * counters are the SDK's, forwarded verbatim, so they are read in their own
 * spelling first and camelCase second rather than assumed.
 */
const numberOf = (raw: unknown): number | undefined => (typeof raw === "number" ? raw : undefined);

const delegatedTo = (update: Record<string, unknown>): Record<string, unknown> => {
  const meta = update["_meta"] as Record<string, unknown> | undefined;
  const claude = meta?.["claudeCode"] as Record<string, unknown> | undefined;
  const response = claude?.["toolResponse"] as Record<string, unknown> | undefined;
  if (response === undefined) {
    return {};
  }
  const retry = response["subagentRetry"] as Record<string, unknown> | undefined;
  const tried = numberOf(retry?.["attempt"]);
  const of = numberOf(retry?.["max_retries"] ?? retry?.["maxRetries"]);
  const inMs = numberOf(retry?.["retry_delay_ms"] ?? retry?.["retryDelayMs"]);
  return {
    ...(typeof response["subagentType"] === "string" ? { subagent: response["subagentType"] } : {}),
    ...(numberOf(response["elapsedTimeSeconds"]) === undefined
      ? {}
      : { elapsed: response["elapsedTimeSeconds"] }),
    // The attempt is the only field worth a row on its own: a retry with no
    // attempt number is a sentence that cannot be written.
    ...(tried === undefined
      ? {}
      : {
          retry: {
            attempt: tried,
            ...(of === undefined ? {} : { of }),
            ...(inMs === undefined ? {} : { inMs }),
          },
        }),
  };
};

/**
 * What the adapter advertises, as the three fields a menu needs.
 *
 * The name is taken with its slash put back on: ACP carries `bro` and what a
 * person types is `/bro`, and the menu matches on what they typed. `input.hint`
 * is the adapter's own shape for "what arguments this takes".
 */
export const commandsOf = (raw: ReadonlyArray<unknown>): ReadonlyArray<ChatCommand> =>
  raw
    .map((one) => one as Record<string, unknown>)
    .map((one) => {
      const name = String(one["name"] ?? "");
      const input = one["input"] as Record<string, unknown> | null | undefined;
      const hint = input?.["hint"];
      return {
        name: name.startsWith("/") ? name : `/${name}`,
        description: typeof one["description"] === "string" ? one["description"] : "",
        ...(typeof hint === "string" && hint !== "" ? { hint } : {}),
      };
    })
    .filter((one) => one.name !== "/");

/**
 * A compaction, if that is what an agent chunk is announcing.
 *
 * ── why this is a string match, and what that costs ───────────────────────
 *
 * `/compact` is the one thing an agent does that changes what it can *see*,
 * and the adapter reports it as ordinary prose. Read out of its own source
 * (`acp-agent.js`, the `status` and `compact_boundary` cases): the three
 * sentences below are composed there and sent as `agent_message_chunk`s,
 * with no `_meta`, no id and no kind to tell them apart from the agent
 * talking about compaction. The only structured thing a compaction produces
 * is a `usage_update` at `compact_boundary`, which says the figure dropped
 * and not why.
 *
 * So the choice is between matching three English sentences and drawing a
 * compaction as three paragraphs of prose. This matches them — narrowly, on
 * the whole trimmed chunk rather than on a substring, so an agent *saying*
 * the word is untouched — and the fallback when upstream rewords one is the
 * behaviour that exists today: the sentence appears as a message. That is a
 * cosmetic regression rather than a broken conversation, which is what makes
 * the trade acceptable.
 *
 * The counts are not read here. `compact_boundary`'s usage update already
 * carries the authoritative post-compaction figure and the context reading
 * under the composer already shows it; a second copy on this row would be
 * the one that drifts.
 */
/**
 * Whether a message should cut the agent off, rather than wait its turn.
 *
 * Exported because it is a *rule* and not a branch: it decides whether an
 * answer a person is reading gets thrown away, and that is worth stating in
 * one place and testing rather than reading out of an `if`.
 *
 * ── every message used to do this ────────────────────────────────────────
 *
 * The daemon tried `_session/steering` on every send, and the adapter
 * delivers a steer at priority `now` — which its own comment spells out:
 * *"Pre-empting means ABORTING."* So the ordinary act of typing while an
 * agent was working destroyed the answer in flight. Reported as steering too
 * aggressively.
 *
 * The CLI's own input queue settles what the default should be. It ranks
 * `{ now: 0, next: 1, later: 2 }`, builds an ordinary user message at `next`,
 * and reads an absent priority as `next` — so waiting for the next boundary
 * is what a person's message does everywhere except here. `later` is not a
 * third choice for a person: it is what the model's own background traffic
 * uses, task notifications and poll events.
 *
 * Three conditions, and each rules it out for a different reason:
 */
export const interrupts = ({
  /** Somebody asked for it — `cmd+Return`. Absent means no. */
  asked,
  /** The adapter advertises `_session/steering`. An older one does not. */
  capable,
  /**
   * A compaction is running.
   *
   * The one case where the intent is honoured by ignoring it: compacting is
   * a turn like any other, so a steer aimed at it is injected into the very
   * turn rewriting the context — which loses the compaction *and* delivers
   * the message into a turn going nowhere. The daemon holds it instead.
   */
  compacting,
}: {
  readonly asked: boolean;
  readonly capable: boolean;
  readonly compacting: boolean;
}): boolean => asked && capable && !compacting;

export const compactionOf = (text: string): ChatUpdate | undefined => {
  const said = text.trim();
  if (/^compacting\.\.\.$/i.test(said)) {
    return { kind: "compact", status: "running" };
  }
  if (/^compacting completed\.$/i.test(said)) {
    return { kind: "compact", status: "done" };
  }
  const failed = /^compacting failed(?::\s*(?<why>.+?))?\.?$/i.exec(said);
  if (failed !== null) {
    const why = failed.groups?.["why"];
    return { kind: "compact", status: "failed", ...(why === undefined ? {} : { text: why }) };
  }
  return undefined;
};

/**
 * One `session/update` as something the window can draw, or nothing.
 *
 * Deliberately lossy: an update kind with nothing in it a person reads is
 * dropped here rather than in the renderer, which keeps the wire the size of
 * what is shown.
 */
export const updateOf = (params: Record<string, unknown>): ChatUpdate | undefined => {
  const update = params["update"] as Record<string, unknown> | undefined;
  const kind = update?.["sessionUpdate"];
  if (update === undefined || typeof kind !== "string") {
    return undefined;
  }

  if (kind === "agent_message_chunk" || kind === "user_message_chunk") {
    const text = textOf(update["content"]);
    if (text === undefined) return undefined;
    if (kind === "agent_message_chunk") {
      const compaction = compactionOf(text);
      if (compaction !== undefined) return compaction;
    }
    return { kind: "message", role: kind === "user_message_chunk" ? "user" : "agent", text };
  }

  if (kind === "agent_thought_chunk") {
    const text = textOf(update["content"]);
    return text === undefined ? undefined : { kind: "message", role: "thought", text };
  }

  // ── the agent's own commands, skills included ──────────────────────────
  //
  // This was dropped, on the grounds that it "says nothing a person reads".
  // That was wrong about what it carries: a *skill* is one of these, so
  // dropping it meant `/bro` could not be found or run from the chat at all
  // while working perfectly in the terminal beside it.
  //
  // The list replaces rather than merges, which is the adapter's own
  // instruction — "the client should REPLACE its cached command list with this
  // payload" — and is why an empty list is still an answer.
  if (kind === "available_commands_update") {
    const raw = update["availableCommands"];
    return { kind: "commands", commands: commandsOf(Array.isArray(raw) ? raw : []) };
  }

  // A tool call arrives as several updates sharing one id: pending with a
  // generic title, then the real command, then the output, then completed. So
  // this is a patch keyed by `id` and the window merges — which is why every
  // field but the id is optional here.
  if (kind === "tool_call" || kind === "tool_call_update") {
    const id = update["toolCallId"];
    if (typeof id !== "string") {
      return undefined;
    }
    const content = update["content"];
    const first = Array.isArray(content) ? (content[0] as Record<string, unknown>) : undefined;
    const output =
      typeof update["rawOutput"] === "string" ? update["rawOutput"] : textOf(first?.["content"]);
    // An edit's content is diffs and its output is nothing, so these two never
    // compete for the row: `first` is only text when the tool answered with
    // text. A call that changed no file has an empty list and says nothing.
    const diffs = diffsOf(content);
    const meta = (update["_meta"] as Record<string, unknown> | undefined)?.["claudeCode"] as
      | Record<string, unknown>
      | undefined;
    const toolName = meta?.["toolName"];
    // What the call is for, which the adapter puts here from the tool's own
    // `description`. Named `title` on its side and `purpose` on ours: the
    // row already has a title, and this is the other thing a row can say.
    const purpose = meta?.["title"];
    return {
      kind: "tool",
      id,
      ...(typeof update["title"] === "string" ? { title: update["title"] } : {}),
      ...(typeof update["kind"] === "string" ? { toolKind: update["kind"] } : {}),
      // The tool's own name, which the kind is too coarse to stand in for:
      // `Bash` and `Read` and `Edit` all reach a client as `execute`,
      // `read`, `edit` — and `Skill`, `AskUserQuestion` and every MCP tool
      // reach it as `other`. It rides on `_meta`, so it is read from there
      // rather than from a field ACP defines.
      ...(typeof toolName === "string" ? { toolName } : {}),
      ...(typeof purpose === "string" ? { purpose } : {}),
      ...(typeof update["status"] === "string" ? { status: update["status"] } : {}),
      ...(output === undefined ? {} : { output }),
      ...(diffs.length === 0 ? {} : { diffs }),
      ...delegatedTo(update),
    } as ChatUpdate;
  }

  // The context figure, and the only place it exists. `size` is not constant:
  // measured 200000 on a turn's first update and 1000000 on its last, because
  // the model in use has a larger window than the default and the adapter
  // learns that as it goes. So this is a whole reading each time, never a
  // delta, and the newest one wins.
  if (kind === "usage_update") {
    const cost = update["cost"] as Record<string, unknown> | undefined;
    return {
      kind: "usage",
      ...(typeof update["used"] === "number" ? { used: update["used"] } : {}),
      ...(typeof update["size"] === "number" ? { size: update["size"] } : {}),
      ...(typeof cost?.["amount"] === "number" ? { cost: cost["amount"] } : {}),
    };
  }

  return undefined;
};

/**
 * The adapter's config options, in this window's shape.
 *
 * Only the selects, because a select is the only kind this window can draw and
 * a row it cannot draw is worse than a row that is not there. Every option the
 * adapter offers today is one — mode, model, effort and fast mode — so nothing
 * is lost by saying so.
 */
export const optionsOf = (raw: unknown): ReadonlyArray<ChatConfigOption> =>
  (Array.isArray(raw) ? raw : [])
    .map((one) => one as Record<string, unknown>)
    .filter((one) => one["type"] === "select" && typeof one["id"] === "string")
    .map((one) => ({
      id: String(one["id"]),
      name: String(one["name"] ?? one["id"]),
      ...(typeof one["description"] === "string" ? { description: one["description"] } : {}),
      currentValue: String(one["currentValue"] ?? ""),
      values: (Array.isArray(one["options"]) ? one["options"] : [])
        .map((value) => value as Record<string, unknown>)
        .map((value) => ({
          value: String(value["value"] ?? ""),
          name: String(value["name"] ?? value["value"] ?? ""),
          ...(typeof value["description"] === "string"
            ? { description: value["description"] }
            : {}),
        })),
    }));

/** A permission request as something with buttons on it. */
export const permissionOf = (params: Record<string, unknown>, id: string): ChatUpdate => {
  const call = params["toolCall"] as Record<string, unknown> | undefined;
  const options = Array.isArray(params["options"]) ? params["options"] : [];
  return {
    kind: "permission",
    id,
    title: typeof call?.["title"] === "string" ? call["title"] : "a tool wants to run",
    // Which call is being asked about. The adapter emits the tool call first,
    // so this nearly always resolves to a row the window is already drawing.
    ...(typeof call?.["toolCallId"] === "string" ? { about: call["toolCallId"] } : {}),
    options: options.map((raw) => {
      const option = raw as Record<string, unknown>;
      return {
        id: String(option["optionId"] ?? ""),
        name: String(option["name"] ?? option["optionId"] ?? ""),
        kind: String(option["kind"] ?? ""),
      };
    }),
  };
};

/**
 * Open one adapter process and hold a conversation in it.
 *
 * The Scope is the process: when the last window on this workspace closes, the
 * adapter goes and the transcript stays on disk, which is the arrangement the
 * measurement at the top says is safe.
 */
export const conversation = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  options: ChatOptions,
): Effect.Effect<Conversation, ChatError, Scope.Scope> =>
  Effect.gen(function* () {
    const adapter = adapterPath();
    if (adapter === undefined) {
      return yield* Effect.fail(
        new ChatError({ reason: `the ACP adapter is not installed — run:\n  ${INSTALL}` }),
      );
    }
    const claude = claudePath();
    if (claude === undefined) {
      return yield* Effect.fail(new ChatError({ reason: "there is no claude on the PATH" }));
    }

    const encoder = new TextEncoder();
    const outbox = yield* Queue.unbounded<Uint8Array>();
    const write = (message: unknown) =>
      Effect.asVoid(Queue.offer(outbox, encoder.encode(`${JSON.stringify(message)}\n`)));

    const handle = yield* Effect.mapError(
      spawner.spawn(
        ChildProcess.make(process.execPath, [adapter], {
          cwd: options.cwd,
          // See acp.ts: `childEnv` empties CLAUDECODE, without which Claude
          // Code refuses to run inside Claude Code; the executable is set
          // after it, because that key is a path rather than a parent
          // describing itself.
          env: { ...childEnv(), CLAUDE_CODE_EXECUTABLE: claude },
          stdin: { stream: Stream.fromQueue(outbox), endOnDone: false },
        }),
      ),
      (cause) => new ChatError({ reason: "could not start the ACP adapter", cause }),
    );

    // The conversation's own scope, so a forked turn belongs to the adapter
    // process rather than to whichever caller happened to send the message.
    // A prompt forked into a handler's scope is interrupted when that call
    // returns, which is immediately — the turn would be cancelled the moment
    // it started.
    const mine = yield* Effect.scope;

    // The adapter stopping, as an edge somebody outside can wait on. The
    // reader already knows — see `abandonWaiting` — and knowing was the whole
    // of what this side did about it. See {@link Conversation.gone}.
    const stopped = yield* Deferred.make<void>();

    const transcript = yield* Ref.make<ReadonlyArray<Numbered>>([]);
    const subscribers = yield* Ref.make(new Set<Queue.Queue<Numbered>>());
    const emit = (update: ChatUpdate) =>
      Effect.gen(function* () {
        const seq = yield* Ref.modify(transcript, (all) => {
          const numbered: Numbered = { seq: all.length, update };
          return [numbered, [...all, numbered]] as const;
        });
        for (const queue of yield* Ref.get(subscribers)) {
          yield* Queue.offer(queue, seq);
        }
      });

    /**
     * Every tool call this conversation has left hanging, resolved.
     *
     * ── a turn ends; the calls inside it may not say so ──────────────────
     *
     * ACP has no "the turn took this call with it" update, and the adapter
     * does not send a terminal status for a call that was in flight when a
     * turn was cancelled, refused, or died. So the row stays `pending` for
     * the life of the conversation — reported as bash calls "that just spin
     * forever and dont resolve", and it is worse than a stuck spinner:
     * every client reads that row as work still happening.
     *
     * Here rather than in each face, for the reason every rule in this
     * repo is: a client deriving it is a second implementation, and the
     * two would disagree about what a hanging call means. What a face gets
     * is an ordinary `tool` update with a terminal status, which every
     * fold already merges by id.
     *
     * `cancelled` and not `failed`: the call did not fail — nothing is
     * known about what it did, only that whatever was watching it stopped.
     * A cross would be a claim about the tool.
     */
    const settleHangingCalls = Effect.gen(function* () {
      const all = yield* Ref.get(transcript);
      for (const id of hanging(all.map((one) => one.update))) {
        yield* emit({ kind: "tool", id, status: "cancelled" });
      }
    });

    // ── a compaction is a turn, and steering into it destroys it ──────────
    //
    // Reported as "steering during compacting should not fail the compact it
    // should queue the message for after". `/compact` is delivered as an
    // ordinary prompt, so it *is* the turn in flight — and a steer is defined
    // as an injection into the turn in flight. The adapter duly injects it
    // into the one turn that is rewriting the context, which comes back as
    // `compacting failed` with the message delivered into a turn that is
    // going nowhere. Both halves are lost, and the only evidence is a
    // sentence in the transcript that reads like the model's own failure.
    //
    // Nothing on the steering request says "not while compacting" — there is
    // no flag for it and `idleBehavior` answers a different question — so the
    // only process that can decline is this one, which is already reading the
    // compaction's own sentences to draw its row. The state is therefore read
    // off the update stream rather than tracked separately: `compactionOf` is
    // the single reader of those three sentences, and a second opinion about
    // whether a compaction is running is the copy that drifts.
    const compacting = yield* Ref.make(false);
    // The key travels with the text, because a held message still has to be
    // able to say which turn is its own when it finally goes — see the `id` on
    // a `turn` update. Held as a pair rather than two lists for the reason
    // this file gives about every other pair: two lists can disagree.
    const waitingToSend = yield* Ref.make<
      ReadonlyArray<{ readonly text: string; readonly key: string }>
    >([]);
    // Assigned once `deliver` exists, below. A `let` rather than a forward
    // declaration because the reader fiber is forked before the session is
    // even open: what it must not do is capture an effect that was empty at
    // the moment the fiber started, and reading the binding at the moment the
    // line runs is what stops that.
    let flushWaiting: Effect.Effect<void> = Effect.void;

    // Requests this client made, waiting for their replies, and requests the
    // agent made, waiting for a person. Two directions, two tables.
    let next = 0;
    const waiting = new Map<number, (message: Record<string, unknown>) => void>();
    const asked = yield* Ref.make(new Map<string, Pending>());

    const request = (
      method: string,
      params: unknown,
    ): Effect.Effect<Record<string, unknown>, ChatError> =>
      Effect.callback<Record<string, unknown>, ChatError>((resume) => {
        next += 1;
        const id = next;
        waiting.set(id, (message) => {
          if (message["error"] !== undefined) {
            resume(
              Effect.fail(
                new ChatError({ reason: `${method}: ${JSON.stringify(message["error"])}` }),
              ),
            );
            return;
          }
          resume(Effect.succeed((message["result"] ?? {}) as Record<string, unknown>));
        });
        // Registered before it is written, and written from inside the
        // register rather than before it. The reader is another fiber: a
        // reply to a request whose waiter is not in the table yet is a reply
        // dropped, and the call then waits for the timeout rather than for
        // an answer that already came back.
        Queue.offerUnsafe(
          outbox,
          encoder.encode(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`),
        );
        return Effect.sync(() => waiting.delete(id));
      });

    /**
     * Tell the adapter something, with no reply expected.
     *
     * A JSON-RPC notification is a message with **no `id`**, and that is the
     * whole difference: a reply carrying a null id is a protocol error at the
     * other end, so a notification sent as a request would leave this side
     * waiting for an answer nobody is required to send.
     */
    /**
     * Every request still waiting, answered with a refusal instead of nothing.
     *
     * ── an adapter that exits answers nothing, and nothing said so ────────
     *
     * `request` is an `Effect.callback` that resumes from the reader's table,
     * and the table is the only thing that ever resumes it — there is no
     * timeout, deliberately, because a turn legitimately runs for twenty
     * minutes. So the exit of the process on the other end is not a failure
     * here; it is an **absence**, and an absence has no edge for anything to
     * hang on.
     *
     * What that cost is the one state this file says twice is the worst to be
     * wrong about. The adapter dies mid-turn, `session/prompt` is never
     * resumed, and the fiber holding it sits in `mine` forever:
     *
     * ```
     *   the transcript   turn started …                ← and no `ended`, ever
     *   this daemon      working, for the life of the conversation
     *   every client     replays that transcript and folds `running: 1`
     *                    — a window opened tomorrow reads it the same way
     * ```
     *
     * Reported as "this thread is thinking but its not". Nothing else on
     * screen disagrees, because every face is folding the same true record of
     * what this process actually said.
     *
     * The repair is that the reader's **end is an event**: stdout closing is
     * how a spawned process says it has gone, so the stream completing is the
     * one place this side can know. Each waiter is handed an ordinary
     * JSON-RPC error rather than a second resume path — `request` already
     * turns an `error` field into a `ChatError`, and a second way to fail a
     * call is the copy that drifts.
     */
    const abandonWaiting = (why: string) =>
      Effect.sync(() => {
        // Emptied before any of them is resumed: resuming a fiber can run it,
        // and a turn that failed is free to send its own next request into a
        // table it would otherwise still be being iterated out of.
        const pendings = Array.from(waiting.values());
        waiting.clear();
        for (const pending of pendings) {
          pending({ error: { code: -32000, message: why } });
        }
      });

    const notify = (method: string, params: unknown): Effect.Effect<void> =>
      Effect.sync(() => {
        Queue.offerUnsafe(
          outbox,
          encoder.encode(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`),
        );
      });

    // ── one row per compaction ──────────────────────────────────────────
    //
    // The three sentences a compaction produces are prose and carry no id, so
    // the outcome has nothing to patch and would draw as a second row under
    // the first. Counted here because this is the process that sees the whole
    // conversation — `compactionOf` is per-update and pure, and keeping it
    // that way is what makes it testable.
    //
    // A `done` arriving with no `running` before it still lands on a row of
    // its own rather than on nothing, which is what a transcript replayed
    // from the middle of a compaction looks like.
    let compactions = 0;
    const identified = (update: ChatUpdate): ChatUpdate => {
      if (update.kind !== "compact") return update;
      if (update.status === "running" || compactions === 0) compactions += 1;
      return { ...update, id: `compact-${String(compactions)}` };
    };

    const lines = Stream.splitLines(Stream.decodeText(handle.stdout));
    const reader = Stream.runForEach(lines, (line) =>
      Effect.gen(function* () {
        const message = parseMessage(line) as Record<string, unknown> | undefined;
        if (message === undefined) {
          return;
        }
        const id = message["id"];
        const method = message["method"];

        if (typeof id === "number" && method === undefined) {
          const pending = waiting.get(id);
          waiting.delete(id);
          if (pending !== undefined) {
            pending(message);
          }
          return;
        }

        if (method === "session/update") {
          const update = updateOf((message["params"] ?? {}) as Record<string, unknown>);
          if (update !== undefined) {
            yield* emit(identified(update));
            if (update.kind === "compact") {
              const running = update.status === "running";
              yield* Ref.set(compacting, running);
              // `failed` flushes too. A compaction that did not work is still
              // a compaction that is over, and holding somebody's message
              // hostage to it would lose the message as well.
              if (!running) yield* flushWaiting;
            }
          }
          return;
        }

        // A request from the agent. A permission prompt is shown to a person;
        // anything else is refused, because an unanswered request stalls the
        // turn until a timeout rather than failing, and a stall is the one
        // outcome nothing on screen explains.
        if (typeof method === "string" && typeof id === "number") {
          if (method === "session/request_permission") {
            const key = `permission-${String(id)}`;
            yield* Ref.update(asked, (all) =>
              new Map(all).set(key, {
                reply: (result) => write({ jsonrpc: "2.0", id, result }),
              }),
            );
            yield* emit(permissionOf((message["params"] ?? {}) as Record<string, unknown>, key));
            return;
          }
          yield* write({
            jsonrpc: "2.0",
            id,
            error: { code: -32601, message: "this client answers nothing" },
          });
        }
      }),
    );
    // `ensuring`, so the end of the reader is the end of every request that
    // was waiting on it — whether the stream finished (the process exited),
    // failed, or was interrupted by this scope closing.
    yield* Effect.forkScoped(
      Effect.ensuring(
        Effect.ignore(reader),
        Effect.andThen(
          abandonWaiting("the ACP adapter stopped answering"),
          Deferred.succeed(stopped, undefined),
        ),
      ),
    );

    const hello = yield* request("initialize", {
      protocolVersion: 1,
      clientCapabilities: { fs: { readTextFile: false, writeTextFile: false } },
    });

    // ── steering is a capability, so it is read rather than assumed ────────
    //
    // A message typed while the agent is working is not a second question. The
    // adapter has a request for it — `_session/steering`, which "injects the
    // message into the in-flight turn rather than queuing it as a separate
    // session/prompt", at a priority that pre-empts the current generation —
    // and it advertises it in the initialize reply rather than in a version
    // number. Absent, sending is the ordinary prompt and nothing is lost but
    // the immediacy.
    const steering =
      (
        (hello["_meta"] as Record<string, unknown> | undefined)?.["steering"] as
          | Record<string, unknown>
          | undefined
      )?.["supported"] === true;

    // ── forking, for a conversation somebody else is already having ───────
    //
    // Also a capability, advertised as
    // `agentCapabilities.sessionCapabilities.fork`, and read for the same
    // reason: an adapter that cannot fork should refuse by name rather than
    // fail inside a request about a method it has never heard of.
    const forkable =
      (
        (hello["agentCapabilities"] as Record<string, unknown> | undefined)?.[
          "sessionCapabilities"
        ] as Record<string, unknown> | undefined
      )?.["fork"] !== undefined;

    // The session for this directory: the one the daemon named, if it is
    // still there, and a new one otherwise.
    //
    // ── never the newest, and this is the bug that shape produces ──────────
    //
    // The first version took the most recently updated session whose cwd
    // matched. In a workspace with an agent running in its terminal, that is
    // the terminal's session — so opening the chat did not start a second
    // conversation, it joined the one somebody was already having, as a second
    // writer, with nothing in either process aware of the other.
    //
    // ── and do not compose the path either ────────────────────────────────
    //
    // `agent-tasks.ts` maps a directory to `~/.claude/projects/<slug>/` by
    // replacing punctuation, and that slug is built from the RESOLVED path, so
    // the guess is wrong the moment anything above it is a symlink. On macOS
    // `/var/…` is `/private/var/…`, and the guess missed by a whole prefix
    // while the session sat plainly in the list:
    //
    //   guessed   -var-folders-…-T-awp-acp-spike-XZUyvb      does not exist
    //   actual    -private-var-folders-…-T-awp-acp-spike
    //
    // ── and do not pre-check the listing either ───────────────────────────
    //
    // This used to confirm the recorded id against `session/list` before
    // loading it, so that a transcript which had been deleted or moved failed
    // here rather than inside `session/load` with a sentence about an id. That
    // was a proxy for "will a load work", and **a fork is the case where the
    // proxy is wrong**: measured with `probe:chat`, a session forked seconds
    // earlier is not in the listing at all —
    //
    //   forked to        b635c85e-…
    //   in the listing   NO   (1 session, and it is the original)
    //
    // — so the check refused it and this code quietly opened a *new* session
    // instead. From outside that is indistinguishable from a fork that
    // carried no memory, which is precisely how it was reported by the probe.
    //
    // So the load is attempted and a refusal falls back to a new session. The
    // error quality the check was protecting is kept by saying so in the log
    // rather than by guessing in advance.
    const here = realpathSync(options.cwd);

    const claudeCode = {
      _meta: {
        claudeCode: {
          options: options.model === undefined ? {} : { model: options.model },
        },
      },
    };

    // ── the agent's own face on the daemon ────────────────────────────────
    //
    // Every wire between this window and its agent pointed one way: the
    // window could type a review at an agent, and the agent could answer only
    // by printing into a terminal amoeba draws. This is the other direction —
    // see `mcp.ts` — and handing it over here is what makes it need no file on
    // disk and no edit to anybody's config.
    //
    // Bound to `options.cwd`, which is the workspace. Every tool is scoped to
    // the directory the server runs in and none of them takes a workspace
    // argument, so a conversation cannot reach another checkout. Same rule as
    // `-R` on every jj call, made structural.
    //
    // Sent on **every** open, load and fork alike. A loaded conversation is a
    // conversation continuing, and one that came back without its tools would
    // read as an agent that had forgotten how to use them.
    const mcpServers = [serverSpec({ entry: mcpEntry(), cwd: options.cwd, url: daemonUrl() })];

    /** Copy the newest other conversation in this directory, and open it. */
    const forkNewest = () =>
      Effect.gen(function* () {
        if (!forkable) {
          return yield* Effect.fail(
            new ChatError({ reason: "this agent cannot fork a conversation" }),
          );
        }
        const now: Record<string, unknown> = yield* Effect.orElseSucceed(
          request("session/list", { cwd: options.cwd }),
          () => ({}) as Record<string, unknown>,
        );
        const others = (Array.isArray(now["sessions"]) ? now["sessions"] : [])
          .map((raw) => raw as Record<string, unknown>)
          .filter((one) => one["cwd"] === here && one["sessionId"] !== options.session);
        // Newest first, by whatever the adapter dates them with. One with no
        // date sorts last rather than being dropped: an undated transcript is
        // still a conversation, and may be the only candidate there is.
        const newest = others.toSorted((left, right) =>
          String(right["updatedAt"] ?? right["createdAt"] ?? "").localeCompare(
            String(left["updatedAt"] ?? left["createdAt"] ?? ""),
          ),
        )[0];
        const from = newest?.["sessionId"];
        if (typeof from !== "string") {
          return yield* Effect.fail(
            new ChatError({ reason: "there is no other conversation in this workspace to open" }),
          );
        }
        return yield* request("session/fork", {
          sessionId: from,
          cwd: options.cwd,
          mcpServers,
          ...claudeCode,
        });
      });

    // The fork, when one was asked for: the newest *other* session in this
    // directory, copied under a new id. Its own is skipped, or reopening a
    // chat would fork the conversation it is already showing.
    //
    // A fork rather than a load, and that inverts the rule above rather than
    // breaking it: loading makes this process a second writer on a transcript
    // an interactive `claude` is still appending to, where a fork reads it,
    // copies it and leaves the original alone.
    const forked = options.fork !== true ? undefined : yield* forkNewest();

    const loaded =
      forked !== undefined || options.session === undefined
        ? undefined
        : yield* Effect.result(
            request("session/load", {
              sessionId: options.session,
              cwd: options.cwd,
              mcpServers,
              ...claudeCode,
            }),
          );

    const opened =
      forked !== undefined
        ? forked
        : loaded !== undefined && Result.isSuccess(loaded)
          ? loaded.success
          : yield* request("session/new", { cwd: options.cwd, mcpServers, ...claudeCode });

    const sessionId = String(opened["sessionId"] ?? options.session ?? "");
    if (sessionId === "") {
      return yield* Effect.fail(
        new ChatError({ reason: "the adapter opened a session with no id" }),
      );
    }

    // The options the open reply carried. Kept rather than re-asked because
    // there is no call that answers "what are my options" — they arrive with
    // the session and are updated by setting one.
    const settings = yield* Ref.make(optionsOf(opened["configOptions"]));

    // Manual, set through the config option rather than through
    // `session/set_mode` — and the difference is not stylistic.
    //
    // The options above are a snapshot of what the open reply said, and the
    // reply says `mode: auto` because that is what a new session opens as.
    // Setting the mode by the other call changes the session and leaves that
    // snapshot behind, so the window drew `auto` on a session running in
    // Manual: a control reporting the opposite of the truth about who approves
    // a tool call. `set_config_option` answers with the whole set, so the same
    // call that changes it is the one that corrects the record.
    //
    // Ignored if the adapter will not have it: an older one that cannot set a
    // mode is still a usable conversation, and refusing to open at all would
    // be a worse answer than a session running in the mode it chose.
    /**
     * Set one of the session's options and keep whatever the reply says.
     *
     * The reply carries the whole set, so the call that changes a setting is
     * also the one that corrects the record. Ignored on failure: an adapter
     * that will not take an option is still a usable conversation, and
     * refusing to open at all would be a worse answer than a session running
     * as it chose.
     */
    const configure = (configId: string, value: string) =>
      Effect.ignore(
        Effect.flatMap(
          request("session/set_config_option", { sessionId, configId, value }),
          (reply) => {
            const fresh = optionsOf(reply["configOptions"]);
            return fresh.length === 0 ? Effect.void : Ref.set(settings, fresh);
          },
        ),
      );

    yield* configure("mode", options.mode ?? MODE);
    // Only when something asked for one. An effort the adapter chose is a
    // reasonable answer, and overwriting it with a guess from here would be
    // this process having an opinion nobody wrote down.
    if (options.effort !== undefined) {
      yield* configure("effort", options.effort);
    }

    // The stored reading, first, so a window that opens and says nothing still
    // has a figure. Before anything else can be emitted, because it describes
    // the state the conversation is *in* rather than something that happened.
    if (options.usage !== undefined) {
      yield* emit({ kind: "usage", used: options.usage.used, size: options.usage.size });
    }

    const promptOf = (text: string) => ({
      sessionId,
      prompt: [{ type: "text", text }],
    });

    /**
     * Get `text` to the agent, whichever way is open right now.
     *
     * Split out of `send` because there are two callers with different
     * timing: somebody pressing return, and the flush that runs when a
     * compaction is over. Only the first has a message to put on the stream —
     * by the time the second runs, the row has been on screen for a minute
     * marked `queued`, and emitting it again would draw it twice.
     */
    const deliver = (
      text: string,
      /**
       * Stop the agent where it is, rather than waiting for it to finish.
       *
       * Off by default, and that is the correction. Every send used to try
       * steering, which the adapter delivers at priority `now` — and `now`
       * *aborts* the generation in flight. Meanwhile the CLI's own input
       * queue ranks `{ now: 0, next: 1, later: 2 }` and builds an ordinary
       * user message at `next`, so waiting for the boundary is what a human
       * message does everywhere else. See `ChatSend.interrupt`.
       */
      interrupt: boolean,
      /**
       * The message this turn is for — `ChatSend.key`, the client's own uuid.
       *
       * Put on both turn edges so a client can tell *whose* turn ended. A
       * window holding two messages behind one slow answer cannot work that
       * out from the edges alone, and the one that used to guess released
       * both on the first end. See the `id` field on `ChatUpdate`.
       */
      key: string,
    ): Effect.Effect<ChatDelivery, ChatError> =>
      Effect.gen(function* () {
        // Steer only when somebody asked to, the agent can be steered, and
        // there is nothing being compacted to steer into.
        //
        // **`idleBehavior: "promptRequired"`, and it is the whole reason this
        // is one call rather than two.** Without it, a steer sent when no
        // turn is running makes the *adapter* start one, detached — so this
        // process would emit no `turn started`, no `turn ended`, and the
        // window would watch a reply arrive with nothing saying a turn was
        // under way. With it the adapter refuses instead, by name, and the
        // ordinary path below runs and owns the lifecycle.
        //
        // It also means there is no "is a turn running" state kept here. The
        // adapter decides, and its own comment says the check and the push
        // "stay in one synchronous section so the turn cannot settle in the
        // gap" — which is a race this side could not have avoided. The
        // compaction is the one thing this side knows and the adapter will
        // not tell it.
        if (
          interrupts({
            asked: interrupt,
            capable: steering,
            compacting: yield* Ref.get(compacting),
          })
        ) {
          const steered = yield* Effect.result(
            request("_session/steering", {
              ...promptOf(text),
              _meta: { steering: { idleBehavior: "promptRequired" } },
            }),
          );
          if (Result.isSuccess(steered) && steered.success["outcome"] === "injected") {
            return "steer" as const;
          }
        }

        // ── the end of a turn is a finalizer, not a success ──────────────
        //
        // A refused or crashed turn still ends. Reporting only the happy edge
        // leaves the window saying "working" for the rest of the session,
        // which is the worst of the three states to be wrong about — and
        // `orElseSucceed`, which is what used to say so, catches a *failure*
        // and nothing else. A defect anywhere below it, or this scope closing
        // under the fiber, took the whole pipeline with it and emitted no
        // edge at all.
        //
        // That is not a hypothetical shape: interruption is not a failure, and
        // this file already records the same mistake in the feeds the window
        // wraps in `Effect.retry`. So the emission is on the exit, where the
        // three outcomes are one outcome.
        const ended = (stopReason: string) =>
          // Ignored, because this runs while the conversation may already be
          // going away: a transcript nobody can be told about is not a reason
          // to fail a finalizer.
          Effect.ignore(
            Effect.gen(function* () {
              // Any call still in flight is settled *before* the turn's own
              // end, so a client folding both in one batch sees the rows
              // resolve and then the turn stop — rather than a turn that
              // ended with work apparently still going on inside it.
              yield* settleHangingCalls;
              yield* emit({ kind: "turn", status: "ended", stopReason, id: key });
              // ── and nothing is left holding ─────────────────────────────
              //
              // The ordinary release is the compaction's own "compacting
              // completed." sentence. This is the case where that sentence
              // never comes: the turn was cancelled, the adapter died, or
              // upstream reworded it — `compactionOf` says in its own note
              // that a rewording degrades to prose, and without this that
              // degradation would also strand somebody's message for the life
              // of the conversation. A turn that has ended is not compacting,
              // whatever was or was not said.
              yield* Ref.set(compacting, false);
              // Read here rather than closed over: `flushWaiting` is a `let`
              // reassigned as messages are held, so the value wanted is the
              // one standing when the turn ends.
              yield* flushWaiting;
            }),
          );

        yield* emit({ kind: "turn", status: "started", id: key });
        yield* Effect.forkIn(
          Effect.onExit(
            Effect.map(request("session/prompt", promptOf(text)), (reply) =>
              String(reply["stopReason"] ?? ""),
            ),
            // An interrupted turn reads as `failed` like a refused one, and
            // deliberately: what a client does with either is stop drawing it
            // as work in progress, and inventing a third word would be a
            // distinction no face has a rendering for.
            (exit) => ended(Exit.isSuccess(exit) ? exit.value : "failed"),
          ),
          mine,
        );
        return "prompt" as const;
      });

    // Assigning the binding declared above. Ignored rather than failed: this
    // runs inside the reader fiber and inside a turn's own end, and neither
    // has anywhere to report a refusal — what a conversation that cannot be
    // had says so on is the update stream.
    flushWaiting = Effect.ignore(
      Effect.gen(function* () {
        for (const { text, key } of yield* Ref.getAndSet(waitingToSend, [])) {
          // Never an interrupt, whatever the sender asked for. This runs when
          // a compaction has just ended, and what it is flushing has been on
          // screen marked `queued` for a minute — the moment to cut a turn
          // short is the moment somebody pressed the key, not a minute later
          // against whatever happens to be running by then.
          yield* deliver(text, false, key);
        }
      }),
    );

    return {
      sessionId,

      gone: Deferred.await(stopped),

      updates: Effect.gen(function* () {
        const queue = yield* Queue.unbounded<Numbered>();
        // Register first, snapshot second. The other order drops anything that
        // lands in between; this one duplicates it, and a duplicate is what the
        // sequence number removes.
        yield* Ref.update(subscribers, (all) => new Set(all).add(queue));
        yield* Effect.addFinalizer(() =>
          Ref.update(subscribers, (all) => {
            const rest = new Set(all);
            rest.delete(queue);
            return rest;
          }),
        );
        const history = yield* Ref.get(transcript);
        const from = history.length;
        return Stream.concat(
          Stream.fromIterable(history.map((one) => one.update)),
          Stream.map(
            Stream.filter(Stream.fromQueue(queue), (one) => one.seq >= from),
            (one) => one.update,
          ),
        );
      }),

      // Forked, because a turn takes as long as the work does and the answer
      // comes back down the update stream. Awaiting it here would make sending
      // a message a call that returns when the agent has finished thinking.
      // The two `turn` updates around it are the daemon's own, and they have
      // to be: a turn is a request and a reply, and the reply is not an
      // update, so nothing the adapter sends marks either edge. Without them a
      // window cannot tell an agent that is working from one that answered
      // with nothing — both are an empty space.
      send: (text: string, key: string, interrupt: boolean) =>
        Effect.gen(function* () {
          // ── what somebody typed, on the stream ──────────────────────────
          //
          // Emitted here because nothing else will: no adapter sends a user
          // chunk on a live turn — measured, zero of them — so a second client
          // watching this conversation saw the answers and never the
          // questions. The sender already has its own copy from the keypress,
          // which is why the key is the client's: one name, two copies, and
          // applying it twice is a no-op.
          //
          // Before the turn edges, and before the request, so the order on the
          // stream is the order it happened in even if the adapter is slow to
          // accept it. It is also before the hold below, which is what makes a
          // held message a row somebody can see rather than a send that did
          // nothing for half a minute.
          yield* emit({ kind: "message", role: "user", text, id: key });

          // Held, not refused and not steered. See the note beside
          // `compacting`: the turn in flight is the one rewriting the
          // context, and injecting into it loses both.
          if (yield* Ref.get(compacting)) {
            yield* Ref.update(waitingToSend, (all) => [...all, { text, key }]);
            return "queued" as const;
          }

          return yield* deliver(text, interrupt, key);
        }),

      /** SIGKILL, so nothing gets to be tidy on the way out. Probe only. */
      stop: Effect.ignore(handle.kill({ killSignal: "SIGKILL" })),

      /** Every session the adapter sees here, by id. Asked for by the probe. */
      sessions: () =>
        Effect.map(
          Effect.orElseSucceed(
            request("session/list", { cwd: options.cwd }),
            () => ({}) as Record<string, unknown>,
          ),
          (all) =>
            (Array.isArray(all["sessions"]) ? all["sessions"] : [])
              .map((raw) => String((raw as Record<string, unknown>)["sessionId"] ?? ""))
              .filter((one) => one !== ""),
        ),

      // Fire and forget, deliberately. The adapter's own handler returns
      // early when there is no live turn — "there is nothing to do here", in
      // its words — so cancelling an idle conversation is a no-op rather than
      // a refusal, and a client does not have to know which it was.
      cancel: notify("session/cancel", { sessionId }),

      answer: (requestId: string, option: string) =>
        Effect.gen(function* () {
          const pending = (yield* Ref.get(asked)).get(requestId);
          if (pending === undefined) {
            return yield* Effect.fail(
              new ChatError({ reason: "that request has already been answered" }),
            );
          }
          yield* Ref.update(asked, (all) => {
            const rest = new Map(all);
            rest.delete(requestId);
            return rest;
          });
          yield* pending.reply({ outcome: { outcome: "selected", optionId: option } });
          // ── and say that it was answered ────────────────────────────────
          //
          // There is no update for this in ACP: the adapter's reply *is* the
          // answer, so the only process that knows a question has been
          // settled is this one. Without it a second client goes on offering
          // buttons for a request that was answered minutes ago — and pressing
          // one gets "that request has already been answered", which is a
          // refusal about somebody else's click.
          //
          // The option id rather than its name: every client holds the options
          // for the request already.
          yield* emit({ kind: "permission", id: requestId, status: "answered", chose: option });
        }),

      config: Ref.get(settings),

      set: (option: string, value: string) =>
        Effect.gen(function* () {
          // `configId`, not `optionId`. Both read as the obvious name and only
          // one is the protocol's — the request is `{ sessionId, configId,
          // value }` — and the adapter answered the wrong one with a refusal
          // this code then swallowed, so every change silently did nothing.
          const reply = yield* request("session/set_config_option", {
            sessionId,
            configId: option,
            value,
          });
          // The reply's own list if it carries one, and the current value
          // patched in if it does not. Trusting the reply is what makes a
          // setting the agent adjusted or refused come back as what actually
          // happened — an adapter that answers with nothing is the case the
          // patch is for, not the ordinary one.
          const fresh = optionsOf(reply["configOptions"]);
          if (fresh.length > 0) {
            yield* Ref.set(settings, fresh);
            return fresh;
          }
          return yield* Ref.updateAndGet(settings, (all) =>
            all.map((one) => (one.id === option ? { ...one, currentValue: value } : one)),
          );
        }),
    };
  });

/**
 * Which session belongs to the chat, for each workspace.
 *
 * This exists because of what `session/list` answers with. Every session ever
 * held in a workspace directory is in that list, the terminal's included — so
 * without a record of its own, the chat has no way to tell the conversation it
 * started from the one somebody is having in the pane beside it. Taking the
 * newest is the obvious rule and it joins the terminal's session as a second
 * writer.
 *
 * One row per workspace, because the agent column shows one conversation. A
 * second would have nowhere to be drawn.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "chat.001-sessions",
    up: [
      `create table chat_sessions (
         project    text not null,
         workspace  text not null,
         session_id text not null,
         primary key (project, workspace)
       ) strict`,
    ],
  },
  {
    /**
     * The last context reading, per session.
     *
     * Keyed by the session and not by the workspace, which is what makes
     * `/new` correct without a delete: a fresh conversation has a new id and
     * therefore no reading, so it cannot inherit the old one's tokens. The
     * row for a forgotten session stays — it is still true of that transcript,
     * which a fork can load later.
     */
    name: "chat.002-usage",
    up: [
      `create table chat_usage (
         session_id text primary key,
         used       integer not null,
         size       integer not null
       ) strict`,
    ],
  },
  // Who is writing which session, across every process on this machine. The
  // table above says which conversation belongs to a workspace; this one says
  // whether anybody is in it. See `session-claim.ts` for why one is not the
  // other — the short of it is that `RcMap` is memory and a session id is not.
  claimMigration,
];

/**
 * Every conversation the window has open, one adapter each.
 *
 * `RcMap` for the reason `Sessions` uses one for ptys: the lifecycle belongs to
 * the callers, not to a counter kept by hand. The first window on a workspace
 * spawns the adapter, a second joins it, and the process goes when the last one
 * closes. Two adapters on one workspace would be two writers on one transcript
 * — which is exactly what `session/load` on a session somebody is already
 * sitting in would be, and nothing in the protocol prevents it.
 */
export class Chat extends Context.Service<
  Chat,
  {
    /** The conversation on a workspace, for as long as the caller's Scope is open. */
    readonly open: (
      project: string,
      workspace: string,
    ) => Effect.Effect<Stream.Stream<ChatUpdate>, ChatError, Scope.Scope>;

    /** Say something to it, and say how it got there. */
    readonly send: (
      project: string,
      workspace: string,
      text: string,
      /** The client's name for this message. See `ChatSend.key`. */
      key: string,
      /** Stop the agent where it is rather than waiting. See `ChatSend.interrupt`. */
      interrupt: boolean,
    ) => Effect.Effect<ChatDelivery, ChatError>;

    /**
     * Say the first thing to a conversation nobody is watching, and stay until
     * the agent has finished answering.
     *
     * ── why this is not `send` ────────────────────────────────────────────
     *
     * `send` returns as soon as the adapter has accepted the prompt, which is
     * right for a person typing: the window is subscribed, so something holds
     * the conversation open while the answer arrives.
     *
     * The create job has no window. `RcMap` releases a conversation two
     * minutes after its last reference goes, and releasing it kills the
     * adapter — so a brief delivered by `send` alone reaches the agent and
     * then has the agent shot two minutes into its first answer. What that
     * looks like from outside is a chat that was asked something and stopped
     * mid-thought, which is indistinguishable from a model that gave up.
     *
     * So this holds the reference until the turn it started has ended, and the
     * caller's own wait is what does the holding. It is the last step of a job
     * that already spends minutes in `bun install`; a step that waits is a
     * step the jobs panel can show, which is better feedback than a job that
     * says succeeded while the agent is still reading.
     */
    readonly brief: (
      project: string,
      workspace: string,
      text: string,
    ) => Effect.Effect<void, ChatError>;

    /**
     * What each workspace's chat agent is doing, now and whenever it changes.
     *
     * Only `working` and `waiting` are ever reported, and a workspace with an
     * idle chat is absent rather than `idle`: this source knows about the
     * conversation in this window and nothing about the agent somebody has
     * running in the workspace's terminal.
     */
    readonly statuses: () => Stream.Stream<ReadonlyMap<string, WorkspaceStatus>>;

    /**
     * Whether this workspace's chat already has a conversation of its own.
     *
     * The stored pointer, not the adapter: `RcMap` releases a conversation two
     * minutes after the last window closes, so "is one running" answers no for
     * every chat nobody is looking at — which is nearly all of them, and the
     * opposite of what a caller asking this wants to know.
     *
     * Asked by the swap, and the answer decides whether it forks. A workspace
     * being moved into the chat for the first time should arrive holding what
     * the terminal has already been told; one that has been here before should
     * find what it left, which a fork would overwrite.
     */
    readonly held: (project: string, workspace: string) => Effect.Effect<boolean, ChatError>;

    /**
     * Fork the terminal's conversation in this workspace and make it the
     * chat's, answering the new session id.
     */
    readonly openTerminal: (project: string, workspace: string) => Effect.Effect<string, ChatError>;

    /**
     * Forget which session this workspace's chat is on and open a new one.
     *
     * The transcript is not touched. What is forgotten is the *pointer* — the
     * row in `chat_sessions` — so the old conversation is still on disk and
     * still loadable by id; it is simply no longer the one this workspace
     * continues. That is the only shape available anyway: deleting a
     * transcript is the adapter's business and there is no call for it.
     */
    readonly fresh: (project: string, workspace: string) => Effect.Effect<string, ChatError>;

    /**
     * Stop the turn it is running.
     *
     * Silent when nothing is running: the adapter answers an idle session by
     * doing nothing, so this is safe to press twice and safe to press early.
     */
    readonly cancel: (project: string, workspace: string) => Effect.Effect<void, ChatError>;

    /** Answer one of its permission requests. */
    readonly answer: (
      project: string,
      workspace: string,
      request: string,
      option: string,
    ) => Effect.Effect<void, ChatError>;

    /** What the session is running as. */
    readonly config: (
      project: string,
      workspace: string,
    ) => Effect.Effect<ReadonlyArray<ChatConfigOption>, ChatError>;

    /** Change one of those, and get the set back as it now stands. */
    readonly set: (
      project: string,
      workspace: string,
      option: string,
      value: string,
    ) => Effect.Effect<ReadonlyArray<ChatConfigOption>, ChatError>;
  }
>()("awp/Chat") {}

/**
 * The key, and it is a composed string because RcMap's lookup takes one value.
 *
 * A separator that cannot appear in either half: a project is a basename and a
 * workspace is a path segment, so neither holds a newline.
 */
const keyOf = (project: string, workspace: string): string => `${project}\n${workspace}`;

const partsOf = (key: string): readonly [string, string] => {
  const at = key.indexOf("\n");
  return [key.slice(0, at), key.slice(at + 1)] as const;
};

/**
 * Run something in `where`, at most one at a time per key.
 *
 * Two mistakes it exists to make impossible, both of which are invisible until
 * an adapter is being held open forever or has been shot mid-turn:
 *
 *   forked in `where`   and never in the caller's scope. The caller is a
 *                       request, and a fiber in a request's scope is
 *                       interrupted when the reply is sent — which is
 *                       immediately, and is the one thing this must outlive
 *   one per key         a second send during a turn would otherwise take a
 *                       second reference, and the release is per finish: one
 *                       of them would be a reference nothing gives back
 *
 * The key is cleared when the work ends however it ends — a failure that left
 * the key set would mean no further work for that conversation, ever, which
 * fails in the direction nobody would look.
 */
export const oneAtATime = (where: Scope.Scope) => {
  const running = new Set<string>();
  return (key: string, work: Effect.Effect<unknown, unknown>): Effect.Effect<void> =>
    Effect.gen(function* () {
      if (running.has(key)) {
        return;
      }
      running.add(key);
      yield* Effect.forkIn(
        Effect.ensuring(
          Effect.ignore(work),
          Effect.sync(() => running.delete(key)),
        ),
        where,
      );
    });
};

/**
 * Which conversation a key currently means, so a dead one cannot speak for it.
 *
 * `RcMap.invalidate` followed by `RcMap.get` — `/new`, the fork, and now the
 * adapter dying — runs the new lookup while the old entry's scope is still
 * closing. A finalizer that reached for the key by name would be reaching past
 * itself at whatever now answers to it, and the one thing it does is kill the
 * entry: **a stale watcher invalidating the conversation `/new` just opened.**
 *
 * So a holder is handed a token and speaks only while it is still the one
 * `take` last gave out. `drop` is the same check, which is what makes it safe
 * to call from a finalizer after a replacement has already taken the key.
 *
 * Exported for its test rather than for a second caller, like {@link once}.
 */
export const generations = <A>() => {
  const live = new Map<string, { readonly token: symbol; readonly value: A }>();
  return {
    take: (key: string, value: A): symbol => {
      const token = Symbol(key);
      live.set(key, { token, value });
      return token;
    },
    /**
     * What the key means right now, for a caller that is about to hold it.
     *
     * A reader rather than a second `take`: whoever asks this is joining a
     * conversation somebody else created, and minting a token here would
     * silently retire the holder that did.
     */
    at: (key: string): symbol | undefined => live.get(key)?.token,
    /**
     * What is still on this key, for the one caller that has to end it.
     *
     * A token says whether somebody is there; it does not say who, and the
     * repair in the lookup needs the conversation itself to kill. Carried here
     * rather than in a second map beside this one, because two maps with one
     * lifetime is a pair that drifts.
     */
    valueAt: (key: string): A | undefined => live.get(key)?.value,
    current: (key: string, token: symbol | undefined): boolean =>
      token !== undefined && live.get(key)?.token === token,
    drop: (key: string, token: symbol): void => {
      if (live.get(key)?.token === token) live.delete(key);
    },
  };
};

/**
 * An effect that happens the first time it is asked and is nothing after.
 *
 * Exported for its test rather than for a second caller. What it guards is a
 * double release: a claim is let go either by the scope that took it or early
 * by the caller that learned it had claimed the wrong session, and if both ran
 * they would decrement a count that a *later* hold may have raised — releasing
 * a session an adapter is sitting in, which is the one thing the claim exists
 * to prevent.
 */
export const once = <A>(effect: Effect.Effect<A>): Effect.Effect<Effect.Effect<void>> =>
  Effect.map(Ref.make(false), (done) =>
    Effect.flatMap(Ref.getAndSet(done, true), (was) => (was ? Effect.void : Effect.asVoid(effect))),
  );

export const make = Effect.gen(function* () {
  // The daemon's own scope, captured here because a fiber that has to outlive
  // the request that started it needs somewhere to live. See
  // `mindUntilSettled`.
  const mine = yield* Effect.scope;
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
  const db = yield* Db;
  // ── the system defaults, read per conversation ─────────────────────────
  //
  // Taken at lookup rather than here, because `settings.ts` is deliberately
  // read per call: a person editing the file should not have to restart a
  // daemon holding a dozen ptys. The next conversation opened gets the new
  // answer; the ones already running keep theirs, which is the same rule the
  // agent's own command line follows.
  const config = yield* Settings;

  // ── who this daemon is, in a sentence somebody can act on ──────────────
  //
  // The port and not the pid alone, because the two-instance workflow in
  // AGENTS.md is exactly when this refusal fires: a person reading "already
  // open in the awp daemon on port 5274" knows which window to go to, where a
  // bare pid tells them to go hunting. The default matches `daemonUrl` in
  // `mcp.ts` — stated rather than imported, which would be a cycle for one
  // string.
  const port = process.env["AWP_DAEMON_PORT"];
  const claims: Claims = claimsOn(db, {
    owner: `the awp daemon on port ${port === undefined || port === "" ? "5274" : port}`,
    pid: process.pid,
  });

  /**
   * How many conversations in *this* process hold each session.
   *
   * `RcMap.invalidate` followed by `RcMap.get` — which is what `/new` and the
   * terminal fork both do — runs the new lookup while the old entry's scope is
   * still closing. Both are this pid, so both take and release the same claim,
   * and the release is the one that happens second: without this the fresh
   * conversation ends up holding nothing, and the next process to ask finds
   * the session free while an adapter is sitting in it.
   *
   * So the row is dropped when the last holder here lets go, not when the
   * first one does. The same shape as the `RcMap` above, one level down.
   */
  const holds = yield* Ref.make(new Map<string, number>());

  const hold = (sessionId: string) =>
    Ref.update(holds, (all) => new Map(all).set(sessionId, (all.get(sessionId) ?? 0) + 1));

  const letGo = (sessionId: string) =>
    Effect.flatMap(
      Ref.modify(holds, (all) => {
        const left = (all.get(sessionId) ?? 1) - 1;
        const rest = new Map(all);
        if (left > 0) {
          rest.set(sessionId, left);
        } else {
          rest.delete(sessionId);
        }
        return [left <= 0, rest];
      }),
      (last) => (last ? claims.release(sessionId) : Effect.void),
    );

  /**
   * Take a session, and hold it for as long as the caller's scope is open.
   *
   * Two guards, cheapest first — see `session-claim.ts`. The process table is
   * only asked when the session was `free`: anything else names a daemon that
   * was in it, and the `claude --resume` a `ps` finds under such a row is that
   * daemon's adapter — ours, or a predecessor's on its way out. Refusing on
   * either is refusing a conversation awp itself is handing over, which is the
   * restart lockout: `take` forgives the predecessor and this gave it straight
   * back.
   */
  const claimed = (sessionId: string) =>
    Effect.gen(function* () {
      const before = yield* claims.take(sessionId);
      if (before === "free") {
        const outside = yield* outsideHolders(spawner, sessionId, new Set([process.pid]));
        if (outside.length > 0) {
          yield* claims.release(sessionId);
          return yield* Effect.fail(heldOutside(sessionId, outside));
        }
      }
      yield* hold(sessionId);
      // The beat, for as long as the conversation is held. It is what turns
      // the row from a lock into an assertion about now: stop beating — crash,
      // kill -9, a laptop closed — and the claim decays instead of locking the
      // conversation out of every future daemon.
      const beating = yield* Effect.forkScoped(
        Effect.forever(Effect.andThen(Effect.sleep(BEAT_EVERY), claims.beat(sessionId))),
      );
      // Let go once, whichever way out comes first: the scope's finalizer is
      // the ordinary one, and the answer here is the early one — for a caller
      // that learns the session it claimed is not the session that exists.
      //
      // The beat is interrupted *before* the row goes, or a fiber nobody can
      // see writes the claim back a moment after it was released, and the
      // session is locked out until this daemon dies.
      const drop = yield* once(Effect.andThen(Fiber.interrupt(beating), letGo(sessionId)));
      yield* Effect.addFinalizer(() => drop);
      return drop;
    }).pipe(
      Effect.mapError((cause) =>
        cause instanceof ChatError
          ? cause
          : new ChatError({
              reason: (cause as { readonly reason?: string }).reason ?? String(cause),
              cause,
            }),
      ),
    );

  const readSession = db.prepare(
    "select session_id from chat_sessions where project = ? and workspace = ?",
  );
  const writeSession = db.prepare(
    `insert into chat_sessions (project, workspace, session_id) values (?, ?, ?)
       on conflict (project, workspace) do update set session_id = excluded.session_id`,
  );
  const dropSession = db.prepare("delete from chat_sessions where project = ? and workspace = ?");

  // The context reading, per session. See `ChatOptions.usage`: the adapter
  // sends none on a load, so a conversation nobody has spoken to today would
  // otherwise have no figure at all.
  const readUsage = db.prepare("select used, size from chat_usage where session_id = ?");
  const writeUsage = db.prepare(
    `insert into chat_usage (session_id, used, size) values (?, ?, ?)
       on conflict (session_id) do update set used = excluded.used, size = excluded.size`,
  );

  /**
   * Workspaces whose next conversation should start by forking the terminal's.
   *
   * A flag read by the lookup rather than a call made beside it, because the
   * fork has to happen *in the process that will hold the conversation* — see
   * `ChatOptions.fork`, and the measurement that says a fresh fork cannot be
   * loaded from anywhere else. So `openTerminal` cannot do the forking; all it
   * can do is arrange for the next open to.
   *
   * In memory and not in the database on purpose: it describes what to do the
   * next time an adapter starts, and a daemon that restarted has no adapter
   * and no window waiting on one.
   */
  const forkNext = yield* Ref.make<ReadonlySet<string>>(new Set());

  /**
   * What each workspace's *chat* agent is doing, and only its chat agent.
   *
   * ── this is a second source, not the answer ───────────────────────────────
   *
   * `workspace-state.ts` reads the same field out of a file the Go
   * implementation writes from Claude Code hooks, and its own note says ACP is
   * what replaces that — "a live notification instead of a hook writing a
   * file". This is that notification.
   *
   * It does **not** replace the file, and the reason is that the two describe
   * different agents. A workspace can have a `claude` running in its terminal
   * *and* a conversation open in this window; the file knows about the first
   * and this knows about the second. So neither can say the other is idle, and
   * the merge is a precedence rather than an override — see `factsWith` in
   * handlers.ts.
   *
   * A `SubscriptionRef` rather than a `Ref` and a queue, because what the
   * sidebar wants is exactly its two halves: the value now, for a window that
   * has just opened, and every change after it.
   */
  const statuses = yield* SubscriptionRef.make<ReadonlyMap<string, WorkspaceStatus>>(new Map());

  const setStatus = (key: string, status: WorkspaceStatus | undefined) =>
    SubscriptionRef.update(statuses, (all) => withStatus(all, key, status));

  /**
   * How to tell a workspace's watcher that a question has been answered.
   *
   * There is no update for it — the adapter does not report that a permission
   * was replied to, because the reply is a reply — so the one place that knows
   * is `answer` below. A map of callbacks rather than a field on the
   * conversation, because the watcher's counters belong to the watcher.
   */
  const forget = new Map<string, (request: string) => Effect.Effect<void>>();

  /** Which adapter each key currently means. See {@link generations}. */
  const current = generations<Conversation>();

  /**
   * The map, named before it exists, because a lookup has to invalidate the
   * entry it is building. Annotated rather than inferred: a lookup that closed
   * over `conversations` directly would give that constant a type written in
   * terms of its own initializer, which is not a type.
   */
  let map: RcMap.RcMap<string, Conversation, ChatError> | undefined;

  const conversations = yield* RcMap.make({
    lookup: (key: string) =>
      Effect.gen(function* () {
        const [project, workspace] = partsOf(key);

        // ── nothing may already be on this key, and if it is, it is stranded ─
        //
        // A lookup runs only when the key is **absent from the map**, so a
        // conversation still registered here is one nothing can reach: no
        // `get` will ever return it and no holder will ever be handed it
        // again. Whether it was orphaned by an `invalidate` this file has not
        // learned about yet, or is a release already tearing down, the answer
        // is the same and killing it is always right.
        //
        // The backstop rather than the guard — `retire` is what should have
        // ended it. It is here because the failure mode is silent
        // accumulation, which this daemon has now been caught by twice: two
        // adapters on one session, and before that fourteen daemons at once
        // (see `main.ts`). Both were invisible until somebody ran `ps`.
        //
        // Said out loud for that reason. It is the only record a recurrence
        // leaves, and `zmx history awp-dev-daemon | grep stranded` is the
        // question this makes answerable without a stakeout.
        const stranded = current.valueAt(key);
        const strandedToken = current.at(key);
        if (stranded !== undefined && strandedToken !== undefined) {
          yield* Effect.logWarning(
            `a stranded conversation was still on ${project}/${workspace} — killing it before opening another. This is a bug: something released the key without ending the adapter.`,
          );
          current.drop(key, strandedToken);
          yield* stranded.stop;
        }

        const remembered = yield* Effect.orElseSucceed(
          attempt("read the chat session", () => readSession.all(project, workspace)),
          () => [],
        );
        const known = remembered[0]?.["session_id"];
        // Taken, not read: a fork is a thing somebody asked for once, and a
        // flag left set would fork again every time the adapter timed out.
        const asked = yield* Ref.getAndUpdate(forkNext, (all) => {
          const rest = new Set(all);
          rest.delete(key);
          return rest;
        });

        // The workspace's own repository is not asked for here: this is the
        // *workspace* directory, and `.awp/config.json` is untracked, so a
        // fresh `jj workspace add` has no copy of it. The global file is the
        // one that answers for a checkout — the same reason the create job
        // reads the config from `input.repo` rather than from the workspace it
        // just made.
        const defaults = yield* config.read();
        // What that session last reported, when there is a session to ask
        // about. A new conversation has nothing, which is the honest answer:
        // it has spent nothing yet.
        const before =
          typeof known === "string"
            ? yield* Effect.orElseSucceed(
                attempt("read the context reading", () => readUsage.all(known)),
                () => [],
              )
            : [];
        const used = before[0]?.["used"];
        const size = before[0]?.["size"];
        // ── nobody else may be writing this transcript ────────────────────
        //
        // Before the adapter is spawned, not after: a refusal that arrives
        // once `claude --resume` is already running has already done the
        // thing it was meant to prevent.
        const entered = typeof known === "string" ? yield* claimed(known) : undefined;

        const held = yield* conversation(spawner, {
          cwd: workspacePath(project, workspace),
          ...(defaults.model === undefined ? {} : { model: defaults.model }),
          ...(defaults.effort === undefined ? {} : { effort: defaults.effort }),
          ...(defaults.mode === undefined ? {} : { mode: defaults.mode }),
          ...(typeof known === "string" ? { session: known } : {}),
          ...(asked.has(key) ? { fork: true } : {}),
          ...(typeof used === "number" && typeof size === "number" && size > 0
            ? { usage: { used, size } }
            : {}),
        });

        // What was actually opened may not be what was asked for — a new
        // conversation, a fork, or an id the adapter chose — and the claim has
        // to follow the session that exists rather than the one that was
        // remembered. Nothing can be holding a session this process has just
        // been given, so this refuses only if the store is wrong about us.
        if (held.sessionId !== known) {
          yield* claimed(held.sessionId);
          // And let go of the one claimed on the way in. It is not the session
          // that exists, the row below is about to stop pointing at it, and a
          // claim nothing points at is a transcript nobody can open again:
          // held for the life of this adapter and beaten the whole time, so it
          // never decays either. Two of them were sitting in the store.
          if (entered !== undefined) {
            yield* entered;
          }
        }

        // Written after the session exists rather than before, and every time
        // rather than only when it is new: a record that named a session the
        // adapter then refused to open would send the next open to the same
        // refusal. What is stored is what was actually opened.
        yield* Effect.ignore(
          attempt("remember the chat session", () =>
            writeSession.run(project, workspace, held.sessionId),
          ),
        );
        // ── watch what it is doing, for the sidebar ────────────────────────
        //
        // Folded from the conversation's own updates rather than asked for,
        // because there is nothing to ask: a turn is a state, and the daemon
        // is the thing that knows both of its edges.
        //
        //   a turn in flight             working
        //   a question nobody has answered   waiting — for a person, which is
        //                                    the one state that is about them
        //   neither                      nothing said, see below
        //
        // A second subscriber on the same conversation, which the numbered
        // queue was already built for. It reads history first, and history
        // carries the turn updates this process emitted before — hence the
        // count rather than a flag, the same reason the panel keeps one.
        const updates = yield* held.updates;
        const inFlight = yield* Ref.make(0);
        const asks = yield* Ref.make<ReadonlySet<string>>(new Set());
        const say = Effect.gen(function* () {
          const waiting = (yield* Ref.get(asks)).size > 0;
          const running = yield* Ref.get(inFlight);
          // **Idle is not reported at all**, and that is the point of the
          // whole arrangement. A chat sitting idle says nothing about the
          // agent somebody has running in the workspace's terminal, and
          // writing `idle` over the file's `working` would claim knowledge
          // this does not have.
          yield* setStatus(key, waiting ? "waiting" : running > 0 ? "working" : undefined);
        });
        yield* Effect.forkScoped(
          // `ensuring`, so the end of the updates is the end of `working`.
          //
          // This is what lets the hold above have no clock. The status is
          // derived from a counter of turn edges, and a turn whose `ended`
          // never arrives — because the adapter died holding it — would
          // otherwise read as in progress forever and pin a conversation
          // that has no process behind it. An update stream that has ended
          // is an adapter that has gone, whatever the counter says.
          Effect.ensuring(
            Effect.ignore(
              Stream.runForEach(updates, (update) =>
                Effect.gen(function* () {
                  if (update.kind === "turn") {
                    yield* Ref.update(inFlight, (was) =>
                      update.status === "started" ? was + 1 : Math.max(0, was - 1),
                    );
                  }
                  // Every reading, because the newest is the only true one and
                  // there are four of them a turn — a write of two integers
                  // against a cost measured in seconds of model time.
                  if (
                    update.kind === "usage" &&
                    update.used !== undefined &&
                    update.size !== undefined &&
                    update.size > 0
                  ) {
                    yield* Effect.ignore(
                      attempt("remember the context reading", () =>
                        writeUsage.run(
                          held.sessionId,
                          update.used as number,
                          update.size as number,
                        ),
                      ),
                    );
                  }
                  if (update.kind === "permission" && update.id !== undefined) {
                    yield* Ref.update(asks, (all) => new Set(all).add(update.id as string));
                  }
                  yield* say;
                }),
              ),
            ),
            setStatus(key, undefined),
          ),
        );
        // The row keeps no state of its own once the adapter has gone: an
        // answer that outlived its conversation is a claim about a process
        // that is not running.
        yield* Effect.addFinalizer(() => setStatus(key, undefined));
        forget.set(key, (id: string) =>
          Effect.andThen(
            Ref.update(asks, (all) => {
              const rest = new Set(all);
              rest.delete(id);
              return rest;
            }),
            say,
          ),
        );

        // ── an adapter with nothing under it must not survive ─────────────
        //
        // The reader ending is the only stop this side sees, and until now all
        // it did was fail the requests already in flight. The entry stayed,
        // so the next message went to a corpse: a fresh `session/prompt`
        // written at a pipe nobody reads, failed by `abandonWaiting` two
        // minutes later when the TTL finally took the row out.
        //
        // Registered last, so on a deliberate close it is the **first**
        // finalizer to run — finalizers are LIFO, the reader is interrupted
        // by one registered inside `conversation`, and by the time `gone`
        // completes this token is no longer the key's. That is how a kill
        // this process asked for is told from an adapter that died: one of
        // them has already given the key up.
        const token = current.take(key, held);
        yield* Effect.addFinalizer(() => Effect.sync(() => current.drop(key, token)));
        yield* Effect.forkIn(
          Effect.andThen(
            held.gone,
            Effect.suspend(() =>
              map === undefined || !current.current(key, token)
                ? Effect.void
                : Effect.andThen(
                    Effect.sync(() => current.drop(key, token)),
                    RcMap.invalidate(map, key),
                  ),
            ),
          ),
          // The daemon's scope and not this entry's: a fiber that lived here
          // would be interrupted by the very close it exists to cause.
          mine,
        );

        return held;
      }),
    // A held-open adapter costs a process and a model's context, and a person
    // switching tabs comes back within seconds. Long enough that a tab switch
    // is free, short enough that a window left on the diff all afternoon is
    // not holding an agent open.
    idleTimeToLive: "2 minutes",
  });
  map = conversations;

  // `send` and `answer` acquire the conversation the same way `open` does,
  // rather than reading an index the way Sessions has to. The difference is
  // that writing to a session nobody is attached to has to fail — a pty is a
  // live thing — where saying something to a conversation nobody has open is
  // perfectly meaningful, and opening one to say it is the right answer.
  /**
   * Whether a turn is in flight on this key, and holding on until none is.
   *
   * ── the status is the key's, so the hold has to name the conversation ────
   *
   * `statuses` is keyed by workspace, not by adapter, and `WAITS` has no
   * `holdsFor` — a turn in progress is never killed. Those two are right on
   * their own and wrong together: once the key has been retired and re-got,
   * this poll is reading the **replacement's** status, so the holder of the
   * outgoing conversation waits for a turn that is not its own. An adapter
   * nobody can reach is then kept alive by whatever now answers to its name,
   * and on a busy workspace that is indefinitely.
   *
   * So the token is part of the question. The moment the key means a different
   * conversation this holder has nothing left to protect: the turn it was
   * minding belonged to a process that has already been retired.
   */
  const busyOn = (key: string, token: symbol | undefined) =>
    Effect.map(
      SubscriptionRef.get(statuses),
      (all) => current.current(key, token) && all.get(key) !== undefined,
    );
  const settled = (key: string, token: symbol | undefined) =>
    settledWhen(busyOn(key, token), WAITS);

  /**
   * Hold the adapter open for the length of a turn, whoever is watching.
   *
   * ── the bug this exists for, measured on itself ───────────────────────────
   *
   * `idleTimeToLive` releases a conversation two minutes after its last
   * reference goes, and releasing it kills the adapter. The comment there says
   * "a person switching tabs comes back within seconds", which is true and is
   * not the case that hurts: **the window's reference is the chat panel's
   * subscription, and Base UI unmounts a hidden tab.** So switching the
   * accessory column to the diff — which is exactly what somebody does while
   * an agent works — starts a two-minute clock on the agent that is working.
   *
   * What it looks like from outside is the agent dying mid-thought. Read off
   * this repository's own transcript, seven times in one afternoon:
   *
   *   every stall   the last message was `stop_reason: tool_use`, and the
   *                 tool result never came back — there was no longer a
   *                 process to give it to
   *   the adapter   2m29s old against a conversation two hours old, having
   *                 started at the exact second of the last stall
   *   and the tell  the model choice reverts on every respawn, so it was
   *                 re-selected by hand once per death
   *
   * `Chat.brief` already holds a reference this way, because the create job
   * has no window — and the interactive path, which has a window that is free
   * to look away, was the one without it. The hold is the same `settled`, so
   * it is bounded by the same two numbers: a turn that never starts gives up
   * after 30 seconds, and one still running after 20 minutes stops being this
   * function's business.
   *
   * ── a turn, not a conversation ────────────────────────────────────────────
   *
   * Only while something is happening. The TTL's argument is right for an idle
   * conversation — a held adapter is a process and a model's context — and the
   * whole of what was wrong is that it was applied to a running one. A
   * permission nobody has answered counts as happening, because `statuses`
   * reports it as `waiting`: an agent shot while asking a question loses the
   * question.
   *
   * `forkIn(mine)` and not `forkScoped`: the caller is a request, and a fiber
   * in the request's scope is interrupted when the reply is sent — which is
   * immediately, and is the one thing this must outlive.
   */
  const alone = oneAtATime(mine);
  const mindUntilSettled = (key: string) =>
    // One holder per conversation: a steer arriving mid-turn must not take a
    // second reference that nothing will release.
    alone(
      key,
      Effect.scoped(
        Effect.gen(function* () {
          yield* RcMap.get(conversations, key);
          // Read after the acquire, so it is the conversation this holder is
          // actually holding rather than whatever the key meant a moment ago.
          const token = current.at(key);
          // Settled, and still settled a moment later — see `untilQuiet`. The
          // plain `settled` would let go on the reading between a turn ending
          // and the queued message behind it starting.
          yield* untilQuiet(busyOn(key, token), WAITS, PROMOTES_WITHIN);
        }),
      ),
    );

  /**
   * Throw this key's conversation away, and mean it.
   *
   * ── `RcMap.invalidate` does not release a conversation anybody holds ──────
   *
   * Measured on a live daemon and then confirmed in Effect's own source
   * (`RcMap.ts`, 4.0.0-rc.112):
   *
   * ```ts
   *   MutableHashMap.remove(self.state.map, key)
   *   if (entry.refCount > 0) return      // ← key gone, scope NOT closed
   *   if (entry.fiber) yield* Fiber.interrupt(entry.fiber)
   *   yield* Scope.close(entry.scope, Exit.void)
   * ```
   *
   * The key is removed **unconditionally** and the scope is closed only if
   * nothing holds it. So an invalidate against a held conversation leaves the
   * adapter running with the map no longer pointing at it: unreachable,
   * unkillable, and counted by nothing. The next `get` misses, runs a fresh
   * lookup, and opens a **second** adapter on the same stored session — which
   * is two `claude` processes on one transcript, the thing `chat_claims`
   * exists to prevent and the one case it cannot see, because both holders
   * are this pid.
   *
   * Two adapters were found alive that way, twice in eleven minutes. It is not
   * a slow close: `probe:adapter-release` measures every close this file makes
   * returning in single-digit milliseconds and killing its child, bun and all.
   * The scope is never closed at all.
   *
   * So the process is ended here rather than left to a holder that may never
   * let go. `getOption` and never `get`: the point is to end what is there,
   * and `get` would spawn an adapter for the privilege of killing it.
   *
   * The token goes too, which is what releases anyone still minding a turn on
   * this key — see `settled`. Without that the holder waits on the status of
   * whatever takes the key next.
   */
  const retire = (key: string) =>
    Effect.scoped(
      Effect.gen(function* () {
        const was = current.at(key);
        const one = yield* Effect.orElseSucceed(RcMap.getOption(conversations, key), () =>
          Option.none<Conversation>(),
        );
        yield* RcMap.invalidate(conversations, key);
        if (was !== undefined) current.drop(key, was);
        if (Option.isSome(one)) yield* one.value.stop;
      }),
    );

  const held = <A>(
    project: string,
    workspace: string,
    use: (conversation: Conversation) => Effect.Effect<A, ChatError>,
  ): Effect.Effect<A, ChatError> =>
    Effect.scoped(Effect.flatMap(RcMap.get(conversations, keyOf(project, workspace)), use));

  return {
    open: (project: string, workspace: string) =>
      Effect.flatMap(RcMap.get(conversations, keyOf(project, workspace)), (one) => one.updates),

    send: (project: string, workspace: string, text: string, key: string, interrupt: boolean) =>
      Effect.tap(
        held(project, workspace, (one) => one.send(text, key, interrupt)),
        // After the send rather than before it: a refusal has no turn to hold
        // open, and a holder waiting 30 seconds for one that will never start
        // would keep a dead conversation alive for exactly as long.
        () => mindUntilSettled(keyOf(project, workspace)),
      ),

    brief: (project: string, workspace: string, text: string) =>
      held(project, workspace, (one) =>
        Effect.andThen(
          // A key of its own, because there is no client to have minted one:
          // a brief is a job talking, and the only thing that reads the key is
          // the dedupe in whatever window opens the conversation later.
          // Never an interrupt: a brief is the *first* thing said to a new
          // conversation, so there is nothing running to cut short, and a job
          // is in no position to decide to stop somebody's agent.
          one.send(text, `brief-${crypto.randomUUID()}`, false),
          // `suspend`, so the token is read once the conversation exists — and
          // read at all, so a job waiting on a brief lets go if the chat is
          // retired underneath it rather than minding a stranger's turn.
          Effect.suspend(() => {
            const key = keyOf(project, workspace);
            return settled(key, current.at(key));
          }),
        ),
      ),

    /**
     * Point this workspace's chat at a fork of the terminal's conversation.
     *
     * Three steps, and each is there for a reason the other two do not cover:
     *
     *   invalidate   the adapter being held is on the old session, and it
     *                lives for two minutes after the last reader goes. The
     *                window would otherwise re-subscribe to what it had.
     *   mark         the fork itself must happen inside the *new* adapter —
     *                a fork is not loadable from another process, measured
     *   acquire      eagerly, so this call can answer with the new id and a
     *                refusal ("nothing else here to open") lands on the press
     *                rather than silently on the next subscribe
     *
     * The window then re-subscribes and finds this same adapter still held,
     * which is why the fork is not made twice.
     */
    held: (project: string, workspace: string) =>
      attempt("read the chat session", () => readSession.all(project, workspace)).pipe(
        Effect.map((rows) => rows.length > 0),
        Effect.mapError((error) => new ChatError({ reason: error.reason, cause: error.cause })),
      ),

    openTerminal: (project: string, workspace: string) =>
      Effect.gen(function* () {
        const key = keyOf(project, workspace);
        yield* retire(key);
        yield* Ref.update(forkNext, (all) => new Set(all).add(key));
        return yield* Effect.scoped(
          Effect.map(RcMap.get(conversations, key), (one) => one.sessionId),
        );
      }).pipe(
        // A refusal must not leave the flag armed, or the next ordinary open
        // of this workspace would try to fork on somebody's behalf minutes
        // later, with nothing on screen having asked for it.
        Effect.tapError(() =>
          Ref.update(forkNext, (all) => {
            const rest = new Set(all);
            rest.delete(keyOf(project, workspace));
            return rest;
          }),
        ),
      ),

    /**
     * Start again: forget the session, throw the adapter away, open a new one.
     *
     * The order is the whole of it, and each step is there for something the
     * others do not cover.
     *
     *   invalidate   the adapter being held is on the old session and lives
     *                for two minutes after the last reader goes. Without this
     *                the window re-subscribes to exactly what it asked to
     *                leave
     *   forget       the row is what the lookup reads to decide between
     *                `session/load` and `session/new`. Left in place, the new
     *                adapter loads the conversation again and nothing has
     *                changed
     *   acquire      eagerly, so the new session id can be answered and a
     *                refusal lands on the keypress rather than silently on
     *                the next subscribe
     *
     * Invalidate *before* forgetting, not after: the finalizer of the old
     * adapter writes nothing, but the lookup of a *new* one would race a
     * delete that had not landed yet, and the failure would be a `/new` that
     * silently continued the old conversation.
     */
    fresh: (project: string, workspace: string) =>
      Effect.gen(function* () {
        const key = keyOf(project, workspace);
        yield* retire(key);
        // Ignored, like every other write to this table. A row that could not
        // be deleted means the next open continues the old conversation, which
        // is the previous behaviour rather than a broken one — and refusing to
        // start a conversation because a pointer could not be cleared would be
        // the worse answer.
        yield* Effect.ignore(
          attempt("forget the chat session", () => dropSession.run(project, workspace)),
        );
        return yield* Effect.scoped(
          Effect.map(RcMap.get(conversations, key), (one) => one.sessionId),
        );
      }),

    cancel: (project: string, workspace: string) => held(project, workspace, (one) => one.cancel),

    answer: (project: string, workspace: string, request: string, option: string) =>
      Effect.tap(
        held(project, workspace, (one) => one.answer(request, option)),
        () => forget.get(keyOf(project, workspace))?.(request) ?? Effect.void,
      ),

    config: (project: string, workspace: string) => held(project, workspace, (one) => one.config),

    set: (project: string, workspace: string, option: string, value: string) =>
      held(project, workspace, (one) => one.set(option, value)),

    /**
     * Every workspace's status, now and on each change.
     *
     * `changes` is not decoration — see `withStatus`. Without it this
     * announces a non-change once per streamed chunk, and because
     * `WorkspaceFactsChanges` is `zipLatest` of the facts table and this, each
     * of those re-sends the whole table. It is not a delta; it is the feed
     * declining to speak when nothing moved.
     */
    statuses: () =>
      Stream.changes(
        Stream.concat(
          Stream.fromEffect(SubscriptionRef.get(statuses)),
          SubscriptionRef.changes(statuses),
        ),
      ),
  };
});

export const layer: Layer.Layer<
  Chat,
  never,
  ChildProcessSpawner.ChildProcessSpawner | Db | Settings
> = Layer.effect(Chat)(make);
