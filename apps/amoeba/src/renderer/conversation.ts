import type { ChatCommand, ChatUpdate } from "@awp-kit/protocol";

// What a conversation looks like once the updates have been folded together.
//
// Kept out of `Chat.tsx` so it can be tested: a file importing StyleX cannot be
// loaded by vitest — `stylex.defineConsts` throws at runtime, because the whole
// point of it is to be compiled away — so anything worth an assertion has to
// live beside the component rather than inside it.
//
// ── an update is a patch, not a row ────────────────────────────────────────
//
// One `cat` arrived as five updates sharing one id: pending with a generic
// title, then the command, then the output, then completed. So this merges by
// id, and a chunk of text appends to the message above it rather than starting
// a new one. Appending each update as its own row is the obvious first version
// and draws one tool call five times.
//
// ── a steer is not a reply, and turns overlap ──────────────────────────────
//
// Measured against a real adapter, `bun run probe:steer` — a long turn, and a
// second message sent twelve seconds into it:
//
//   0s    turn started          the first turn
//   2.7s  agent "…"
//   12s   turn started          ← the steer. The first turn is still working
//   20.7s turn ended            the FIRST turn, while the second still runs
//   23s   agent "heron"
//   23.1s turn ended
//
//   user chunks echoed back   0      the adapter never sends the steer back
//
// Three things follow, and each was a way the panel had it wrong:
//
//   *turns overlap*, so `running` is a count. As a flag the first `ended`
//   cleared it while the steer's own turn was still working, and the panel
//   went quiet for the two seconds before the answer arrived.
//
//   *nothing echoes a steer back*, so the local copy is the only record of
//   what a person typed. It cannot be dropped in favour of the wire.
//
//   *a steer is answered after the turn it interrupted*, so it is queued, not
//   said. Appending it to the end put it above the rest of a reply that was
//   already in flight, and two turns in the transcript read as though the
//   agent answered a question before it was asked.
//
// So a queued message floats at the tail and everything the agent is still
// saying is inserted above it — which is where it belongs, because it belongs
// to the turn before it.
//
// ── and then the daemon stopped producing that sequence ────────────────────
//
// The two turns above were the daemon sending a second `session/prompt`, which
// is the wrong request: the adapter has `_session/steering`, which injects the
// message into the turn already running. With it the same run reads
//
//   turns  started → ended        one turn, the steer inside it
//
// and there is nothing to queue. That does not make any of this dead code — an
// agent that does not advertise steering still gets the two-turn shape, and so
// does a message sent while the adapter happens to be between turns. What it
// does mean is that **whether a message waits is the daemon's answer, not a
// guess made here**: `ChatSend` reports `steer` or `prompt`, and `waiting`
// below is applied to the one case that really waits. Setting it from
// `running` at send time was the first version and it showed a `queued` label
// for a few milliseconds on every ordinary steer.

/** A message being built out of chunks. */
export interface Said {
  readonly kind: "said";
  readonly key: string;
  readonly role: "user" | "agent" | "thought";
  readonly text: string;
  /**
   * Typed while a turn was in flight, and not answered yet.
   *
   * True only of this window's own copy of a steer. Nothing on the wire is
   * ever queued: an update has by definition already happened.
   */
  readonly queued: boolean;
}

/** A tool call, merged from however many updates describe it. */
export interface Ran {
  readonly kind: "ran";
  readonly key: string;
  readonly title: string;
  /** `execute`, `read`, `edit` — what sort of thing it is, not what it ran. */
  readonly toolKind: string;
  readonly status: string;
  readonly output: string;
  /** Which kind of subagent this call spawned, when it spawned one. */
  readonly subagent: string | undefined;
  /** How long it has been running, in seconds. */
  readonly elapsed: number | undefined;
  /**
   * A question about this call, when there is one outstanding.
   *
   * On the row rather than beside it. The adapter emits the tool call before
   * it asks — so a separate question row was always a second copy of the
   * command that was already on screen directly above it, and the buttons
   * belonged to neither. Seen in a real window before it was fixed:
   *
   *   ran   for i in 1 2 3 4 5; do echo "$i"; sleep 3; done
   *   for i in 1 2 3 4 5; do echo "$i"; sleep 3; done      ← the question
   *   Deny  Allow Once  Always Allow
   */
  readonly ask: Asked | undefined;
  /** Why it looks stalled: a rate-limit retry the subagent is waiting out. */
  readonly retry:
    | {
        readonly attempt: number;
        readonly of?: number | undefined;
        readonly inMs?: number | undefined;
      }
    | undefined;
}

