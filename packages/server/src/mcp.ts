// The agent's face on the daemon.
//
// ── every wire pointed one way ──────────────────────────────────────────────
//
// The window can type at an agent — a review, a page note, a task — and the
// agent could say nothing back except by printing into a terminal that amoeba
// only draws. This is the other direction: an MCP server the agent connects
// to, over the same handlers the window uses.
//
// Three decisions, and all three are the sort that is hard to change later.
//
// ── 1. the transport is stdio, one server per agent ────────────────────────
//
// The alternative was one HTTP server on a known port with the workspace as an
// argument. That is one process instead of many, and it makes the binding
// below *conventional* rather than structural: any agent that could reach the
// port could name any workspace. stdio has no port to reach and no argument to
// get wrong, and the cost — a process per conversation — is a process that does
// nothing but forward, and dies with the agent.
//
// ── 2. the scope is the working directory, and there is no parameter ───────
//
// Every tool here is bound to the checkout the server is running in, and the
// binding is **the absence of an argument**. `ThreadAt`, `ReviewAt` and
// `ReviewFile` all take a directory rather than a `(project, workspace)` pair,
// so there is no call an agent could make that reaches somewhere else. That is
// the structural form of the `-R` rule on every jj call, and it is the reason
// this is safe to hand an agent at all.
//
// The Go implementation is the argument for it: an agent that ran the filing
// command in the *source* repository filed seven findings into that
// repository's own review, and both sides reported success. `NotAWorkspace`
// exists so that arrives as a sentence naming the directory.
//
// ── 3. no MCP SDK ──────────────────────────────────────────────────────────
//
// MCP's stdio transport is line-delimited JSON-RPC 2.0, which is byte for byte
// what `acp.ts` already speaks to the Claude Code adapter — and that client is
// hand-rolled here for the same reason. Three methods are answered
// (`initialize`, `tools/list`, `tools/call`) plus one notification ignored, and
// a dependency for that would be a dependency whose upgrades this repo has to
// track for thirty lines of dispatch.
//
// ── what is testable, and what is not ──────────────────────────────────────
//
// `answer` is a pure function of a request and a reader, so the dispatch, the
// tool list, the error shape and every sentence a tool produces are covered by
// unit tests against a fake reader. What no test can say is whether an agent's
// MCP client accepts the handshake — that is `bun run probe:mcp`.

import { join } from "node:path";
import { Effect, Result } from "effect";
import type { CommentKind, ReviewComment, Task, ThreadHere } from "@awp-kit/protocol";

/**
 * Where this package's stdio entry point is on disk.
 *
 * `import.meta.dir` and not a path composed from the daemon's cwd: the daemon
 * is started from wherever somebody ran it, and a relative path would resolve
 * against that. The file sits beside this one, which is the one relationship
 * that cannot drift.
 */
export const mcpEntry = (): string => join(import.meta.dir, "mcp-main.ts");

/**
 * Which daemon a spawned server should talk to.
 *
 * Composed from the same environment variable the daemon binds with, so a
 * second instance's agents reach the second instance. Reading the daemon's own
 * `DAEMON_PORT` would be an import cycle — `daemon.ts` builds the layer that
 * holds `Chat` — and the variable is the thing both sides already agree on.
 */
export const daemonUrl = (): string => {
  const asked = process.env["AWP_DAEMON_URL"];
  if (asked !== undefined && asked !== "") {
    return asked;
  }
  const port = process.env["AWP_DAEMON_PORT"];
  return `ws://127.0.0.1:${port === undefined || port === "" ? "5274" : port}`;
};

/**
 * How the agent is told to start this server.
 *
 * ── the process is its own runtime, and the daemon's port travels with it ──
 *
 * `process.execPath` rather than a `bun` on the PATH, for the same reason
 * `adapterPath` does it: the daemon runs under Bun and the agent's environment
 * is not the daemon's. And `AWP_DAEMON_URL` is passed through rather than left
 * to the default, so a second instance's agents talk to the second instance —
 * otherwise every branch daemon's conversations would file findings into the
 * one somebody is working in, which is the same class of mistake the directory
 * binding exists to prevent, one level up.
 *
 * `cwd` is what binds the tools, and the caller passes the *workspace* rather
 * than the repository. See the note at the top.
 */
