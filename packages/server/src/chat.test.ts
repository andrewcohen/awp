import { Effect, Exit, RcMap, Ref, Scope } from "effect";
import type { WorkspaceStatus } from "@awp-kit/protocol";
import { describe, expect, it } from "vitest";
import {
  MODE,
  compactionOf,
  interrupts,
  hanging,
  generations,
  migrations,
  once,
  optionsOf,
  permissionOf,
  settledWhen,
  untilQuiet,
  updateOf,
  oneAtATime,
  withStatus,
} from "./chat";

// The shapes here are not invented: they are the updates a real turn produced,
// copied off a spike against the adapter on 2026-08-28. A fixture written from
// the schema would agree with the schema rather than with the adapter, which is
// the thing that has to be got right.

/** One `agent_message_chunk`, which is how a compaction announces itself. */
const chunk = (text: string) =>
  updateOf({ update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text } } });

describe("compactionOf", () => {
  it("takes a failure with no reason on it", () => {
    // The adapter writes `Compacting failed.` when the SDK gives it no
    // `compact_error`, and a row saying `failed` with an empty sentence
    // under it is worse than one saying nothing.
    expect(compactionOf("Compacting failed.")).toEqual({ kind: "compact", status: "failed" });
  });

  it("is not fooled by a sentence that only starts with the word", () => {
    expect(compactionOf("Compacting is about to happen")).toBeUndefined();
    expect(compactionOf("done compacting")).toBeUndefined();
  });
});

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

  it("recognises a compaction rather than drawing it as three paragraphs", () => {
    expect(chunk("Compacting...")).toEqual({ kind: "compact", status: "running" });
    expect(chunk("\n\nCompacting completed.")).toEqual({ kind: "compact", status: "done" });
    expect(chunk("\n\nCompacting failed: out of tokens")).toEqual({
      kind: "compact",
      status: "failed",
      text: "out of tokens",
    });
  });

  it("leaves an agent talking ABOUT compaction alone", () => {
    // The match is the whole trimmed chunk, so the one cost of reading three
    // English sentences is bounded: prose that merely mentions the word is
    // still a message.
    const said = updateOf({
      update: {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text: "Compacting... is what /compact prints. Run it yourself." },
      },
    });
    expect(said?.kind).toBe("message");
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

describe("letting go once", () => {
  it("happens the first time it is asked and is nothing after", async () => {
    const answer = await Effect.runPromise(
      Effect.gen(function* () {
        const ran = yield* Ref.make(0);
        const drop = yield* once(Ref.update(ran, (n) => n + 1));
        yield* drop;
        yield* drop;
        yield* drop;
        return yield* Ref.get(ran);
      }),
    );

    // Both callers are real: the adapter drops the session it claimed on the
    // way in as soon as it learns the id it actually opened, and the scope's
    // finalizer drops it again when the conversation ends. Counted twice, the
    // second one decrements a hold a *later* adapter has taken, and releases a
    // session that is being written — the one thing the claim is for.
    expect(answer).toBe(1);
  });
});

describe("which conversation a key currently means", () => {
  it("lets the holder speak, and the one it replaced say nothing", () => {
    const keys = generations<string>();
    const key = "thicket\nlantern";

    const first = keys.take(key, "the first adapter");
    expect(keys.current(key, first)).toBe(true);

    // `/new`, the fork, and an adapter that died all do this: invalidate, then
    // get, so the replacement is taken while the old entry's scope is still
    // closing. The old holder's finalizer runs after, and the one thing it
    // would do is invalidate — the conversation `/new` just opened.
    const second = keys.take(key, "the one that replaced it");
    expect(keys.current(key, first)).toBe(false);
    expect(keys.current(key, second)).toBe(true);

    // And its `drop` is the same check, so arriving late is not a way to
    // release a key somebody else is holding.
    keys.drop(key, first);
    expect(keys.current(key, second)).toBe(true);

    keys.drop(key, second);
    expect(keys.current(key, second)).toBe(false);
  });

  it("reads the current token without minting one", () => {
    const keys = generations<string>();
    const key = "thicket\nlantern";

    // Nothing has taken it, so there is nothing to hold: a holder asking this
    // before a lookup has run must not be told it owns the key.
    expect(keys.at(key)).toBeUndefined();
    expect(keys.current(key, keys.at(key))).toBe(false);

    const token = keys.take(key, "an adapter");
    // A reader, not a second `take` — whoever asks is JOINING a conversation
    // somebody else created, and minting here would retire the holder that
    // did, which is the bug this exists to prevent rather than cause.
    expect(keys.at(key)).toBe(token);
    expect(keys.current(key, keys.at(key))).toBe(true);
  });

  it("keeps what is on the key, so the lookup has something to kill", () => {
    const keys = generations<string>();
    const key = "thicket\nlantern";

    // The repair in the lookup runs when a key still has a conversation on it.
    // A token says somebody is there; it does not say who, and what the repair
    // needs is the thing itself.
    expect(keys.valueAt(key)).toBeUndefined();

    const token = keys.take(key, "the stranded adapter");
    expect(keys.valueAt(key)).toBe("the stranded adapter");

    keys.drop(key, token);
    // Dropped means nothing is on the key, so the next lookup kills nothing —
    // which is the ordinary path and must stay silent.
    expect(keys.valueAt(key)).toBeUndefined();
  });

  it("does not confuse two workspaces", () => {
    const keys = generations<string>();
    const one = keys.take("thicket\nlantern", "one adapter");
    const two = keys.take("thicket\norchard", "another");

    keys.drop("thicket\nlantern", one);

    expect(keys.current("thicket\norchard", two)).toBe(true);
  });
});

describe("RcMap.invalidate against an entry somebody holds", () => {
  it("removes the key and releases nothing, which is why `retire` kills the process", async () => {
    // Not a test of our code. This pins Effect's behaviour, because the whole
    // argument for `retire` rests on it and an upgrade that changed it should
    // break here rather than in a daemon at four in the afternoon.
    //
    // Measured first on a live daemon: two adapters, one session id, one pid.
    const released: Array<string> = [];

    const leftOver = await Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const map = yield* RcMap.make({
            lookup: (key: string) =>
              Effect.acquireRelease(Effect.succeed(`adapter for ${key}`), () =>
                Effect.sync(() => released.push(key)),
              ),
          });

          // The holder that does not let go: the chat panel's subscription, or
          // `mindUntilSettled` on a turn that has not ended.
          const holder = yield* Scope.make();
          yield* Scope.provide(RcMap.get(map, "lantern"), holder);

          yield* RcMap.invalidate(map, "lantern");

          // The key is gone from the map...
          const present = yield* RcMap.has(map, "lantern");
          // ...and nothing was released, so the adapter is still running with
          // nothing pointing at it.
          const releasedByInvalidate = [...released];

          // So the next lookup misses and builds a SECOND one beside the first.
          // In the daemon both of these are `claude --resume=<the same id>`.
          yield* Scope.provide(RcMap.get(map, "lantern"), holder);

          yield* Scope.close(holder, Exit.void);
          return { present, releasedByInvalidate, afterTheHolderLetGo: [...released] };
        }),
      ),
    );

    expect(leftOver.present).toBe(false);
    expect(leftOver.releasedByInvalidate).toEqual([]);
    // Two of them, and only once the holder finally let go — which for an
    // unbounded `settled` on a busy workspace is a long time.
    expect(leftOver.afterTheHolderLetGo).toEqual(["lantern", "lantern"]);
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
      "chat.003-claims",
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
// `startsWithin` is here rather than in the daemon because its failure is
// silent: without it nothing ever returns from an adapter that took the prompt
// and ignored it. There is deliberately no bound on the *hold* — see WAITS.
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

  it("gives up on a turn that outlasts an explicit window, and does not fail", async () => {
    // The bound is still offered — a caller that must return can ask for one —
    // and giving up must not read as a failure.
    const started = Date.now();
    const exit = await Effect.runPromiseExit(settledWhen(Effect.succeed(true), waits));
    const took = Date.now() - started;

    // `Exit.isSuccess`, not a tag check — see the note in CLAUDE.md on `_tag`.
    expect(Exit.isSuccess(exit)).toBe(true);
    expect(took).toBeGreaterThanOrEqual(2500);
    expect(took).toBeLessThan(6000);
  });

  it("holds a turn in progress with no bound at all, which is what the daemon asks for", async () => {
    // **A turn in progress is never killed**, and the reason is not that the
    // turn is precious — it is that killing one mid-edit leaves the working
    // copy changed and the transcript with no record of the change, so the
    // next `claude --resume` reads the two and concludes another writer is in
    // the workspace. There is no other writer.
    //
    // Asserted as "still running", which is the only honest shape for a wait
    // that is supposed to have no end: the effect is raced against a window
    // several times the old bound's poll and must lose.
    const exit = await Effect.runPromise(
      Effect.exit(
        Effect.timeout(
          settledWhen(Effect.succeed(true), { startsWithin: "600 millis" }),
          "2 seconds",
        ),
      ),
    );

    expect(Exit.isFailure(exit)).toBe(true);
  });

  it("still gives up on a turn that never starts, with no bound on the hold", async () => {
    // The two bounds answer different questions, and dropping one must not
    // drop the other: nothing should wait forever on an adapter that took the
    // prompt and did nothing with it.
    const started = Date.now();
    await Effect.runPromise(settledWhen(Effect.succeed(false), { startsWithin: "600 millis" }));

    expect(Date.now() - started).toBeLessThan(2500);
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

// ── holding the adapter open for the turn, whoever is watching ──────────────
//
// The failure this is the guard for was measured on this repository's own
// conversation: `idleTimeToLive` releases a conversation two minutes after its
// last reference, releasing kills the adapter, and the window's reference is
// the chat panel's subscription — which Base UI drops the moment somebody
// switches the accessory column to the diff. Seven turns died that way in one
// afternoon, each with `stop_reason: tool_use` and a tool result that came back
// to a process that no longer existed.
//
// What is testable without an adapter is the wiring, and both ways it can be
// wrong are invisible in use: a fiber in the caller's scope is killed when the
// reply is sent, and a second hold per conversation is a reference nothing
// gives back.
/** A scope that behaves like the daemon's: outlives every request in it. */
const withScope = <A>(use: (scope: Scope.Scope) => Effect.Effect<A>) =>
  Effect.gen(function* () {
    const scope = yield* Scope.make();
    const answer = yield* use(scope);
    yield* Scope.close(scope, Exit.void);
    return answer;
  });

describe("one at a time, per conversation", () => {
  it("outlives the scope of whoever asked for it", async () => {
    // `forkScoped` here rather than `forkIn` is the mistake that reads as
    // working: the hold is taken and then interrupted a millisecond later,
    // when the request that sent the message answers.
    const ran = await Effect.runPromise(
      withScope((scope) =>
        Effect.gen(function* () {
          const done = yield* Ref.make(false);
          const hold = oneAtATime(scope);
          // A request's own scope, closed the moment it answers.
          yield* Effect.scoped(hold("one", Effect.sleep("40 millis")));
          yield* Effect.forkIn(
            Effect.andThen(Effect.sleep("40 millis"), Ref.set(done, true)),
            scope,
          );
          yield* Effect.sleep("120 millis");
          return yield* Ref.get(done);
        }),
      ),
    );
    expect(ran).toBe(true);
  });

  it("takes one hold per key, however many times it is asked", async () => {
    const held = await Effect.runPromise(
      withScope((scope) =>
        Effect.gen(function* () {
          const taken = yield* Ref.make(0);
          const hold = oneAtATime(scope);
          const work = Effect.andThen(
            Ref.update(taken, (was) => was + 1),
            Effect.sleep("80 millis"),
          );
          // A message, then a steer, then another steer — one turn.
          yield* hold("one", work);
          yield* hold("one", work);
          yield* hold("one", work);
          yield* Effect.sleep("20 millis");
          return yield* Ref.get(taken);
        }),
      ),
    );
    expect(held).toBe(1);
  });

  it("keeps the keys apart", async () => {
    const held = await Effect.runPromise(
      withScope((scope) =>
        Effect.gen(function* () {
          const taken = yield* Ref.make(0);
          const hold = oneAtATime(scope);
          const work = Effect.andThen(
            Ref.update(taken, (was) => was + 1),
            Effect.sleep("80 millis"),
          );
          yield* hold("one", work);
          yield* hold("another", work);
          yield* Effect.sleep("20 millis");
          return yield* Ref.get(taken);
        }),
      ),
    );
    expect(held).toBe(2);
  });

  it("lets the next turn hold again once the last one ended", async () => {
    const held = await Effect.runPromise(
      withScope((scope) =>
        Effect.gen(function* () {
          const taken = yield* Ref.make(0);
          const hold = oneAtATime(scope);
          yield* hold(
            "one",
            Ref.update(taken, (was) => was + 1),
          );
          yield* Effect.sleep("40 millis");
          yield* hold(
            "one",
            Ref.update(taken, (was) => was + 1),
          );
          yield* Effect.sleep("40 millis");
          return yield* Ref.get(taken);
        }),
      ),
    );
    expect(held).toBe(2);
  });

  it("frees the key when the work fails, rather than wedging it", async () => {
    // A key left set by a failure is a conversation that can never be held
    // open again — which fails in the direction nobody would look, because
    // everything goes on working until an adapter is quietly shot.
    const held = await Effect.runPromise(
      withScope((scope) =>
        Effect.gen(function* () {
          const taken = yield* Ref.make(0);
          const hold = oneAtATime(scope);
          yield* hold(
            "one",
            Effect.andThen(
              Ref.update(taken, (was) => was + 1),
              Effect.fail("no"),
            ),
          );
          yield* Effect.sleep("40 millis");
          yield* hold(
            "one",
            Ref.update(taken, (was) => was + 1),
          );
          yield* Effect.sleep("40 millis");
          return yield* Ref.get(taken);
        }),
      ),
    );
    expect(held).toBe(2);
  });
});

// ── a queued message is a SECOND turn, and the hold was armed for the first ─
//
// `send` arms `mindUntilSettled`, and `oneAtATime` makes that a no-op while a
// hold is already running. That is right for a **steer**: it is injected into
// the turn already going, so the holder minding that turn is minding this
// message too — which is what the test above pins.
//
// A queued message is not that. The adapter keeps it, finishes the turn it
// arrived during, and then promotes its queue head and notifies nobody (see
// `conversation.ts`). So the second turn begins just after the holder that
// would have minded it watched the first one end and let go, and the only
// thing left holding the conversation is whatever window is subscribed.
//
// A window is free to look away. `watchChat` is keyed on the workspace, so
// switching threads tears `ChatOpen` down — and then `idleTimeToLive` kills an
// adapter that is mid-turn.
//
// Measured on this daemon, 2026-09-18: an adapter respawned at the second a
// tool call died, with no `stranded` warning in the log — so the conversation
// was released cleanly rather than orphaned, with a turn in flight. The TTL
// here is 100ms against the daemon's two minutes; nothing else is scaled.
describe("the turn a queued message starts", () => {
  it("is held too, rather than left to whoever happens to be watching", async () => {
    const released: Array<string> = [];

    const stillThere = await Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const scope = yield* Effect.scope;
          const map = yield* RcMap.make({
            lookup: (key: string) =>
              Effect.acquireRelease(Effect.succeed(key), () =>
                Effect.sync(() => released.push(key)),
              ),
            idleTimeToLive: "100 millis",
          });

          const working = yield* Ref.make(false);
          const hold = oneAtATime(scope);
          // `mindUntilSettled`, with the conversation's status standing in for
          // the `SubscriptionRef` the daemon polls.
          const mind = hold(
            "harbor-works",
            Effect.scoped(
              Effect.andThen(
                RcMap.get(map, "harbor-works"),
                untilQuiet(Ref.get(working), { startsWithin: "600 millis" }, "600 millis"),
              ),
            ),
          );

          // Somebody sends, and the turn starts.
          yield* mind;
          yield* Ref.set(working, true);
          yield* Effect.sleep("250 millis");

          // Somebody types again while it is still going. The adapter queues
          // it; `send` arms the hold and `oneAtATime` drops it on the floor.
          yield* mind;

          // The first turn ends...
          yield* Ref.set(working, false);
          yield* Effect.sleep("120 millis");
          // ...and the adapter promotes the message it was holding.
          yield* Ref.set(working, true);

          // Well past the TTL, with that turn still running. Nobody is
          // subscribed — this is the window having switched threads.
          yield* Effect.sleep("400 millis");
          return released.length === 0;
        }),
      ),
    );

    expect(stillThere).toBe(true);
  });
});

