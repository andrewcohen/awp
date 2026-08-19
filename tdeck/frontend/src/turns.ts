// Folding a stream of events into a conversation.
//
// The backend sends what happened — a token, a tool starting, a tool finishing.
// The view renders what it means: turns, each with prose, thinking and the tools
// that ran inside it. This module is the whole of the translation, deliberately
// pure and deliberately not in a component: it is the piece most likely to be
// wrong about an edge case, and a pure function is the only kind you can be sure
// about without clicking.
//
// Why here and not on the server: the events are small and incremental, and
// re-sending whole turns down the wire on every token is exactly the mistake
// gdeck's chat made — megabytes a second to deliver one new sentence. Folding at
// the edge keeps the stream cheap and still keeps ACP's vocabulary out of the
// JSX, because the backend has already translated it into something neutral.

import type { UiEvent } from "./api";

export type Tool = {
  id: string;
  name: string;
  summary: string;
  detail: string;
  isError: boolean;
  file: string;
  patch: string;
};

export type Turn = {
  kind: "user" | "agent";
  text: string;
  thinking: string;
  tools: Tool[];
};

export type Conversation = {
  turns: Turn[];
  // The question the agent is currently waiting on, if any. Not part of a turn:
  // it is a thing to answer rather than a thing that happened, and it disappears
  // once answered rather than staying in the record.
  permission: { title: string; options: { id: string; name: string }[] } | null;
  usage: { used: number; size: number; cost?: number } | null;
  title: string;
  busy: boolean;
  error: string;
};

export const emptyConversation = (): Conversation => ({
  turns: [],
  permission: null,
  usage: null,
  title: "",
  busy: false,
  error: "",
});

// The turn the agent is currently speaking into, created on demand.
//
// Chunks arrive without any statement that a turn has begun, so "begun" is
// simply the first chunk after something else — a user message, or a `done`.
function speaking(turns: Turn[]): Turn {
  const last = turns[turns.length - 1];
  if (last && last.kind === "agent") return last;
  const fresh: Turn = { kind: "agent", text: "", thinking: "", tools: [] };
  turns.push(fresh);
  return fresh;
}

// Applies one event. Returns a new Conversation when something changed, so React
// re-renders, and the same one when nothing did.
//
// The turns array is copied shallowly and the touched turn replaced: enough for
// React to see a change without cloning a conversation of several hundred turns
// on every token.
export function apply(state: Conversation, event: UiEvent): Conversation {
  switch (event.kind) {
    case "user": {
      const turns = [
        ...state.turns,
        { kind: "user" as const, text: event.text, thinking: "", tools: [] },
      ];
      return { ...state, turns, busy: true, error: "" };
    }

    case "text": {
      const turns = [...state.turns];
      const turn = speaking(turns);
      turns[turns.length - 1] = { ...turn, text: turn.text + event.text };
      return { ...state, turns, busy: true };
    }

    case "thought": {
      const turns = [...state.turns];
      const turn = speaking(turns);
      turns[turns.length - 1] = {
        ...turn,
        thinking: turn.thinking + event.text,
      };
      return { ...state, turns, busy: true };
    }

    case "tool": {
      const turns = [...state.turns];
      const turn = speaking(turns);
      const tool: Tool = {
        id: event.id,
        name: event.title,
        summary: "",
        detail: "",
        isError: event.status === "failed",
        file: "",
        patch: "",
      };
      turns[turns.length - 1] = { ...turn, tools: [...turn.tools, tool] };
      return { ...state, turns, busy: true };
    }

    case "tool_update": {
      // A tool belongs to whichever turn started it, which is not necessarily
      // the last one — an update can land after the agent has moved on. So the
      // search is backwards through every turn rather than into the current one.
      const turns = [...state.turns];
      for (let i = turns.length - 1; i >= 0; i--) {
        const turn = turns[i]!;
        const at = turn.tools.findIndex((t) => t.id === event.id);
        if (at < 0) continue;
        const tools = [...turn.tools];
        tools[at] = {
          ...tools[at]!,
          ...contentOf(event.content),
          ...statusOf(event.status),
        };
        turns[i] = { ...turn, tools };
        return { ...state, turns };
      }
      return state;
    }

    case "permission":
      return {
        ...state,
        permission: { title: event.title, options: event.options },
      };

    case "permission_resolved":
      return { ...state, permission: null };

    case "usage":
      return {
        ...state,
        usage: { used: event.used, size: event.size, cost: event.cost },
      };

    case "title":
      return event.title ? { ...state, title: event.title } : state;

    case "done":
      return { ...state, busy: false, permission: null };

    case "error":
      return { ...state, busy: false, error: event.message };

    // "plan" and "other" are carried by the protocol and not rendered yet.
    // Ignoring them explicitly rather than by omission, so adding a renderer is
    // a change here rather than an archaeology exercise.
    default:
      return state;
  }
}

