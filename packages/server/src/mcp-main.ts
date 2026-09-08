// The MCP server as a process: stdin and stdout on one side, the daemon's
// socket on the other.
//
// Everything about *what* it answers is in `mcp.ts` and is a pure function.
// This file is the two things that cannot be: the line framing, and the
// working directory the whole server is bound to.
//
//     bun run mcp                      the daemon on its default port
//     AWP_DAEMON_URL=… bun run mcp     a second instance
//
// ── the cwd is the binding, and it is read exactly once ────────────────────
//
// Every tool is scoped to the checkout this process is running in, and no tool
// takes a workspace argument — see the note at the top of `mcp.ts`. So the
// binding is `process.cwd()`, read here, at startup, and handed to `answer` as
// a value. A tool that read it per call would be a tool that changes what it
// is bound to if anything ever chdir'd.
//
// It is not validated here either. `NotAWorkspace` is the daemon's answer and
// it names the directory, which is a far better first tool call than a server
// that refused to start with a message into a pipe nobody is reading yet.

import { Effect, Result, Stream } from "effect";
import * as client from "@awp-kit/protocol/client";
import { type Daemon, answer, parseLine } from "./mcp";

const url = process.env["AWP_DAEMON_URL"] ?? client.DEFAULT_DAEMON_URL;

/**
 * The daemon, as the four calls `mcp.ts` asks for.
 *
 * Each maps the rpc's typed refusal onto `{ reason }`, which is all the
 * dispatch wants — it renders a sentence either way, and a tool result has
 * nowhere to put a tag. A defect stays a defect: `Effect.orDie` is not used
 * here because these three rpcs have no other failure mode, and one that grew
 * a second should break this file at compile time rather than be swallowed.
 */
/**
 * Whatever the daemon or the socket said, as one sentence.
 *
 * Two error channels reach here and both have to render. The rpc's own
 * refusals carry `reason` — `NotAWorkspace`, `ReviewFileFailed` — and the
 * transport's carry `message`. A tool result has nowhere to put a tag, so what
 * is kept is the prose, which is the interface an agent reads.
 *
 * The socket case is the one worth naming: the daemon not being up is not a
 * defect of this process, and an agent told "awp is not running" can carry on
 * without it. Dying instead would take the agent's whole tool surface with it.
 */
const refusal = (error: unknown): { readonly reason: string } => {
  const reason = (error as { readonly reason?: unknown } | null)?.reason;
  if (typeof reason === "string" && reason !== "") {
    return { reason };
  }
  const message = (error as { readonly message?: unknown } | null)?.message;
  return {
    reason:
      typeof message === "string" && message !== "" ? `awp: ${message}` : `awp: ${String(error)}`,
  };
};

const over = (rpc: client.AwpClientShape): Daemon => ({
  threadAt: (from) => rpc.ThreadAt({ from }).pipe(Effect.mapError(refusal)),
  commentsAt: (from) =>
    rpc.ReviewAt({ from }).pipe(
      Effect.map((found) => found.comments),
      Effect.mapError(refusal),
    ),
  file: (finding) => rpc.ReviewFile(finding).pipe(Effect.mapError(refusal)),
});

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;
  const daemon = over(rpc);
  const cwd = process.cwd();

  // Line-delimited JSON, which is MCP's stdio framing and byte for byte what
  // `acp.ts` reads from the Claude Code adapter. `splitLines` after
  // `decodeText`, so a message split across two chunks is one message — a
  // decode per chunk would cut multi-byte characters in half.
  const stdin = Stream.fromReadableStream<Uint8Array, never>({
    evaluate: () => Bun.stdin.stream(),
    // stdin ending is how this process is told to stop, and a read error on it
    // is the same event wearing a worse hat. Dying with a stack trace into a
    // pipe the client has already closed would be noise nobody reads.
    onError: () => undefined as never,
  });

  yield* Stream.runForEach(Stream.splitLines(Stream.decodeText(stdin)), (line) =>
    Effect.gen(function* () {
      const request = parseLine(line);
      if (request === undefined) {
        return;
      }
      // Every reply is written, including the ones that failed, and a defect
      // inside a tool becomes a reply rather than the end of the process: an
      // MCP server that exits mid-conversation takes the agent's whole tool
      // surface with it, and the agent is told nothing about why.
      const written = yield* Effect.result(answer(request, cwd, daemon));
      const reply = Result.isSuccess(written)
        ? written.success
        : {
            jsonrpc: "2.0" as const,
            id: request.id ?? null,
            error: { code: -32603, message: "awp: the tool failed unexpectedly" },
          };
      if (reply !== undefined) {
        // `process.stdout.write` and not `console.log`: one write per line,
        // with the newline in the same write. A framed protocol on a pipe
        // cannot afford a second writer putting anything between them.
        process.stdout.write(`${JSON.stringify(reply)}\n`);
      }
    }),
  );
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
