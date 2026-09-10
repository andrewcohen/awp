#!/usr/bin/env bun
// Does a client come back when the daemon does?
//
// The socket reconnects on its own — `makeProtocolSocket` retries its loop —
// so the question is never about the socket. It is about what was built on
// it: a feed has to resubscribe and a *call* has to be asked again, and only
// the second needs anybody's help.
//
// ── what makes this safe to run ─────────────────────────────────────────
//
// It starts a daemon of its own on 5284 and kills only that: the pid is one
// this process spawned, so there is no name to get wrong and no session to
// touch. The daemon somebody is working in, on 5274, is never addressed. The
// two calls it makes are questions.
//
// The store is shared, which is the one thing to know before running it —
// check for jobs in flight first, as AGENTS.md says:
//
//   sqlite3 ~/.awp/awp.sqlite \
//     "select count(*) from jobs where status in ('queued','running')"

import { onConnection, onReconnect, threads } from "../daemon";

const PORT = 5284;

if (process.env["AWP_DAEMON_URL"] !== `ws://127.0.0.1:${String(PORT)}`) {
  console.error(`run me as: AWP_DAEMON_URL=ws://127.0.0.1:${String(PORT)} bun ${process.argv[1]}`);
  console.error("— the module reads the url once, at import, and this is not 5274 by accident");
  process.exit(1);
}

const daemon = (): ReturnType<typeof Bun.spawn> =>
  Bun.spawn(["bun", "run", "daemon"], {
    cwd: new URL("../../../..", import.meta.url).pathname,
    // The daemon spawns `zmx attach`, so the marker goes out neutralised —
    // set, never omitted, which is the rule this repo learned the hard way.
    env: { ...process.env, AWP_DAEMON_PORT: String(PORT), ZMX_SESSION: "" },
    stdout: "ignore",
    stderr: "ignore",
  });

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Every connection edge, in order, so the reconnect is visible as an edge. */
const edges: string[] = [];
onConnection((state) => edges.push(state ? "up" : "down"));

/** What a list-shaped caller does: ask now, and again when the daemon is back. */
let asks = 0;
let last = "not asked";
const ask = () => {
  asks += 1;
  threads().then(
    (all) => {
      last = `${String(all.length)} threads`;
    },
    (error: unknown) => {
      last = `refused: ${String(error)}`;
    },
  );
};
onReconnect(ask);

let one = daemon();
await wait(2500);
ask();
await wait(1500);
console.log(`  first        ${last}, after ${String(asks)} ask(s)`);

one.kill();
await one.exited;
await wait(1000);
console.log(`  daemon down  edges ${edges.join(" · ")}`);

one = daemon();
// The socket backs off exponentially to one attempt every 5s, so this waits
// longer than a person would: what is being measured is that it happens at
// all, not how quickly.
await wait(9000);
console.log(`  daemon up    edges ${edges.join(" · ")}`);
console.log(`  re-asked     ${asks > 1 ? "yes" : "NO"} (${String(asks)} ask(s))`);
console.log(`  answer       ${last}`);

one.kill();
await one.exited;
process.exit(0);
