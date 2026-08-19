// The smallest thing that is a chat UI over a Claude subscription.
//
// One Node process. It spawns the ACP adapter, speaks the protocol to it, and
// serves a page that shows what comes back. No Go, no Wails, no gdeck — the
// point is to find out what an ACP-driven chat actually feels like before any of
// it is built into something.
//
// What this is testing, specifically:
//   - does text stream as it is generated, rather than arriving in a lump
//   - do tool calls show up as they start, with enough detail to render
//   - does a permission request reach the UI and can the UI answer it
//   - does the agent run under the local Claude subscription with no API key
//
// Run: npm start, then open http://localhost:4317

import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { ClientSideConnection, ndJsonStream, PROTOCOL_VERSION } from "@agentclientprotocol/sdk";

const here = dirname(fileURLToPath(import.meta.url));
const PORT = 4317;

// Browsers attached to the event stream. A chat with one viewer is the normal
// case; the set exists so a reload does not lose the session.
const viewers = new Set();

function broadcast(event) {
  const line = `data: ${JSON.stringify(event)}\n\n`;
  for (const res of viewers) res.write(line);
}

// Everything the agent says, kept so a reload can rebuild the conversation.
// The adapter owns the real session; this is only what has been shown.
const shown = [];

function emit(event) {
  shown.push(event);
  broadcast(event);
}

// --- the ACP client half ----------------------------------------------------
//
// The adapter calls these. A real editor would read and write the user's files
// and put permission requests in front of them; this does the minimum that
// keeps a session honest.
class Chat {
  constructor() {
    this.pendingPermission = null;
  }

  async sessionUpdate({ update }) {
    // The protocol's update union. Translated here rather than in the browser
    // so the page stays a renderer — and so the shapes are visible in one place
    // while we are still learning them.
    switch (update.sessionUpdate) {
      case "agent_message_chunk":
        emit({ kind: "text", text: update.content?.text ?? "" });
        break;
      case "agent_thought_chunk":
        emit({ kind: "thought", text: update.content?.text ?? "" });
        break;
      case "tool_call":
        emit({
          kind: "tool",
          id: update.toolCallId,
          title: update.title ?? update.kind ?? "tool",
          status: update.status ?? "pending",
        });
        break;
      case "tool_call_update":
        emit({
          kind: "tool_update",
          id: update.toolCallId,
          status: update.status,
          // Content arrives as blocks; a diff is its own kind, which is the
          // thing a terminal cannot show and this can.
          content: update.content ?? null,
        });
        break;
      case "plan":
        emit({ kind: "plan", entries: update.entries ?? [] });
        break;
      default:
        emit({ kind: "other", update });
    }
  }

  async requestPermission({ toolCall, options }) {
    // The reason ACP is worth the adapter: the agent asks, and the UI answers.
    // Parked here as a promise until the browser posts a choice back.
    emit({
      kind: "permission",
      title: toolCall?.title ?? "run a tool",
      options: options.map((o) => ({ id: o.optionId, name: o.name, kind: o.kind })),
    });
    return new Promise((resolve) => {
      this.pendingPermission = (optionId) => {
        this.pendingPermission = null;
        emit({ kind: "permission_resolved", optionId });
        resolve({ outcome: { outcome: "selected", optionId } });
      };
    });
  }

  async readTextFile({ path }) {
    return { content: readFileSync(path, "utf8") };
  }

  async writeTextFile() {
    // Not in the POC: this chat is for asking, not for editing. Refusing is
    // more honest than silently succeeding.
    throw new Error("writes are not enabled in this experiment");
  }
}

// --- wiring -----------------------------------------------------------------

const adapter = spawn(
  join(here, "node_modules", ".bin", "claude-agent-acp"),
  [],
  { stdio: ["pipe", "pipe", "inherit"], cwd: process.cwd() },
);

const chat = new Chat();
const stream = ndJsonStream(
  // Web streams both ways: what the adapter reads, and what it writes.
  new WritableStream({ write: (chunk) => void adapter.stdin.write(chunk) }),
  new ReadableStream({
    start(controller) {
      adapter.stdout.on("data", (d) => controller.enqueue(new Uint8Array(d)));
      adapter.stdout.on("end", () => controller.close());
    },
  }),
);

const conn = new ClientSideConnection(() => chat, stream);

const init = await conn.initialize({
  protocolVersion: PROTOCOL_VERSION,
  clientCapabilities: { fs: { readTextFile: true, writeTextFile: false } },
});
console.log(`initialized: protocol ${init.protocolVersion}`);
console.log("agent capabilities:", JSON.stringify(init.agentCapabilities));

const session = await conn.newSession({ cwd: process.cwd(), mcpServers: [] });
const sessionId = session.sessionId;
console.log(`session ${sessionId}`);

// Permission policy is a session mode, not something this client invents.
//
// The adapter advertises Claude Code's own modes — default, acceptEdits, plan
// and bypassPermissions — so "auto mode" here is the same auto mode as the TUI,
// applied by the agent rather than by a client that clicks its own buttons.
// Worth the check: a hand-rolled auto-approve would answer prompts the agent
// only sends in the modes that ask, and would silently diverge from what the
// same setting means everywhere else in awp.
const modes = session.modes ?? null;
if (modes) {
  console.log(`modes: ${modes.availableModes.map((m) => m.id).join(", ")} (current ${modes.currentModeId})`);
}
console.log(`open http://localhost:${PORT}`);

let busy = false;

async function say(text) {
  if (busy) return;
  busy = true;
  emit({ kind: "user", text });
  try {
    const res = await conn.prompt({
      sessionId,
      prompt: [{ type: "text", text }],
    });
    emit({ kind: "done", stopReason: res.stopReason });
  } catch (err) {
    emit({ kind: "error", message: String(err?.message ?? err) });
  } finally {
    busy = false;
  }
}

createServer((req, res) => {
  if (req.url === "/") {
    res.writeHead(200, { "content-type": "text/html" });
    res.end(readFileSync(join(here, "index.html")));
    return;
  }

  if (req.url === "/events") {
    res.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    // Replay first, so a reload shows the conversation instead of a blank page.
    for (const event of shown) res.write(`data: ${JSON.stringify(event)}\n\n`);
    viewers.add(res);
    req.on("close", () => viewers.delete(res));
    return;
  }

  if (req.url === "/modes") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(modes ?? { availableModes: [], currentModeId: null }));
    return;
  }

  if (req.method === "POST" && (req.url === "/say" || req.url === "/permit" || req.url === "/mode")) {
    let body = "";
    req.on("data", (d) => (body += d));
    req.on("end", () => {
      const payload = JSON.parse(body || "{}");
      if (req.url === "/say") void say(payload.text ?? "");
      else if (req.url === "/mode") {
        void conn
          .setSessionMode({ sessionId, modeId: payload.modeId })
          .then(() => emit({ kind: "mode", modeId: payload.modeId }))
          .catch((err) => emit({ kind: "error", message: String(err?.message ?? err) }));
      } else chat.pendingPermission?.(payload.optionId);
      res.writeHead(204).end();
    });
    return;
  }

  res.writeHead(404).end();
}).listen(PORT);
