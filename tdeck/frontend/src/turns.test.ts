import { expect, test } from "bun:test";
import { apply, emptyConversation, unifiedPatch } from "./turns";
import type { UiEvent } from "./api";

const fold = (events: UiEvent[]) => events.reduce(apply, emptyConversation());

test("chunks become one turn, not a turn per token", () => {
  const chat = fold([
    { kind: "user", text: "hello" },
    { kind: "text", text: "wor" },
    { kind: "text", text: "ld" },
    { kind: "done", stopReason: "end_turn" },
  ]);
  expect(chat.turns).toHaveLength(2);
  expect(chat.turns[1]!.text).toBe("world");
  expect(chat.busy).toBe(false);
});

test("a new user message starts a new agent turn", () => {
  const chat = fold([
    { kind: "user", text: "one" },
    { kind: "text", text: "a" },
    { kind: "done", stopReason: "end_turn" },
    { kind: "user", text: "two" },
    { kind: "text", text: "b" },
  ]);
  expect(chat.turns.map((t) => t.text)).toEqual(["one", "a", "two", "b"]);
});

test("a tool update finds its tool in an earlier turn", () => {
  // The agent can start a tool, keep talking, and only later report the result —
  // by which point the current turn is not the one that owns the tool.
  const chat = fold([
    { kind: "user", text: "go" },
    { kind: "tool", id: "t1", title: "Read", status: "pending" },
    { kind: "done", stopReason: "end_turn" },
    { kind: "user", text: "again" },
    { kind: "tool_update", id: "t1", status: "failed" },
  ]);
  expect(chat.turns[1]!.tools[0]!.isError).toBe(true);
});

test("diff content becomes a patch, text content becomes detail", () => {
  const chat = fold([
    { kind: "tool", id: "t1", title: "Edit", status: "pending" },
    {
      kind: "tool_update",
      id: "t1",
      status: "completed",
      content: [
        {
          type: "diff",
          path: "a.ts",
          oldText: "one\ntwo\n",
          newText: "one\nTWO\n",
        },
      ],
    },
    { kind: "tool", id: "t2", title: "Bash", status: "pending" },
    {
      kind: "tool_update",
      id: "t2",
      content: [
        {
          type: "content",
          content: { type: "text", text: "line one\nline two" },
        },
      ],
    },
  ]);
  const [edit, bash] = chat.turns[0]!.tools;
  expect(edit!.file).toBe("a.ts");
  expect(edit!.patch).toContain("-two");
  expect(edit!.patch).toContain("+TWO");
  expect(bash!.detail).toBe("line one\nline two");
  expect(bash!.summary).toBe("line one");
});

test("permission arrives and clears", () => {
  const asked = fold([
    {
      kind: "permission",
      title: "run rm",
      options: [{ id: "y", name: "yes" }],
    },
  ]);
  expect(asked.permission?.title).toBe("run rm");
  expect(
    apply(asked, { kind: "permission_resolved", optionId: "y" }).permission,
  ).toBeNull();
});

test("an unchanged file produces a patch with no edits", () => {
  const patch = unifiedPatch("a.ts", "one\ntwo", "one\ntwo");
  expect(
    patch.split("\n").filter((l) => l.startsWith("+") && !l.startsWith("+++")),
  ).toEqual([]);
  expect(
    patch.split("\n").filter((l) => l.startsWith("-") && !l.startsWith("---")),
  ).toEqual([]);
});

test("an insertion keeps the surrounding lines as context", () => {
  const patch = unifiedPatch("a.ts", "one\nthree", "one\ntwo\nthree");
  const body = patch.split("\n").slice(2);
  expect(body).toEqual([" one", "+two", " three"]);
});

test("a new file is all additions", () => {
  const body = unifiedPatch("a.ts", "", "one\ntwo").split("\n").slice(2);
  expect(body).toEqual(["+one", "+two"]);
});

test("a file past the size cap degrades rather than hanging", () => {
  // Quadratic in the number of lines, so past the cap it must not run the LCS
  // at all. The assertion that matters is that this returns promptly; the shape
  // check is a proxy for having taken the cheap branch.
  const big = Array.from({ length: 5000 }, (_, i) => `line ${i}`).join("\n");
  const patch = unifiedPatch("big.ts", big, big + "\nextra");
  expect(patch).toContain("-line 0");
  expect(patch).toContain("+line 0");
});

test("a message typed mid-turn is queued, then delivered", () => {
  const queued = fold([
    { kind: "user", text: "first" },
    { kind: "text", text: "working" },
    { kind: "queued", text: "second" },
  ]);
  // Not part of the conversation yet — the agent has not seen it.
  expect(queued.queued).toEqual(["second"]);
  expect(queued.turns).toHaveLength(2);

  const delivered = (
    [
      { kind: "done", stopReason: "end_turn" },
      { kind: "unqueued", text: "second" },
      { kind: "user", text: "second" },
    ] as UiEvent[]
  ).reduce(apply, queued);
  expect(delivered.queued).toEqual([]);
  expect(delivered.turns[delivered.turns.length - 1]?.text).toBe("second");
});

test("two identical queued messages are two messages", () => {
  // Removing by value has to remove one, not all: dropping both on the first
  // delivery would lose a message from the view that the agent still holds.
  const chat = fold([
    { kind: "user", text: "go" },
    { kind: "queued", text: "again" },
    { kind: "queued", text: "again" },
    { kind: "unqueued", text: "again" },
  ]);
  expect(chat.queued).toEqual(["again"]);
});

test("image and audio blocks become media instead of vanishing", () => {
  // These used to fall out of the content loop entirely: a tool that returned a
  // screenshot showed an empty row and nothing said anything was lost.
  const chat = fold([
    { kind: "tool", id: "t1", title: "Screenshot", status: "pending" },
    {
      kind: "tool_update",
      id: "t1",
      content: [
        { type: "image", mimeType: "image/png", data: "AAAA" },
        { type: "content", content: { type: "audio", mimeType: "audio/wav", data: "BBBB" } },
        { type: "resource", mimeType: "video/mp4", data: "CCCC" },
      ],
    },
  ]);
  const tool = chat.turns[0]!.tools[0]!;
  expect(tool.media.map((m) => m.kind)).toEqual(["image", "audio", "video"]);
  expect(tool.media[0]!.src).toBe("data:image/png;base64,AAAA");
});

test("a resource that is not media stays out of the media list", () => {
  const chat = fold([
    { kind: "tool", id: "t1", title: "Read", status: "pending" },
    {
      kind: "tool_update",
      id: "t1",
      content: [{ type: "resource", mimeType: "application/json", data: "e30=" }],
    },
  ]);
  expect(chat.turns[0]!.tools[0]!.media).toEqual([]);
});
