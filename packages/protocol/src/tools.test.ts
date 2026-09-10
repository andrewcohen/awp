import { describe, expect, it } from "vitest";
import { heldBack, toolLabel, toolTitleOf, toolVerb } from "./tools";

// One rule, two faces. What is tested here is the rule; each face has a test
// that its own item shape reaches it.

describe("what a tool call is called", () => {
  it("is the tool's own name, lowercased", () => {
    expect(toolVerb({ toolName: "Bash", toolKind: "execute" })).toBe("bash");
    expect(toolVerb({ toolName: "Read", toolKind: "read" })).toBe("read");
    expect(toolVerb({ toolName: "Edit", toolKind: "edit" })).toBe("edit");
  });

  it("is the tool half of an mcp name", () => {
    // The server is already in the title, and the terminal's column is nine
    // cells — `mcp__awp__awp_thread` is twenty.
    expect(toolVerb({ toolName: "mcp__awp__awp_thread", toolKind: "other" })).toBe("awp_thread");
    // A server whose own name has an underscore in it still splits at the
    // double one, which is what the pattern is for.
    expect(toolVerb({ toolName: "mcp__my_server__do_thing" })).toBe("do_thing");
  });

  it("keeps a name nothing here has heard of", () => {
    // A list of known tools would report every new one as `other`, which is
    // the failure this repairs.
    expect(toolVerb({ toolName: "SomeNewTool", toolKind: "other" })).toBe("somenewtool");
  });

  it("shortens the two that are two words", () => {
    expect(toolVerb({ toolName: "WebFetch" })).toBe("fetch");
    expect(toolVerb({ toolName: "WebSearch" })).toBe("search");
  });

  it("says what a subagent is, not that one was spawned", () => {
    // `spawned` said neither what was handed off nor to what — and the call
    // itself is a `Task`, which says less again.
    expect(toolVerb({ subagent: "code-reviewer", toolName: "Task" })).toBe("code-reviewer");
  });

  it("falls back to the kind, and then to a word", () => {
    // An older daemon, or a row replayed from a transcript written by one. A
    // coarse label beats no label.
    expect(toolVerb({ toolKind: "execute" })).toBe("execute");
    expect(toolVerb({ toolName: "", toolKind: "" })).toBe("did");
    expect(toolVerb({})).toBe("did");
  });
});

describe("what a row says", () => {
  it("is what the call is for, when the agent said", () => {
    // The one line a row gets, spent on intent rather than on forty
    // characters of a heredoc.
    expect(
      toolTitleOf({
        toolName: "Bash",
        title: "python3 - <<'PY'\nimport json\nPY",
        purpose: "Read the lockfile and print its version",
      }),
    ).toBe("Read the lockfile and print its version");
  });

  it("is the title for everything with no purpose", () => {
    // Which is every tool but Bash: nothing else in the set has the field.
    expect(toolTitleOf({ toolName: "Read", title: "src/lines.ts" })).toBe("src/lines.ts");
  });

  it("drops the placeholder a pending Bash call carries", () => {
    // The adapter titles a Bash call `Terminal` until its input has
    // streamed in, which names nothing and repeats the verb. What is left
    // is the tool's own name — see the pending-row case below.
    expect(toolTitleOf({ toolName: "Bash", title: "Terminal" })).toBe("bash");
    // And it is kept for anything else that is genuinely called that.
    expect(toolTitleOf({ toolName: "Read", title: "Terminal" })).toBe("Terminal");
  });

  it("says when the title is still behind the row", () => {
    // What a disclosure needs to know: a row drawn as its purpose has a
    // command to open, and a row drawn as its command has nothing more.
    expect(heldBack({ toolName: "Bash", title: "ls -la", purpose: "List the files" })).toBe(true);
    expect(heldBack({ toolName: "Bash", title: "ls -la" })).toBe(false);
    expect(heldBack({ toolName: "Bash", title: "Terminal" })).toBe(false);
  });
});

describe("what a row puts before its title", () => {
  it("is nothing for a command, which is most rows", () => {
    // A label on the majority is a label on the baseline — the same
    // arithmetic as the accent and the inbox's leading icon.
    expect(toolLabel({ toolName: "Bash", title: "bun run lint" })).toBe("");
    // And nothing for the same tool reported by an older daemon, which
    // sends the kind and no name.
    expect(toolLabel({ toolKind: "execute", title: "bun run lint" })).toBe("");
    expect(toolLabel({})).toBe("");
  });

  it("is the name for anything that is not a command", () => {
    expect(toolLabel({ toolName: "Read", title: "src/lines.ts" })).toBe("read");
    expect(toolLabel({ toolName: "Edit", title: "Edit lines.ts" })).toBe("edit");
    // A spawn with no title of its own says what it is once, as the row
    // itself, rather than as a label in front of a repeat of the word.
    expect(toolLabel({ subagent: "code-reviewer", toolName: "Task" })).toBe("");
    expect(toolTitleOf({ subagent: "code-reviewer", toolName: "Task" })).toBe("code-reviewer");
    // With a title, the label is what says work was handed off.
    expect(toolLabel({ subagent: "code-reviewer", title: "review the diff" })).toBe(
      "code-reviewer",
    );
  });

  it("is nothing for a tool that names itself", () => {
    // `awp_tasks awp_tasks` is not two pieces of information.
    expect(toolLabel({ toolName: "mcp__awp__awp_tasks", title: "awp_tasks" })).toBe("");
  });
});

describe("a row that has no title yet", () => {
  it("says the tool's name rather than nothing", () => {
    // The label is suppressed for Bash, so a pending call whose command has
    // not streamed in would otherwise be a mark and a blank line.
    expect(toolTitleOf({ toolName: "Bash", title: "Terminal" })).toBe("bash");
    expect(toolTitleOf({ toolName: "Read", title: "" })).toBe("read");
  });

  it("is not holding anything back", () => {
    // There is nothing to open: `Terminal` is the adapter saying the
    // command has not arrived, not a command.
    expect(heldBack({ toolName: "Bash", title: "Terminal" })).toBe(false);
  });
});