export const serverSpec = (options: {
  readonly entry: string;
  readonly cwd: string;
  readonly url: string;
}): {
  readonly name: string;
  readonly command: string;
  readonly args: ReadonlyArray<string>;
  readonly cwd: string;
  readonly env: ReadonlyArray<{ readonly name: string; readonly value: string }>;
} => ({
  name: "awp",
  command: process.execPath,
  args: ["run", options.entry],
  cwd: options.cwd,
  // A list of `{ name, value }` and not an object, because that is the shape
  // ACP's `McpServer` declares. Written out rather than spread from the
  // parent's environment: what this hands a separate process is a deliberate
  // list of one, the same rule the guest preload follows.
  env: [{ name: "AWP_DAEMON_URL", value: options.url }],
});

/**
 * The protocol version this server answers with.
 *
 * Echoed rather than negotiated. A client asking for a version this does not
 * know still gets a working server, because nothing here is version-specific —
 * and refusing over a number would refuse a client that would have worked.
 */
export const PROTOCOL = "2025-06-18";

/** One JSON-RPC message, as much of it as this server reads. */
export interface Request {
  readonly jsonrpc?: string;
  readonly id?: string | number | null;
  readonly method?: string;
  readonly params?: Record<string, unknown> | undefined;
}

/**
 * One line of the protocol, or nothing if it was not one.
 *
 * A blank line and a line of garbage are both dropped rather than answered.
 * There is nothing to answer *to* — an unparseable line has no `id`, so a
 * JSON-RPC error reply would carry a null id and tell the client nothing it
 * could act on.
 */
export const parseLine = (line: string): Request | undefined => {
  const trimmed = line.trim();
  if (trimmed === "") {
    return undefined;
  }
  try {
    const value = JSON.parse(trimmed) as unknown;
    return typeof value === "object" && value !== null ? (value as Request) : undefined;
  } catch {
    return undefined;
  }
};

/** What the server writes back. `id` absent means nothing is written at all. */
export interface Reply {
  readonly jsonrpc: "2.0";
  readonly id: string | number | null;
  readonly result?: unknown;
  readonly error?: { readonly code: number; readonly message: string };
}

/**
 * What the server needs from the daemon, as questions and two acts.
 *
 * An interface rather than the rpc client, so the dispatch can be tested
 * against answers written by hand — including the refusals, which are the half
 * that matters here: a tool whose failure renders as an empty string is a tool
 * an agent will call again.
 */
export interface Refusal {
  readonly reason: string;
}

/** What `awp_file_finding` asks the daemon for, once the arguments are read. */
export interface Finding {
  readonly from: string;
  readonly path: string;
  readonly line: number;
  readonly endLine?: number | undefined;
  readonly kind?: CommentKind | undefined;
  readonly body: string;
  readonly text?: string | undefined;
}

export interface Daemon {
  readonly threadAt: (from: string) => Effect.Effect<ThreadHere, Refusal>;
  readonly commentsAt: (from: string) => Effect.Effect<ReadonlyArray<ReviewComment>, Refusal>;
  readonly file: (finding: Finding) => Effect.Effect<{ readonly where: string }, Refusal>;
  readonly browse: (
    from: string,
    url: string,
  ) => Effect.Effect<{ readonly thread: string | undefined; readonly url: string }, Refusal>;
  readonly board: (filter: {
    readonly tags?: ReadonlyArray<string>;
    readonly statuses?: ReadonlyArray<string>;
  }) => Effect.Effect<ReadonlyArray<Task>, Refusal>;
}

