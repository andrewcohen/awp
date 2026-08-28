// One-off: link the pull requests that were only ever readable off a name.
//
// Every review workspace made before `thread_prs` existed says which PR it is
// about only in its own name — `pr-2418`, or the Go implementation's
// `pr-<n>-<branch>`. The inbox still finds those by parsing the name, so
// nothing is broken; what is missing is the *record*, and a record half of the
// data has is worse than one none of it has.
//
// Idempotent — `link` is an upsert — and it writes nothing for a workspace
// whose name does not parse. Run against whichever daemon holds the store:
//
//     bun packages/server/src/probe/link-prs.ts ws://127.0.0.1:5284

import { Effect } from "effect";
import * as client from "@awp-kit/protocol/client";
import { reviewNumber } from "../inbox";

const url = process.argv[2] ?? client.DEFAULT_DAEMON_URL;

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;
  const threads = yield* rpc.ThreadList();

  let linked = 0;
  for (const thread of threads) {
    for (const member of thread.members) {
      const number = reviewNumber(member.workspace);
      if (number === undefined) {
        continue;
      }
      const already = thread.prs.some(
        (pr) => pr.project === member.project && pr.number === number,
      );
      if (already) {
        console.log(`  already  ${member.project}#${number}  ${thread.title}`);
        continue;
      }
      yield* rpc.ThreadLinkPr({ thread: thread.id, pr: { project: member.project, number } });
      linked += 1;
      console.log(`  linked   ${member.project}#${number}  ${thread.title}`);
    }
  }
  console.log(`${linked} linked`);
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