/** A question only a person can answer. */
export interface Asked {
  readonly kind: "asked";
  readonly key: string;
  readonly title: string;
  readonly options: ReadonlyArray<{ id: string; name: string; kind: string }>;
}

export type Item = Said | Ran | Asked;

/**
 * The conversation as the panel holds it.
 *
 * `running` is separate from the items because a turn is not a thing said — it
 * is a state the whole conversation is in, and drawing it as a row would put a
 * "working…" line permanently in the transcript once it had finished.
 */
export interface Conversation {
  readonly items: ReadonlyArray<Item>;
  /**
   * How many turns are in flight.
   *
   * A count, not a flag, and the measurement at the top is the reason: a
   * steer starts a second turn while the first is still working, and the
   * first one's end arrives first. A boolean cleared there says the agent has
   * finished while it is still answering — which is the one of the three
   * states it is worst to be wrong about.
   */
  readonly running: number;
  /** Why the last turn ended, when it ended for a reason worth saying. */
  readonly stopped: string | undefined;
  /**
   * How much of the context window is spoken for, as a fraction.
   *
   * A whole reading each time and never a delta: `size` is not constant —
   * measured at 200000 on a turn's first update and 1000000 on its last,
   * because the model in use has a larger window than the default and the
   * adapter learns that as it goes. Keeping an earlier `size` beside a later
   * `used` would report a session as five times fuller than it is.
   */
  readonly full: number | undefined;
  /**
   * The same reading in tokens, kept beside the fraction.
   *
   * The fraction is what the bar draws and these are what its tooltip says —
   * `18,606 of 200,000` is the answer to "how much is that", and a percentage
   * cannot be turned back into it. Both come from one update, so keeping them
   * apart would be two states that can disagree.
   */
  readonly used: number | undefined;
  readonly size: number | undefined;
  /**
   * The agent's own slash commands, skills included.
   *
   * On the conversation rather than fetched by the composer, because the
   * adapter *pushes* the set when it changes — a skill discovered as the agent
   * works in a subdirectory — and the daemon replays the last one to a window
   * that opens later. So the reducer that already reads the stream is the one
   * thing that has to know.
   */
  readonly commands: ReadonlyArray<ChatCommand>;
}

export const nothing: Conversation = {
  items: [],
  running: 0,
  stopped: undefined,
  full: undefined,
  used: undefined,
  size: undefined,
  commands: [],
};

/** Where the trailing run of queued messages starts, or the end of the list. */
const tail = (items: ReadonlyArray<Item>): number => {
  let at = items.length;
  while (at > 0) {
    const item = items[at - 1];
    if (item?.kind !== "said" || !item.queued) {
      return at;
    }
    at -= 1;
  }
  return at;
};

/**
 * The conversation so far, plus one more update.
 *
 * Pure, and exported, because this is the whole of what the panel does with
 * what the daemon sends — and the shapes it has to get right came off a real
 * turn rather than off the schema.
 */
