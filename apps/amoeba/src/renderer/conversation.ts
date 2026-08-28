import type { ChatUpdate } from "@awp-kit/protocol";

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

/** A message being built out of chunks. */
export interface Said {
  readonly kind: "said";
  readonly key: string;
  readonly role: "user" | "agent" | "thought";
  readonly text: string;
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
  /** A turn is in flight: the agent is working and has not finished. */
  readonly running: boolean;
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
}

export const nothing: Conversation = {
  items: [],
  running: false,
  stopped: undefined,
  full: undefined,
};

/**
 * The conversation so far, plus one more update.
 *
 * Pure, and exported, because this is the whole of what the panel does with
 * what the daemon sends — and the shapes it has to get right came off a real
 * turn rather than off the schema.
 */
export const fold = (state: Conversation, update: ChatUpdate): Conversation => {
  // A turn is a state, not an entry. The daemon says so on either side of the
  // prompt it made, because nothing the adapter sends marks either edge — see
  // `send` in chat.ts. Replayed history carries these too, and the last one
  // wins, so a window opening in the middle of a turn says so.
  if (update.kind === "turn") {
    return update.status === "started"
      ? { ...state, running: true, stopped: undefined }
      : {
          ...state,
          running: false,
          // `end_turn` is the ordinary ending and says nothing worth a line.
          // Anything else — refused, cancelled, out of tokens — is the reason a
          // reply stopped where it did, and is the one case somebody needs told.
          stopped:
            update.stopReason === undefined || update.stopReason === "end_turn"
              ? undefined
              : update.stopReason,
        };
  }

  if (update.kind === "usage") {
    const { used, size } = update;
    return used === undefined || size === undefined || size <= 0
      ? state
      : { ...state, full: used / size };
  }

  if (update.kind === "message") {
    const role = update.role ?? "agent";
    const last = state.items.at(-1);
    // Chunks. A model answers in fragments and each is its own update, so a
    // new row per update would draw one sentence as a column of words.
    if (last?.kind === "said" && last.role === role) {
      return {
        ...state,
        items: [...state.items.slice(0, -1), { ...last, text: last.text + (update.text ?? "") }],
      };
    }
    return {
      ...state,
      items: [
        ...state.items,
        { kind: "said", key: `said-${String(state.items.length)}`, role, text: update.text ?? "" },
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
    };
    return {
      ...state,
      items:
        at < 0
          ? [...state.items, merged]
          : [...state.items.slice(0, at), merged, ...state.items.slice(at + 1)],
    };
  }

  if (update.kind === "permission" && update.id !== undefined) {
    return {
      ...state,
      items: [
        ...state.items,
        {
          kind: "asked",
          key: update.id,
          title: update.title ?? "a tool wants to run",
          options: (update.options ?? []).map((option) => ({ ...option })),
        },
      ],
    };
  }

  return state;
};

/**
 * What a tool is, in a word, from the agent's own vocabulary.
 *
 * A row that says only what was run leaves the reader parsing a command to
 * find out whether anything was written. The kind is the field that answers
 * it, and it is already on the wire.
 */
export const verb = (item: Ran): string =>
  item.toolKind === "execute"
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
