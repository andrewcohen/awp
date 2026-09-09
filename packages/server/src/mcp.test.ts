import { Effect } from "effect";
import { describe, expect, it } from "vitest";
import type { ReviewComment, Task, ThreadHere } from "@awp-kit/protocol";
import {
  type Daemon,
  TOOLS,
  answer,
  commentsSaid,
  kindOf,
  parseLine,
  taskSaid,
  tasksSaid,
  threadSaid,
} from "./mcp";

// The dispatch is a pure function of a request and a reader, so everything
// here is testable without a process: the handshake, the tool list, the shape
// of a refusal, and every sentence a tool produces. What no test can say is
// whether an agent's MCP client accepts any of it — that is `probe:mcp`.

const HERE = "/Users/x/.awp/workspaces/rowan/tabular-exports";

const here = (over: Partial<ThreadHere> = {}): ThreadHere =>
  ({
    project: "rowan",
    workspace: "tabular-exports",
    dir: HERE,
    thread: undefined,
    ...over,
  }) as ThreadHere;

const comment = (over: Partial<ReviewComment>): ReviewComment =>
  ({
    id: "c1",
    project: "rowan",
    workspace: "tabular-exports",
    revision: "@",
    path: "src/export.ts",
    side: "additions",
    line: 12,
    endLine: 12,
    body: "this is the risky one",
    author: "agent",
    kind: "comment",
    text: undefined,
    createdAt: new Date(0),
    sentAt: new Date(0),
    ...over,
  }) as ReviewComment;

/** A daemon that answers, plus a record of what it was asked. */
const daemonOf = (
  over: Partial<Daemon> = {},
): { readonly daemon: Daemon; readonly asked: unknown[] } => {
  const asked: unknown[] = [];
  return {
    asked,
    daemon: {
      threadAt: (from) => {
        asked.push({ threadAt: from });
        return Effect.succeed(here());
      },
      commentsAt: (from) => {
        asked.push({ commentsAt: from });
        return Effect.succeed([]);
      },
      file: (finding) => {
        asked.push(finding);
        return Effect.succeed({ where: "added a comment to rowan/tabular-exports" });
      },
      board: (filter) => {
        asked.push({ board: filter });
        return Effect.succeed([]);
      },
      browse: (from, url) => {
        asked.push({ browse: from, url });
        return Effect.succeed({ thread: "th-1", url });
      },
      ...over,
    },
  };
};

const call = (name: string, args: Record<string, unknown> = {}, over?: Partial<Daemon>) => {
  const { daemon, asked } = daemonOf(over);
  const reply = Effect.runSync(
    answer(
      { jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } },
      HERE,
      daemon,
    ),
  );
  const result = reply?.result as
    | { readonly content: ReadonlyArray<{ readonly text: string }>; readonly isError?: boolean }
    | undefined;
  return { text: result?.content?.[0]?.text ?? "", failed: result?.isError === true, asked };
};

describe("the handshake", () => {
  it("declares tools and nothing it does not implement", () => {
    const reply = Effect.runSync(
      answer({ jsonrpc: "2.0", id: 1, method: "initialize" }, HERE, daemonOf().daemon),
    );
    // Declaring a capability this does not implement is how a client comes
    // back with a `resources/list` that answers with an error on every open.
    const result = reply?.result as { readonly capabilities: unknown } | undefined;
    expect(result?.capabilities).toEqual({ tools: {} });
  });

  it("answers nothing at all to a notification", () => {
    // A message with no id. Replying to one with a null id is a protocol error
    // at the other end, and `notifications/initialized` is sent by every
    // client on every connection — so getting this wrong breaks all of them.
    expect(
      Effect.runSync(
        answer({ jsonrpc: "2.0", method: "notifications/initialized" }, HERE, daemonOf().daemon),
      ),
    ).toBeUndefined();
  });

  it("refuses an unknown METHOD as a json-rpc error", () => {
    // Not a tool result. Clients probe for optional methods expecting exactly
    // this code, and a `{ isError: true }` result would read as "the method
    // exists and went wrong".
    const reply = Effect.runSync(
      answer({ jsonrpc: "2.0", id: 7, method: "resources/list" }, HERE, daemonOf().daemon),
    );
    expect(reply?.error?.code).toBe(-32601);
    expect(reply?.result).toBeUndefined();
  });

  it("refuses an unknown TOOL as a result, not an error", () => {
    // The other way round, and for the opposite reason: the model chose the
    // name, so the sentence has to reach the model. A json-rpc error is
    // reported to the client, which is not who got it wrong.
    const got = call("awp_invent_something");
    expect(got.failed).toBe(true);
    expect(got.text).toContain("no tool called awp_invent_something");
  });
});

