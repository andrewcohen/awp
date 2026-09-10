// The TUI's one connection to the daemon.
//
// A copy of the renderer's seam in shape and a third of its size, and the
// interesting thing about writing it was how little had to change: the client
// in `@awp-kit/protocol/client` says in its own comment that it serves "a
// browser, a test, and a command-line tool", and it does — `globalThis.WebSocket`
// exists in Bun, so the same layer builds here.
//
// Everything above this file is callbacks. Everything below keeps its Scopes
// and its interruption, which is what makes the daemon's end of a stream close
// when this process stops caring.

import type {
  ChatConfigOption,
  ChatDelivery,
  ChatUpdate,
  McpStatus,
  WorkspaceFacts,
} from "@awp-kit/protocol";
import {
  AwpClient,
  type AwpClientShape,
  layerClient,
  layerConnection,
} from "@awp-kit/protocol/client";
import { Effect, Fiber, ManagedRuntime, Schedule, Stream } from "effect";
import { appendFileSync } from "node:fs";

/**
 * Which daemon.
 *
 * `AWP_DAEMON_URL` because that is the variable `chat.ts` already hands its MCP
 * servers, so an agent's tools and a TUI beside them reach the same instance.
 * A second instance on 5284 is the repo's own convention for looking at a
 * branch without disturbing the daemon somebody is working in.
 */
export const url = process.env.AWP_DAEMON_URL ?? "ws://127.0.0.1:5274";

const listeners = new Set<(connected: boolean) => void>();
let connected = false;

export const onConnection = (listener: (connected: boolean) => void): (() => void) => {
  listeners.add(listener);
  listener(connected);
  return () => listeners.delete(listener);
};

/**
 * Run `again` each time the daemon comes back — and not for the state it is
 * in now.
 *
 * ── a stream is not a substitute for asking ─────────────────────────────
 *
 * The socket reconnects on its own: `makeProtocolSocket` wraps its loop in a
 * retry, so the connection is back within seconds of a daemon restart. What
 * does not come back is everything built on it. A feed is resubscribed by
 * `subscribe` below; a *call* was asked once, in a mount effect, and nothing
 * asks it again — so after a restart the thread list is whatever it was
 * before, and one that failed during the outage is empty for good.
 *
 * The window has had this since it learned to survive a restart and the TUI
 * did not, which is the whole of "the client does not reconnect": the socket
 * was never the problem.
 *
 * The transition, not the state. `onConnection` reports where things stand
 * the moment it is called, which is what a status line wants and exactly
 * wrong here: a component that has just asked would ask again for the same
 * answer.
 */
export const onReconnect = (again: () => void): (() => void) => {
  let first = true;
  return onConnection((state) => {
    if (first) {
      first = false;
      return;
    }
    if (state) again();
  });
};

/**
 * Somewhere to see what the socket did.
 *
 * A TUI owns the screen, so `console.log` is captured by the renderer's own
 * console overlay and a failure that happens before anything is drawn has
 * nowhere to go. `AWP_TUI_LOG` is that nowhere.
 */
export const logTui = (line: string) => {
  const path = process.env.AWP_TUI_LOG;
  if (path === undefined) return;
  // Appended, not written: `Bun.write` truncates, so a log written that way
  // holds one line — the last one — and every earlier thing the process said
  // is gone by the time anybody reads it.
  appendFileSync(path, `${new Date().toISOString()} ${line}\n`);
};

const announce = (state: boolean) => {
  logTui(`connection ${state}`);
  connected = state;
  for (const listener of listeners) listener(state);
};

const runtime = ManagedRuntime.make(
  layerClient(url, layerConnection({ opened: () => announce(true), lost: () => announce(false) })),
);

// An rpc stream is a request, so its fiber dies with the connection it was made
// on. The socket reconnects on its own; a feed does not, and a feed that did
// not resubscribe would leave the transcript showing whatever it last heard
// while the status line said the daemon was fine.
const RESUBSCRIBE = Schedule.min([Schedule.exponential(500, 1.5), Schedule.spaced(5000)]);

const subscribe = <E>(run: (rpc: AwpClientShape) => Effect.Effect<void, E>): (() => void) => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, run).pipe(
      Effect.retry(RESUBSCRIBE),
      Effect.catchCause(() => Effect.void),
    ),
  );
  return () => {
    runtime.runFork(Fiber.interrupt(fiber));
  };
};

/**
 * The daemon's own sentence, out of a refusal.
 *
 * Every refusal in the contract is a `Schema.TaggedError` carrying `reason` and
 * none of them sets `message`, so `String(error)` is the tag and only the tag —
 * "ChatUnavailable" where the field says which directory and why.
 */
export const said = (error: unknown): string => {
  const reason = (error as { reason?: unknown } | null)?.reason;
  if (typeof reason === "string") return reason;
  const message = (error as { message?: unknown } | null)?.message;
  return typeof message === "string" ? message : String(error);
};

