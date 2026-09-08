import { Effect, Exit, Ref } from "effect";
import { describe, expect, it } from "vitest";
import { MODE, migrations, optionsOf, permissionOf, settledWhen, updateOf } from "./chat";

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

  it("drops the command list, which nothing draws yet", () => {
    // Two arrive on every turn and both were dropped as "nobody reads these".
    // One of them turned out to be the only place the context figure exists —
    // see below — and this is the other. It is the slash-command list, and it
    // stays dropped only until something shows it.
    expect(updateOf({ update: { sessionUpdate: "available_commands_update" } })).toBeUndefined();
  });

  it("reads the context figure, which arrives as a whole reading", () => {
    expect(
      updateOf({ update: { sessionUpdate: "usage_update", used: 18_606, size: 200_000 } }),
    ).toEqual({ kind: "usage", used: 18_606, size: 200_000 });
  });

  it("takes the cost out of the object it arrives in", () => {
    expect(
      updateOf({
        update: {
          sessionUpdate: "usage_update",
          used: 1,
          size: 2,
          cost: { amount: 0.1166475, currency: "USD" },
        },
      })?.cost,
    ).toBeCloseTo(0.1166, 4);
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

  // ── a delegated call ─────────────────────────────────────────────────────
  //
  // The shape is the adapter's own, read out of its source rather than
  // guessed: a `tool_progress` beat is a `tool_call_update` carrying
  // `_meta.claudeCode.toolResponse`, and the retry counters inside it are the
  // SDK's, forwarded verbatim in the SDK's spelling.

  it("keeps what a Task call spawned, and how long it has been at it", () => {
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_01",
          status: "in_progress",
          _meta: {
            claudeCode: {
              toolName: "Task",
              toolResponse: { subagentType: "code-reviewer", elapsedTimeSeconds: 134 },
            },
          },
        },
      }),
    ).toMatchObject({ subagent: "code-reviewer", elapsed: 134 });
  });

  it("keeps the retry counters, which are why a spawn looks stalled", () => {
    // snake_case, because they are the SDK's own fields and the adapter passes
    // them through untouched. Reading only camelCase finds nothing and says
    // nothing, which is exactly the picture this is meant to replace.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_01",
          _meta: {
            claudeCode: {
              toolResponse: {
                subagentType: "code-reviewer",
                subagentRetry: { attempt: 2, max_retries: 5, retry_delay_ms: 30_000 },
              },
            },
          },
        },
      })?.retry,
    ).toEqual({ attempt: 2, of: 5, inMs: 30_000 });
  });

  it("says nothing about a retry with no attempt to name", () => {
    // A retry with no attempt number is a sentence that cannot be written.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_01",
          _meta: { claudeCode: { toolResponse: { subagentRetry: {} } } },
        },
      })?.retry,
    ).toBeUndefined();
  });

  it("adds nothing to an ordinary tool call", () => {
    const update = updateOf({
      update: { sessionUpdate: "tool_call", toolCallId: "toolu_01", title: "cat notes.txt" },
    });
    expect(update?.subagent).toBeUndefined();
    expect(update?.elapsed).toBeUndefined();
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

  it("names the call it is asking about", () => {
    // The adapter emits the tool call before it asks — `ensureToolCallEmitted`
    // in its own source — so this id resolves to a row the window is already
    // drawing, and the buttons go on that row instead of on a second one
    // repeating the same command.
    expect(
      permissionOf(
        { toolCall: { toolCallId: "toolu_01", title: "rm /tmp/notes.txt" }, options: [] },
        "permission-4",
      ).about,
    ).toBe("toolu_01");
    expect(permissionOf({ toolCall: { title: "rm" } }, "permission-4").about).toBeUndefined();
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

describe("optionsOf", () => {
  // The shapes are the adapter's own, measured 2026-08-28: four options, all
  // selects, all with a current value and the values they accept.
  const raw = [
    {
      id: "mode",
      name: "Mode",
      description: "Session permission mode",
      category: "mode",
      type: "select",
      currentValue: "auto",
      options: [
        { value: "auto", name: "Auto", description: "Use a model classifier" },
        { value: "default", name: "Manual" },
      ],
    },
    {
      id: "model",
      name: "Model",
      type: "select",
      currentValue: "opus",
      options: [{ value: "opus", name: "Opus" }],
    },
  ];

  it("keeps the id, the current value and every value on offer", () => {
    const [mode] = optionsOf(raw);
    expect(mode).toEqual({
      id: "mode",
      name: "Mode",
      description: "Session permission mode",
      currentValue: "auto",
      values: [
        { value: "auto", name: "Auto", description: "Use a model classifier" },
        { value: "default", name: "Manual" },
      ],
    });
  });

  it("keeps only what this window can draw", () => {
    // A row it cannot draw is worse than a row that is not there: it would be
    // a control that looks operable and is not. Every option the adapter
    // offers today is a select, so nothing is lost by saying so.
    expect(optionsOf([{ id: "note", type: "string", currentValue: "hi" }])).toEqual([]);
    expect(optionsOf(undefined)).toEqual([]);
  });
});

// ── holding a conversation open for the turn it was just told to start ──────
//
// `send` returns as soon as the adapter accepts a prompt, which is right for a
// person typing: their window is subscribed, so something holds the
// conversation. The create job has no window — `RcMap` releases a conversation
// two minutes after its last reference and releasing it kills the adapter — so
// a brief delivered by `send` alone gets the agent shot two minutes into its
// first answer.
//
// The bounds are here rather than in the daemon because both failures are
// silent: one hangs a job step forever, the other holds one for twenty
// minutes over an adapter that ignored what it was told.
/** A reading that answers from a script, one call at a time. */
const readings = (script: ReadonlyArray<boolean>) =>
  Effect.gen(function* () {
    const at = yield* Ref.make(0);
    const reads: Array<number> = [];
    return {
      reads,
      busy: Effect.gen(function* () {
        const next = yield* Ref.getAndUpdate(at, (was) => was + 1);
        reads.push(next);
        return script[next] ?? script.at(-1) ?? false;
      }),
    };
  });

describe("waiting for a turn to settle", () => {
  const waits = { startsWithin: "600 millis", holdsFor: "3 seconds" } as const;

  it("returns once a turn has started and finished", async () => {
    // idle, idle, working, working, idle — the shape of a real brief: the
    // adapter takes a moment to start the turn, and the answer takes longer.
    const done = await Effect.runPromise(
      Effect.gen(function* () {
        const { busy, reads } = yield* readings([false, true, true, false]);
        yield* settledWhen(busy, waits);
        return reads.length;
      }),
    );

    // Four readings: one before the turn, one that sees it, and on to the one
    // that sees it gone. It did not return on the first idle reading, which is
    // the whole hazard — the status is absent both before a turn and after it.
    expect(done).toBeGreaterThanOrEqual(4);
  });

  it("gives up when no turn ever starts, rather than hanging", async () => {
    // An adapter that accepted the prompt and did nothing with it is a real
    // thing — the reason `send` reports how it was delivered at all.
    const started = Date.now();
    await Effect.runPromise(settledWhen(Effect.succeed(false), waits));
    const took = Date.now() - started;

    expect(took).toBeGreaterThanOrEqual(500);
    // The START bound, not the WHOLE one. Waiting the long window here is a
    // job step asleep for twenty minutes over nothing.
    expect(took).toBeLessThan(2500);
  });

  it("gives up on a turn that outlasts the window, and does not fail", async () => {
    // A turn running for an hour is the agent doing what it was asked. Giving
    // up is not a failure: the transcript is on disk, so somebody opening the
    // chat re-acquires the adapter and replays what happened.
    const started = Date.now();
    const exit = await Effect.runPromiseExit(settledWhen(Effect.succeed(true), waits));
    const took = Date.now() - started;

    // `Exit.isSuccess`, not a tag check — see the note in CLAUDE.md on `_tag`.
    expect(Exit.isSuccess(exit)).toBe(true);
    expect(took).toBeGreaterThanOrEqual(2500);
    expect(took).toBeLessThan(6000);
  });
});
