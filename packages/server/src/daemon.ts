// The daemon: the contract, its handlers, and a websocket to reach them by.
//
// Assembled here and nowhere else. Every layer below is a tag with a fake
// somewhere, and this file is the one place that says which implementation is
// the real one — which is what keeps `handlers.test.ts` able to drive the whole
// stack over fakes without a socket.

import { homedir } from "node:os";
import { join } from "node:path";
import { erase, layer as jobsLayer } from "@awp-kit/jobs";
import { migrations as jobMigrations, layerSqlite } from "@awp-kit/jobs/sqlite";
import { layer as dbLayer } from "@awp-kit/store";
import { AwpRpcs } from "@awp-kit/protocol";
import { NodeSocketServer } from "@effect/platform-node-shared";
import { Effect, FileSystem, Layer } from "effect";
import { createWorkspace } from "./jobs/create-workspace";
import { demo } from "./jobs/demo";
import { Jj } from "./jj";
import * as intent from "./intent";
import * as jjCli from "./jj-cli";
import { Multiplexer } from "./multiplexer";
import { Threads } from "./threads";
import { RpcSerialization, RpcServer } from "effect/unstable/rpc";
import * as attachment from "./attachment";
import * as handlers from "./handlers";
import * as ptyBun from "./pty-bun";
import * as sessions from "./sessions";
import * as settings from "./settings";
import { migrations as threadMigrations, layer as threadsLayer } from "./threads";
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
export const AWP_DB = join(homedir(), ".awp", "awp.sqlite");

/**
 * One connection, migrated with every package's tables.
 *
 * One file rather than one per package, because the first real job spans them:
 * creating a workspace writes a job record *and* claims the workspace for a
 * thread, and two files means it can do one and not the other with nothing
 * afterwards able to say which. Migrations are named rather than numbered so
 * that appending to either list cannot renumber the other's — see
 * `@awp-kit/store`.
 *
 * `Layer.orDie` because a database that will not open is not a condition the
 * daemon can serve around. A jobs system with no memory is not a jobs system,
 * and a sidebar with no threads reads as having lost them; starting anyway
 * would make every enqueue a silent no-op.
 */
export const db = Layer.orDie(dbLayer(AWP_DB, [...jobMigrations, ...threadMigrations]));

/**
 * The jobs runner, over every kind the daemon knows.
 *
 * Kinds are erased at this call rather than inside the runner: a registry of
 * kinds with different inputs has no honest element type, and doing the erasure
 * where each kind's schema is still in hand keeps the cast that would otherwise
 * be needed from existing anywhere.
 */
/**
 * The kinds the daemon knows, built where its services are.
 *
 * `Layer.unwrap` because a `JobStep.run` has no requirements — a step resumed
 * by a restarted daemon has no caller whose context it could inherit — so a
 * kind that needs jj, zmx and the thread store has to *close over* them. This
 * is the one place all three exist at once.
 *
 * `demo` needs nothing and is listed beside them until it goes.
 */
export const jobs = Layer.unwrap(
  Effect.gen(function* () {
    const deps = {
      jj: yield* Jj,
      mux: yield* Multiplexer,
      threads: yield* Threads,
      files: yield* FileSystem.FileSystem,
    };
    return jobsLayer([erase(demo), erase(createWorkspace(deps))]);
  }),
).pipe(Layer.provide(Layer.orDie(layerSqlite)));

export const threads = threadsLayer;

/** The real services: real zmx, real ptys, real jj. */
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
  Layer.provide(db),
  Layer.provide(intent.layer),
  Layer.provide(settings.layer()),
  Layer.provide(jjCli.layer),
  Layer.provide(zmx.layer),
);
