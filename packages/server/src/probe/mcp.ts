// Whether an MCP client can actually talk to the server, asked of a real one.
//
//     bun run probe:mcp                                the default daemon
//     bun run probe:mcp ws://127.0.0.1:5284 <dir>      a second instance
//
// ── what mcp.test.ts structurally cannot say ───────────────────────────────
//
// The dispatch is a pure function and its tests cover every sentence, every
// refusal and the shape of the tool list. Three things are outside them, and
// each has bitten this repo in another guise:
//
//   the framing        one JSON object per line, in and out, through a pipe.
//                      A reply written with a second writer between the object
//                      and its newline is a protocol nobody can parse
//   the wire encoding  `thread: undefined` crosses a real socket here, not
//                      RpcTest's in-memory one. JSON has no undefined, and
//                      this repo has already lost a day to a schema that
//                      required a key JSON drops — see the note on the jobs
//                      store
//   the binding        the server takes its scope from `process.cwd()`, and
//                      the only way to know it took the right one is to spawn
//                      it somewhere and read what it says
//
// Read-only apart from the last check, which files a review comment — so it
// files into a workspace this repository made and refuses anything else.

import { homedir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import { AwpClient, DEFAULT_DAEMON_URL, layerClient } from "@awp-kit/protocol/client";
import { mcpEntry } from "../mcp";

const url = process.argv[2] ?? DEFAULT_DAEMON_URL;
const cwd = process.argv[3] ?? join(homedir(), ".awp", "workspaces", "awp", "awp-kit-amoeba");

/** Whether a directory is one of ours, for the one check that writes. */
const ours = (dir: string): boolean => dir.startsWith(join(homedir(), ".awp", "workspaces", "awp"));

const child = Bun.spawn([process.execPath, "run", mcpEntry()], {
  cwd,
  env: { ...process.env, AWP_DAEMON_URL: url },
  stdin: "pipe",
  stdout: "pipe",
  stderr: "pipe",
});

const lines = (async function* () {
  const decoder = new TextDecoder();
  let held = "";
  for await (const chunk of child.stdout) {
    held += decoder.decode(chunk, { stream: true });
    const parts = held.split("\n");
    held = parts.pop() ?? "";
    for (const part of parts) {
      if (part.trim() !== "") {
        yield JSON.parse(part) as Record<string, unknown>;
      }
    }
  }
})();

let next = 0;

/**
 * One request, and the reply to it. Awaited, so ordering is not in question.
 *
 * **`await` the flush.** Bun's writable end buffers, and a `flush()` whose
 * promise is dropped can leave the line unsent while this waits for an answer
 * to it — which presents as a server that never replies, and is the first
 * thing this probe did. The server was answering the whole time; nothing had
 * reached it.
 *
 * The timeout is here for the same reason: a probe that hangs says nothing at
 * all, and "nothing at all" is the one result that cannot be read.
 */
const ask = async (method: string, params?: unknown): Promise<Record<string, unknown>> => {
  next += 1;
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: next, method, params })}\n`);
  await child.stdin.flush();
  const reply = await Promise.race([
    lines.next(),
    new Promise<never>((_, reject) =>
      setTimeout(() => reject(new Error(`no reply to ${method} in 10s`)), 10_000),
    ),
  ]);
  if (reply.done === true) {
    throw new Error(
      `the server closed its output; stderr: ${await new Response(child.stderr).text()}`,
    );
  }
  return reply.value;
};

/** A tool call, reduced to the two things worth printing. */
const tool = async (
  name: string,
  args: Record<string, unknown> = {},
): Promise<{ readonly failed: boolean; readonly text: string }> => {
  const reply = await ask("tools/call", { name, arguments: args });
  const result = reply["result"] as
    | { readonly content?: ReadonlyArray<{ readonly text?: string }>; readonly isError?: boolean }
    | undefined;
  return { failed: result?.isError === true, text: result?.content?.[0]?.text ?? "" };
};

console.log(`  daemon        ${url}`);
console.log(`  cwd           ${cwd}`);

const hello = await ask("initialize", {
  protocolVersion: "2025-06-18",
  capabilities: {},
  clientInfo: { name: "awp-probe", version: "0" },
});
const info = hello["result"] as Record<string, unknown> | undefined;
console.log(
  `  initialize    ${info === undefined ? "NO RESULT" : `${JSON.stringify(info["serverInfo"])} ${JSON.stringify(info["capabilities"])}`}`,
);

// A notification, which must be answered with NOTHING. Sent between two
// requests so that a stray reply to it would be read as the answer to the next
// one — which is exactly how it would break a real client, and is invisible if
// it is the last thing sent.
child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" })}\n`);
await child.stdin.flush();

