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

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { spawn } from "node:child_process";

const AWP_BIN = process.env.AWP_BIN || "awp";

function report(state: "working" | "idle" | "waiting" | "exited"): void {
  if (!process.env.TMUX) return;
  try {
    const child = spawn(AWP_BIN, ["internal", "report-status", "--state", state], {
      stdio: "ignore",
      detached: true,
    });
    child.unref();
    child.on("error", () => {
      // swallow: awp may not be on PATH; never break a turn.
    });
  } catch {
    // swallow
  }
}

export default function (pi: ExtensionAPI) {
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