/**
 * A model's string, as one of the four kinds — or nothing.
 *
 * Narrowed here rather than passed through, because the tool's own schema
 * declares the enum and this is the only place that can hold it to it. A model
 * that answers `nitpick` gets the default rather than a refusal: the kind is
 * the least important field on a finding, and losing a whole remark over a
 * synonym would be the wrong trade.
 */
const KINDS = new Set<string>(["comment", "suggestion", "question", "praise"]);

export const kindOf = (value: string | undefined): CommentKind | undefined =>
  value !== undefined && KINDS.has(value) ? (value as CommentKind) : undefined;

/**
 * The tools, and the descriptions are the interface.
 *
 * A model chooses a tool by reading these, so each one says what it answers
 * *and* the thing that is not guessable — that the scope is the working
 * directory and cannot be redirected. Without that an agent asked to look at
 * another checkout will hunt for a parameter that does not exist.
 *
 * `inputSchema` is JSON Schema because MCP says so. It is written by hand
 * rather than derived from the contract's `Schema`, and deliberately: what the
 * agent should send is not what the rpc takes — `from` is supplied by this
 * process and must not be a field a model can fill in.
 */
export const TOOLS = [
  {
    name: "awp_thread",
    description:
      "The work this checkout is part of: its title, the pull requests it is about, " +
      "what it follows on from, and every other checkout the same thread holds — with " +
      "each one's directory and whether an agent is running in it. " +
      "Use it before assuming a change is confined to this repository: one piece of work " +
      "often spans two, and the other half is a directory you can read. " +
      "Scoped to the workspace this server runs in; there is no way to ask about another.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
  },
  {
    name: "awp_review_comments",
    description:
      "Review comments filed against this checkout — the remarks left for you, and the " +
      "findings you have already filed. Read it before re-reporting: a comment with an " +
      "author of 'human' is somebody asking you for something, and one with 'agent' is " +
      "your own and already delivered.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
  },
  {
    name: "awp_file_finding",
    description:
      "File a review comment against a line of this checkout, so it appears beside the " +
      "code in the diff panel rather than in a paragraph somebody has to map back onto " +
      "files. Use it to annotate your own work — which part is risky, which file is " +
      "generated and not worth reading. The line is verified against the file, so pass " +
      "'text' and a moved line is refused rather than silently pointing at whatever now " +
      "occupies that number.",
    inputSchema: {
      type: "object",
      properties: {
        path: { type: "string", description: "Repository-relative, as the diff names it." },
        line: { type: "integer", description: "One-based, as the diff renders it." },
        endLine: { type: "integer", description: "The last line of a range. Omit for one line." },
        kind: {
          type: "string",
          enum: ["comment", "suggestion", "question", "praise"],
          description:
            "comment: an observation. suggestion: a change worth making. question: an " +
            "answer is wanted first. praise: worth keeping, and worth saying so.",
        },
        body: { type: "string", description: "What you have to say about the line." },
        text: {
          type: "string",
          description:
            "The line's exact text. Checked against the file, so a stale line number is " +
            "refused where the mistake is.",
        },
      },
      required: ["path", "line", "body"],
      additionalProperties: false,
    },
  },
  {
    name: "awp_browse",
    description:
      "Point the web panel beside this workspace at a page, so a person can see what you " +
      "are talking about instead of copying a link out of a message. Use it for the thing " +
      "under discussion — the failing build, the pull request, the docs page whose wording " +
      "is the argument — not for pages you are reading yourself; a fetch is cheaper and " +
      "nobody has to look at it. " +
      "The url must be absolute, http or https. Scoped to the workspace this server runs " +
      "in, and the panel is shared by every checkout of the same piece of work.",
    inputSchema: {
      type: "object",
      properties: {
        url: {
          type: "string",
          description: "Absolute, with a scheme: https://example.com/build/412",
        },
      },
      required: ["url"],
      additionalProperties: false,
    },
  },
  {
    name: "awp_tasks",
    description:
      "What is already written down as work to do — this project's task list, with each " +
      "task's status and id. Read it BEFORE planning: the most common way to waste an " +
      "hour here is to propose something that is already a task, with the reasoning for " +
      "it already argued out. Subjects only; pass an id to awp_task for the whole entry.",
    inputSchema: {
      type: "object",
      properties: {
        // Deliberately not a project NAME. The binding rule holds — there is
        // no way to name another project — but the cross-cutting read is the
        // reason the store exists, so it is offered as a scope with no
        // argument to get wrong.
        scope: {
          type: "string",
          enum: ["project", "all"],
          description:
            "project: the repository this checkout belongs to, the default. all: every " +
            "project awp knows about.",
        },
        includeDone: {
          type: "boolean",
          description: "Finished tasks too. Off by default — the list is read to plan from.",
        },
      },
      additionalProperties: false,
    },
  },
  {
    name: "awp_task",
    description:
      "One task in full: the argument behind it, what was measured, what was tried and " +
      "did not work. This is the half worth reading — a task here is an argument rather " +
      "than a ticket, and the subject alone tells you almost nothing.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "string", description: "As awp_tasks reports it." } },
      required: ["id"],
      additionalProperties: false,
    },
  },
] as const;

