// Can a session started by the terminal be loaded by an ACP client?
//
// If yes, the chat is not a separate agent — it is another window onto the same
// conversation, and the "one writer" objection that shaped this whole design
// gets much narrower.
import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ClientSideConnection, ndJsonStream, PROTOCOL_VERSION } from "@agentclientprotocol/sdk";

const here = dirname(fileURLToPath(import.meta.url));
const [, , sessionId, cwd] = process.argv;

const adapter = spawn(join(here, "node_modules", ".bin", "claude-agent-acp"), [], {
  stdio: ["pipe", "pipe", "inherit"],
});
const stream = ndJsonStream(
  new WritableStream({ write: (c) => void adapter.stdin.write(c) }),
  new ReadableStream({
    start(ctrl) {
      adapter.stdout.on("data", (d) => ctrl.enqueue(new Uint8Array(d)));
      adapter.stdout.on("end", () => ctrl.close());
    },
  }),
);

let replayed = 0;
const client = {
  async sessionUpdate({ update }) {
    replayed++;
    if (replayed <= 6) {
      const text = update.content?.text ?? update.title ?? update.sessionUpdate;
      console.log(`  replay ${replayed}: ${update.sessionUpdate} — ${String(text).slice(0, 90)}`);
    }
  },
  async requestPermission() { return { outcome: { outcome: "cancelled" } }; },
  async readTextFile() { return { content: "" }; },
  async writeTextFile() { return {}; },
};

const conn = new ClientSideConnection(() => client, stream);
await conn.initialize({ protocolVersion: PROTOCOL_VERSION, clientCapabilities: { fs: {} } });

try {
  const res = await conn.loadSession({ sessionId, cwd, mcpServers: [] });
  console.log(`\nloaded. ${replayed} updates replayed. modes:`, JSON.stringify(res?.modes?.currentModeId ?? null));
} catch (err) {
  console.log("\nloadSession failed:", err?.message ?? err);
}
process.exit(0);
