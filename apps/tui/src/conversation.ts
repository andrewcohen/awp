// A stream of updates, folded into something a column can draw.
//
// The daemon sends chunks: a message arrives as many `message` updates that
// append, and one tool call arrives as five `tool` updates that have to be
// merged by id or a single `cat` draws five rows. That merge is the whole of
// this file, and it is the same shape the amoeba panel's fold has — kept
// separate rather than imported because that one returns React items and the
// renderer may not import a node builtin, so the two live on opposite sides of
// a lint rule.
//
//   message  role, text        appended to the block above it when the role
//                              and the turn match, so a paragraph is a block
//   tool     id, title, status merged by id, in the position it first appeared
//   permission               → hung on the tool it is `about`, because the
//                              adapter emits the call before it asks and a
//                              standalone row is the same command twice
//   turn     started/ended     a count, not a flag: a steer means two turns
//                              overlap and the first `ended` arrives while the
//                              second is still working

import type { ChatCommand, ChatDiff, ChatUpdate } from "@awp-kit/protocol";
import { toolLabel, toolTitleOf } from "@awp-kit/protocol/tools";

export type Item =
  | {
      kind: "said";
      role: "user" | "agent" | "thought";
      text: string;
      turn: number;
      mine?: boolean;
      /**
       * The daemon's name for a whole message, when it had one.
       *
       * Only an echo of what somebody typed carries one — see `ChatSend.key`
       * — and it is what makes applying the echo twice a no-op: the client
       * that sent it already drew the row.
       */
      key?: string;
    }
  | {
      kind: "tool";
      id: string;
      title: string;
      status: string;
      output: string;
      /**
       * What it changed on disk, as patches. Empty for every call that
       * changed nothing, which is most of them.
       *
       * Composed by the daemon — see `ChatDiff` — so this column and the
       * window draw the same patch rather than each diffing two texts. It is
       * also what decides whether a row is worth a block of its own: see
       * {@link grouped}.
       */
      diffs?: ReadonlyArray<ChatDiff>;
      toolKind?: string;
      /**
       * The tool's own name — `Bash`, `Read`, `mcp__awp__awp_thread`.
       *
       * What a row is labelled with — when it is labelled at all. The kind
       * is ten values for fifty tools: see `ChatUpdate.toolName`, and
       * `toolLabel` for what is suppressed and why.
       */
      toolName?: string;
      /**
       * What the call is for — Bash's own `description`, forwarded by the
       * adapter. Drawn in place of the command; see `toolTitleOf`.
       */
      purpose?: string;
      subagent?: string;
      /**
       * Which turn made the call.
       *
       * Here so that a run of receipts can be left open while the turn that
       * is producing it is still going, and rolled up when it ends — see
       * {@link grouped}. Nothing on the wire carries it: a tool update has an
       * id and no turn, so it is taken from the fold's own count at the
       * moment the row first appears.
       */
      turn?: number;
      ask?: {
        request: string;
        options: ReadonlyArray<{ id: string; label: string }>;
        /** What was chosen, once anybody in any client has chosen. */
        answered?: string;
      };
    };

export type Conversation = {
  readonly items: ReadonlyArray<Item>;
  /** Turns in flight. Zero means the agent is not working. */
  readonly running: number;
  /** Which turn we are in, so a new turn starts a new block. */
  readonly turn: number;
  readonly asks: number;
  /** Tokens spent so far, when anything has said. */
  readonly used?: number;
  /** The context window those tokens are out of. */
  readonly size?: number;
  /**
   * The agent's own slash commands, skills included.
   *
   * On the conversation rather than asked for, because the adapter *pushes*
   * the set when it changes — a skill discovered as the agent works in a
   * subdirectory — and the daemon replays the last one to a client that opens
   * later. So the thing already reading the stream is the one that has to
   * know.
   */
  readonly commands: ReadonlyArray<ChatCommand>;
};

export const empty: Conversation = { items: [], running: 0, turn: 0, asks: 0, commands: [] };

const roleOf = (update: ChatUpdate): "user" | "agent" | "thought" =>
  update.role === "user" ? "user" : update.role === "thought" ? "thought" : "agent";