const listed = await ask("tools/list");
const tools = (listed["result"] as { readonly tools?: ReadonlyArray<{ readonly name: string }> })
  ?.tools;
console.log(
  `  tools/list    ${tools === undefined ? "NO RESULT — the notification was answered" : tools.map((one) => one.name).join(", ")}`,
);

const thread = await tool("awp_thread");
console.log(
  `  awp_thread    ${thread.failed ? "REFUSED " : ""}${thread.text.replaceAll("\n", "\n                ")}`,
);

// ── the task board, which is the reason the store exists ───────────────────
//
// Read against the real daemon, because the one thing a test cannot say is
// whether anything actually ingested: `tasks.test.ts` proves ingest over a
// temp file, and a board that comes back empty here means the sweep never
// ran — which looks exactly like a project with no TODO.md.
const tasks = await tool("awp_tasks");
const first = tasks.text.split("\n").slice(0, 4).join("\n                ");
console.log(`  awp_tasks     ${tasks.failed ? "REFUSED " : ""}${first}`);

// And one in full, by the id the listing just gave. The value of a task here
// is its argument rather than its subject, so a listing that cannot be
// followed is half a feature.
const id = /^\s+(\S+)/mu.exec(tasks.text.split("\n").slice(1).join("\n"))?.[1];
if (id !== undefined) {
  const one = await tool("awp_task", { id });
  console.log(
    `  awp_task      ${one.failed ? "REFUSED " : ""}${one.text.slice(0, 120).replaceAll("\n", " · ")}`,
  );
}

const comments = await tool("awp_review_comments");
console.log(
  `  comments      ${comments.failed ? "REFUSED " : ""}${comments.text.replaceAll("\n", "\n                ")}`,
);

// ── the binding, from the other side ──────────────────────────────────────
//
// A server started outside the workspaces root must refuse rather than resolve
// to something: the Go implementation's seven findings went into the wrong
// review because nothing did.
//
// `homedir()` and **not** `process.cwd()`, which was the first version and
// reported NOT REFUSED — correctly, because this repository is itself checked
// out at `~/.awp/workspaces/awp/awp-kit-amoeba`, so the probe's own directory
// is a workspace. A check that cannot fail is worse than no check: it reads as
// a pass.
const stray = Bun.spawn([process.execPath, "run", mcpEntry()], {
  cwd: homedir(),
  env: { ...process.env, AWP_DAEMON_URL: url },
  stdin: "pipe",
  stdout: "pipe",
  stderr: "pipe",
});
stray.stdin.write(
  `${JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "awp_thread", arguments: {} } })}\n`,
);
await stray.stdin.flush();
stray.stdin.end();
const strayReply = JSON.parse((await new Response(stray.stdout).text()).split("\n")[0] ?? "{}") as {
  readonly result?: {
    readonly content?: ReadonlyArray<{ readonly text?: string }>;
    readonly isError?: boolean;
  };
};
console.log(
  `  outside       ${strayReply.result?.isError === true ? "refused: " : "NOT REFUSED — "}${strayReply.result?.content?.[0]?.text ?? ""}`,
);
stray.kill();

if (ours(cwd)) {
  const body = "probe:mcp wrote this, and removes it again";
  const filed = await tool("awp_file_finding", {
    path: "AGENTS.md",
    line: 1,
    body,
    kind: "comment",
  });
  console.log(`  file_finding  ${filed.failed ? "REFUSED " : ""}${filed.text}`);
  const after = await tool("awp_review_comments");
  console.log(
    `  round trip    ${after.text.includes(body) ? "the finding came back" : "NOT VISIBLE"}`,
  );

  // ── and taken back out again ────────────────────────────────────────────
  //
  // The diff panel this files into is a person's real review, so a probe that
  // left its own remarks there would be a probe nobody runs twice. Removed
  // over the rpc rather than through a tool, because there deliberately is no
  // removal tool: an agent that could delete review comments could delete the
  // ones somebody left for it.
  const removed = await Effect.runPromise(
    Effect.scoped(
      Effect.gen(function* () {
        const rpc = yield* AwpClient;
        const found = yield* rpc.ReviewAt({ from: cwd });
        const mine = found.comments.filter((one) => one.body === body);
        for (const one of mine) {
          yield* rpc.ReviewRemove({ comment: one.id });
        }
        return mine.length;
      }),
    ).pipe(Effect.provide(layerClient(url))),
  );
  console.log(`  tidied        removed ${removed}`);
} else {
  console.log(`  file_finding  skipped — ${cwd} is not a workspace this repo made`);
}

child.kill();
