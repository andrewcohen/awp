import { Data, Effect, Queue, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { childEnv } from "./zmx-session";

// Asking Claude something over ACP instead of over `claude -p`.
//
// ACP — the Agent Client Protocol — is JSON-RPC, one object per line, over a
// child process's stdin and stdout. The agent side is not written here:
// `@agentclientprotocol/claude-agent-acp` is a published adapter that speaks
// the protocol and drives the Claude Agent SDK underneath. So this file is a
// *client*, and a small one, because the exchange it needs is three calls
// long.
//
//   → initialize                       what each side can do
//   ← protocolVersion, capabilities
//   → session/new       cwd, model     a conversation, in a directory
//   ← sessionId
//   → session/prompt    the question
//   ← session/update …  agent_message_chunk, one per fragment of the answer
//   ← stopReason                       the turn is over
//
// ── why not `claude -p` ────────────────────────────────────────────────────
//
// Measured against each other on the same question, on this machine:
//
//   claude -p      11.0 · 11.2 · 13.9 · 28.0 seconds
//   ACP            6.4 seconds end to end — 0.9 spawn, 1.3 session, 4.2 answer
//
// Faster is the smaller half. What matters more is that the answer arrives as
// **structured updates rather than as a terminal's stdout**: a text chunk is
// labelled a text chunk, a tool call is labelled a tool call, and the turn
// ends with a reason. Fishing an object out of prose still happens here — see
// `findObject` in intent.ts — but it is now a defence rather than the design.
//
// ── the one thing that stops it working ────────────────────────────────────
//
// Claude Code refuses to run inside Claude Code:
//
//   To bypass this check, unset the CLAUDECODE environment variable.
//
// The daemon is frequently started from inside a session, so this is the
// ordinary case rather than an edge. `childEnv()` already empties the whole
// CLAUDE_CODE family for a different reason — a spawned agent believing it is
// a continuation of the session that started the daemon — and empties
// `CLAUDECODE` with them. So the guard that exists for zmx and for transcripts
// is what makes this work, and it is passed deliberately rather than by
// habit. `probe/acp.ts` asserts on it from outside.

/** Anything that went wrong asking. */
export class AcpError extends Data.TaggedError("AcpError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * Where the adapter is, and why it is not a dependency of this repo.
 *
 * ── 306MB, in every workspace ─────────────────────────────────────────────
 *
 * Depending on `@agentclientprotocol/claude-agent-acp` pulls in the Claude
 * Agent SDK, whose platform package is a **single 306MB file** — a whole
 * bundled `claude`:
 *
 *   node_modules/.bun/@anthropic-ai+claude-agent-sdk-darwin-arm64@0.3.232
 *     └── claude                                        306M, one file
 *
 * That is not one copy. The create job's bootstrap hook runs `bun install` in
 * every workspace it makes, so every checkout on the machine would carry
 * another. Measured on this one: 1.3GB, of which 1.1GB is node_modules.
 *
 * A blanket `--omit=optional` is not the answer either — TypeScript's own
 * compiler binary is an optional dependency too, and the typecheck gate needs
 * it.
 *
 * ── the machine already has a claude ──────────────────────────────────────
 *
 * The whole point of this path is to drive the `claude` a person already
 * installed, so a second copy inside the tree is exactly the thing to remove.
 * The SDK says so itself, in the error it raises when the binary is missing:
 *
 *   Claude native binary not found for darwin-arm64. Reinstall
 *   @anthropic-ai/claude-agent-sdk without --omit=optional, or set
 *   CLAUDE_CODE_EXECUTABLE.
 *
 * So the adapter lives once per machine at {@link TOOLS}, installed *with*
 * `--omit=optional`, and is pointed at the system binary. Measured:
 *
 *   as a dependency, per workspace   306MB
 *   at ~/.awp/tools, once            51MB, and it answers in 2.9s
 *
 * ── what this costs ───────────────────────────────────────────────────────
 *
 * A setup step, and a failure mode when nobody has run it. Both are answered
 * by the sentence in {@link adapterPath}, which names the command — and by
 * the naming call's fallback, which was already there for a model that cannot
 * be reached and does not care why.
 */
const TOOLS = join(homedir(), ".awp", "tools");

/** The adapter's executable, inside the per-machine install. */
const ADAPTER = join(
  TOOLS,
  "node_modules",
  "@agentclientprotocol",
  "claude-agent-acp",
  "dist",
  "index.js",
);

/** How to put it there. Said in the failure, because a path is not an answer. */
export const INSTALL = `mkdir -p ${TOOLS} && cd ${TOOLS} && bun add --omit=optional @agentclientprotocol/claude-agent-acp`;

/**
 * The adapter, or a refusal naming the command that installs it.
 *
 * Run under this process's own runtime — `process.execPath` — rather than
 * through the `.bin` shim, whose shebang names `node` and would ignore that
 * the daemon runs under Bun.
 */
export const adapterPath = (): string | undefined => (existsSync(ADAPTER) ? ADAPTER : undefined);

/**
 * The `claude` the SDK should drive, found the way a shell would.
 *
 * Needed because the adapter is installed without its bundled binary, and
 * looked up here rather than trusted from the environment: `childEnv()`
 * empties every `CLAUDE_CODE_*` marker — a parent describing itself — and
 * `CLAUDE_CODE_EXECUTABLE` is one of those names without being one of those
 * things. Emptied and then set again, in that order, is the only way to have
 * both rules.
 */
export const claudePath = (path: string | undefined = process.env["PATH"]): string | undefined => {
  for (const dir of (path ?? "").split(":")) {
    if (dir === "") {
      continue;
    }
    const candidate = join(dir, "claude");
    if (existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
};

const INITIALIZE = 1;
const NEW_SESSION = 2;
const PROMPT = 3;

interface Message {
  readonly id?: number;
  readonly method?: string;
  readonly result?: Record<string, unknown>;
  readonly error?: unknown;
  readonly params?: Record<string, unknown>;
}

/** One line of the protocol, or nothing if it was not one. */
export const parseMessage = (line: string): Message | undefined => {
  const trimmed = line.trim();
  if (trimmed === "") {
    return undefined;
  }
  try {
    const value = JSON.parse(trimmed) as unknown;
    return typeof value === "object" && value !== null ? (value as Message) : undefined;
  } catch {
    return undefined;
  }
};

/**
 * The text of an `agent_message_chunk`, if that is what this is.
 *
 * Thoughts and tool calls arrive down the same channel and are deliberately
 * dropped: `agent_thought_chunk` is the model reasoning aloud, and putting it
 * in the answer would put reasoning inside the JSON object being fished out.
 */
export const chunkText = (message: Message): string | undefined => {
  if (message.method !== "session/update") {
    return undefined;
  }
  const update = message.params?.["update"] as Record<string, unknown> | undefined;
  if (update?.["sessionUpdate"] !== "agent_message_chunk") {
    return undefined;
  }
  const content = update["content"] as Record<string, unknown> | undefined;
  return content?.["type"] === "text" && typeof content["text"] === "string"
    ? content["text"]
    : undefined;
};

export interface Question {
  /** The directory the session is opened in. */
  readonly cwd: string;
  /** What to ask. */
  readonly prompt: string;
  /** Which model, by the adapter's own id — `haiku`, `sonnet`, `default`. */
  readonly model?: string;
}

interface State {
  readonly sessionId?: string;
  readonly text: string;
  readonly answered: boolean;
}

/**
 * Ask one question and read the whole answer.
 *
 * One session, one turn, then the process goes — the `Scope` on `spawn` is
 * what promises that, including when the caller is interrupted by a timeout.
 * A conversation that outlives a single question is a different shape and
 * belongs with the chat, not here.
 */
export const ask = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  question: Question,
): Effect.Effect<string, AcpError> =>
  Effect.gen(function* () {
    // Requests are written by the fold below, as replies come back, so stdin
    // has to stay open after the first one — hence `endOnDone: false`. A
    // stdin that closes when the initialize is written ends the conversation
    // one message in.
    // Bytes, because that is what a child's stdin takes. A queue of strings
    // encoded on the way out would be the same thing with one more hop.
    const encoder = new TextEncoder();
    const outbox = yield* Queue.unbounded<Uint8Array>();
    const send = (message: unknown) =>
      Effect.asVoid(Queue.offer(outbox, encoder.encode(`${JSON.stringify(message)}\n`)));

    const adapter = adapterPath();
    if (adapter === undefined) {
      return yield* Effect.fail(
        new AcpError({ reason: `the ACP adapter is not installed — run:\n  ${INSTALL}` }),
      );
    }
    const claude = claudePath();
    if (claude === undefined) {
      return yield* Effect.fail(new AcpError({ reason: "there is no claude on the PATH" }));
    }

    const handle = yield* Effect.mapError(
      spawner.spawn(
        ChildProcess.make(process.execPath, [adapter], {
          cwd: question.cwd,
          env: {
            // The whole reason this runs at all. See the note at the top.
            ...childEnv(),
            // After `childEnv`, never inside it. That function empties every
            // `CLAUDE_CODE_*` key because they are a parent describing itself;
            // this one is a path, and putting it in the exception list there
            // would weaken a rule that has already been got wrong twice.
            CLAUDE_CODE_EXECUTABLE: claude,
          },
          stdin: { stream: Stream.fromQueue(outbox), endOnDone: false },
        }),
      ),
      (cause) => new AcpError({ reason: "could not start the ACP adapter", cause }),
    );

    yield* send({
      jsonrpc: "2.0",
      id: INITIALIZE,
      method: "initialize",
      params: {
        protocolVersion: 1,
        clientCapabilities: { fs: { readTextFile: false, writeTextFile: false } },
      },
    });

    const messages = Stream.filter(
      Stream.map(Stream.splitLines(Stream.decodeText(handle.stdout)), parseMessage),
      (message): message is Message => message !== undefined,
    );

    const final = yield* Effect.mapError(
      Stream.runFoldEffect(
        // Inclusive: the reply that ends the turn is the last element folded,
        // not the first one dropped.
        Stream.takeUntil(messages, (message) => message.id === PROMPT),
        (): State => ({ text: "", answered: false }),
        (state: State, message: Message) =>
          Effect.gen(function* () {
            if (message.error !== undefined && message.id !== undefined) {
              return yield* Effect.fail(
                new AcpError({ reason: `the agent refused: ${JSON.stringify(message.error)}` }),
              );
            }

            // A request *from* the agent — a permission prompt, a file read.
            // Tools are disabled below so none is expected, and one arriving
            // unanswered would stall the turn until the timeout rather than
            // fail. Refusing is the answer that keeps the turn moving.
            if (message.method !== undefined && message.id !== undefined) {
              yield* send({
                jsonrpc: "2.0",
                id: message.id,
                error: { code: -32601, message: "this client answers nothing" },
              });
              return state;
            }

            if (message.id === INITIALIZE) {
              yield* send({
                jsonrpc: "2.0",
                id: NEW_SESSION,
                method: "session/new",
                params: {
                  cwd: question.cwd,
                  mcpServers: [],
                  _meta: {
                    claudeCode: {
                      options: {
                        ...(question.model === undefined ? {} : { model: question.model }),
                        // An empty tool list, for the reason the `claude -p`
                        // version disabled them: a question that cannot reach
                        // for Bash is one that cannot do anything surprising.
                        tools: [],
                      },
                    },
                  },
                },
              });
              return state;
            }

            if (message.id === NEW_SESSION) {
              const sessionId = message.result?.["sessionId"];
              if (typeof sessionId !== "string") {
                return yield* Effect.fail(
                  new AcpError({ reason: "the adapter opened a session with no id" }),
                );
              }
              yield* send({
                jsonrpc: "2.0",
                id: PROMPT,
                method: "session/prompt",
                params: {
                  sessionId,
                  prompt: [{ type: "text", text: question.prompt }],
                },
              });
              return { ...state, sessionId };
            }

            if (message.id === PROMPT) {
              return { ...state, answered: true };
            }

            const text = chunkText(message);
            return text === undefined ? state : { ...state, text: state.text + text };
          }),
      ),
      (cause) =>
        cause instanceof AcpError
          ? cause
          : new AcpError({ reason: "could not read from the ACP adapter", cause }),
    );

    // The stream ending before the turn did means the adapter left — which is
    // what a crash, a failed login or a killed process all look like from
    // here. Said as its own sentence rather than returned as an empty answer,
    // because an empty answer is indistinguishable from a model that said
    // nothing.
    if (!final.answered) {
      return yield* Effect.fail(
        new AcpError({ reason: "the ACP adapter stopped before answering" }),
      );
    }
    return final.text;
  }).pipe(Effect.scoped);