export const fold = (state: Conversation, update: ChatUpdate): Conversation => {
  // Replaced, never merged — the adapter's own instruction, and it is why an
  // empty list is an answer rather than a no-op: a command that has gone
  // should stop being offered.
  if (update.kind === "commands") {
    return { ...state, commands: update.commands ?? [] };
  }

  if (update.kind === "turn") {
    const started = update.status === "started";
    return {
      ...state,
      running: started ? state.running + 1 : Math.max(0, state.running - 1),
      // A new turn on the way in, so the next thing anybody says is a new
      // block rather than an append to the last answer.
      turn: started ? state.turn + 1 : state.turn,
    };
  }

  if (update.kind === "message") {
    const text = update.text ?? "";
    if (text === "") return state;
    const role = roleOf(update);
    // A named message is the daemon's echo of what somebody typed, in this
    // client or another one — a whole message rather than a chunk, and
    // idempotent by key so the sender does not draw its own twice.
    if (update.id !== undefined) {
      const key = update.id;
      return state.items.some((item) => item.kind === "said" && item.key === key)
        ? state
        : {
            ...state,
            items: [...state.items, { kind: "said", role, text, turn: state.turn, key }],
          };
    }
    const last = state.items.at(-1);
    if (last?.kind === "said" && last.role === role && last.turn === state.turn) {
      return {
        ...state,
        items: [...state.items.slice(0, -1), { ...last, text: last.text + text }],
      };
    }
    return { ...state, items: [...state.items, { kind: "said", role, text, turn: state.turn }] };
  }

  if (update.kind === "tool" && update.id !== undefined) {
    const at = state.items.findIndex((item) => item.kind === "tool" && item.id === update.id);
    const was = at < 0 ? undefined : state.items[at];
    const before = was?.kind === "tool" ? was : undefined;
    // A later update carries only what changed, so every field falls back to
    // what the row already had. The `ask` is the one that matters: a progress
    // beat arriving after a permission request would otherwise drop the
    // question, leaving the agent waiting on a row with no buttons.
    // Named rather than inlined: `a ?? b === undefined` parses as
    // `a ?? (b === undefined)`, which is how the tool kind got dropped the
    // first time this was written.
    const toolKind = update.toolKind ?? before?.toolKind;
    const toolName = update.toolName ?? before?.toolName;
    const purpose = update.purpose ?? before?.purpose;
    const subagent = update.subagent ?? before?.subagent;
    // Replaced, never merged: the adapter sends its guess at the change when
    // the call is made and the real one when it has run, about the same file,
    // so a merge draws the edit twice.
    const diffs = update.diffs ?? before?.diffs;
    const merged: Item = {
      kind: "tool",
      id: update.id,
      title: update.title ?? before?.title ?? "",
      status: update.status ?? before?.status ?? "",
      output: update.output ?? before?.output ?? "",
      ...(diffs === undefined ? {} : { diffs }),
      ...(toolKind === undefined ? {} : { toolKind }),
      ...(toolName === undefined ? {} : { toolName }),
      ...(purpose === undefined ? {} : { purpose }),
      ...(subagent === undefined ? {} : { subagent }),
      // The turn it first appeared in, kept across every later patch: a call
      // that finishes after the turn ended still belongs to the turn that
      // made it.
      turn: before?.turn ?? state.turn,
      ...(before?.ask === undefined ? {} : { ask: before.ask }),
    };
    return at < 0
      ? { ...state, items: [...state.items, merged] }
      : { ...state, items: state.items.map((item, index) => (index === at ? merged : item)) };
  }

  // Answered, by whoever answered it. The buttons go and what was chosen
  // stays: nothing in ACP reports this, so the daemon says it — and without it
  // this client goes on offering a question settled in the window minutes ago.
  if (update.kind === "permission" && update.id !== undefined && update.status === "answered") {
    const id = update.id;
    return {
      ...state,
      asks: Math.max(0, state.asks - 1),
      items: state.items.map((item) =>
        item.kind === "tool" && item.ask?.request === id
          ? {
              ...item,
              ask: {
                ...item.ask,
                answered:
                  item.ask.options.find((option) => option.id === update.chose)?.label ??
                  "answered",
              },
            }
          : item,
      ),
    };
  }

  if (update.kind === "permission" && update.id !== undefined) {
    const options = (update.options ?? []).map((option) => ({
      id: option.id,
      label: option.name,
    }));
    const ask = { request: update.id, options };
    const at = state.items.findIndex((item) => item.kind === "tool" && item.id === update.about);
    if (at >= 0) {
      return {
        ...state,
        asks: state.asks + 1,
        items: state.items.map((item, index) =>
          index === at && item.kind === "tool" ? { ...item, ask } : item,
        ),
      };
    }
    // A question about a call this client was never told about. Refusing to
    // draw it would leave the agent waiting on somebody who cannot see what it
    // asked for.
    return {
      ...state,
      asks: state.asks + 1,
      items: [
        ...state.items,
        {
          kind: "tool",
          id: update.id,
          title: update.title ?? "permission",
          status: "asking",
          output: "",
          ask,
        },
      ],
    };
  }

  // ── the context figure ──────────────────────────────────────────────────
  //
  // Dropped here once, under a note saying it said nothing a person reads.
  // It is the only place the figure exists, and the status row under the
  // composer is where somebody deciding whether to start a fresh conversation
  // reads it.
  //
  // A whole reading each time and never a delta, and the newest wins: `size`
  // is not constant — measured 200000 on a turn's first update and 1000000 on
  // its last, because the adapter learns the model's real window as it goes.
  if (update.kind === "usage") {
    return {
      ...state,
      ...(update.used === undefined ? {} : { used: update.used }),
      ...(update.size === undefined ? {} : { size: update.size }),
    };
  }

  return state;
};

