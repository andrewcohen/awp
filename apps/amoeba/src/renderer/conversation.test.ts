import { describe, expect, it } from "vitest";
import type { ChatUpdate } from "@awp-kit/protocol";
import { type Conversation, fold, nothing } from "./conversation";

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
    expect(started.running).toBe(true);
    expect(started.items).toEqual([]);
  });

  it("says nothing about an ordinary ending", () => {
    // `end_turn` is what every normal reply ends with. A line saying so after
    // each one is a line the eye learns to skip.
    const ended = fold(
      { ...nothing, running: true },
      { kind: "turn", status: "ended", stopReason: "end_turn" },
    );
    expect(ended).toEqual(nothing);
  });

  it("keeps a reason that is not an ordinary ending", () => {
    const ended = fold(
      { ...nothing, running: true },
      { kind: "turn", status: "ended", stopReason: "refusal" },
    );
    expect(ended).toMatchObject({ running: false, stopped: "refusal" });
  });

  it("clears the last reason when the next turn starts", () => {
    // Otherwise the sentence explaining why the previous reply stopped sits
    // under the new one, describing something that is no longer happening.
    const again = fold({ ...nothing, stopped: "refusal" }, { kind: "turn", status: "started" });
    expect(again).toMatchObject({ running: true, stopped: undefined });
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