describe("the binding is the absence of an argument", () => {
  // The whole scope decision, as a test. The Go implementation filed seven
  // findings into the wrong repository because the directory was a parameter
  // somebody could get wrong; here there is nothing to pass.
  it("no tool accepts a project or workspace", () => {
    for (const tool of TOOLS) {
      const properties = Object.keys(tool.inputSchema.properties);
      expect(properties).not.toContain("project");
      expect(properties).not.toContain("workspace");
      expect(properties).not.toContain("from");
      expect(properties).not.toContain("dir");
    }
  });

  it("every tool is asked about the server's own directory", () => {
    expect(call("awp_thread").asked).toEqual([{ threadAt: HERE }]);
    expect(call("awp_review_comments").asked).toEqual([{ commentsAt: HERE }]);
    expect(call("awp_file_finding", { path: "a.ts", line: 1, body: "x" }).asked[0]).toMatchObject({
      from: HERE,
    });
  });
});

describe("awp_thread", () => {
  it("names the other checkouts and where they are", () => {
    // The reason the tool exists. A pair is not actionable; a directory is.
    const said = threadSaid(
      here({
        thread: {
          id: "t1",
          title: "tabular exports",
          parent: undefined,
          prs: [],
          checkouts: [
            { project: "rowan", workspace: "tabular-exports", dir: HERE, running: true },
            {
              project: "beta",
              workspace: "tabular-exports",
              dir: "/Users/x/.awp/workspaces/beta/tabular-exports",
              running: false,
            },
          ],
        },
      }) as ThreadHere,
    );
    expect(said).toContain("1 other checkout in this thread");
    expect(said).toContain("beta/tabular-exports at /Users/x/.awp/workspaces/beta/tabular-exports");
    // Its own row is not one of the "others" — the header already said where
    // the caller is, and repeating it as a sibling would make a thread of one
    // read as a thread of two.
    expect(said).not.toContain(`  rowan/tabular-exports at ${HERE}`);
  });

  it("says which siblings have an agent in them", () => {
    const said = threadSaid(
      here({
        thread: {
          id: "t1",
          title: "tabular exports",
          parent: "the api rewrite",
          prs: [{ project: "beta", number: 2418 }],
          checkouts: [
            { project: "rowan", workspace: "tabular-exports", dir: HERE, running: false },
            { project: "beta", workspace: "api", dir: "/w/beta/api", running: true },
          ],
        },
      }) as ThreadHere,
    );
    expect(said).toContain("beta/api at /w/beta/api (an agent is running here)");
    expect(said).toContain("Follows on from: the api rewrite");
    expect(said).toContain("beta#2418");
  });

  it("a checkout no thread claims is an answer, not a refusal", () => {
    // Most checkouts on a real machine predate threads. A refusal here would
    // make the tool useless on the ordinary case.
    const got = call("awp_thread");
    expect(got.failed).toBe(false);
    expect(got.text).toContain("No thread claims this checkout");
  });

  it("a directory that is not a workspace refuses with the daemon's sentence", () => {
    // `NotAWorkspace` names the directory, and that sentence IS the interface
    // — it is the only thing that tells an agent it is somewhere unexpected.
    const got = call(
      "awp_thread",
      {},
      {
        threadAt: () => Effect.fail({ reason: "/tmp/x is not inside an awp workspace" }),
      },
    );
    expect(got.failed).toBe(true);
    expect(got.text).toBe("/tmp/x is not inside an awp workspace");
  });
});