export const fold = (state: Conversation, update: ChatUpdate): Conversation => {
  // Replaced, never merged — the adapter's own instruction, and it is why an
  // empty list is an answer rather than a no-op: a command that has gone
  // should stop being offered.
  if (update.kind === "commands") {
    return { ...state, commands: update.commands ?? [] };
  }

  // A turn is a state, not an entry. The daemon says so on either side of the
  // prompt it made, because nothing the adapter sends marks either edge — see
  // `send` in chat.ts. Replayed history carries these too, so a window opening
  // in the middle of a turn says so.
  if (update.kind === "turn") {
    if (update.status === "started") {
      return { ...state, running: state.running + 1, stopped: undefined };
    }
    const left = Math.max(0, state.running - 1);
    return {
      ...state,
      running: left,
      // A queued message stops being queued when a turn ends, because the turn
      // that ended is the one it was waiting behind. From here on it is the
      // current subject and the answer comes after it.
      items: state.items.map((item) =>
        item.kind === "said" && item.queued ? { ...item, queued: false } : item,
      ),
      // `end_turn` is the ordinary ending and says nothing worth a line.
      // Anything else — refused, cancelled, out of tokens — is the reason a
      // reply stopped where it did, and is the one case somebody needs told.
      //
      // Only once nothing is left in flight, though: a reason drawn under a
      // turn that is still working describes something that is not what the
      // agent is doing now.
      stopped:
        left > 0 || update.stopReason === undefined || update.stopReason === "end_turn"
          ? undefined
          : update.stopReason,
    };
  }

  if (update.kind === "usage") {
    const { used, size } = update;
    return used === undefined || size === undefined || size <= 0
      ? state
      : { ...state, full: used / size, used, size };
  }

  if (update.kind === "message") {
    const role = update.role ?? "agent";
    // Above anything queued. What the agent is still saying belongs to the
    // turn a steer interrupted, so it goes before it rather than after.
    const at = tail(state.items);
    const last = state.items[at - 1];
    // Chunks. A model answers in fragments and each is its own update, so a
    // new row per update would draw one sentence as a column of words.
    if (last?.kind === "said" && last.role === role) {
      return {
        ...state,
        items: [
          ...state.items.slice(0, at - 1),
          { ...last, text: last.text + (update.text ?? "") },
          ...state.items.slice(at),
        ],
      };
    }
    return {
      ...state,
      items: [
        ...state.items.slice(0, at),
        {
          kind: "said",
          key: `said-${String(state.items.length)}`,
          role,
          text: update.text ?? "",
          queued: false,
        },
        ...state.items.slice(at),
      ],
    };
  }

  if (update.kind === "tool" && update.id !== undefined) {
    const at = state.items.findIndex((item) => item.kind === "ran" && item.key === update.id);
    const found = at < 0 ? undefined : (state.items[at] as Ran);
    const merged: Ran = {
      kind: "ran",
      key: update.id,
      // Every field but the id is optional on the wire, and a later update
      // that says nothing about the title must not blank the one already
      // shown. That is what makes this a merge rather than a replacement.
      title: update.title ?? found?.title ?? "a tool",
      toolKind: update.toolKind ?? found?.toolKind ?? "",
      status: update.status ?? found?.status ?? "pending",
      output: update.output ?? found?.output ?? "",
      // The subagent facts arrive on the progress updates rather than on the
      // call, so they are merged like everything else — and a call that has
      // stopped retrying says nothing about `retry`, which must not blank a
      // sentence a person is reading. `undefined` is the absence and the merge
      // keeps whatever was last said.
      subagent: update.subagent ?? found?.subagent,
      elapsed: update.elapsed ?? found?.elapsed,
      retry: update.retry ?? found?.retry,
      ask: found?.ask,
    };
    const where = at < 0 ? tail(state.items) : at;
    return {
      ...state,
      items:
        at < 0
          ? [...state.items.slice(0, where), merged, ...state.items.slice(where)]
          : [...state.items.slice(0, at), merged, ...state.items.slice(at + 1)],
    };
  }

  if (update.kind === "permission" && update.id !== undefined) {
    const asked: Asked = {
      kind: "asked",
      key: update.id,
      title: update.title ?? "a tool wants to run",
      options: (update.options ?? []).map((option) => ({ ...option })),
    };
    // On the call it is about, when that call is on screen — which it nearly
    // always is, because the adapter emits the tool call before it asks.
    const about =
      update.about === undefined
        ? -1
        : state.items.findIndex((item) => item.kind === "ran" && item.key === update.about);
    if (about >= 0) {
      return {
        ...state,
        items: state.items.map((item, index) =>
          index === about ? { ...(item as Ran), ask: asked } : item,
        ),
      };
    }
    // A question about a call this window has not been told about is still a
    // question, and refusing to draw it would leave the agent waiting on
    // somebody who cannot see what it asked.
    const at = tail(state.items);
    return {
      ...state,
      items: [...state.items.slice(0, at), asked, ...state.items.slice(at)],
    };
  }

  return state;
};