/**
 * The statuses a task list is read to plan from.
 *
 * Named rather than "not completed", because the set is open — a source may
 * gain a status this window has never seen, and a negative filter would then
 * quietly include it. `agent-tasks.ts` says as much about Claude Code's own.
 */
const OPEN = ["pending", "in_progress", "blocked"] as const;

/** A tool's answer, as MCP carries one: content, and whether it went wrong. */
const said = (text: string, failed = false): unknown => ({
  content: [{ type: "text", text }],
  // ── a refused tool is a RESULT, not a JSON-RPC error ────────────────────
  //
  // An error reply is for a malformed request; a tool that ran and refused has
  // something to say, and `isError` is how MCP carries it. Sent as an error
  // instead, most clients hide the sentence and tell the model only that the
  // call failed — which for `NotAWorkspace` throws away the one thing worth
  // knowing, the directory it was in.
  ...(failed ? { isError: true } : {}),
});

/**
 * The task list, as prose.
 *
 * Subjects and ids, one per line, and nothing else — a list read to plan from
 * is scanned, and 46 entries' worth of argument is most of a context window.
 * `awp_task` is the way to the body, which is where the value is.
 *
 * `[in progress]` rather than a column of every status, because pending is the
 * ordinary case and marking it would be marking every row — the same
 * arithmetic as the inbox's leading icon having none for the common state.
 */
export const tasksSaid = (tasks: ReadonlyArray<Task>, scope: string): string => {
  if (tasks.length === 0) {
    return `No tasks recorded for ${scope}.`;
  }
  const lines = tasks.map((task) => {
    const status = task.status === "pending" ? "" : ` [${task.status.replaceAll("_", " ")}]`;
    return `  ${task.id}${status}  ${task.subject}`;
  });
  return [`${tasks.length} task${tasks.length === 1 ? "" : "s"} for ${scope}:`, ...lines].join(
    "\n",
  );
};

/** One task in full. The subject, then the argument under it, verbatim. */
export const taskSaid = (task: Task): string =>
  [
    `${task.subject}${task.status === "pending" ? "" : ` — ${task.status.replaceAll("_", " ")}`}`,
    task.tags.length === 0 ? undefined : `Tags: ${task.tags.join(", ")}`,
    "",
    task.description === "" ? "(no description)" : task.description,
  ]
    .filter((line) => line !== undefined)
    .join("\n");

/** `path:12`, or `path:12-18` for a range. */
const at = (comment: ReviewComment): string =>
  comment.endLine > comment.line
    ? `${comment.path}:${comment.line}-${comment.endLine}`
    : `${comment.path}:${comment.line}`;

/**
 * The thread, as prose.
 *
 * Prose and not JSON, because what reads it is a model and the fields that
 * matter are the ones a sentence puts in order. A JSON blob makes every field
 * equally prominent, and here they are not: the other checkouts' *directories*
 * are the reason to call this, and they are what a model has to notice.
 */