describe("whether a message cuts the agent off", () => {
  // The rule this file exists to pin. A steer is delivered at the adapter's
  // `now` priority, which aborts the generation in flight — so the question
  // "did anybody ask for that" has to be answered before the other two.
  const asking = { asked: true, capable: true, compacting: false };

  it("waits unless somebody asked to interrupt", () => {
    expect(interrupts({ ...asking, asked: false })).toBe(false);
  });

  it("interrupts when asked, and the adapter can", () => {
    expect(interrupts(asking)).toBe(true);
  });

  it("waits when the adapter cannot be steered at all", () => {
    expect(interrupts({ ...asking, capable: false })).toBe(false);
  });

  // Not a failure and not the caller being overruled for its own sake: a
  // steer aimed at a compaction is injected into the turn rewriting the
  // context, which loses the compaction and delivers the message into a turn
  // going nowhere. The daemon holds it and sends it after.
  it("waits through a compaction, however it was asked for", () => {
    expect(interrupts({ ...asking, compacting: true })).toBe(false);
  });
});

describe("a status that did not change", () => {
  // The facts feed is `zipLatest` of the workspace table and this, so anything
  // this announces re-sends the whole table to every window. It is written once
  // per streamed chunk, and measured on the socket mid-turn that came to 17
  // frames a second at ~8.5KB — 140KB/s of a table that had not moved.
  //
  // The dedupe is `Stream.changes`, which compares with `Equal.equals`, which
  // on a plain `Map` is `===`. So the property to hold is *reference identity*,
  // not contents: a `withStatus` that returned a faithful copy would pass any
  // test about what the map contains and restore the flood in silence.

  it("is the same map, not an equal one", () => {
    const all: ReadonlyMap<string, WorkspaceStatus> = new Map([["thicket/lantern", "working"]]);
    expect(withStatus(all, "thicket/lantern", "working")).toBe(all);
  });

  it("is a new map as soon as something moves", () => {
    const all: ReadonlyMap<string, WorkspaceStatus> = new Map([["thicket/lantern", "working"]]);
    expect(withStatus(all, "thicket/lantern", "waiting")).not.toBe(all);
    expect(withStatus(all, "orchard/harbor-works", "working")).not.toBe(all);
    expect(withStatus(all, "thicket/lantern", undefined)).not.toBe(all);
  });

  // There is deliberately no test at the stream level. `SubscriptionRef`
  // conflates — a subscriber that has not caught up sees only the latest
  // value — so a test that writes the same status ten times and counts
  // emissions passes whether or not the guard is there, which is the one
  // thing a regression test must not do. The reference identity above is the
  // property, and removing the early return in `withStatus` fails it.
});