describe("awp_review_comments", () => {
  it("separates what was left for the agent from what it filed", () => {
    // The distinction that stops an agent re-reporting its own findings, and
    // the reason `author` is on a comment at all.
    const said = commentsSaid([
      comment({ author: "human", kind: "question", body: "why here?", line: 4, endLine: 9 }),
      comment({ author: "agent", body: "generated, skip" }),
    ]);
    expect(said).toContain("1 left for you:");
    expect(said).toContain("src/export.ts:4-9 [question] why here?");
    expect(said).toContain("1 you filed already:");
  });

  it("flattens a body onto one line", () => {
    // The list is scanned. A body with newlines in it would break the
    // one-remark-per-line shape that makes it scannable.
    expect(commentsSaid([comment({ body: "one\ntwo" })])).toContain("[comment] one two");
  });

  it("says so when there are none", () => {
    expect(commentsSaid([])).toContain("No review comments");
  });
});

describe("awp_browse", () => {
  it("the sentence says the page and whose panel it is", () => {
    const { text, failed, asked } = call("awp_browse", {
      url: "https://example.invalid/build/412",
    });
    expect(failed).toBe(false);
    // The thread rather than the checkout, because that is what the panel is
    // keyed by: an agent that has just moved this page has moved what every
    // sibling checkout of the same work shows.
    expect(text).toContain("web panel for this thread");
    expect(text).toContain("https://example.invalid/build/412");
    // The directory is the binding and is not an argument — the same rule
    // every other tool here has.
    expect(asked).toEqual([{ browse: HERE, url: "https://example.invalid/build/412" }]);
  });

  it("a workspace no thread claims is still a panel to point at", () => {
    const { text, failed } = call(
      "awp_browse",
      { url: "https://example.invalid/" },
      { browse: (_from, url) => Effect.succeed({ thread: undefined, url }) },
    );
    expect(failed).toBe(false);
    expect(text).toContain("this workspace's web panel");
  });

  it("no url is refused here rather than forwarded", () => {
    // Forwarded, the daemon's refusal would be about a url of "", which reads
    // as a bug in this server rather than as a call to fix.
    const { text, failed, asked } = call("awp_browse");
    expect(failed).toBe(true);
    expect(text).toContain("needs a url");
    expect(asked).toEqual([]);
  });

  it("the daemon's own refusal is the sentence, and it is a result", () => {
    const { text, failed } = call(
      "awp_browse",
      { url: "effect schema v4" },
      { browse: () => Effect.fail({ reason: "effect schema v4 is not a url" }) },
    );
    // `isError` on a result, never a JSON-RPC error: a model has to read why.
    expect(failed).toBe(true);
    expect(text).toContain("is not a url");
  });

  it("it is on the tool list, and takes only a url", () => {
    const tool = TOOLS.find((one) => one.name === "awp_browse");
    expect(tool).toBeDefined();
    expect(Object.keys(tool?.inputSchema.properties ?? {})).toEqual(["url"]);
  });
});

describe("awp_file_finding", () => {
  it("passes the line and the anchor text through", () => {
    const got = call("awp_file_finding", {
      path: "src/export.ts",
      line: 12,
      endLine: 14,
      kind: "suggestion",
      body: "this is the risky one",
      text: "const rows = await all()",
    });
    expect(got.failed).toBe(false);
    expect(got.asked[0]).toEqual({
      from: HERE,
      path: "src/export.ts",
      line: 12,
      endLine: 14,
      kind: "suggestion",
      body: "this is the risky one",
      text: "const rows = await all()",
    });
  });

  it("refuses a call missing what it needs, before asking the daemon", () => {
    // Forwarded, the daemon's refusal would be about a path of "" — which
    // reads as a bug in this server rather than as a call to fix.
    const got = call("awp_file_finding", { path: "a.ts" });
    expect(got.failed).toBe(true);
    expect(got.text).toContain("needs path, line and body");
    expect(got.asked).toEqual([]);
  });

  it("drops a kind it does not recognise rather than refusing", () => {
    // The kind is the least important field on a finding. Losing a whole
    // remark over a synonym would be the wrong trade.
    expect(kindOf("nitpick")).toBeUndefined();
    expect(kindOf("praise")).toBe("praise");
    expect(
      call("awp_file_finding", { path: "a.ts", line: 1, body: "x", kind: "nitpick" }).asked[0],
    ).toMatchObject({ kind: undefined });
  });

  it("answers with where it went", () => {
    // `ReviewFiled.where` exists because an agent filing from the wrong
    // directory is the failure this call is shaped around, and a reply naming
    // the review is the only thing that makes it visible.
    expect(call("awp_file_finding", { path: "a.ts", line: 1, body: "x" }).text).toContain(
      "rowan/tabular-exports",
    );
  });

  it("a non-integer line is not a line", () => {
    // A model that answers 12.5 would otherwise reach the daemon, which reads
    // it as a line number and stores a remark nothing can render.
    const got = call("awp_file_finding", { path: "a.ts", line: 12.5, body: "x" });
    expect(got.failed).toBe(true);
    expect(got.asked).toEqual([]);
  });
});

