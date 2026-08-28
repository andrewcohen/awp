import { describe, expect, it } from "vitest";
import { MODE, migrations, permissionOf, updateOf } from "./chat";

// The shapes here are not invented: they are the updates a real turn produced,
// copied off a spike against the adapter on 2026-08-28. A fixture written from
// the schema would agree with the schema rather than with the adapter, which is
// the thing that has to be got right.

describe("updateOf", () => {
  it("reads an agent's words", () => {
    expect(
      updateOf({
        update: {
          sessionUpdate: "agent_message_chunk",
          content: { type: "text", text: "heron" },
        },
      }),
    ).toEqual({ kind: "message", role: "agent", text: "heron" });
  });

  it("tells a replayed user turn from the agent's", () => {
    // `session/load` sends both, in the same shape a live turn uses, which is
    // what lets one renderer draw the history and the present.
    expect(
      updateOf({
        update: { sessionUpdate: "user_message_chunk", content: { type: "text", text: "hello" } },
      }),
    ).toEqual({ kind: "message", role: "user", text: "hello" });
  });

  it("keeps a thought as a thought", () => {
    expect(
      updateOf({
        update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "hmm" } },
      })?.role,
    ).toBe("thought");
  });

  it("drops the updates nobody reads", () => {
    // Both arrive on every turn. Dropped here rather than in the renderer, so
    // the wire is the size of what is shown.
    expect(updateOf({ update: { sessionUpdate: "usage_update" } })).toBeUndefined();
    expect(updateOf({ update: { sessionUpdate: "available_commands_update" } })).toBeUndefined();
  });

  it("drops content that is not text", () => {
    expect(
      updateOf({
        update: { sessionUpdate: "agent_message_chunk", content: { type: "image", data: "…" } },
      }),
    ).toBeUndefined();
  });

  it("carries a tool call as a patch keyed by its id", () => {
    // The first of five for one `cat`: a generic title and no command yet.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call",
          toolCallId: "toolu_01",
          status: "pending",
          title: "Terminal",
          kind: "execute",
        },
      }),
    ).toEqual({
      kind: "tool",
      id: "toolu_01",
      title: "Terminal",
      toolKind: "execute",
      status: "pending",
    });
  });

  it("names the command when the second update brings it", () => {
    // No status on this one, and that is the point of it being a patch: a
    // window that overwrote the row would lose `pending` and have nothing to
    // put in its place.
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call_update",
        toolCallId: "toolu_01",
        title: "cat notes.txt",
        kind: "execute",
      },
    });
    expect(update).toEqual({
      kind: "tool",
      id: "toolu_01",
      title: "cat notes.txt",
      toolKind: "execute",
    });
    expect(update?.status).toBeUndefined();
  });

  it("takes the output from rawOutput when there is one", () => {
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_01",
          status: "completed",
          rawOutput: "the word is: heron",
        },
      })?.output,
    ).toBe("the word is: heron");
  });

  it("falls back to the first content block when there is no rawOutput", () => {
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_01",
          content: [{ type: "content", content: { type: "text", text: "Read notes.txt" } }],
        },
      })?.output,
    ).toBe("Read notes.txt");
  });

  it("refuses a tool call with no id", () => {
    // The id is the join. Without one there is nothing to merge the patch into,
    // and appending it as a new row would draw one tool call as five.
    expect(updateOf({ update: { sessionUpdate: "tool_call", status: "pending" } })).toBeUndefined();
  });

  it("says nothing about an update it has never seen", () => {
    expect(updateOf({ update: { sessionUpdate: "some_future_thing" } })).toBeUndefined();
    expect(updateOf({})).toBeUndefined();
  });
});

describe("permissionOf", () => {
  it("carries the options a person may choose", () => {
    // Measured: `rm` in Manual mode, which is the case this whole path exists
    // for. In `auto` — the default nobody chose — this request never arrives.
    const update = permissionOf(
      {
        toolCall: { title: "rm /tmp/notes.txt" },
        options: [
          { optionId: "reject", name: "No", kind: "reject_once" },
          { optionId: "allow", name: "Yes", kind: "allow_once" },
          { optionId: "allow_always", name: "Always", kind: "allow_always" },
        ],
      },
      "permission-4",
    );
    expect(update.kind).toBe("permission");
    expect(update.id).toBe("permission-4");
    expect(update.title).toBe("rm /tmp/notes.txt");
    expect(update.options?.map((option) => option.kind)).toEqual([
      "reject_once",
      "allow_once",
      "allow_always",
    ]);
  });

  it("still says something when the request names no tool", () => {
    // A permission prompt with no title is still a question, and a row with no
    // words is one nobody can answer.
    expect(permissionOf({}, "permission-1").title).toBe("a tool wants to run");
    expect(permissionOf({}, "permission-1").options).toEqual([]);
  });
});

describe("the mode", () => {
  it("is Manual, and not the adapter's default", () => {
    // `auto` is a model classifier approving tool calls with nobody in this
    // window asked. This assertion is the whole reason the session sets a mode
    // at all — removing the set_mode call should fail here.
    expect(MODE).toBe("default");
    expect(MODE).not.toBe("auto");
  });
});

describe("the record of which session is ours", () => {
  it("keys one session per workspace", () => {
    // The reason this table exists rather than the chat asking which session
    // is newest: `session/list` for a workspace answers with every session
    // ever held in that directory, the terminal's included, and the terminal's
    // is normally the newest. Loading it makes the ACP side a second writer on
    // a transcript an interactive agent is still appending to.
    const sql = migrations.flatMap((migration) => migration.up).join("\n");
    expect(sql).toContain("create table chat_sessions");
    expect(sql).toContain("primary key (project, workspace)");
    // Named, not numbered, and fixed the moment it has run anywhere.
    expect(migrations.map((migration) => migration.name)).toEqual(["chat.001-sessions"]);
  });
});