/**
 * Something this window has just sent, before anything has echoed it back.
 *
 * Its own function rather than a `fold` over a synthesized update, and that is
 * the fix for the reorder as much as the queueing is: the local copy is not
 * something the daemon said, and dressing it up as an update is what let it be
 * treated as one — merged into whatever was above it, and placed by arrival
 * order in a list arrival order does not describe.
 *
 * Measured: the adapter never echoes a user message back on a live turn, so
 * this is the only record of it until the session is opened again, when
 * `session/load` replays it.
 */
export const mine = (state: Conversation, text: string, key: string): Conversation => ({
  ...state,
  items: [
    ...state.items,
    {
      kind: "said",
      key,
      role: "user",
      text,
      // Never queued on the way out, and that is a deliberate change from the
      // first version, which set it from `running` here. Whether a mid-turn
      // message waits is not this side's to decide: the adapter steers it into
      // the running turn when it can, and only its answer says which happened.
      // Guessing produced a `queued` that appeared for a few milliseconds and
      // then took itself back on every ordinary steer.
      queued: false,
    },
  ],
});

/**
 * The message on `key` is waiting behind a turn.
 *
 * Said by the daemon rather than worked out here — `ChatSend` answers `steer`
 * when the message was injected into the turn already running and `prompt`
 * when it started one of its own. A `prompt` sent while the agent was working
 * is the one case that waits, and it is the one this marks.
 *
 * The key comes from the caller for the same reason: this has to name the
 * message it just sent, and a position in a list is not a name — the list
 * grows while the reply is in the air.
 */
export const waiting = (state: Conversation, key: string): Conversation => ({
  ...state,
  items: state.items.map((item) =>
    item.kind === "said" && item.key === key ? { ...item, queued: true } : item,
  ),
});

/**
 * What a tool is, in a word, from the agent's own vocabulary.
 *
 * A row that says only what was run leaves the reader parsing a command to
 * find out whether anything was written. The kind is the field that answers
 * it, and it is already on the wire.
 */
export const verb = (item: Ran): string =>
  // A delegated call is the exception, and it is the one worth making: `ran
  // Task` is what a spawn used to read as, which says neither that work was
  // handed off nor to what.
  item.subagent !== undefined
    ? "spawned"
    : item.toolKind === "execute"
      ? "ran"
      : item.toolKind === "read"
        ? "read"
        : item.toolKind === "edit"
          ? "edited"
          : item.toolKind === "search"
            ? "searched"
            : item.toolKind === ""
              ? "did"
              : item.toolKind;

/**
 * The transcript as blocks, with a run of tool calls counted as one.
 *
 * ── why the grouping is not cosmetic ──────────────────────────────────────
 *
 * Reported twice: the tool lines are "hard to look at", and "they should
 * collapse probably after some length". A turn is regularly a dozen calls
 * between two sentences, and drawn as a dozen equal rows they are most of the
 * transcript by height while being the least of it by interest — the answer is
 * what somebody came to read.
 *
 * So consecutive `ran` items become one block the eye can take in or skip, and
 * a long block draws its tail with a count for the rest. Only *consecutive*
 * ones: a call after a sentence is a new piece of work, and merging across the
 * sentence would put the two in one box and lose the order they happened in.
 *
 * Everything else passes through untouched, which is what keeps this a
 * grouping rather than a second transcript model.
 */
