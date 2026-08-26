// The daemon: the contract, its handlers, and a websocket to reach them by.
//
// Assembled here and nowhere else. Every layer below is a tag with a fake
// somewhere, and this file is the one place that says which implementation is
// the real one — which is what keeps `handlers.test.ts` able to drive the whole
// stack over fakes without a socket.

import { homedir } from "node:os";
import { join } from "node:path";
import { erase, layer as jobsLayer } from "@awp-kit/jobs";
import { layerSqlite } from "@awp-kit/jobs/sqlite";
import { AwpRpcs } from "@awp-kit/protocol";
import { NodeSocketServer } from "@effect/platform-node-shared";
import { Layer } from "effect";
import { demo } from "./jobs/demo";
import { RpcSerialization, RpcServer } from "effect/unstable/rpc";
import * as attachment from "./attachment";
import * as handlers from "./handlers";
import * as ptyBun from "./pty-bun";
import * as sessions from "./sessions";
import { layer as threadsLayer } from "./threads";
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

/**
 * Where jobs are kept between runs.
 *
 * Outside the repository and outside any workspace, because a job belongs to
 * the machine rather than to a checkout: the daemon that resumes it after a
 * restart is not necessarily standing in the directory that enqueued it.
 */
export const JOBS_DB = join(homedir(), ".awp", "jobs.sqlite");

/**
 * The jobs runner, over every kind the daemon knows.
 *
 * Kinds are erased at this call rather than inside the runner: a registry of
 * kinds with different inputs has no honest element type, and doing the erasure
 * where each kind's schema is still in hand keeps the cast that would otherwise
 * be needed from existing anywhere.
 *
 * `Layer.orDie` because a store that will not open is not a condition the
 * daemon can serve around — a jobs system with no memory is not a jobs system,
 * and starting anyway would make every enqueue a silent no-op.
 */
export const jobs = jobsLayer([erase(demo)]).pipe(Layer.provide(Layer.orDie(layerSqlite(JOBS_DB))));

/**
 * Where threads are written down.
 *
 * Beside the jobs database and not inside it, and JSON rather than sqlite. A
 * thread is a dozen records written when a person types a title — none of what
 * sqlite was bought for applies, and a file that can be opened in an editor is
 * worth more while the shape of a thread is still being argued about. See
 * `threads.ts`.
 */
export const THREADS_FILE = join(homedir(), ".awp", "threads.json");

/**
 * `Layer.orDie` for the same reason the jobs store gets it: a daemon that
 * cannot read its threads has no sidebar to draw, and starting anyway would
 * report an empty list — which reads as having lost them.
 */
export const threads = Layer.orDie(threadsLayer(THREADS_FILE));

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
  Layer.provide(jobs),
  Layer.provide(threads),
  Layer.provide(zmx.layer),
);
