import { describe, expect, it } from "vitest";
import type { ChatUpdate } from "@awp-kit/protocol";
import {
  type Conversation,
  fold,
  grouped,
  mine,
  nothing,
  stalled,
  toolTitle,
  took,
  verb,
  waiting,
} from "./conversation";

// What the panel does with what the daemon sends, and nothing else. The shapes
// are the ones a real turn produced — see chat.test.ts in the daemon, which
// tests the other half of the same sequence.

const said = (role: "user" | "agent" | "thought", text: string): ChatUpdate => ({
  kind: "message",
  role,
  text,
});

describe("fold", () => {
  it("joins chunks from the same speaker into one message", () => {
    // A model answers in fragments and each is its own update. A row per
    // update draws one sentence as a column of words.
    const items = [said("agent", "the word "), said("agent", "is heron")].reduce(
      (all, update) => fold(all, update),
      nothing as Conversation,
    );
    expect(items.items).toHaveLength(1);
    expect(items.items[0]).toMatchObject({
      kind: "said",
      role: "agent",
      text: "the word is heron",
    });
  });

  it("starts a new message when the speaker changes", () => {
    const items = [said("user", "hello"), said("agent", "hi")].reduce(
      (all, update) => fold(all, update),
      nothing as Conversation,
    );
    expect(items.items.map((item) => (item.kind === "said" ? item.role : item.kind))).toEqual([
      "user",
      "agent",
    ]);
  });

  it("merges the five updates of one tool call into one row", () => {
    // The exact sequence measured for a single `cat`: pending with a generic
    // title, then the command, then a description, then the output, then
    // completed. Appending each would draw five rows for one command.
    const items = (
      [
        { kind: "tool", id: "t1", title: "Terminal", toolKind: "execute", status: "pending" },
        { kind: "tool", id: "t1", title: "cat notes.txt", toolKind: "execute" },
        { kind: "tool", id: "t1", output: "the word is: heron" },
        { kind: "tool", id: "t1", status: "completed", output: "the word is: heron" },
      ] satisfies ReadonlyArray<ChatUpdate>
    ).reduce((all, update) => fold(all, update), nothing as Conversation);

    expect(items.items).toHaveLength(1);
    expect(items.items[0]).toEqual({
      kind: "ran",
      key: "t1",
      title: "cat notes.txt",
      toolKind: "execute",
      status: "completed",
      output: "the word is: heron",
    });
  });

  it("does not blank a field an update said nothing about", () => {
    // The one that makes this a merge rather than a replacement. The second
    // update carries no status, and overwriting would leave the row with
    // nothing where `pending` was.
    const items = (
      [
        { kind: "tool", id: "t1", title: "Terminal", status: "pending" },
        { kind: "tool", id: "t1", title: "cat notes.txt" },
      ] satisfies ReadonlyArray<ChatUpdate>
    ).reduce((all, update) => fold(all, update), nothing as Conversation);
    expect(items.items[0]).toMatchObject({ status: "pending", title: "cat notes.txt" });
  });

  it("keeps a tool call where it first appeared", () => {
    // Order is when it started, not when it last changed. A row that jumped to
    // the bottom every time its output grew would move under the pointer of
    // somebody reading it.
    const items = (
      [
        { kind: "tool", id: "t1", title: "first" },
        said("agent", "thinking"),
        { kind: "tool", id: "t1", status: "completed" },
      ] satisfies ReadonlyArray<ChatUpdate>
    ).reduce((all, update) => fold(all, update), nothing as Conversation);
    expect(items.items.map((item) => item.kind)).toEqual(["ran", "said"]);
  });

  it("carries a permission request with its options", () => {
    const items = fold(nothing, {
      kind: "permission",
      id: "permission-4",
      title: "rm notes.txt",
      options: [{ id: "allow", name: "Yes", kind: "allow_once" }],
    });
    expect(items.items[0]).toEqual({
      kind: "asked",
      key: "permission-4",
      title: "rm notes.txt",
      options: [{ id: "allow", name: "Yes", kind: "allow_once" }],
    });
  });

  it("puts a question on the call it is about", () => {
    // Seen in a real window: the command was drawn twice, once on the tool row
    // and once as the question directly beneath it, with the buttons belonging
    // to neither. The adapter emits the tool call before it asks, so the row
    // to hang it on is always there.
    const items = (
      [
        { kind: "tool", id: "t1", title: "rm notes.txt", toolKind: "execute" },
        {
          kind: "permission",
          id: "permission-4",
          about: "t1",
          title: "rm notes.txt",
          options: [{ id: "allow", name: "Yes", kind: "allow_once" }],
        },
      ] satisfies ReadonlyArray<ChatUpdate>
    ).reduce((all, update) => fold(all, update), nothing as Conversation);

    expect(items.items).toHaveLength(1);
    expect(items.items[0]).toMatchObject({ kind: "ran", ask: { key: "permission-4" } });
  });

  it("keeps the question while the call it is about goes on changing", () => {
    // The status moves the moment a person allows it, and a merge that forgot
    // the question would take the buttons away mid-press.
    const items = (
      [
        { kind: "tool", id: "t1", title: "rm notes.txt" },
        { kind: "permission", id: "p1", about: "t1", title: "rm notes.txt", options: [] },
        { kind: "tool", id: "t1", status: "in_progress" },
      ] satisfies ReadonlyArray<ChatUpdate>
    ).reduce((all, update) => fold(all, update), nothing as Conversation);
    expect(items.items[0]).toMatchObject({ status: "in_progress", ask: { key: "p1" } });
  });

  it("still draws a question about a call it was never told about", () => {
    // Refusing would leave the agent waiting on somebody who cannot see what
    // it asked.
    const items = fold(nothing, {
      kind: "permission",
      id: "p1",
      about: "never-seen",
      title: "rm notes.txt",
      options: [],
    });
    expect(items.items[0]).toMatchObject({ kind: "asked", key: "p1" });
  });

  it("ignores an update with nothing to key on", () => {
    // The id is the join. Without one there is nothing to merge into, and
    // appending would draw a tool call that can never be completed.
    expect(fold(nothing, { kind: "tool" })).toEqual(nothing);
    expect(fold(nothing, { kind: "permission" })).toEqual(nothing);
  });
});

