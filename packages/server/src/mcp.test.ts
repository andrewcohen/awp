import { Effect } from "effect";
import { describe, expect, it } from "vitest";
import { gadgetScope } from "@awp-kit/protocol";
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

/** One task, as the store would answer it. */
const entry = (over: Partial<Task> = {}): Task => ({
  id: "awp:20260915-ab12",
  subject: "measure the sweep",
  description: "over every checkout",
  status: "pending",
  source: "awp",
  tags: ["project:rowan"],
  seq: undefined,
  updatedAt: 0,
  ...over,
});

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
      gadget: (from, name, source) => {
        asked.push({ gadget: from, name, source });
        return Effect.succeed({ thread: "th-1", url: `gadget://th-1/${name}` });
      },
      addTask: (task) => {
        asked.push({ addTask: task });
        return Effect.succeed(entry({ subject: task.subject, tags: task.tags }));
      },
      setTaskStatus: (id, status) => {
        asked.push({ setTaskStatus: id, status });
        return Effect.succeed(entry({ id, status }));
      },
      tagTask: (id, tag, on) => {
        asked.push({ tagTask: id, tag, on });
        return Effect.succeed(entry({ id, tags: on ? [tag] : [] }));
      },
      sendMessage: (from, to, body) => {
        asked.push({ sendMessage: from, to, body });
        return Effect.succeed({
          id: "m20260916-0001",
          thread: "th-1",
          from: { project: "rowan", workspace: "tabular-exports" },
          to: { project: "beta", workspace: to },
          body,
          sentAt: 1_787_000_000_000,
          notifiedAt: undefined,
          readAt: undefined,
        });
      },
      inbox: (from) => {
        asked.push({ inbox: from });
        return Effect.succeed([]);
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

describe("awp_gadget", () => {
  it("the sentence is the address, so it can be opened again later", () => {
    const { text, failed, asked } = call("awp_gadget", {
      name: "cost-table",
      source: "# Costs\n",
    });
    expect(failed).toBe(false);
    expect(text).toContain("web panel for this thread");
    // The address and not just the name: `awp_browse` takes it, which is how
    // anybody gets back to a gadget after the column has moved on.
    expect(text).toContain("gadget://th-1/cost-table");
    expect(asked).toEqual([{ gadget: HERE, name: "cost-table", source: "# Costs\n" }]);
  });

  it("half a call is refused here rather than forwarded", () => {
    const { text, failed, asked } = call("awp_gadget", { name: "cost-table" });
    expect(failed).toBe(true);
    expect(text).toContain("needs a name and a source");
    expect(asked).toEqual([]);
  });

  it("a document that does not compile comes back as the compiler's sentence", () => {
    // The reason the compile is in the daemon at all: what reads this is the
    // model that wrote the document, and it is still holding the source.
    const { text, failed } = call(
      "awp_gadget",
      { name: "broken", source: "# Hi\n\n<Unclosed\n" },
      { gadget: () => Effect.fail({ reason: "Unexpected end of file (line 3)" }) },
    );
    expect(failed).toBe(true);
    expect(text).toContain("line 3");
  });

  it("the description names what a document is in scope of", () => {
    // A gadget defines its own components, so the only thing awp publishes is
    // this list — and an agent that is not told about it writes a document
    // that cannot move. The contract owns the names; this is where they are
    // said out loud.
    const tool = TOOLS.find((one) => one.name === "awp_gadget");
    expect(tool).toBeDefined();
    expect(Object.keys(tool?.inputSchema.properties ?? {})).toEqual(["name", "source"]);
    for (const name of gadgetScope) {
      expect(tool?.description).toContain(name);
    }
    expect(tool?.description).toContain("import");
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
  updatedAt: 0,
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

describe("awp_task_add", () => {
  it("tags the task with the project the server is standing in", () => {
    // Never an argument, like every other tool here — a task with no project
    // tag is one the panel's own scope cannot find, and one with the wrong
    // tag is a write into somebody else's list.
    const got = call("awp_task_add", { subject: "measure the sweep" });
    expect(got.failed).toBe(false);
    expect(got.asked).toContainEqual({
      addTask: {
        subject: "measure the sweep",
        description: "",
        status: "pending",
        tags: ["project:rowan"],
      },
    });
  });

  it("tags the thread as well, when one claims this checkout", () => {
    // What makes the store answer "this piece of work" rather than only "this
    // project". Applied rather than asked for: the agent has no way to know
    // the id, and an argument for it would be a second thing to get wrong.
    const got = call(
      "awp_task_add",
      { subject: "measure the sweep" },
      {
        threadAt: () =>
          Effect.succeed(
            here({
              thread: {
                id: "20260915-ab12",
                title: "tasks",
                parent: undefined,
                prs: [],
                checkouts: [],
              },
            } as Partial<ThreadHere>),
          ),
      },
    );
    expect(JSON.stringify(got.asked)).toContain("thread:20260915-ab12");
  });

  it("starts pending unless asked otherwise", () => {
    const got = call("awp_task_add", { subject: "x", status: "in_progress" });
    expect(got.asked).toContainEqual(expect.objectContaining({ addTask: expect.anything() }));
    expect(JSON.stringify(got.asked)).toContain("in_progress");
  });

  it("refuses with no subject, before asking the daemon", () => {
    const got = call("awp_task_add");
    expect(got.failed).toBe(true);
    expect(got.asked).toEqual([]);
  });

  it("says the id it wrote, so the next call has something to name", () => {
    const got = call("awp_task_add", { subject: "measure the sweep" });
    expect(got.text).toContain("awp:20260915-ab12");
  });
});

describe("awp_task_status and awp_task_tag are bound to this project", () => {
  // An id can name a task in any project, which every other tool here is
  // structurally unable to do. So the binding is a check rather than an
  // absence, and this is the test that says so.
  const elsewhere = { board: () => Effect.succeed([]) };

  it("refuses an id that is not this project's, and does not write", () => {
    const got = call(
      "awp_task_status",
      { id: "awp:somebody-else", status: "completed" },
      elsewhere,
    );
    expect(got.failed).toBe(true);
    expect(got.text).toContain("rowan");
    expect(JSON.stringify(got.asked)).not.toContain("setTaskStatus");
  });

  it("asks the board for this project's tasks and nothing wider", () => {
    call("awp_task_tag", { id: "awp:20260915-ab12", tag: "thread:x" }, elsewhere);
    expect(elsewhere.board).toBeDefined();
    const got = call("awp_task_tag", { id: "awp:20260915-ab12", tag: "thread:x" });
    expect(got.asked).toContainEqual({ board: { tags: ["project:rowan"] } });
  });

  it("moves a task that is this project's", () => {
    const got = call(
      "awp_task_status",
      { id: "awp:20260915-ab12", status: "completed" },
      { board: () => Effect.succeed([entry()]) },
    );
    expect(got.failed).toBe(false);
    expect(got.text).toContain("completed");
  });

  it("removes a tag when told to, and defaults to applying one", () => {
    const own = { board: () => Effect.succeed([entry()]) };
    const on = call("awp_task_tag", { id: "awp:20260915-ab12", tag: "thread:x" }, own);
    expect(on.text).toContain("thread:x");
    const off = call("awp_task_tag", { id: "awp:20260915-ab12", tag: "thread:x", on: false }, own);
    expect(off.text).toContain("no tags");
  });

  it("refuses with no id, before asking the daemon", () => {
    expect(call("awp_task_status", { status: "completed" }).asked).toEqual([]);
    expect(call("awp_task_tag", { tag: "x" }).asked).toEqual([]);
  });
});

describe("awp_thread names the way to reach the other checkouts", () => {
  const withOthers = (others: ReadonlyArray<[string, string]>) => ({
    project: "grove",
    workspace: "testing-multi",
    dir: HERE,
    thread: {
      id: "th-1",
      title: "Testing Multi",
      parent: undefined,
      prs: [],
      checkouts: [
        { project: "grove", workspace: "testing-multi", dir: HERE, running: true },
        ...others.map(([project, workspace]) => ({
          project,
          workspace,
          dir: `/w/${project}`,
          running: false,
        })),
      ],
    },
  });

  it("teaches the project form, which is the one that usually works", () => {
    const said = threadSaid(withOthers([["redwood", "testing-multi"]]));
    expect(said).toContain("awp_message");
    expect(said).toContain('e.g. "redwood"');
  });

  it("falls back to the pair when no single project names one checkout", () => {
    // Two siblings in one project: the short form would be refused as
    // ambiguous, and an example a model copies and gets refused for is worse
    // than a longer one.
    const said = threadSaid(
      withOthers([
        ["redwood", "the-api"],
        ["redwood", "the-ui"],
      ]),
    );
    expect(said).toContain("redwood/the-api");
  });

  it("says nothing about messaging when there is nobody to message", () => {
    // An instruction with no object. The tool list already carries the
    // general case for a checkout that later gains a sibling.
    expect(threadSaid(withOthers([]))).not.toContain("awp_message");
  });
});

describe("awp_message", () => {
  it("names the recipient it reached, and does not echo the body back", () => {
    const got = call("awp_message", { to: "exports-ui", body: "the endpoint is live on 4000" });

    expect(got.failed).toBe(false);
    expect(got.text).toContain("exports-ui");
    // What the sender needs to know is that it was accepted and when it will
    // land — not its own sentence read back to it.
    expect(got.text).toContain("next free");
  });

  it("is asked about the server's own directory, like every other tool", () => {
    expect(call("awp_message", { to: "exports-ui", body: "hi" }).asked).toEqual([
      { sendMessage: HERE, to: "exports-ui", body: "hi" },
    ]);
  });

  it("refuses an empty call before asking the daemon", () => {
    // Two different missing halves, and each says which one. A tool that
    // answered "invalid arguments" would have the model guess at which.
    const noRecipient = call("awp_message", { body: "hi" });
    expect(noRecipient.failed).toBe(true);
    expect(noRecipient.text).toContain("recipient");
    expect(noRecipient.asked).toEqual([]);

    const noBody = call("awp_message", { to: "exports-ui" });
    expect(noBody.failed).toBe(true);
    expect(noBody.text).toContain("body");
    expect(noBody.asked).toEqual([]);
  });

  it("renders the daemon's refusal rather than a failure of its own", () => {
    const got = call(
      "awp_message",
      { to: "nobody", body: "hi" },
      {
        sendMessage: () =>
          Effect.fail({ reason: "no checkout called nobody — this thread holds exports-ui" }),
      },
    );

    expect(got.failed).toBe(true);
    // The sentence is the interface: what reads it is a model, and the roster
    // in it is what stops the next call being another guess.
    expect(got.text).toContain("exports-ui");
  });
});

/** A message waiting for this checkout, from a sibling in its thread. */
const from = (workspace: string, body: string) => ({
  id: `m-${workspace}`,
  thread: "th-1",
  from: { project: "beta", workspace },
  to: { project: "rowan", workspace: "tabular-exports" },
  body,
  sentAt: 0,
  notifiedAt: 0,
  readAt: undefined,
});

describe("awp_messages", () => {
  it("says nothing is waiting rather than answering with an empty string", () => {
    // A tool whose refusal or emptiness renders as "" is a tool an agent calls
    // again — the same argument `commentsSaid` is written around.
    const got = call("awp_messages");
    expect(got.failed).toBe(false);
    expect(got.text).toBe("No new messages.");
  });

  it("gives the bodies whole, with the sender and how to answer", () => {
    const got = call(
      "awp_messages",
      {},
      { inbox: () => Effect.succeed([from("exports-ui", "the endpoint is live on 4000")]) },
    );

    // Whole, not summarised: the tool exists so the recipient reads what was
    // said rather than this process's rendering of it.
    expect(got.text).toContain("the endpoint is live on 4000");
    expect(got.text).toContain("beta/exports-ui");
    // The address to answer on travels with the message. An agent should never
    // have to be told separately how to be reachable — and it is the PAIR,
    // because a thread's checkouts commonly share a workspace name and the
    // short form would be refused as ambiguous by the call it is suggesting.
    expect(got.text).toContain('awp_message to: "beta/exports-ui"');
  });

  it("counts them, so an agent knows whether it has read everything", () => {
    const got = call(
      "awp_messages",
      {},
      { inbox: () => Effect.succeed([from("exports-ui", "one"), from("the-api", "two")]) },
    );
    expect(got.text).toContain("2 new messages");
  });
});
