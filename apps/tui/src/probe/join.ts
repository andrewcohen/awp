#!/usr/bin/env bun
// Join a conversation the daemon already holds, and say what came down.
//
// Read only: it opens the stream, listens, and closes. `ChatOpen` adds a
// reference to an adapter that is already held, so nothing about this starts,
// stops or steers an agent — the one call that would is `ChatSend`, and it is
// deliberately not here.

import type { ChatUpdate } from "@awp-kit/protocol";
import { onConnection, url, watchChat } from "../daemon";

const project = process.argv[2] ?? "awp";
const workspace = process.argv[3] ?? "opentui-terminal-poc";
const seconds = Number(process.argv[4] ?? 6);

const seen: ChatUpdate[] = [];
let connected = false;

onConnection((state) => {
  connected = state;
});

console.log(`\n  daemon      ${url}`);
console.log(`  workspace   ${project}/${workspace}`);

const stop = watchChat(project, workspace, (update) => seen.push(update));
await new Promise<void>((resolve) => {
  setTimeout(resolve, seconds * 1000);
});
stop();

const kinds = new Map<string, number>();
for (const update of seen) kinds.set(update.kind, (kinds.get(update.kind) ?? 0) + 1);

const text = (role: string) =>
  seen
    .filter((update) => update.kind === "message" && update.role === role)
    .map((update) => update.text ?? "")
    .join("");

console.log(`  connected   ${connected}`);
console.log(`  updates     ${seen.length} in ${seconds}s`);
console.log(`  kinds       ${JSON.stringify(Object.fromEntries(kinds))}`);
console.log(`  agent said  ${JSON.stringify(text("agent").trim().slice(-160))}`);
console.log(`  user said   ${JSON.stringify(text("user").trim().slice(-160))}`);
console.log(
  `  tools       ${new Set(seen.filter((u) => u.kind === "tool").map((u) => u.id)).size} distinct`,
);
console.log(`  asks        ${seen.filter((u) => u.kind === "permission").length}\n`);
process.exit(0);