describe("parseLine", () => {
  it("drops a blank line and a line of garbage", () => {
    // Neither has an id, so there is nothing a json-rpc error could be
    // addressed to.
    expect(parseLine("")).toBeUndefined();
    expect(parseLine("   ")).toBeUndefined();
    expect(parseLine("not json")).toBeUndefined();
    expect(parseLine("[1,2]")).toEqual([1, 2]);
    expect(parseLine('{"method":"x"}')).toEqual({ method: "x" });
  });
});

const task = (over: Partial<Task> = {}): Task => ({
  id: "todo:thicket#113",
  subject: "Dragging a divider near the top moves the window",
  description: "The cause is almost certainly the drag region.",
  status: "pending",
  source: "todo",
  tags: ["project:thicket"],
  seq: 113,
  ...over,
});

describe("awp_tasks", () => {
  it("asks for this project's tasks, from the directory and never an argument", () => {
    // The binding rule again. The cross-cutting read is deliberately offered,
    // but as a scope with nothing to name — so there is no call an agent could
    // make that reaches a project it is not standing in.
    const got = call("awp_tasks");
    expect(got.asked).toEqual([
      { threadAt: HERE },
      { board: { tags: ["project:rowan"], statuses: ["pending", "in_progress", "blocked"] } },
    ]);
  });

  it("scope all drops the tag and does not ask where it is", () => {
    expect(call("awp_tasks", { scope: "all" }).asked).toEqual([
      { board: { statuses: ["pending", "in_progress", "blocked"] } },
    ]);
  });

  it("includeDone drops the status filter rather than adding to it", () => {
    // A negative filter would quietly include a status this window has never
    // seen, which is why the open set is named.
    const asked = call("awp_tasks", { scope: "all", includeDone: true }).asked;
    expect(asked).toEqual([{ board: {} }]);
  });

  it("a directory that is not a workspace refuses with the daemon's sentence", () => {
    const got = call(
      "awp_tasks",
      {},
      { threadAt: () => Effect.fail({ reason: "/tmp/x is not inside an awp workspace" }) },
    );
    expect(got.failed).toBe(true);
    expect(got.text).toBe("/tmp/x is not inside an awp workspace");
  });

  it("says so when there are none", () => {
    expect(tasksSaid([], "thicket")).toContain("No tasks recorded for thicket");
  });

  it("marks a status that is not the ordinary one, and only that", () => {
    // pending is most rows, and marking every row is not marking anything.
    const said = tasksSaid([task(), task({ id: "todo:thicket#91", status: "in_progress" })], "x");
    expect(said).toContain("2 tasks for x");
    expect(said).toContain("todo:thicket#113  Dragging");
    expect(said).toContain("[in progress]");
  });
});

describe("awp_task", () => {
  it("answers the whole entry, because that is where the argument is", () => {
    const said = taskSaid(task({ description: "Measured: 4.5s for eleven pull requests." }));
    expect(said).toContain("Dragging a divider near the top moves the window");
    expect(said).toContain("Tags: project:thicket");
    expect(said).toContain("Measured: 4.5s");
  });

  it("names the tool that lists the ids when the id is wrong", () => {
    // The sentence IS the interface here: what reads it is a model, and
    // "not found" alone leaves it guessing at the format.
    const got = call("awp_task", { id: "nope" }, { board: () => Effect.succeed([task()]) });
    expect(got.failed).toBe(true);
    expect(got.text).toContain("awp_tasks lists them");
  });

  it("refuses a call with no id before asking the daemon", () => {
    const got = call("awp_task");
    expect(got.failed).toBe(true);
    expect(got.asked).toEqual([]);
  });
});