export const threadSaid = (here: ThreadHere): string => {
  const head = `You are in ${here.project}/${here.workspace} at ${here.dir}.`;
  const thread = here.thread;
  if (thread === undefined) {
    return `${head}\nNo thread claims this checkout, so there is no wider piece of work recorded for it.`;
  }
  const others = thread.checkouts.filter(
    (one) => one.project !== here.project || one.workspace !== here.workspace,
  );
  const lines = [
    head,
    `Thread: ${thread.title === "" ? "untitled" : thread.title}`,
    ...(thread.parent === undefined ? [] : [`Follows on from: ${thread.parent}`]),
    ...(thread.prs.length === 0
      ? []
      : [`Pull requests: ${thread.prs.map((pr) => `${pr.project}#${pr.number}`).join(", ")}`]),
    others.length === 0
      ? "This is the only checkout in the thread."
      : `${others.length} other checkout${others.length === 1 ? "" : "s"} in this thread:`,
    ...others.map(
      (one) =>
        `  ${one.project}/${one.workspace} at ${one.dir}${one.running ? " (an agent is running here)" : ""}`,
    ),
  ];
  return lines.join("\n");
};

/** The comments, as prose. Newest last, which is the order they were written. */
export const commentsSaid = (comments: ReadonlyArray<ReviewComment>): string => {
  if (comments.length === 0) {
    return "No review comments have been filed against this checkout.";
  }
  const mine = comments.filter((one) => one.author === "agent");
  const theirs = comments.filter((one) => one.author === "human");
  const one = (comment: ReviewComment): string =>
    `  ${at(comment)} [${comment.kind}] ${comment.body.replaceAll("\n", " ")}`;
  return [
    ...(theirs.length === 0
      ? []
      : [`${theirs.length} left for you:`, ...theirs.map((entry) => one(entry))]),
    ...(mine.length === 0
      ? []
      : [`${mine.length} you filed already:`, ...mine.map((entry) => one(entry))]),
  ].join("\n");
};

/** A string field of `params.arguments`, or undefined. */
const text = (args: Record<string, unknown>, key: string): string | undefined => {
  const value = args[key];
  return typeof value === "string" && value !== "" ? value : undefined;
};

/** An integer field of `params.arguments`, or undefined. Never NaN. */
const whole = (args: Record<string, unknown>, key: string): number | undefined => {
  const value = args[key];
  return typeof value === "number" && Number.isInteger(value) ? value : undefined;
};

/**
 * One request, answered.
 *
 * `undefined` for a notification — a message with no `id`, which MCP uses for
 * `notifications/initialized` and which must be answered with *nothing* rather
 * than with a reply carrying a null id. A reply to a notification is a protocol
 * error at the other end.
 *
 * @param cwd  the checkout this server is bound to. Passed in rather than read
 *             from `process` so that every sentence below is testable, and so
 *             that the one place it comes from is the one place it can be got
 *             wrong.
 */
export const answer = (
  request: Request,
  cwd: string,
  daemon: Daemon,
): Effect.Effect<Reply | undefined> =>
  Effect.gen(function* () {
    const id = request.id;
    if (id === undefined || id === null) {
      return undefined;
    }
    const reply = (result: unknown): Reply => ({ jsonrpc: "2.0", id, result });

    switch (request.method) {
      case "initialize":
        return reply({
          protocolVersion: PROTOCOL,
          // Tools and nothing else. Declaring a capability this does not
          // implement is how a client comes back with a `resources/list` that
          // answers with a JSON-RPC error on every open.
          capabilities: { tools: {} },
          serverInfo: { name: "awp", version: "0.0.0" },
        });

      case "tools/list":
        return reply({ tools: TOOLS });

      case "tools/call": {
        const name = request.params?.["name"];
        const raw = request.params?.["arguments"];
        const args =
          typeof raw === "object" && raw !== null ? (raw as Record<string, unknown>) : {};

        switch (name) {
          case "awp_thread": {
            const found = yield* Effect.result(daemon.threadAt(cwd));
            return reply(
              Result.isSuccess(found)
                ? said(threadSaid(found.success))
                : said(found.failure.reason, true),
            );
          }

          case "awp_review_comments": {
            const found = yield* Effect.result(daemon.commentsAt(cwd));
            return reply(
              Result.isSuccess(found)
                ? said(commentsSaid(found.success))
                : said(found.failure.reason, true),
            );
          }

          case "awp_tasks": {
            const all = text(args, "scope") === "all";
            const done = args["includeDone"] === true;
            // The project comes from the directory this server was started
            // in, never from an argument — the same binding every other tool
            // here has. `awp_thread` already answers it, and its refusal for
            // a directory outside a workspace is the one wanted.
            const where = all ? undefined : yield* Effect.result(daemon.threadAt(cwd));
            if (where !== undefined && !Result.isSuccess(where)) {
              return reply(said(where.failure.reason, true));
            }
            const project = where === undefined ? undefined : where.success.project;
            const found = yield* Effect.result(
              daemon.board({
                ...(project === undefined ? {} : { tags: [`project:${project}`] }),
                ...(done ? {} : { statuses: OPEN }),
              }),
            );
            return reply(
              Result.isSuccess(found)
                ? said(tasksSaid(found.success, project ?? "every project"))
                : said(found.failure.reason, true),
            );
          }

          case "awp_task": {
            const wanted = text(args, "id");
            if (wanted === undefined) {
              return reply(said("awp_task needs an id — awp_tasks lists them", true));
            }
            const found = yield* Effect.result(daemon.board({}));
            if (!Result.isSuccess(found)) {
              return reply(said(found.failure.reason, true));
            }
            const one = found.success.find((task) => task.id === wanted);
            return reply(
              one === undefined
                ? said(`no task called ${wanted} — awp_tasks lists them`, true)
                : said(taskSaid(one)),
            );
          }

          case "awp_browse": {
            const url = text(args, "url");
            if (url === undefined) {
              return reply(said("awp_browse needs a url", true));
            }
            const went = yield* Effect.result(daemon.browse(cwd, url));
            if (!Result.isSuccess(went)) {
              return reply(said(went.failure.reason, true));
            }
            // What the panel now shows, and *whose* panel it is. The second
            // half is the part a model cannot work out: the page belongs to
            // the thread rather than to this checkout, so an agent that has
            // just moved it has moved what a sibling checkout shows too.
            const where =
              went.success.thread === undefined
                ? "this workspace's web panel"
                : "the web panel for this thread";
            return reply(said(`${where} is now showing ${went.success.url}`));
          }

          case "awp_file_finding": {
            const path = text(args, "path");
            const line = whole(args, "line");
            const body = text(args, "body");
            // Refused here rather than forwarded, because the daemon's own
            // refusal for a missing path would be about a path of "", which
            // reads as a bug in this server rather than a call to fix.
            if (path === undefined || line === undefined || body === undefined) {
              return reply(said("awp_file_finding needs path, line and body", true));
            }
            const filed = yield* Effect.result(
              daemon.file({
                from: cwd,
                path,
                line,
                endLine: whole(args, "endLine"),
                kind: kindOf(text(args, "kind")),
                body,
                text: text(args, "text"),
              }),
            );
            return reply(
              Result.isSuccess(filed)
                ? said(filed.success.where)
                : said(filed.failure.reason, true),
            );
          }

          default:
            return reply(said(`no tool called ${String(name)}`, true));
        }
      }

      default:
        // ── the one place a JSON-RPC error is right ─────────────────────────
        // A method this server does not implement is a malformed request, not
        // a tool that refused. -32601 is JSON-RPC's own code for it, and
        // clients probe for optional methods expecting exactly this.
        return {
          jsonrpc: "2.0",
          id,
          error: { code: -32601, message: `no method ${String(request.method)}` },
        };
    }
  });
