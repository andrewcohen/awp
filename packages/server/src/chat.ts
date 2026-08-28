// A conversation with an agent, over ACP rather than down a pty.
//
// The terminal reaches an agent by writing bytes at a program that draws a
// picture of a conversation. This reaches the conversation itself: a text
// chunk arrives labelled a text chunk, a tool call arrives as a tool call with
// a status that later changes, and a turn ends with a reason. What the window
// draws is a record rather than a rectangle of ANSI.
//
// ── a session is a file, not a process ────────────────────────────────────
//
// Measured 2026-08-28, and it is the fact the whole design rests on. The
// adapter was SIGKILLed twelve seconds into a running Bash loop; a fresh
// process, given the same session id, replayed the history and answered from
// it:
//
//   descendants of the dead pid   0        nothing was orphaned
//   the loop                      killed with it
//   session/load in a new process replayed, and remembered a word given
//                                 to the process that had died
//
// So there is nothing here to keep alive across a daemon restart, and nothing
// to daemonize under zmx — a pty is the wrong pipe for line-delimited JSON-RPC
// anyway. What a restart costs is the turn that was in flight, which the
// transcript records as a tool call that started and never completed. That is
// the honest account and it is the one the agent itself gives when asked.
//
// ── load, resume, fork ────────────────────────────────────────────────────
//
//   session/load     replays the history as updates, then continues   ← here
//   session/resume   replays nothing, remembers everything
//   session/fork     a new id, the same memory, the original untouched
//
// `load` is what opening a window on a conversation means: the same update
// shape a live turn uses, so one renderer draws the history and the present
// without knowing which it is looking at.
//
// ── the mode is chosen, because the default is not ours to accept ─────────
//
// `session/new` opens in `auto` — "use a model classifier to approve/deny
// permission prompts". Measured in `default` (Manual): reading a file was not
// referred to this client at all, and `rm` was, with reject_once, allow_once
// and allow_always. So a session amoeba opens on somebody's repository is put
// in Manual and the refusals are shown to a person. Leaving it at `auto` would
// mean nobody in this window is ever asked.

import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer, Queue, RcMap, Ref, type Scope, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { realpathSync } from "node:fs";
import type { ChatUpdate } from "@awp-kit/protocol";
import { INSTALL, adapterPath, claudePath, parseMessage } from "./acp";
import { workspacePath } from "./jobs/create-workspace";
import { childEnv } from "./zmx-session";

