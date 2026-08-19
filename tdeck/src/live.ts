// Which agents are already running, so tdeck does not start a second one.
//
// Two ways to duplicate work by accident, and this exists to close both:
//
//   1. Clicking a workspace whose zmx agent is already working spawns another
//      agent in the same working tree. Two agents, one checkout, editing.
//   2. Resuming the conversation that agent is driving attaches a second writer
//      to it. The earlier belief that the agent would refuse this was wrong —
//      see the correction in tdeck.md — so the guard has to be here.
//
// Nothing is written to say who owns what. Ownership is *derived*, every time,
// from things that cannot go stale: a live process list, and the modification
// time of the file the agent is appending to. A flag in a state file would be
// wrong exactly when it matters — after a crash, a kill -9, a laptop sleep —
// and that is when someone is most likely to click the row again.

import { homedir } from "node:os";
import { join } from "node:path";
import { readdir, stat } from "node:fs/promises";

// A zmx session running an agent, as zmx itself reports it.
export type LiveAgent = { name: string; dir: string; clients: number };

// How recently a transcript must have been touched to count as being written.
//
// Long enough to cover an agent thinking between tool calls, short enough that
// a conversation abandoned an hour ago is not called live. This is a heuristic
// and is treated as one everywhere it is used: it decides whether to *ask*, not
// whether to allow.
const liveWindowMs = 3 * 60 * 1000;

// Parse `zmx ls`. Its lines are tab-separated key=value pairs, with the caller's
// own session marked by a leading arrow.
//
// Matching is on `awp_workspace` / `awp_project` tags and `start_dir` — never on
// the workspace store's SessionName. Those fields are tmux-era ("[awp]awp__default",
// "$10") and do not resemble zmx's names ("awp.awp.default.agent"); anything
// matching on them would silently never match at all.
export async function liveAgents(): Promise<LiveAgent[]> {
  try {
    const proc = Bun.spawn(["zmx", "ls"], { stdout: "pipe", stderr: "ignore" });
    const text = await new Response(proc.stdout).text();
    const out: LiveAgent[] = [];
    for (const line of text.split("\n")) {
      const fields = new Map<string, string>();
      for (const part of line.trim().replace(/^→\s*/, "").split("\t")) {
        const at = part.indexOf("=");
        if (at > 0) fields.set(part.slice(0, at).trim(), part.slice(at + 1));
      }
      const name = fields.get("name") ?? "";
      const dir = fields.get("start_dir") ?? "";
      if (!name || !dir) continue;
      // Either the explicit tag, or a command that is plainly an agent. The tag
      // is absent on older sessions, and the deck's own default-workspace panes
      // are among them.
      const isAgent =
        fields.get("awp_kind") === "agent" ||
        name.endsWith(".agent") ||
        (fields.get("cmd") ?? "").startsWith("claude");
      if (!isAgent) continue;
      out.push({ name, dir, clients: Number(fields.get("clients") ?? 0) });
    }
    return out;
  } catch {
    // No zmx on the PATH is a normal condition for someone running tdeck alone.
    // The consequence is fewer warnings, not a broken screen.
    return [];
  }
}

// Claude Code writes one transcript per session, in a directory named after the
// cwd with every non-alphanumeric character replaced by a dash. Reading only the
// names and modification times — never the contents, which is the mistake this
// whole surface was built to stop making.
function transcriptDir(cwd: string): string {
  return join(homedir(), ".claude", "projects", cwd.replace(/[^a-zA-Z0-9]/g, "-"));
}

// Session ids whose transcript was appended to just now, which is the closest
// thing to "someone is talking to this conversation right now".
export async function activeSessions(cwd: string): Promise<Set<string>> {
  const active = new Set<string>();
  try {
    const dir = transcriptDir(cwd);
    const names = await readdir(dir);
    const now = Date.now();
    await Promise.all(
      names
        .filter((name) => name.endsWith(".jsonl"))
        .map(async (name) => {
          const info = await stat(join(dir, name));
          if (now - info.mtimeMs < liveWindowMs) active.add(name.replace(/\.jsonl$/, ""));
        }),
    );
  } catch {
    // No transcripts for this directory yet.
  }
  return active;
}
