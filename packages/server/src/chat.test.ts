import { Effect, Exit, Ref } from "effect";
import { describe, expect, it } from "vitest";
import { MODE, hanging, migrations, optionsOf, permissionOf, settledWhen, updateOf } from "./chat";

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

  it("keeps the command list, because a skill is one of them", () => {
    // Both of the updates dropped as "nobody reads these" turned out to
    // matter. One was the only place the context figure exists; this is the
    // other, and dropping it meant a skill the terminal runs happily could
    // not be found or invoked from the chat at all.
    const said = updateOf({
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [
          { name: "bro", description: "a skill.\nAnd its second line.", input: { hint: "[file]" } },
          { name: "compact", description: "", input: null },
        ],
      },
    });
    expect(said?.kind).toBe("commands");
    // The slash is put back on: ACP carries `bro` and what a person types is
    // `/bro`, which is what the menu matches against.
    expect(said?.commands).toStrictEqual([
      { name: "/bro", description: "a skill.\nAnd its second line.", hint: "[file]" },
      { name: "/compact", description: "" },
    ]);
  });

  it("answers an empty list rather than nothing", () => {
    // The adapter's own instruction is that the client REPLACES its cached
    // list with the payload, so a set that has become empty is an answer: a
    // command that has gone must stop being offered.
    const said = updateOf({
      update: { sessionUpdate: "available_commands_update", availableCommands: [] },
    });
    expect(said?.kind).toBe("commands");
    expect(said?.commands).toStrictEqual([]);
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

  it("takes the tool's own name off `_meta`", () => {
    // ACP's `kind` is ten words for the fifty tools an agent has: `Bash` is
    // the whole of `execute`, and `Skill`, `AskUserQuestion` and every MCP
    // tool are `other`. The name is what a client labels a row with, and it
    // rides on `_meta.claudeCode` rather than on a field ACP defines — so it
    // was simply not being read, and every terminal row said `execute`.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call",
          toolCallId: "toolu_01",
          status: "pending",
          title: "Terminal",
          kind: "execute",
          _meta: { claudeCode: { toolName: "Bash" } },
        },
      }),
    ).toMatchObject({ toolKind: "execute", toolName: "Bash" });
  });

  it("leaves the name out when the adapter sent none", () => {
    // An older adapter, and every row replayed from a transcript written by
    // one. A client falls back to the kind rather than drawing nothing.
    expect(
      updateOf({
        update: { sessionUpdate: "tool_call", toolCallId: "toolu_01", kind: "execute" },
      }),
    ).not.toHaveProperty("toolName");
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

  // ── an edit, which is the one call with something to show ──────────────
  //
  // The shape is the adapter's own, read out of `tools.ts` in
  // claude-code-acp 0.16.2: an `Edit` becomes `{type: "diff", path, oldText,
  // newText}` on the call's content, a `Write` sends `oldText: null`, and the
  // result path sends **one block per hunk** out of the SDK's structuredPatch.
  // None of it is a patch, and both faces would otherwise have to diff two
  // whole texts themselves.

  it("turns an edit into a patch a client can draw", () => {
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "toolu_01",
        kind: "edit",
        title: "Edit notes.txt",
        content: [
          {
            type: "diff",
            path: "/repo/notes.txt",
            oldText: "one\ntwo\nthree\n",
            newText: "one\n2\nthree\n",
          },
        ],
      },
    });
    expect(update?.diffs).toHaveLength(1);
    expect(update?.diffs?.[0]?.path).toBe("/repo/notes.txt");
    // Unified, and named on both sides — the name is where a renderer reads
    // the language from.
    expect(update?.diffs?.[0]?.patch).toContain("--- notes.txt");
    expect(update?.diffs?.[0]?.patch).toContain("-two");
    expect(update?.diffs?.[0]?.patch).toContain("+2");
    // The context around the change survives, which is the whole reason this
    // is a diff rather than two blocks of text: a reader has to see where in
    // the file the change landed.
    expect(update?.diffs?.[0]?.patch).toContain(" three");
  });

  it("reads a Write, whose old text is null", () => {
    // A new file is every line added, and jsdiff produces that on its own from
    // an empty left-hand side. Getting this wrong draws a new file as nothing.
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "toolu_02",
        kind: "edit",
        content: [{ type: "diff", path: "/repo/new.ts", oldText: null, newText: "export {};\n" }],
      },
    });
    expect(update?.diffs?.[0]?.patch).toContain("+export {};");
  });

  it("keeps one patch per hunk, because that is how they arrive", () => {
    // A MultiEdit of two places in one file is two blocks, each holding only
    // its own before and after. Concatenating them would make one patch whose
    // line numbers describe neither.
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call_update",
        toolCallId: "toolu_03",
        content: [
          { type: "diff", path: "/repo/a.ts", oldText: "alpha\n", newText: "ALPHA\n" },
          { type: "diff", path: "/repo/a.ts", oldText: "omega\n", newText: "OMEGA\n" },
        ],
      },
    });
    expect(update?.diffs).toHaveLength(2);
  });

  it("does not leave the marker that crashes the window's renderer", () => {
    // An Edit's two sides are a fragment of a file, so they almost never end
    // in a newline — and jsdiff says so with git's own
    // `\ No newline at end of file`, which `@pierre/diffs` throws on from
    // inside its renderer. The patch parses; the panel then dies, and the
    // agent column becomes a stack trace for an edit that worked.
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "toolu_06",
        kind: "edit",
        content: [{ type: "diff", path: "/repo/a.ts", oldText: "heron", newText: "lantern" }],
      },
    });
    expect(update?.diffs?.[0]?.patch).not.toContain("No newline");
    expect(update?.diffs?.[0]?.patch).toContain("-heron");
    expect(update?.diffs?.[0]?.patch).toContain("+lantern");
  });

  it("does not invent a line to delete for a new file", () => {
    // A Write's old side is absent, and giving *that* a newline would put an
    // empty line in the patch for the file not to have had.
    const update = updateOf({
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "toolu_07",
        kind: "edit",
        content: [{ type: "diff", path: "/repo/new.ts", oldText: null, newText: "one\ntwo" }],
      },
    });
    const patch = update?.diffs?.[0]?.patch ?? "";
    expect(patch).not.toContain("No newline");
    expect(patch.split("\n").filter((line) => line.startsWith("-"))).toEqual(["--- new.ts"]);
  });

  it("says nothing about a block that changed nothing", () => {
    // The adapter does send them — a Write of content already on disk, and the
    // no-op hunk in a structured patch. An empty patch under a row is a row
    // claiming an edit that did not happen.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_04",
          content: [{ type: "diff", path: "/repo/a.ts", oldText: "same\n", newText: "same\n" }],
        },
      })?.diffs,
    ).toBeUndefined();
  });

  it("leaves a call that changed no file alone", () => {
    // Which is most of them. `diffs` absent rather than empty, so a merge in a
    // client keeps whatever the row already had.
    expect(
      updateOf({
        update: {
          sessionUpdate: "tool_call_update",
          toolCallId: "toolu_05",
          content: [{ type: "content", content: { type: "text", text: "Read notes.txt" } }],
        },
      })?.diffs,
    ).toBeUndefined();
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
    expect(migrations.map((migration) => migration.name)).toEqual([
      "chat.001-sessions",
      "chat.002-usage",
    ]);
  });

  it("keys a context reading by the session, not by the workspace", () => {
    // Which is what makes `/new` correct with no delete: a fresh conversation
    // has a new id and therefore no reading, so it cannot inherit the tokens
    // of the one it replaced. Keyed by workspace it would.
    const sql = migrations.flatMap((migration) => migration.up).join("\n");
    expect(sql).toContain("create table chat_usage");
    expect(sql).toContain("session_id text primary key");
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

const tool = (id: string, status?: string) =>
  ({ kind: "tool", id, ...(status === undefined ? {} : { status }) }) as never;

describe("a call the turn ended underneath", () => {
  it("is the one nothing finished", () => {
    // ACP has no "the turn took this call with it" update, and the adapter
    // sends no terminal status for a call in flight when a turn is
    // cancelled or dies. Left alone the row reads as work still happening,
    // for the life of the conversation — reported as bash calls that "just
    // spin forever and dont resolve".
    expect(
      hanging([tool("a", "pending"), tool("b", "completed"), tool("c", "in_progress")]),
    ).toEqual(["a", "c"]);
  });

  it("folds a call's updates to its last status, not its first", () => {
    // A tool call is a patch keyed by id — five updates for one `cat` — so
    // asking whether any update said `completed` is the wrong question.
    expect(hanging([tool("a", "pending"), tool("a", "completed")])).toEqual([]);
    expect(hanging([tool("a", "completed"), tool("a", "pending")])).toEqual(["a"]);
  });

  it("counts a call that was never given a status at all", () => {
    // The adapter opens a call and can simply stop. Nothing said it was
    // running, and nothing will say it is not.
    expect(hanging([tool("a")])).toEqual(["a"]);
    // A later patch that carries only output leaves the status alone.
    expect(
      hanging([tool("a", "completed"), { kind: "tool", id: "a", output: "x" } as never]),
    ).toEqual([]);
  });

  it("leaves one it has already settled", () => {
    // The emit goes through the transcript, so a second turn ending must
    // not settle the same call again — every subscriber would draw it
    // twice and a replay would carry both.
    expect(hanging([tool("a", "cancelled")])).toEqual([]);
  });

  it("ignores everything that is not a tool call", () => {
    expect(hanging([{ kind: "message", role: "agent", text: "hello" } as never])).toEqual([]);
  });
});
