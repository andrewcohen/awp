import { describe, expect, test } from "vitest";
import { COMMANDS, type Command, agentCommands, commandOf, completed, matching } from "./commands";

// The interception rule, which is the only part of this that can be got wrong
// in a way somebody would feel: a prefix match would eat a message about a
// path, and no match at all would send `/new` to the agent as a question.

describe("what the menu lists", () => {
  test("a bare slash lists everything", () => {
    expect(matching("/")).toEqual(COMMANDS);
  });

  test("a prefix narrows it", () => {
    expect(matching("/n").map((one) => one.name)).toEqual(["/new"]);
    expect(matching("/mc").map((one) => one.name)).toEqual(["/mcp"]);
  });

  test("nothing is listed for prose, even prose with a slash in it", () => {
    // The case that matters: a path is not a command, and a menu appearing
    // over somebody's message would be the tell that this rule is too wide.
    expect(matching("/tmp/build.log is missing")).toEqual([]);
    expect(matching("please /new")).toEqual([]);
    expect(matching("what does /mcp do")).toEqual([]);
    expect(matching("")).toEqual([]);
  });
});

describe("what is run", () => {
  test("an exact command is one", () => {
    expect(commandOf("/new")?.name).toBe("/new");
    // The trailing space a keyboard leaves behind, and nothing more.
    expect(commandOf("/mcp ")?.name).toBe("/mcp");
  });

  test("a message that begins with a command is a message", () => {
    // Sent, not run. There is no escape syntax because there is nothing to
    // escape: only the bare word is a command.
    expect(commandOf("/new branch for the exports")).toBeUndefined();
    expect(commandOf("/newsletter")).toBeUndefined();
    expect(commandOf("/mcp?")).toBeUndefined();
  });

  test("a draft with a newline is prose whatever it starts with", () => {
    expect(commandOf("/new\nand then this")).toBeUndefined();
  });
});

describe("the agent's own commands", () => {
  const theirs = agentCommands([
    { name: "/bro", description: "a skill of somebody's", hint: "[file]" },
    { name: "/agents", description: "manage agents" },
  ]);

  test("they are listed beside the window's, ours first", () => {
    expect(matching("/", theirs).map((one) => one.name)).toEqual([
      "/new",
      "/mcp",
      "/agents",
      "/bro",
    ]);
  });

  test("filtering reaches them", () => {
    expect(matching("/br", theirs).map((one) => one.name)).toEqual(["/bro"]);
  });

  test("one of theirs is never intercepted, and that is the whole point", () => {
    // Run by the window it would be swallowed; sent, the adapter passes it to
    // the CLI, which resolves the skill. `commandOf` answering it would be
    // the difference between `/bro` working and doing nothing at all.
    expect(commandOf("/bro")).toBeUndefined();
    expect(matching("/bro", theirs)[0]?.mine).toBe(false);
    expect(commandOf("/new")?.mine).toBe(true);
  });

  test("a command with arguments completes with the space to type them after", () => {
    const bro = matching("/bro", theirs)[0] as Command;
    expect(completed(bro)).toBe("/bro ");
    // Ours take no arguments, and a space there would be trimmed back off on
    // every keystroke.
    expect(completed(commandOf("/new") as Command)).toBe("/new");
  });
});
