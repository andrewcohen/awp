// Where the daemon's socket and the state that survives a restart live.
//
// Under ~/.awp because that is where awp already keeps its per-user state, and
// tdeck is a client of awp rather than a separate product with its own
// directory. One place to look when something is stuck.

import { homedir } from "node:os";
import { join } from "node:path";
import { mkdirSync } from "node:fs";

export const runtimeDir = join(homedir(), ".awp", "tdeck");

export function ensureRuntimeDir(): string {
  mkdirSync(runtimeDir, { recursive: true });
  return runtimeDir;
}

export const socketPath = join(runtimeDir, "adapter.sock");
export const statePath = join(runtimeDir, "state.json");
export const daemonLogPath = join(runtimeDir, "adapterd.log");