function statusOf(status: string | undefined): Partial<Tool> {
  if (!status) return {};
  return { isError: status === "failed" };
}

// ACP delivers a tool's result as content blocks. A diff is its own kind, which
// is the whole reason this surface exists rather than a terminal — everything
// else lands as text under a disclosure.
function contentOf(content: unknown): Partial<Tool> {
  if (!Array.isArray(content)) return {};
  const out: Partial<Tool> = {};
  const text: string[] = [];

  for (const block of content) {
    const kind = str(block, "type");
    if (kind === "diff") {
      const path = str(block, "path");
      out.file = path;
      out.patch = unifiedPatch(
        path,
        str(block, "oldText"),
        str(block, "newText"),
      );
      continue;
    }
    // { type: "content", content: { type: "text", text } } is the common shape;
    // a bare { type: "text", text } turns up too.
    const inner = (block as { content?: unknown }).content;
    const body = str(inner, "text") || str(block, "text");
    if (body) text.push(body);
  }

  if (text.length) {
    const joined = text.join("\n");
    out.detail = joined;
    out.summary = joined.split("\n")[0]?.slice(0, 120) ?? "";
  }
  return out;
}

function str(obj: unknown, key: string): string {
  if (typeof obj !== "object" || obj === null) return "";
  const value = (obj as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

// A unified patch from two versions of a file.
//
// The agent sends the before and after text; @pierre/diffs renders a patch. The
// gap between those is this function, which in gdeck lived in Go.
//
// Line-based LCS, which is quadratic. Real edits are small and the inputs are
// files a person is reading, so that is fine — but "fine" has a limit, and past
// it a 20,000-line file would freeze the tab computing a diff nobody asked to
// see. Beyond the cap the patch degrades to a whole-file replacement, which is
// honest and cheap rather than clever and hung.
const diffLineCap = 4000;

export function unifiedPatch(
  path: string,
  oldText: string,
  newText: string,
): string {
  const before = oldText === "" ? [] : oldText.split("\n");
  const after = newText === "" ? [] : newText.split("\n");
  const header = `--- a/${path}\n+++ b/${path}\n`;

  if (before.length > diffLineCap || after.length > diffLineCap) {
    return (
      header +
      before.map((l) => `-${l}`).join("\n") +
      "\n" +
      after.map((l) => `+${l}`).join("\n")
    );
  }

  // lcs[i][j] = length of the longest common subsequence of before[i:] and
  // after[j:]. Built backwards so the walk that follows reads forwards.
  const lcs: number[][] = Array.from({ length: before.length + 1 }, () =>
    new Array<number>(after.length + 1).fill(0),
  );
  for (let i = before.length - 1; i >= 0; i--) {
    for (let j = after.length - 1; j >= 0; j--) {
      lcs[i]![j] =
        before[i] === after[j]
          ? lcs[i + 1]![j + 1]! + 1
          : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
    }
  }

  const lines: string[] = [];
  let i = 0;
  let j = 0;
  while (i < before.length && j < after.length) {
    if (before[i] === after[j]) {
      lines.push(` ${before[i]}`);
      i++;
      j++;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      lines.push(`-${before[i]}`);
      i++;
    } else {
      lines.push(`+${after[j]}`);
      j++;
    }
  }
  while (i < before.length) lines.push(`-${before[i++]}`);
  while (j < after.length) lines.push(`+${after[j++]}`);

  return header + lines.join("\n");
}
