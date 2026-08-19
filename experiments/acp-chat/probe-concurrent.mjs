// Does one adapter host many sessions at once, or is it one at a time?
//
// The sidebar in tdeck's phase 1 assumes a single ACP connection can carry N
// concurrent conversations — click session B while A is still working, and both
// keep streaming. If instead prompt() on A blocks B until A finishes, the whole
// shape is wrong: it would mean an adapter per chat, N node processes, and the
// "import, not a subprocess" argument for ACP loses most of its force.
//
// Cheap to find out. Two sessions, both prompted within a few ms of each other,
// and the measurement is whether their update streams interleave.
//
//   interleaves > 0  -> genuinely concurrent
//   interleaves == 0 -> serialised; A ran to completion before B started
//
// Run: node probe-concurrent.mjs
import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ClientSideConnection, ndJsonStream, PROTOCOL_VERSION } from "@agentclientprotocol/sdk";

const here = dirname(fileURLToPath(import.meta.url));

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

const t0 = Date.now();
const at = () => String(Date.now() - t0).padStart(5);

// One record per update, in arrival order. Which session it belonged to is the
// only field that matters; the rest is for reading the log by eye.
const timeline = [];

const client = {
  async sessionUpdate({ sessionId, update }) {
    timeline.push({ ms: Date.now() - t0, sessionId, kind: update.sessionUpdate });
  },
  async requestPermission() { return { outcome: { outcome: "cancelled" } }; },
  async readTextFile() { return { content: "" }; },
  async writeTextFile() { return {}; },
};

const conn = new ClientSideConnection(() => client, stream);
await conn.initialize({ protocolVersion: PROTOCOL_VERSION, clientCapabilities: { fs: {} } });

const a = await conn.newSession({ cwd: process.cwd(), mcpServers: [] });
const b = await conn.newSession({ cwd: process.cwd(), mcpServers: [] });
console.log(`A ${a.sessionId}`);
console.log(`B ${b.sessionId}`);

const label = (id) => (id === a.sessionId ? "A" : id === b.sessionId ? "B" : "?");

// Long enough to overlap if overlapping is possible, and no tools, so nothing
// but generation speed decides the timing.
const ask = (n) =>
  `Count from 1 to 30. Put each number on its own line with a one-word association. No preamble.`;

console.log(`\n${at()}ms  prompting both`);
const results = await Promise.allSettled([
  conn.prompt({ sessionId: a.sessionId, prompt: [{ type: "text", text: ask(1) }] })
    .then((r) => (console.log(`${at()}ms  A done (${r.stopReason})`), r)),
  conn.prompt({ sessionId: b.sessionId, prompt: [{ type: "text", text: ask(2) }] })
    .then((r) => (console.log(`${at()}ms  B done (${r.stopReason})`), r)),
]);

for (const [i, r] of results.entries()) {
  if (r.status === "rejected") console.log(`  ${i ? "B" : "A"} rejected: ${r.reason?.message ?? r.reason}`);
}

// An interleave is any point where consecutive updates came from different
// sessions. Serialised execution produces exactly one such point — the handover
// — so the bar is "more than one".
let interleaves = 0;
for (let i = 1; i < timeline.length; i++) {
  if (timeline[i].sessionId !== timeline[i - 1].sessionId) interleaves++;
}

const first = {}, last = {};
for (const e of timeline) {
  const k = label(e.sessionId);
  first[k] ??= e.ms;
  last[k] = e.ms;
}

console.log(`\nupdates: ${timeline.length}   interleaves: ${interleaves}`);
for (const k of ["A", "B"]) {
  if (first[k] === undefined) { console.log(`${k}: no updates`); continue; }
  console.log(`${k}: first ${first[k]}ms  last ${last[k]}ms`);
}
if (first.A !== undefined && first.B !== undefined) {
  const overlap = Math.min(last.A, last.B) - Math.max(first.A, first.B);
  console.log(`overlap: ${overlap}ms  ->  ${overlap > 0 && interleaves > 1 ? "CONCURRENT" : "SERIALISED"}`);
}

// First 40 arrivals, so the shape is visible and not just the verdict.
console.log("\narrival order (first 40): " + timeline.slice(0, 40).map((e) => label(e.sessionId)).join(""));

process.exit(0);