describe("a turn", () => {
  it("is a state, not an entry in the transcript", () => {
    // Drawn as a row it would leave a permanent "working…" line in the
    // history the moment the turn finished.
    const started = fold(nothing, { kind: "turn", status: "started" });
    expect(started.running).toBe(1);
    expect(started.items).toEqual([]);
  });

  it("says nothing about an ordinary ending", () => {
    // `end_turn` is what every normal reply ends with. A line saying so after
    // each one is a line the eye learns to skip.
    const ended = fold(
      { ...nothing, running: 1 },
      { kind: "turn", status: "ended", stopReason: "end_turn" },
    );
    expect(ended).toEqual(nothing);
  });

  it("keeps a reason that is not an ordinary ending", () => {
    const ended = fold(
      { ...nothing, running: 1 },
      { kind: "turn", status: "ended", stopReason: "refusal" },
    );
    expect(ended).toMatchObject({ running: 0, stopped: "refusal" });
  });

  it("clears the last reason when the next turn starts", () => {
    // Otherwise the sentence explaining why the previous reply stopped sits
    // under the new one, describing something that is no longer happening.
    const again = fold({ ...nothing, stopped: "refusal" }, { kind: "turn", status: "started" });
    expect(again).toMatchObject({ running: 1, stopped: undefined });
  });

  it("counts turns rather than flagging one, because two overlap", () => {
    // Measured, `bun run probe:steer`: a message sent twelve seconds into a
    // turn starts a second one, and the FIRST turn's end arrives first. A flag
    // cleared there says the agent has finished while it is still answering.
    const both = [
      { kind: "turn", status: "started" },
      { kind: "turn", status: "started" },
      { kind: "turn", status: "ended", stopReason: "end_turn" },
    ].reduce((state, update) => fold(state, update as never), nothing);
    expect(both.running).toBe(1);
    expect(fold(both, { kind: "turn", status: "ended", stopReason: "end_turn" }).running).toBe(0);
  });

  it("holds a reason back while another turn is still working", () => {
    // A sentence about a turn that stopped, drawn under one that has not,
    // describes something that is not what the agent is doing now.
    const one = fold(
      { ...nothing, running: 2 },
      {
        kind: "turn",
        status: "ended",
        stopReason: "refusal",
      },
    );
    expect(one).toMatchObject({ running: 1, stopped: undefined });
  });
});

