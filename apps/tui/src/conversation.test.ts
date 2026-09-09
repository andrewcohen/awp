import { describe, expect, it } from "vitest";
import { empty, fold, grouped, mine } from "./conversation";

// The fold is the whole of what a column draws, and it is the same shape the
// window's has — kept separate rather than imported, because that one returns
// React items and the renderer may not reach for a node builtin.
//
// What is tested here is the part that only matters with *two* clients open on
// one conversation, because that is what this app made possible: the daemon
// echoes what somebody typed and says when a question was answered, and
// neither of those exists in ACP.

const said = (text: string, extra: Record<string, unknown> = {}) =>
  ({ kind: "message", role: "agent", text, ...extra }) as never;

describe("chunks", () => {
  it("appends an unnamed chunk to the block above it", () => {
    const after = [said("Every file "), said("was tokenized")].reduce(
      (state, update) => fold(state, update),
      empty,
    );
    expect(after.items).toHaveLength(1);
    expect(after.items[0]).toMatchObject({ text: "Every file was tokenized" });
  });
});

describe("what the other client typed", () => {
  it("is a whole message, not a chunk", () => {
    const answered = fold(empty, said("I will"));
    const after = fold(answered, said("no, stop", { role: "user", id: "abc" }));
    expect(after.items).toHaveLength(2);
    expect(after.items[1]).toMatchObject({ role: "user", text: "no, stop" });
  });

  it("is ignored when this client sent it", () => {
    // The local copy is drawn on the keypress with the key this client minted,
    // so the echo names a row already there. Without the dedupe everything
    // typed here appears twice.
    const after = fold(
      mine(empty, "no, stop", "abc"),
      said("no, stop", { role: "user", id: "abc" }),
    );
    expect(after.items).toHaveLength(1);
  });
});

describe("a question answered anywhere", () => {
  const asked = [
    { kind: "tool", id: "toolu_01", title: "rm -rf .cache", status: "pending" },
    {
      kind: "permission",
      id: "perm-1",
      about: "toolu_01",
      options: [
        { id: "a", name: "Allow Once", kind: "allow_once" },
        { id: "d", name: "Deny", kind: "reject_once" },
      ],
    },
  ].reduce((state, update) => fold(state, update as never), empty);

  it("is on the call, with the words that were on the button", () => {
    const after = fold(asked, {
      kind: "permission",
      id: "perm-1",
      status: "answered",
      chose: "d",
    } as never);
    const row = after.items[0];
    expect(row?.kind === "tool" && row.ask?.answered).toBe("Deny");
    // And the count of outstanding questions comes back down, which is what
    // the header reads.
    expect(after.asks).toBe(0);
  });

  it("says `answered` for an option it does not know", () => {
    const after = fold(asked, {
      kind: "permission",
      id: "perm-1",
      status: "answered",
      chose: "gone",
    } as never);
    const row = after.items[0];
    expect(row?.kind === "tool" && row.ask?.answered).toBe("answered");
  });
});

// ── what a tool call changed, and what gets rolled up ──────────────────────

const call = (id: string, extra: Record<string, unknown> = {}) =>
  ({ kind: "tool", id, title: id, status: "completed", ...extra }) as never;

const patch = (path: string) => ({ path, patch: `--- ${path}\n+one\n` });

describe("an edit", () => {
  it("keeps the patch the daemon composed", () => {
    const after = fold(empty, call("t1", { toolKind: "edit", diffs: [patch("a.ts")] }));
    expect(after.items[0]).toMatchObject({ diffs: [{ path: "a.ts" }] });
  });

  it("takes the newer patch rather than both", () => {
    // The adapter sends its guess when the call is made and the real change
    // when it has run, about the same file. Merged, the column draws the edit
    // twice — and the older of the two is the wrong one.
    const guessed = fold(empty, call("t1", { diffs: [patch("guess.ts")] }));
    const after = fold(guessed, call("t1", { diffs: [patch("real.ts")] }));
    expect(after.items[0]).toMatchObject({ diffs: [{ path: "real.ts" }] });
  });

  it("survives a progress beat that says nothing about it", () => {
    const edited = fold(empty, call("t1", { diffs: [patch("a.ts")] }));
    const after = fold(edited, call("t1", { status: "completed" }));
    expect(after.items[0]).toMatchObject({ diffs: [{ path: "a.ts" }] });
  });
});

describe("grouped", () => {
  it("rolls a run of receipts into one block", () => {
    const blocks = grouped(
      [call("a"), call("b"), call("c")].reduce((state, one) => fold(state, one), empty).items,
    );
    expect(blocks.map((block) => block.kind)).toEqual(["calls"]);
  });

  it("leaves a call that changed a file on its own", () => {
    // The whole difference from the window's grouping. A column this narrow
    // has room for one thing at a time, and an edit folded behind a count is
    // the change itself folded away.
    const items = [call("a"), call("b", { diffs: [patch("x.ts")] }), call("c")].reduce(
      (state, one) => fold(state, one),
      empty,
    ).items;
    expect(grouped(items).map((block) => block.kind)).toEqual(["calls", "one", "calls"]);
  });

  it("leaves a question on its own", () => {
    // The one row that wants something from a person. Rolled up behind a
    // count, the agent waits on somebody who cannot see what it asked.
    const asked = [
      call("a"),
      { kind: "permission", id: "p1", about: "a", options: [{ id: "y", name: "Allow" }] } as never,
    ].reduce((state, one) => fold(state, one), empty);
    expect(grouped(asked.items).map((block) => block.kind)).toEqual(["one"]);
  });

  it("does not merge across a sentence", () => {
    const items = [call("a"), said("done"), call("b")].reduce(
      (state, one) => fold(state, one),
      empty,
    ).items;
    expect(grouped(items).map((block) => block.kind)).toEqual(["calls", "one", "calls"]);
  });
});

describe("the context figure", () => {
  it("is the newest whole reading, never a delta", () => {
    // `size` is not constant: measured 200000 on a turn's first update and
    // 1000000 on its last, because the adapter learns the model's real window
    // as it goes. Keeping an earlier pair beside a later one reports a
    // conversation as five times as full as it is.
    const after = [
      { kind: "usage", used: 1000, size: 200_000 } as never,
      { kind: "usage", used: 28_148, size: 1_000_000 } as never,
    ].reduce((state, one) => fold(state, one), empty);
    expect(after).toMatchObject({ used: 28_148, size: 1_000_000 });
  });
});
