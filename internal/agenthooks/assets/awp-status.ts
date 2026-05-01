// awp-status: reports pi.dev agent state to awp via `awp internal report-status`.
//
// Installed globally by `awp init hooks` at ~/.pi/agent/extensions/awp-status.ts.
// Gates on $TMUX so it's a no-op outside tmux. awp itself falls back to
// reading session env from tmux when its own env is missing, so this works
// for processes that predate env injection.
//
// State mapping:
//   before_agent_start, tool_execution_start -> working
//   agent_end                                 -> idle
//   session_shutdown                          -> exited
//
// awp dedupes consecutive identical writes, so emitting on every tool start
// is safe.
//
// Set AWP_DEBUG=1 to append diagnostics to ~/.awp/pi-extension.log.

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { spawnSync } from "child_process";
import { appendFileSync, mkdirSync } from "fs";
import { homedir } from "os";
import { dirname, join } from "path";

const AWP_BIN = process.env.AWP_BIN || "awp";
const DEBUG = !!process.env.AWP_DEBUG;
const LOG_PATH = join(homedir(), ".awp", "pi-extension.log");

function log(msg: string): void {
  if (!DEBUG) return;
  try {
    mkdirSync(dirname(LOG_PATH), { recursive: true });
    appendFileSync(LOG_PATH, `${new Date().toISOString()} ${msg}\n`);
  } catch {
    // never throw out of the log path
  }
}

function report(state: "working" | "idle" | "waiting" | "exited"): void {
  if (!process.env.TMUX) {
    log(`skip ${state}: TMUX unset`);
    return;
  }
  try {
    const r = spawnSync(AWP_BIN, ["internal", "report-status", "--state", state], {
      stdio: "ignore",
      timeout: 2000,
    });
    if (r.error) {
      log(`report ${state}: spawn error ${(r.error as Error).message}`);
    } else {
      log(`report ${state}: status=${r.status}`);
    }
  } catch (e) {
    log(`report ${state}: threw ${(e as Error)?.message ?? String(e)}`);
  }
}

export default function (pi: ExtensionAPI) {
  log(`loaded (awp_bin=${AWP_BIN}, tmux=${process.env.TMUX ? "yes" : "no"})`);
  pi.on("before_agent_start", async () => {
    report("working");
  });
  pi.on("agent_end", async () => {
    report("idle");
  });
  pi.on("tool_execution_start", async () => {
    report("working");
  });
  pi.on("session_shutdown", async () => {
    report("exited");
  });
}
