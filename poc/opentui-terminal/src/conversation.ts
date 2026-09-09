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

import type { ChatUpdate } from "@awp-kit/protocol";

export type Item =
  | { kind: "said"; role: "user" | "agent" | "thought"; text: string; turn: number; mine?: boolean }
  | {
      kind: "tool";
      id: string;
      title: string;
      status: string;
      output: string;
      toolKind?: string;
      subagent?: string;
      ask?: { request: string; options: ReadonlyArray<{ id: string; label: string }> };
    };

export type Conversation = {
  readonly items: ReadonlyArray<Item>;
  /** Turns in flight. Zero means the agent is not working. */
  readonly running: number;
  /** Which turn we are in, so a new turn starts a new block. */
  readonly turn: number;
  readonly asks: number;
};

export const empty: Conversation = { items: [], running: 0, turn: 0, asks: 0 };

const roleOf = (update: ChatUpdate): "user" | "agent" | "thought" =>
  update.role === "user" ? "user" : update.role === "thought" ? "thought" : "agent";

export const fold = (state: Conversation, update: ChatUpdate): Conversation => {
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
    const subagent = update.subagent ?? before?.subagent;
    const merged: Item = {
      kind: "tool",
      id: update.id,
      title: update.title ?? before?.title ?? "",
      status: update.status ?? before?.status ?? "",
      output: update.output ?? before?.output ?? "",
      ...(toolKind === undefined ? {} : { toolKind }),
      ...(subagent === undefined ? {} : { subagent }),
      ...(before?.ask === undefined ? {} : { ask: before.ask }),
    };
    return at < 0
      ? { ...state, items: [...state.items, merged] }
      : { ...state, items: state.items.map((item, index) => (index === at ? merged : item)) };
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

  // `usage` says nothing a person reads.
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
export const mine = (state: Conversation, text: string): Conversation => ({
  ...state,
  items: [...state.items, { kind: "said", role: "user", text, turn: state.turn, mine: true }],
});