describe("a steer", () => {
  // The reported bug: "when you steer the message gets out of order". What was
  // happening is in the note at the top of conversation.ts — the local copy of
  // a mid-turn message was appended to the end, and the rest of a reply that
  // was still arriving landed below it.

  it("waits only when the daemon says it started a turn of its own", () => {
    // Whether a mid-turn message waits is the adapter's answer, not this
    // side's guess: `_session/steering` injects it into the running turn when
    // it can, and `ChatSend` reports `steer` when it did. Setting `queued`
    // optimistically here made it appear and then take itself back on every
    // ordinary steer.
    const sent = mine({ ...nothing, running: 1 }, "no, not that file", "mine-1");
    expect(sent.items[0]).toMatchObject({ role: "user", queued: false });
    expect(waiting(sent, "mine-1").items[0]).toMatchObject({ queued: true });
  });

  it("names the message rather than its place in the list", () => {
    // The reply arrives while the list is still growing, so a position would
    // name a different message by the time it lands.
    const two = mine(mine(nothing, "first", "mine-1"), "second", "mine-2");
    const after = fold(waiting(two, "mine-2"), said("agent", "…"));
    expect(after.items.map((item) => (item.kind === "said" ? item.queued : false))).toEqual([
      false,
      false,
      true,
    ]);
    // And the agent's reply went above the one that is waiting.
    expect(after.items.map((item) => (item.kind === "said" ? item.text : ""))).toEqual([
      "first",
      "…",
      "second",
    ]);
  });

  it("does nothing for a key it does not hold", () => {
    expect(waiting(mine(nothing, "hello", "mine-1"), "mine-9").items[0]).toMatchObject({
      queued: false,
    });
  });

  it("lets the interrupted reply carry on above it", () => {
    // The whole of the fix. Two turns in, the old order read as though the
    // agent had answered a question before it was asked.
    const after = fold(
      waiting(
        mine(
          fold({ ...nothing, running: 1 }, said("agent", "I will look at ")),
          "no, not that one",
          "mine-1",
        ),
        "mine-1",
      ),
      said("agent", "src/foo.ts"),
    );
    expect(
      after.items.map((item) => (item.kind === "said" ? `${item.role}: ${item.text}` : item.kind)),
    ).toEqual(["agent: I will look at src/foo.ts", "user: no, not that one"]);
  });

  it("stops waiting when a turn ends, and the answer comes after it", () => {
    const queued = waiting(
      mine({ ...nothing, running: 1 }, "say heron instead", "mine-1"),
      "mine-1",
    );
    const ended = fold(queued, { kind: "turn", status: "ended", stopReason: "end_turn" });
    expect(ended.items[0]).toMatchObject({ queued: false });
    const answered = fold(ended, said("agent", "heron"));
    expect(answered.items.map((item) => (item.kind === "said" ? item.role : item.kind))).toEqual([
      "user",
      "agent",
    ]);
  });

  it("keeps a tool call and a question above it too", () => {
    // Everything the agent produces belongs to the turn the steer interrupted,
    // not to the steer — so a tool call starting mid-steer goes above it.
    const queued = waiting(mine({ ...nothing, running: 1 }, "wait", "mine-1"), "mine-1");
    const after = [
      { kind: "tool", id: "t1", title: "Terminal" },
      { kind: "permission", id: "p1", title: "rm notes.txt" },
    ].reduce((state, update) => fold(state, update as never), queued);
    expect(after.items.map((item) => item.kind)).toEqual(["ran", "asked", "said"]);
  });
});

describe("a delegated call", () => {
  // There is no subagent update kind in ACP — measured in the adapter's own
  // source — so a spawn is one tool call that takes a while, and all this can
  // do is label it honestly.

  it("says it spawned something, and what", () => {
    const after = fold(nothing, {
      kind: "tool",
      id: "t1",
      title: "Task",
      toolKind: "other",
      subagent: "code-reviewer",
    });
    expect(verb(after.items[0] as never)).toBe("spawned");
    expect(after.items[0]).toMatchObject({ subagent: "code-reviewer" });
  });

  it("keeps the retry counters an update stopped mentioning", () => {
    // They arrive on the progress beats, so a later beat that says nothing
    // about a retry must not blank a sentence somebody is reading.
    const after = [
      {
        kind: "tool",
        id: "t1",
        subagent: "code-reviewer",
        retry: { attempt: 2, of: 5, inMs: 30_000 },
      },
      { kind: "tool", id: "t1", status: "in_progress" },
    ].reduce((state, update) => fold(state, update as never), nothing);
    expect(stalled(after.items[0] as never)).toBe("attempt 2 of 5, retrying in 30s");
  });

  it("says nothing about a call that is not retrying", () => {
    const after = fold(nothing, { kind: "tool", id: "t1", title: "cat notes.txt" });
    expect(stalled(after.items[0] as never)).toBeUndefined();
  });

  it("reads a long spawn in minutes", () => {
    expect(took(9)).toBe("9s");
    expect(took(134)).toBe("2m14s");
  });
});