export type Block =
  | { readonly kind: "one"; readonly key: string; readonly item: Item }
  | { readonly kind: "calls"; readonly key: string; readonly items: ReadonlyArray<Ran> };

export const grouped = (items: ReadonlyArray<Item>): ReadonlyArray<Block> => {
  const out: Array<Block> = [];
  for (const item of items) {
    const last = out.at(-1);
    if (item.kind === "ran" && last?.kind === "calls") {
      out[out.length - 1] = { ...last, items: [...last.items, item] };
      continue;
    }
    if (item.kind === "ran") {
      out.push({ kind: "calls", key: item.key, items: [item] });
      continue;
    }
    out.push({ kind: "one", key: item.key, item });
  }
  return out;
};

/**
 * A tool call's subject, in the three pieces a person reads it in.
 *
 * ── what was wrong with drawing the title whole ────────────────────────────
 *
 * Reported as "tool lines are hard to read i cant parse the important info
 * from them". The title is whatever the agent named — a path, a command, a
 * query — and drawn as one run of monospace with `overflow-wrap: anywhere` it
 * wrapped mid-word across three lines with nothing in it emphasised:
 *
 *     read  apps/amoeba/src/renderer/highlig
 *     hting.tsx
 *
 * The information is not evenly spread. For a path it is the **basename** —
 * `highlighting.tsx` is what somebody is looking for, and the directories are
 * where it happens to live. For a command it is the **first line**; a heredoc
 * or a `&&` chain continues below and is the part opening the row is for.
 *
 * So `lead` is muted and allowed to clip, `name` is not, and `more` counts the
 * lines that were left off. A path is recognised by holding a slash and no
 * whitespace — deliberately narrow, because a *command* with a path in it
 * ("cat src/x.ts") must keep its verb, and the verb is what a naive split at
 * the last slash would throw away.
 */
export interface ToolTitle {
  /** The part that can be clipped: a path's directories, or nothing. */
  readonly lead: string;
  /** The part that may not be: a basename, or the first line. */
  readonly name: string;
  /** How many further lines there are, which the row says rather than draws. */
  readonly more: number;
}

export const toolTitle = (title: string): ToolTitle => {
  const lines = title.split("\n");
  const first = lines[0] ?? "";
  const more = Math.max(0, lines.length - 1);
  const cut = first.lastIndexOf("/");
  // A path, and not a command that mentions one. `\s` rather than " ": a tab
  // in a title is the same evidence, and a trailing newline is already gone.
  if (cut > 0 && cut < first.length - 1 && !/\s/.test(first)) {
    return { lead: first.slice(0, cut + 1), name: first.slice(cut + 1), more };
  }
  return { lead: "", name: first, more };
};

/** How long something has been going, in the shortest form that is honest. */
export const took = (seconds: number): string =>
  seconds < 60
    ? `${String(Math.round(seconds))}s`
    : `${String(Math.floor(seconds / 60))}m${String(Math.round(seconds % 60)).padStart(2, "0")}s`;

/**
 * Why a delegated call looks stalled, as a sentence.
 *
 * The adapter forwards these so a client can say why a spawn is sitting
 * still — a subagent waiting out a rate limit and a subagent doing slow work
 * are otherwise the same picture, and only one of them is worth waiting for.
 */
export const stalled = (item: Ran): string | undefined => {
  const retry = item.retry;
  if (retry === undefined) {
    return undefined;
  }
  const count =
    retry.of === undefined
      ? `attempt ${String(retry.attempt)}`
      : `attempt ${String(retry.attempt)} of ${String(retry.of)}`;
  return retry.inMs === undefined
    ? `${count}, retrying`
    : `${count}, retrying in ${took(retry.inMs / 1000)}`;
};
