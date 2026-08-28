// What a particular daemon reports, read-only.
//
// For the two-instance workflow in AGENTS.md: with a branch's daemon running
// beside the one in use, "is this record missing, or is that window merely
// stale?" is a question about a specific process, and nothing else here can
// ask it of one. Both calls are questions — no attach, no write, no session
// touched — so this is safe against a daemon somebody is working in.
//
//     bun run probe:ask                      the default, ws://127.0.0.1:5274
//     bun run probe:ask ws://127.0.0.1:5284  a second instance
//
// It also demonstrates, by living here rather than in a scratch directory, the
// thing AGENTS.md warns about: run this from /tmp and `effect` resolves out of
// bun's global cache at 3.22.1, and it dies inside `fiberRefs/patch.js` before
// sending a byte. Two Effect runtimes, wearing a completely unrecognisable hat.

import { Effect } from "effect";
import * as client from "@awp-kit/protocol/client";

const url = process.argv[2] ?? client.DEFAULT_DAEMON_URL;

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;
  const threads = yield* rpc.ThreadList();
  const sessions = yield* rpc.SessionList();
  const claimed = threads.filter((thread) =>
    thread.members.some((member) => member.workspace.startsWith("pr-")),
  );

  console.log(url);
  console.log(`  threads       ${threads.length}`);
  console.log(
    `  review work   ${
      claimed
        .map(
          (thread) =>
            `${thread.members.map((member) => `${member.project}/${member.workspace}`).join(",")}`,
        )
        .join(" | ") || "none"
    }`,
  );
  // How long the inbox takes to answer, which is the cache question made
  // measurable: cold in memory but warm on disk should be milliseconds, and a
  // real `gh pr list` is seconds.
  const started = Date.now();
  const inbox = yield* rpc.InboxList({});
  console.log(
    `  inbox         ${inbox.items.length} rows from ${inbox.sources.length} project(s) in ${Date.now() - started}ms`,
  );

  // The rows a repair could act on: a workspace, and whether it still contains
  // what the pull request is. Printed because "moved" is invisible from outside
  // — the checkout looks exactly as it did.
  const checked = inbox.items.filter((item) => item.workspace !== undefined);
  console.log(
    `  checkouts     ${
      checked
        .map(
          (item) =>
            `${item.project}/${item.workspace}#${item.number} ${item.moved ? "MOVED" : "current"}`,
        )
        .join(", ") || "none"
    }`,
  );

  console.log(
    `  their sessions ${
      sessions
        .filter((session) => session.identity?.workspace?.startsWith("pr-") === true)
        .map((session) => session.name)
        .join(", ") || "none"
    }`,
  );
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