/** Anything that stopped a conversation being had. */
export class ChatError extends Data.TaggedError("ChatError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * Manual, and said out loud rather than left to the default.
 *
 * See the note at the top: the default is a model approving tool calls on this
 * client's behalf, which is not a thing to inherit by saying nothing.
 */
export const MODE = "default";

/** What a turn is asked to run as. */
export interface ChatOptions {
  readonly cwd: string;
  readonly model?: string;
  /**
   * The session to continue, when the daemon has a record of one.
   *
   * **Nothing is loaded that is not named here**, and that is the whole of the
   * rule. `session/list` for a workspace directory answers with every session
   * ever held in it — including the one the *terminal's* `claude` is running
   * right now, which is normally the newest. Taking the newest and loading it
   * makes the ACP side a second writer on a transcript an interactive agent is
   * still appending to, and neither process knows about the other.
   *
   * So an unknown session means a new one, never somebody else's. The list is
   * still asked for, but only to check that this id is really there: a
   * transcript deleted or moved would otherwise fail inside `session/load`
   * with a sentence about a session id, one step after the mistake.
   */
  readonly session?: string;
}

interface Pending {
  readonly reply: (message: Record<string, unknown>) => Effect.Effect<void>;
}

/**
 * An update, numbered.
 *
 * The number is what makes a late subscriber correct without a lock. Somebody
 * opening a window takes the transcript so far and then reads the live feed,
 * and an update that landed between those two steps would otherwise arrive
 * twice — once in the history and once down the queue. Registering first and
 * dropping anything already in the snapshot is exact, and needs no mutual
 * exclusion in a runtime that has none to offer.
 */
interface Numbered {
  readonly seq: number;
  readonly update: ChatUpdate;
}

interface Conversation {
  /**
   * The session this conversation is on.
   *
   * Read by the caller and written down, so the next open continues this one
   * rather than starting again beside it — or, worse, joining a session that
   * belongs to something else.
   */
  readonly sessionId: string;
  /** The history so far, then everything that happens next. */
  readonly updates: Effect.Effect<Stream.Stream<ChatUpdate>, never, Scope.Scope>;
  /** Say something. Returns as soon as the turn has started, not when it ends. */
  readonly send: (text: string) => Effect.Effect<void, ChatError>;
  /** Answer a permission request by the id the update carried. */
  readonly answer: (request: string, option: string) => Effect.Effect<void, ChatError>;
}

/** The text of a content block, if it is text. */
const textOf = (content: unknown): string | undefined => {
  const block = content as Record<string, unknown> | undefined;
  return block?.["type"] === "text" && typeof block["text"] === "string"
    ? block["text"]
    : undefined;
};

/**
 * One `session/update` as something the window can draw, or nothing.
 *
 * Deliberately lossy. `usage_update` and `available_commands_update` arrive on
 * every turn and say nothing a person reads; dropping them here rather than in
 * the renderer keeps the wire the size of what is shown.
 */
export const updateOf = (params: Record<string, unknown>): ChatUpdate | undefined => {
  const update = params["update"] as Record<string, unknown> | undefined;
  const kind = update?.["sessionUpdate"];
  if (update === undefined || typeof kind !== "string") {
    return undefined;
  }

  if (kind === "agent_message_chunk" || kind === "user_message_chunk") {
    const text = textOf(update["content"]);
    return text === undefined
      ? undefined
      : { kind: "message", role: kind === "user_message_chunk" ? "user" : "agent", text };
  }

  if (kind === "agent_thought_chunk") {
    const text = textOf(update["content"]);
    return text === undefined ? undefined : { kind: "message", role: "thought", text };
  }

  // A tool call arrives as several updates sharing one id: pending with a
  // generic title, then the real command, then the output, then completed. So
  // this is a patch keyed by `id` and the window merges — which is why every
  // field but the id is optional here.
  if (kind === "tool_call" || kind === "tool_call_update") {
    const id = update["toolCallId"];
    if (typeof id !== "string") {
      return undefined;
    }
    const content = update["content"];
    const first = Array.isArray(content) ? (content[0] as Record<string, unknown>) : undefined;
    const output =
      typeof update["rawOutput"] === "string" ? update["rawOutput"] : textOf(first?.["content"]);
    return {
      kind: "tool",
      id,
      ...(typeof update["title"] === "string" ? { title: update["title"] } : {}),
      ...(typeof update["kind"] === "string" ? { toolKind: update["kind"] } : {}),
      ...(typeof update["status"] === "string" ? { status: update["status"] } : {}),
      ...(output === undefined ? {} : { output }),
    } as ChatUpdate;
  }

  return undefined;
};

/** A permission request as something with buttons on it. */
export const permissionOf = (params: Record<string, unknown>, id: string): ChatUpdate => {
  const call = params["toolCall"] as Record<string, unknown> | undefined;
  const options = Array.isArray(params["options"]) ? params["options"] : [];
  return {
    kind: "permission",
    id,
    title: typeof call?.["title"] === "string" ? call["title"] : "a tool wants to run",
    options: options.map((raw) => {
      const option = raw as Record<string, unknown>;
      return {
        id: String(option["optionId"] ?? ""),
        name: String(option["name"] ?? option["optionId"] ?? ""),
        kind: String(option["kind"] ?? ""),
      };
    }),
  };
};

/**
 * Open one adapter process and hold a conversation in it.
 *
 * The Scope is the process: when the last window on this workspace closes, the
 * adapter goes and the transcript stays on disk, which is the arrangement the
 * measurement at the top says is safe.
 */
export const conversation = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  options: ChatOptions,
): Effect.Effect<Conversation, ChatError, Scope.Scope> =>
  Effect.gen(function* () {
    const adapter = adapterPath();
    if (adapter === undefined) {
      return yield* Effect.fail(
        new ChatError({ reason: `the ACP adapter is not installed — run:\n  ${INSTALL}` }),
      );
    }
    const claude = claudePath();
    if (claude === undefined) {
      return yield* Effect.fail(new ChatError({ reason: "there is no claude on the PATH" }));
    }

    const encoder = new TextEncoder();
    const outbox = yield* Queue.unbounded<Uint8Array>();
    const write = (message: unknown) =>
      Effect.asVoid(Queue.offer(outbox, encoder.encode(`${JSON.stringify(message)}\n`)));

    const handle = yield* Effect.mapError(
      spawner.spawn(
        ChildProcess.make(process.execPath, [adapter], {
          cwd: options.cwd,
          // See acp.ts: `childEnv` empties CLAUDECODE, without which Claude
          // Code refuses to run inside Claude Code; the executable is set
          // after it, because that key is a path rather than a parent
          // describing itself.
          env: { ...childEnv(), CLAUDE_CODE_EXECUTABLE: claude },
          stdin: { stream: Stream.fromQueue(outbox), endOnDone: false },
        }),
      ),
      (cause) => new ChatError({ reason: "could not start the ACP adapter", cause }),
    );

    // The conversation's own scope, so a forked turn belongs to the adapter
    // process rather than to whichever caller happened to send the message.
    // A prompt forked into a handler's scope is interrupted when that call
    // returns, which is immediately — the turn would be cancelled the moment
    // it started.
    const mine = yield* Effect.scope;

    const transcript = yield* Ref.make<ReadonlyArray<Numbered>>([]);
    const subscribers = yield* Ref.make(new Set<Queue.Queue<Numbered>>());
    const emit = (update: ChatUpdate) =>
      Effect.gen(function* () {
        const seq = yield* Ref.modify(transcript, (all) => {
          const numbered: Numbered = { seq: all.length, update };
          return [numbered, [...all, numbered]] as const;
        });
        for (const queue of yield* Ref.get(subscribers)) {
          yield* Queue.offer(queue, seq);
        }
      });

    // Requests this client made, waiting for their replies, and requests the
    // agent made, waiting for a person. Two directions, two tables.
    let next = 0;
    const waiting = new Map<number, (message: Record<string, unknown>) => void>();
    const asked = yield* Ref.make(new Map<string, Pending>());

    const request = (
      method: string,
      params: unknown,
    ): Effect.Effect<Record<string, unknown>, ChatError> =>
      Effect.callback<Record<string, unknown>, ChatError>((resume) => {
        next += 1;
        const id = next;
        waiting.set(id, (message) => {
          if (message["error"] !== undefined) {
            resume(
              Effect.fail(
                new ChatError({ reason: `${method}: ${JSON.stringify(message["error"])}` }),
              ),
            );
            return;
          }
          resume(Effect.succeed((message["result"] ?? {}) as Record<string, unknown>));
        });
        // Registered before it is written, and written from inside the
        // register rather than before it. The reader is another fiber: a
        // reply to a request whose waiter is not in the table yet is a reply
        // dropped, and the call then waits for the timeout rather than for
        // an answer that already came back.
        Queue.offerUnsafe(
          outbox,
          encoder.encode(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`),
        );
        return Effect.sync(() => waiting.delete(id));
      });

    const lines = Stream.splitLines(Stream.decodeText(handle.stdout));
    const reader = Stream.runForEach(lines, (line) =>
      Effect.gen(function* () {
        const message = parseMessage(line) as Record<string, unknown> | undefined;
        if (message === undefined) {
          return;
        }
        const id = message["id"];
        const method = message["method"];

        if (typeof id === "number" && method === undefined) {
          const pending = waiting.get(id);
          waiting.delete(id);
          if (pending !== undefined) {
            pending(message);
          }
          return;
        }

        if (method === "session/update") {
          const update = updateOf((message["params"] ?? {}) as Record<string, unknown>);
          if (update !== undefined) {
            yield* emit(update);
          }
          return;
        }

        // A request from the agent. A permission prompt is shown to a person;
        // anything else is refused, because an unanswered request stalls the
        // turn until a timeout rather than failing, and a stall is the one
        // outcome nothing on screen explains.
        if (typeof method === "string" && typeof id === "number") {
          if (method === "session/request_permission") {
            const key = `permission-${String(id)}`;
            yield* Ref.update(asked, (all) =>
              new Map(all).set(key, {
                reply: (result) => write({ jsonrpc: "2.0", id, result }),
              }),
            );
            yield* emit(permissionOf((message["params"] ?? {}) as Record<string, unknown>, key));
            return;
          }
          yield* write({
            jsonrpc: "2.0",
            id,
            error: { code: -32601, message: "this client answers nothing" },
          });
        }
      }),
    );
    yield* Effect.forkScoped(Effect.ignore(reader));

    yield* request("initialize", {
      protocolVersion: 1,
      clientCapabilities: { fs: { readTextFile: false, writeTextFile: false } },
    });

    // The session for this directory: the one the daemon named, if it is
    // still there, and a new one otherwise.
    //
    // ── never the newest, and this is the bug that shape produces ──────────
    //
    // The first version took the most recently updated session whose cwd
    // matched. In a workspace with an agent running in its terminal, that is
    // the terminal's session — so opening the chat did not start a second
    // conversation, it joined the one somebody was already having, as a second
    // writer, with nothing in either process aware of the other.
    //
    // ── and do not compose the path either ────────────────────────────────
    //
    // `agent-tasks.ts` maps a directory to `~/.claude/projects/<slug>/` by
    // replacing punctuation, and that slug is built from the RESOLVED path, so
    // the guess is wrong the moment anything above it is a symlink. On macOS
    // `/var/…` is `/private/var/…`, and the guess missed by a whole prefix
    // while the session sat plainly in the list:
    //
    //   guessed   -var-folders-…-T-awp-acp-spike-XZUyvb      does not exist
    //   actual    -private-var-folders-…-T-awp-acp-spike
    //
    // So the list is asked for — and used only to confirm the id the caller
    // already holds.
    const here = realpathSync(options.cwd);
    const listed: Record<string, unknown> = yield* Effect.orElseSucceed(
      request("session/list", { cwd: options.cwd }),
      () => ({}) as Record<string, unknown>,
    );
    const sessions = Array.isArray(listed["sessions"]) ? listed["sessions"] : [];
    const known =
      options.session === undefined
        ? undefined
        : sessions
            .map((raw) => raw as Record<string, unknown>)
            .find((session) => session["sessionId"] === options.session && session["cwd"] === here);

    const claudeCode = {
      _meta: {
        claudeCode: {
          options: options.model === undefined ? {} : { model: options.model },
        },
      },
    };

    const opened =
      known === undefined
        ? yield* request("session/new", { cwd: options.cwd, mcpServers: [], ...claudeCode })
        : yield* request("session/load", {
            sessionId: options.session,
            cwd: options.cwd,
            mcpServers: [],
            ...claudeCode,
          });

    const sessionId = String(opened["sessionId"] ?? options.session ?? "");
    if (sessionId === "") {
      return yield* Effect.fail(
        new ChatError({ reason: "the adapter opened a session with no id" }),
      );
    }

    // After the session exists, and ignored if the adapter will not have it:
    // an older one that cannot set a mode is still a usable conversation, and
    // refusing to open at all would be a worse answer than a session running
    // in the mode it chose.
    yield* Effect.ignore(request("session/set_mode", { sessionId, modeId: MODE }));

    const promptOf = (text: string) => ({
      sessionId,
      prompt: [{ type: "text", text }],
    });

    return {
      sessionId,

      updates: Effect.gen(function* () {
        const queue = yield* Queue.unbounded<Numbered>();
        // Register first, snapshot second. The other order drops anything that
        // lands in between; this one duplicates it, and a duplicate is what the
        // sequence number removes.
        yield* Ref.update(subscribers, (all) => new Set(all).add(queue));
        yield* Effect.addFinalizer(() =>
          Ref.update(subscribers, (all) => {
            const rest = new Set(all);
            rest.delete(queue);
            return rest;
          }),
        );
        const history = yield* Ref.get(transcript);
        const from = history.length;
        return Stream.concat(
          Stream.fromIterable(history.map((one) => one.update)),
          Stream.map(
            Stream.filter(Stream.fromQueue(queue), (one) => one.seq >= from),
            (one) => one.update,
          ),
        );
      }),

      // Forked, because a turn takes as long as the work does and the answer
      // comes back down the update stream. Awaiting it here would make sending
      // a message a call that returns when the agent has finished thinking.
      send: (text: string) =>
        Effect.asVoid(
          Effect.forkIn(Effect.ignore(request("session/prompt", promptOf(text))), mine),
        ),

      answer: (requestId: string, option: string) =>
        Effect.gen(function* () {
          const pending = (yield* Ref.get(asked)).get(requestId);
          if (pending === undefined) {
            return yield* Effect.fail(
              new ChatError({ reason: "that request has already been answered" }),
            );
          }
          yield* Ref.update(asked, (all) => {
            const rest = new Map(all);
            rest.delete(requestId);
            return rest;
          });
          yield* pending.reply({ outcome: { outcome: "selected", optionId: option } });
        }),
    };
  });

/**
 * Which session belongs to the chat, for each workspace.
 *
 * This exists because of what `session/list` answers with. Every session ever
 * held in a workspace directory is in that list, the terminal's included — so
 * without a record of its own, the chat has no way to tell the conversation it
 * started from the one somebody is having in the pane beside it. Taking the
 * newest is the obvious rule and it joins the terminal's session as a second
 * writer.
 *
 * One row per workspace, because the agent column shows one conversation. A
 * second would have nowhere to be drawn.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "chat.001-sessions",
    up: [
      `create table chat_sessions (
         project    text not null,
         workspace  text not null,
         session_id text not null,
         primary key (project, workspace)
       ) strict`,
    ],
  },
];

/**
 * Every conversation the window has open, one adapter each.
 *
 * `RcMap` for the reason `Sessions` uses one for ptys: the lifecycle belongs to
 * the callers, not to a counter kept by hand. The first window on a workspace
 * spawns the adapter, a second joins it, and the process goes when the last one
 * closes. Two adapters on one workspace would be two writers on one transcript
 * — which is exactly what `session/load` on a session somebody is already
 * sitting in would be, and nothing in the protocol prevents it.
 */
export class Chat extends Context.Service<
  Chat,
  {
    /** The conversation on a workspace, for as long as the caller's Scope is open. */
    readonly open: (
      project: string,
      workspace: string,
    ) => Effect.Effect<Stream.Stream<ChatUpdate>, ChatError, Scope.Scope>;

    /** Say something to it. */
    readonly send: (
      project: string,
      workspace: string,
      text: string,
    ) => Effect.Effect<void, ChatError>;

    /** Answer one of its permission requests. */
    readonly answer: (
      project: string,
      workspace: string,
      request: string,
      option: string,
    ) => Effect.Effect<void, ChatError>;
  }
>()("awp/Chat") {}

/**
 * The key, and it is a composed string because RcMap's lookup takes one value.
 *
 * A separator that cannot appear in either half: a project is a basename and a
 * workspace is a path segment, so neither holds a newline.
 */
const keyOf = (project: string, workspace: string): string => `${project}\n${workspace}`;

const partsOf = (key: string): readonly [string, string] => {
  const at = key.indexOf("\n");
  return [key.slice(0, at), key.slice(at + 1)] as const;
};

export const make = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
  const db = yield* Db;

  const readSession = db.prepare(
    "select session_id from chat_sessions where project = ? and workspace = ?",
  );
  const writeSession = db.prepare(
    `insert into chat_sessions (project, workspace, session_id) values (?, ?, ?)
       on conflict (project, workspace) do update set session_id = excluded.session_id`,
  );

  const conversations = yield* RcMap.make({
    lookup: (key: string) =>
      Effect.gen(function* () {
        const [project, workspace] = partsOf(key);
        const remembered = yield* Effect.orElseSucceed(
          attempt("read the chat session", () => readSession.all(project, workspace)),
          () => [],
        );
        const known = remembered[0]?.["session_id"];

        const held = yield* conversation(spawner, {
          cwd: workspacePath(project, workspace),
          ...(typeof known === "string" ? { session: known } : {}),
        });

        // Written after the session exists rather than before, and every time
        // rather than only when it is new: a record that named a session the
        // adapter then refused to open would send the next open to the same
        // refusal. What is stored is what was actually opened.
        yield* Effect.ignore(
          attempt("remember the chat session", () =>
            writeSession.run(project, workspace, held.sessionId),
          ),
        );
        return held;
      }),
    // A held-open adapter costs a process and a model's context, and a person
    // switching tabs comes back within seconds. Long enough that a tab switch
    // is free, short enough that a window left on the diff all afternoon is
    // not holding an agent open.
    idleTimeToLive: "2 minutes",
  });

  // `send` and `answer` acquire the conversation the same way `open` does,
  // rather than reading an index the way Sessions has to. The difference is
  // that writing to a session nobody is attached to has to fail — a pty is a
  // live thing — where saying something to a conversation nobody has open is
  // perfectly meaningful, and opening one to say it is the right answer.
  const held = <A>(
    project: string,
    workspace: string,
    use: (conversation: Conversation) => Effect.Effect<A, ChatError>,
  ): Effect.Effect<A, ChatError> =>
    Effect.scoped(Effect.flatMap(RcMap.get(conversations, keyOf(project, workspace)), use));

  return {
    open: (project: string, workspace: string) =>
      Effect.flatMap(RcMap.get(conversations, keyOf(project, workspace)), (one) => one.updates),

    send: (project: string, workspace: string, text: string) =>
      held(project, workspace, (one) => one.send(text)),

    answer: (project: string, workspace: string, request: string, option: string) =>
      held(project, workspace, (one) => one.answer(request, option)),
  };
});

export const layer: Layer.Layer<Chat, never, ChildProcessSpawner.ChildProcessSpawner | Db> =
  Layer.effect(Chat)(make);