/**
 * What this client has just sent, before anything has echoed it back.
 *
 * The daemon emits only the turn's edges — see chat.ts's `send` — and the
 * adapter emits no user chunk on a live turn, so this is the only record of
 * what was typed until the conversation is opened again and `session/load`
 * replays it. Which is also the finding: a *second* client sees the turn start
 * and the answer, with the question missing.
 */
export const mine = (state: Conversation, text: string, key: string): Conversation => ({
  ...state,
  items: [...state.items, { kind: "said", role: "user", text, turn: state.turn, mine: true, key }],
});

/**
 * What a row is called.
 *
 * The rule is `toolVerb`, shared with the window — a `Bash` call labelled
 * `bash` here and `ran` there is two vocabularies for one conversation. This
 * is the shape adapter, and nothing more.
 */
export const verbOf = (item: Item): string => (item.kind === "tool" ? toolLabel(item) : "");

/**
 * The title worth drawing, which for a Bash call whose command has not
 * arrived yet is none. Shared with the window — see `toolTitleOf`.
 */
export const titleOf = (item: Item): string => (item.kind === "tool" ? toolTitleOf(item) : "");

/**
 * A call that has something to show, as opposed to one that leaves a receipt.
 *
 * The line is the patch. A `read`, a `grep`, a `bun run test` says what it did
 * in its title and nothing else is coming; an edit's whole content is what it
 * changed, and rolling that away leaves a transcript of an agent that
 * evidently did some work somewhere.
 *
 * A question is the other exception, for the reason it is everywhere else
 * here: it is the one row that wants something from a person.
 */
const standsAlone = (item: Item): boolean =>
  item.kind === "tool" &&
  ((item.diffs ?? []).length > 0 || (item.ask !== undefined && item.ask.answered === undefined));

/** The transcript as blocks, with a run of receipts counted as one. */
export type Block =
  | { readonly kind: "one"; readonly item: Item }
  | {
      readonly kind: "calls";
      readonly items: ReadonlyArray<Item>;
      /** Whether the turn that is making these is still going. */
      readonly live: boolean;
    };

/**
 * Roll up the calls that produced nothing to look at — once they are over.
 *
 * ── a run is folded when its turn ends, not while it is happening ────────
 *
 * The first version folded every run to its last three rows, which is the
 * window's rule. In a column that is the whole screen it reads wrong for the
 * one case somebody is actually watching: while the agent works, the calls
 * scrolling past *are* the progress, and hiding all but three of them hides
 * the thing being waited for. Once the turn has ended they are a receipt, and
 * a dozen receipts are most of the transcript by height and the least of it
 * by interest.
 *
 * So a block knows whether it is live, and the drawing decides — `live` is
 * the turn currently in flight, and a run belonging to it is drawn whole.
 *
 * What is never folded, live or not: a call that changed a file, and a
 * question. The change is the thing worth reading, and the question is the
 * one row that wants something from a person.
 *
 * Consecutive only, the same as the window's: a call after a sentence is a
 * new piece of work, and merging across the sentence loses the order things
 * happened in.
 */
export const grouped = (
  items: ReadonlyArray<Item>,
  /** The turn in flight, when one is. Its calls are drawn whole. */
  live?: number,
): ReadonlyArray<Block> => {
  const out: Block[] = [];
  for (const item of items) {
    if (item.kind !== "tool" || standsAlone(item)) {
      out.push({ kind: "one", item });
      continue;
    }
    const last = out.at(-1);
    if (last?.kind === "calls")
      out[out.length - 1] = { kind: "calls", items: [...last.items, item], live: last.live };
    else
      out.push({
        kind: "calls",
        items: [item],
        // A tool row with no turn on it predates the field; treating it as
        // finished is the safe way round, since the alternative is a
        // transcript that never folds anything again.
        live: item.turn !== undefined && item.turn === live,
      });
  }
  return out;
};