describe("context", () => {
  it("is a fraction of whatever the window is now", () => {
    const seen = fold(nothing, { kind: "usage", used: 50_000, size: 200_000 });
    expect(seen.full).toBe(0.25);
  });

  it("takes the newest pair whole, never a new used against an old size", () => {
    // Measured on one turn: `size` was 200000 on the first update and 1000000
    // on the last, because the model in use has a larger window than the
    // default and the adapter learns it as it goes. Keeping the earlier size
    // would report this session as five times fuller than it is.
    const after = [
      { kind: "usage", used: 18_606, size: 200_000 },
      { kind: "usage", used: 18_619, size: 1_000_000 },
    ].reduce((state, update) => fold(state, update as never), nothing);
    expect(after.full).toBeCloseTo(0.0186, 4);
  });

  it("says nothing when the reading is not a reading", () => {
    expect(fold(nothing, { kind: "usage", used: 10 }).full).toBeUndefined();
    expect(fold(nothing, { kind: "usage", used: 10, size: 0 }).full).toBeUndefined();
  });
});

describe("toolTitle", () => {
  it("keeps a path's basename and lets its directories clip", () => {
    const path = toolTitle("apps/amoeba/src/renderer/highlighting.tsx");
    expect(path.lead).toBe("apps/amoeba/src/renderer/");
    expect(path.name).toBe("highlighting.tsx");
    expect(path.more).toBe(0);
  });

  it("leaves a command whole, path in it or not", () => {
    // The narrow rule earning its keep: split at the last slash, this would
    // read `x.ts` and throw the verb away — the row would say `ran  x.ts`.
    expect(toolTitle("cat src/x.ts")).toStrictEqual({ lead: "", name: "cat src/x.ts", more: 0 });
    expect(toolTitle("bun run typecheck")).toStrictEqual({
      lead: "",
      name: "bun run typecheck",
      more: 0,
    });
  });

  it("draws the first line and counts the rest", () => {
    const heredoc = toolTitle("jj describe --stdin <<'EOF'\nwip: a thing\nand more\nEOF");
    expect(heredoc.name).toBe("jj describe --stdin <<'EOF'");
    expect(heredoc.more).toBe(3);
  });

  it("is not fooled by a trailing slash or a leading one", () => {
    expect(toolTitle("packages/server/src/").name).toBe("packages/server/src/");
    expect(toolTitle("/etc/hosts")).toStrictEqual({ lead: "/etc/", name: "hosts", more: 0 });
  });

  it("says nothing about an empty title", () => {
    expect(toolTitle("")).toStrictEqual({ lead: "", name: "", more: 0 });
  });
});

const spoke = (key: string) =>
  ({ kind: "said", key, role: "agent", text: "…", queued: false }) as never;
const called = (key: string) =>
  ({ kind: "ran", key, title: key, toolKind: "read", status: "completed", output: "" }) as never;

describe("grouped", () => {
  it("makes one block of a run of calls", () => {
    const blocks = grouped([spoke("a"), called("b"), called("c"), called("d"), spoke("e")]);
    expect(blocks.map((block) => block.kind)).toEqual(["one", "calls", "one"]);
    expect(blocks[1]?.kind === "calls" && blocks[1].items.length).toBe(3);
  });

  it("does not merge across a sentence", () => {
    // A call after an answer is a new piece of work, and one box holding both
    // runs would lose the order they happened in.
    const blocks = grouped([called("a"), spoke("b"), called("c")]);
    expect(blocks.map((block) => block.kind)).toEqual(["calls", "one", "calls"]);
  });

  it("keys a block by its first call, so a growing run keeps its identity", () => {
    // React remounts a block whose key changes, and a run grows by one on
    // every update — keyed by the last call, every arrival would throw away
    // the disclosure state of every row in it.
    const one = grouped([called("a"), called("b")]);
    const two = grouped([called("a"), called("b"), called("c")]);
    expect(one[0]?.key).toBe(two[0]?.key);
  });

  it("leaves everything else alone", () => {
    expect(grouped([]).length).toBe(0);
    expect(grouped([spoke("a")]).map((block) => block.kind)).toEqual(["one"]);
  });
});

describe("the context reading", () => {
  it("keeps the tokens beside the fraction", () => {
    const after = fold(nothing, { kind: "usage", used: 18_606, size: 200_000 } as never);
    expect(after.full).toBeCloseTo(0.093, 3);
    expect([after.used, after.size]).toEqual([18_606, 200_000]);
  });

  it("takes the newest pair whole, tokens included", () => {
    // `size` changes mid-turn — measured 200000 then 1000000 — so an earlier
    // count beside a later size would report a session five times fuller than
    // it is, in the tooltip as well as in the percentage.
    const after = [
      { kind: "usage", used: 18_606, size: 200_000 },
      { kind: "usage", used: 18_619, size: 1_000_000 },
    ].reduce((state, update) => fold(state, update as never), nothing);
    expect([after.used, after.size]).toEqual([18_619, 1_000_000]);
  });
});
