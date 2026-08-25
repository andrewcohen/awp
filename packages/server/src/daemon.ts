// The daemon: the contract, its handlers, and a websocket to reach them by.
//
// Assembled here and nowhere else. Every layer below is a tag with a fake
// somewhere, and this file is the one place that says which implementation is
// the real one — which is what keeps `handlers.test.ts` able to drive the whole
// stack over fakes without a socket.

import { AwpRpcs } from "@awp-kit/protocol";
import { NodeSocketServer } from "@effect/platform-node-shared";
import { Layer } from "effect";
import { RpcSerialization, RpcServer } from "effect/unstable/rpc";
import * as attachment from "./attachment";
import * as handlers from "./handlers";
import * as ptyBun from "./pty-bun";
import * as sessions from "./sessions";
import * as zmx from "./zmx";

/**
 * Loopback, and not configurable yet.
 *
 * The daemon hands out ptys attached to the user's own agent sessions. Binding
 * anywhere but the loopback interface would put a shell on the network, so the
 * host is stated here rather than read from anywhere that could be set to
 * something else by accident.
 */
export const DAEMON_HOST = "127.0.0.1";
export const DAEMON_PORT = 5274;

/**
 * ndjson, deliberately, and worth revisiting once there is a measurement.
 *
 * A websocket already frames its messages, so `json` would do — but ndjson
 * frames itself as well, which means it cannot be silently wrong if the
 * transport underneath ever changes. It is also readable in a network log,
 * which is worth something while none of this has run against real zmx yet.
 *
 * `msgPack` is the faster answer and is a layer swap away. The reason not to
 * reach for it now is that nothing has been measured, and the thing to find out
 * first is whether serialization appears in the keystroke budget at all.
 */
const serialization = RpcSerialization.layerNdjson;

/** The real services: real zmx, real ptys. */
export const services = sessions.layer.pipe(
  Layer.provide(attachment.layer),
  Layer.provide(ptyBun.layer),
  Layer.provide(zmx.layer),
);

/**
 * Everything, listening.
 *
 * Launching this is what makes the daemon a daemon; until then the same
 * handlers are just a Layer that RpcTest can drive directly.
 */
export const layer = RpcServer.layer(AwpRpcs).pipe(
  Layer.provide(RpcServer.layerProtocolSocketServer),
  Layer.provide(serialization),
  Layer.provide(NodeSocketServer.layerWebSocket({ host: DAEMON_HOST, port: DAEMON_PORT })),
  Layer.provide(handlers.layer),
  Layer.provide(services),
  Layer.provide(zmx.layer),
);
