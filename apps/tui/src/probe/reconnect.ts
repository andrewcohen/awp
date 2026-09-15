#!/usr/bin/env bun
// Does a client come back when the daemon does?
//
// The socket reconnects on its own — `makeProtocolSocket` retries its loop —
// so the question is never about the socket. It is about what was built on
// it: a feed has to resubscribe and a *call* has to be asked again.
//
// ── and the first of those is why this file gained a second half ─────────
//
// It used to measure only the edges and the re-ask, and it passed for as long
// as it has existed while **every feed in both faces was dead after a
// restart**. A dropped connection does not reach a feed as a failure — the
// client writes an `RpcClientError` into each in-flight request, and a feed
// whose contract declares no error has nowhere to put one, so it arrives as
// a defect and `Effect.retry` steps over it. What that looked like from
// outside: a window reporting the daemon as fine, with lists correct once
// and nothing ever changing again.
//
// So the feed is watched here, and a push after the restart is the assertion.
// `watchFacts` is the one to watch because its first push is the whole table:
// a resubscribe produces one immediately, where a feed of genuine edges could
// answer "no push" by there being nothing to say.
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

import { onConnection, onReconnect, threads, watchFacts } from "../daemon";

/**
 * 5284 by default, and movable — because the refusal below has to have an
 * answer. A daemon somebody forgot about can hold the second-instance port
 * for days, and "this probe cannot run today" is not a useful place to stop.
 */
const PORT = Number(process.env["AWP_PROBE_PORT"] ?? 5284);

if (process.env["AWP_DAEMON_URL"] !== `ws://127.0.0.1:${String(PORT)}`) {
  console.error(`run me as: AWP_DAEMON_URL=ws://127.0.0.1:${String(PORT)} bun ${process.argv[1]}`);
  console.error("— the module reads the url once, at import, and this is not 5274 by accident");
  process.exit(1);
}

/**
 * The daemon's entry point, and deliberately not the `daemon` script.
 *
 * `bun run daemon` is three processes — the script runner, a filter shim and
 * the daemon itself — and `Bun.spawn(...).kill()` reaches only the first:
 *
 *   76507  bun run daemon          ← killed
 *   76508    bun run --filter …
 *   76509      bun run src/main.ts ← still listening, still answering
 *
 * So the outage this probe is built around never happened. It killed a
 * wrapper, the connection never dropped, and every check below passed on the
 * *first* connection. Spawning the entry point is one process, and one
 * process is one `kill`.
 */
const daemon = (): ReturnType<typeof Bun.spawn> =>
  Bun.spawn(["bun", "run", "src/main.ts"], {
    cwd: new URL("../../../../packages/server", import.meta.url).pathname,
    // The daemon spawns `zmx attach`, so the marker goes out neutralised —
    // set, never omitted, which is the rule this repo learned the hard way.
    env: { ...process.env, AWP_DAEMON_PORT: String(PORT), ZMX_SESSION: "" },
    stdout: "ignore",
    stderr: "ignore",
  });

/**
 * Refuse to run against a daemon this probe did not start.
 *
 * A squatter on the port is not a theory: a daemon left over from six days
 * earlier held 5284 on this machine, so every run bound nothing, talked to a
 * stranger, and reported a pass. The kill below would then be killing a
 * process that was never listening — which is the same failure as the wrapper
 * above, arriving by a different route.
 */
const answering = async (): Promise<boolean> =>
  await new Promise((resolve) => {
    const socket = new WebSocket(`ws://127.0.0.1:${String(PORT)}`);
    const settle = (open: boolean) => {
      socket.close();
      resolve(open);
    };
    socket.addEventListener("open", () => settle(true));
    socket.addEventListener("error", () => settle(false));
    setTimeout(() => settle(false), 1500);
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

/** What a feed-shaped caller does: subscribe once, and never think about it. */
let pushes = 0;
const stopWatching = watchFacts(() => {
  pushes += 1;
});

if (await answering()) {
  console.error(`something is already listening on ${String(PORT)}.`);
  console.error("— this probe kills what it starts, so it will not start beside a stranger");
  process.exit(1);
}

let one = daemon();
await wait(2500);
ask();
await wait(1500);
console.log(`  first        ${last}, after ${String(asks)} ask(s)`);
console.log(`  feed         ${String(pushes)} push(es)`);
const before = pushes;

const edgesBefore = edges.length;
one.kill();
await one.exited;
await wait(1500);
console.log(`  daemon down  edges ${edges.join(" · ")}`);
if (edges.length === edgesBefore) {
  // The outage is the experiment. Without it every line below reports on the
  // first connection, which is how this file passed while both faces lost
  // every feed on a restart.
  console.error("  NOTHING DROPPED — the daemon that was killed was not the one being talked to");
  process.exit(1);
}

one = daemon();
// The socket backs off exponentially to one attempt every 5s, so this waits
// longer than a person would: what is being measured is that it happens at
// all, not how quickly.
await wait(9000);
console.log(`  daemon up    edges ${edges.join(" · ")}`);
console.log(`  re-asked     ${asks > 1 ? "yes" : "NO"} (${String(asks)} ask(s))`);
console.log(`  answer       ${last}`);
console.log(
  `  resubscribed ${pushes > before ? "yes" : "NO"} (${String(pushes - before)} push(es) since)`,
);

stopWatching();
one.kill();
await one.exited;
process.exit(0);
