// awp-status: reports pi.dev agent state to awp via `awp internal report-status`.
//
// Installed globally by `awp init hooks` at ~/.pi/agent/extensions/awp-status.ts.
// Gates on $TMUX so it's a no-op outside tmux. awp itself falls back to
// reading session env from tmux when its own env is missing, so this works
// for processes that predate env injection.
//
// State mapping:
//   session_start                             -> idle
//   before_agent_start, tool_execution_start -> working
//   agent_end                                 -> idle
//   session_shutdown(reason=quit)             -> exited
//
// awp dedupes consecutive identical writes, so emitting on every tool start
// is safe. Non-quit session_shutdown events are ignored because pi also emits
// them for reload/new/resume/fork flows where the process remains alive.
//
// Set AWP_DEBUG=1 to append diagnostics to ~/.awp/pi-extension.log.

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { spawn } from "child_process";
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

function report(
  state: "working" | "idle" | "waiting" | "exited",
  wait = false,
  prompt?: string,
): Promise<void> {
  if (!process.env.TMUX) {
    log(`skip ${state}: TMUX unset`);
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    try {
      const args = ["internal", "report-status", "--state", state];
      if (prompt && prompt.trim() !== "") {
        args.push("--prompt", prompt);
      }
      const child = spawn(AWP_BIN, args, {
        stdio: "ignore",
      });
      let done = false;
      let timer: ReturnType<typeof setTimeout>;
      const finish = (msg: string) => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        log(msg);
        resolve();
      };
      timer = setTimeout(() => {
        finish(`report ${state}: timeout`);
        child.kill();
      }, 2000);
      if (!wait) (timer as { unref?: () => void }).unref?.();

      child.on("error", (e) => finish(`report ${state}: spawn error ${e.message}`));
      child.on("exit", (code, signal) =>
        finish(`report ${state}: status=${code}${signal ? ` signal=${signal}` : ""}`),
      );
      if (!wait) child.unref();
    } catch (e) {
      log(`report ${state}: threw ${(e as Error)?.message ?? String(e)}`);
      resolve();
    }
  });
}

export default function (pi: ExtensionAPI) {
  log(`loaded (awp_bin=${AWP_BIN}, tmux=${process.env.TMUX ? "yes" : "no"})`);
  pi.on("session_start", async () => {
    void report("idle");
  });
  pi.on("before_agent_start", async (event) => {
    // before_agent_start fires once per user turn with the raw prompt text,
    // so this is the natural place to capture ActivePrompt for the deck.
    void report("working", false, event.prompt);
  });
  pi.on("agent_end", async () => {
    void report("idle");
  });
  pi.on("tool_execution_start", async () => {
    void report("working");
  });
  pi.on("session_shutdown", async (event) => {
    if (event.reason === "quit") {
      await report("exited", true);
    } else {
      log(`skip exited: session_shutdown reason=${event.reason}`);
    }
  });
}
