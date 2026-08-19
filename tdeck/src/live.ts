// Which agents are already running in a directory, so tdeck does not start a
// second one in the same checkout.
//
// This once also decided which *conversation* was being written to, by stating
// transcript files under ~/.claude/projects. That half is gone: `session/list`
// carries `updatedAt` and it is the same signal from the same record, so the
// filesystem version was a Claude-specific reimplementation of something the
// protocol already hands over.
//
// What remains has no protocol answer and never will. Whether a pty somewhere
// is running an agent in this directory is a fact about processes on this
// machine, and ACP has nothing to say about programs it is not speaking to.
//
// Nothing is written to say who owns what. Ownership is *derived*, every time,
// from a live process list. A flag in a state file would be wrong exactly when
// it matters — after a crash, a kill -9, a laptop sleep — and that is when
// someone is most likely to click the row again.

// A zmx session running an agent, as zmx itself reports it.
export type LiveAgent = { name: string; dir: string; clients: number };

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