/** The conversation on a workspace: the history, then whatever happens next. */
export const watchChat = (
  project: string,
  workspace: string,
  onUpdate: (update: ChatUpdate) => void,
  /**
   * Called before a *resubscription* replays, and never for the first one.
   *
   * Every `ChatOpen` replays the whole transcript — which is how a client
   * that opens at noon sees what was said at nine, and is why resubscribing
   * hands this one the conversation a second time. Without emptying what is
   * held, a daemon restart draws every message twice.
   */
  onRestart?: () => void,
): (() => void) => {
  let first = true;
  return subscribe((rpc) =>
    // `suspend`, so it runs per attempt: the retry re-runs the effect rather
    // than the call that built it.
    Effect.suspend(() => {
      if (first) {
        first = false;
      } else {
        logTui("chat feed: resubscribed, replaying");
        onRestart?.();
      }
      return Stream.runForEach(rpc.ChatOpen({ project, workspace }), (update) =>
        Effect.sync(() => onUpdate(update)),
      ).pipe(Effect.tapCause((cause) => Effect.sync(() => logTui(`chat feed: ${String(cause)}`))));
    }),
  );
};

/**
 * Say something.
 *
 * Resolves when it has been delivered rather than when the agent has finished,
 * and answers which way it went: `steer` into the turn already running, or
 * `prompt` for a turn of its own.
 */
export const chatSend = (
  project: string,
  workspace: string,
  text: string,
  /** This client's name for the message. See `ChatSend.key` in the contract. */
  key: string,
): Promise<ChatDelivery> =>
  runtime.runPromise(
    Effect.flatMap(AwpClient, (rpc) => rpc.ChatSend({ project, workspace, text, key })),
  );

/**
 * Stop the turn the agent is in.
 *
 * Nothing comes back and nothing needs to: an idle conversation ignores it,
 * and a running one ends the way every turn ends, on the update stream.
 */
export const chatCancel = (project: string, workspace: string): Promise<void> =>
  runtime.runPromise(
    Effect.asVoid(Effect.flatMap(AwpClient, (rpc) => rpc.ChatCancel({ project, workspace }))),
  );

/** Answer a permission request by the id its update carried. */
export const chatAnswer = (
  project: string,
  workspace: string,
  request: string,
  option: string,
): Promise<void> =>
  runtime.runPromise(
    Effect.asVoid(
      Effect.flatMap(AwpClient, (rpc) => rpc.ChatAnswer({ project, workspace, request, option })),
    ),
  );

/**
 * What this session is running as: the mode, the model, the effort, fast mode.
 *
 * A call and not a field on the stream, which is the contract's own reasoning
 * — a list of every model the agent offers would otherwise cross the wire
 * several times a turn. Read once when the screen opens, which is all the
 * status row under the composer needs: nothing here changes without somebody
 * changing it, and this client offers no way to.
 */
export const chatConfig = (
  project: string,
  workspace: string,
): Promise<ReadonlyArray<ChatConfigOption>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ChatConfig({ project, workspace })));

/**
 * `/new`: forget which conversation this workspace is having.
 *
 * Not a delete and not a fork. The daemon drops the stored session id and
 * throws away the adapter holding it, so the next open is a `session/new`;
 * the transcript stays on disk, and is loadable by anything that knows its
 * id. The reply is the new session id, which this client does not need — what
 * it needs is the refusal, on the keypress rather than silently on the next
 * subscribe.
 */
export const chatFresh = (project: string, workspace: string): Promise<string> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ChatFresh({ project, workspace })));

/**
 * `/mcp`: the server this workspace's conversation is handed.
 *
 * No error channel in the contract, deliberately — it is a description of
 * what the daemon passes on every open, composed from the same functions that
 * pass it. A workspace with no conversation open still has an answer, which is
 * the right one: the question is "what will this agent be able to do".
 */
export const mcpStatus = (project: string, workspace: string): Promise<McpStatus> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.McpStatus({ project, workspace })));

/**
 * What is known about every workspace, and again whenever it changes.
 *
 * A stream rather than a call, which is the contract's own split: a thread
 * changes when a person changes it, but an agent goes from working to waiting
 * on its own. The first push is the table as it stands, so a subscriber has
 * an answer without asking for one.
 *
 * The list uses one field of it — `lastActiveAt`, which is what orders it.
 */
export const watchFacts = (onFacts: (facts: ReadonlyArray<WorkspaceFacts>) => void): (() => void) =>
  subscribe((rpc) =>
    Stream.runForEach(rpc.WorkspaceFactsChanges(), (facts) =>
      Effect.sync(() => onFacts(facts)),
    ).pipe(Effect.tapCause((cause) => Effect.sync(() => logTui(`facts feed: ${String(cause)}`)))),
  );

/** Every thread, newest first — the list this POC opens on. */
export const threads = () =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadList()));

/**
 * Where a workspace is on disk.
 *
 * Asked rather than composed. `~/.awp/workspaces/<project>/<workspace>` is the
 * daemon's convention, and a client that spelled it out itself would be a
 * second implementation of it — the copy that drifts being the one nobody
 * tests. Same argument as `SessionIdentity` being on the wire.
 */
export const workspaceDir = (project: string, workspace: string) =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.WorkspaceDir({ project, workspace })));

/** Every session the daemon can see, which is how the TUI finds a workspace. */
export const sessions = () =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.SessionList()));
