// Does the socket carry the contract?
//
// Everything up to this point has been tested with the transport taken out —
// RpcTest connects a client straight to the handlers, which is the right way to
// test a translation and no way at all to test a wire. This asks the only
// question that leaves: whether a real client, over a real websocket, gets the
// real daemon's answer back in the right shape.
//
// Safe from inside a zmx session, unlike its neighbours in this directory. It
// calls SessionList and nothing else, which is `zmx ls` — a question that costs
// nothing and touches no session. It deliberately does not attach.

import { Effect, Layer, Result } from "effect";
import * as client from "@awp-kit/protocol/client";
import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { layer as daemonLayer, DAEMON_HOST, DAEMON_PORT } from "../daemon";
import { identity, isLive } from "../multiplexer";

const url = `ws://${DAEMON_HOST}:${DAEMON_PORT}`;

const daemon = daemonLayer.pipe(
  Layer.provide(NodeChildProcessSpawner.layer),
  Layer.provide(NodeFileSystem.layer),
  Layer.provide(NodePath.layer),
);

const program = Effect.gen(function* () {
  const rpc = yield* client.make;

  const started = Date.now();
  const sessions = yield* rpc.SessionList();
  const elapsed = Date.now() - started;

  console.log(`\n  ${sessions.length} sessions over the wire in ${elapsed}ms\n`);

  for (const session of sessions.slice(0, 12)) {
    const mine = identity({ ...session, created: session.created });
    console.log(
      `  ${isLive(session) ? "live" : "dead"}  ${session.name.padEnd(48)}` +
        `  clients=${session.clients}  ${mine?.kind ?? "-"}`,
    );
  }

  // The two things a shape-only test cannot see. A Date that arrived as a
  // string would still be truthy, and labels that arrived as "[object Object]"
  // would still have a length.
  const dated = sessions.find((s) => s.created !== undefined);
  console.log(
    `\n  created is a Date:  ${dated?.created instanceof Date}` +
      `  (${dated?.created?.toISOString() ?? "no session reported one"})`,
  );
  const labelled = sessions.find((s) => Object.keys(s.labels).length > 0);
  console.log(`  labels survive:     ${JSON.stringify(labelled?.labels ?? {})}\n`);
}).pipe(Effect.provide(client.layer(url)), Effect.provide(daemon), Effect.scoped);

const result = await Effect.runPromise(Effect.result(program));
if (Result.isFailure(result)) {
  console.error("\n  round trip failed:", result.failure, "\n");
  process.exit(1);
}
process.exit(0);
