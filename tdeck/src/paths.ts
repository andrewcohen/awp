// Where the daemon's socket and logs live, and how to run a second tdeck that
// cannot disturb the first.
//
// Under ~/.awp because that is where awp already keeps per-user state, and
// tdeck is a client of awp rather than a separate product with its own
// directory. One place to look when something is stuck.
//
// TDECK_INSTANCE is the whole of the isolation story, and it exists for one
// specific job: building tdeck in tdeck. Editing the transport from inside the
// instance hosting you is never safe — restarting the daemon to test a change
// kills the agent making it, mid-tool-call. Two instances is the only version
// that works, so:
//
//   TDECK_INSTANCE=dev TDECK_PORT=4319 bun src/adapterd.ts
//   TDECK_INSTANCE=dev TDECK_PORT=4319 bun src/server.ts
//
// gets a separate socket, a separate log and a separate port, and the agent
// doing the work is hosted by the untouched one.

import { homedir } from "node:os";
import { join } from "node:path";
import { mkdirSync } from "node:fs";

const instance = process.env.TDECK_INSTANCE ?? "";

// The suffix is empty for the default instance, so the ordinary paths stay the
// ordinary paths and nobody has to know this feature exists.
const suffix = instance === "" ? "" : `-${instance}`;

export const runtimeDir = join(homedir(), ".awp", `tdeck${suffix}`);

export function ensureRuntimeDir(): string {
  mkdirSync(runtimeDir, { recursive: true });
  return runtimeDir;
}

export const socketPath = process.env.TDECK_SOCKET ?? join(runtimeDir, "adapter.sock");
export const daemonLogPath = join(runtimeDir, "adapterd.log");
export const dropDir = join(runtimeDir, "drops");

export const port = Number(process.env.TDECK_PORT) || 4317;

// Named in logs so two running instances are distinguishable at a glance.
export const instanceName = instance === "" ? "default" : instance;
