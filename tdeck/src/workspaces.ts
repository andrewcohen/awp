// awp's workspaces, read from awp's own state.
//
// This is the file that makes tdeck *awp* rather than a nice Claude client. The
// deck's value was never one conversation rendered well — it was eighteen
// workspaces and knowing which three need you. That knowledge already exists and
// is already correct: hooks call `awp internal report-status`, which writes the
// workspace store, which is what the TUI's status dots read.
//
// So this reads. It does not compute status, does not watch agents, does not
// write. awp's brain stays Go — zmx, workspaces, jj, review, deckdata and the
// hooks that produce live status are the product — and tdeck is a client of it.
// The moment this file starts deciding what "working" means, there are two
// answers to that question and they will disagree on a Friday.

import { homedir } from "node:os";
import { join } from "node:path";
import { watch, type FSWatcher } from "node:fs";

const statePath = join(homedir(), ".awp", "workspace-state.json");

export type Workspace = {
  project: string;
  projectPath: string;
  name: string;
  displayName: string;
  path: string;
  // awp's own vocabulary, passed through rather than remapped: idle, working,
  // waiting, error, starting. A second spelling of these is a second source of
  // truth about what an agent is doing.
  status: string;
  bookmark: string;
  prNumber: number;
  unread: boolean;
  lastActiveAt: string;
  // Whether a tdeck conversation is already open on this directory. Filled in
  // by the caller, which is the only part that knows.
  sessionId?: string;
};

function str(obj: unknown, key: string): string {
  if (typeof obj !== "object" || obj === null) return "";
  const value = (obj as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

function num(obj: unknown, key: string): number {
  if (typeof obj !== "object" || obj === null) return 0;
  const value = (obj as Record<string, unknown>)[key];
  return typeof value === "number" ? value : 0;
}

// The last path segment, which is what a person calls the project. The store is
// keyed by absolute path because that is what identifies a repo; nobody reads
// their sidebar in absolute paths.
function projectName(path: string): string {
  return path.split("/").filter(Boolean).pop() ?? path;
}

export async function readWorkspaces(): Promise<Workspace[]> {
  let raw: unknown;
  try {
    raw = await Bun.file(statePath).json();
  } catch {
    // No awp state is a normal condition — a fresh machine, or someone running
    // tdeck without ever having used the deck. An empty list renders as an
    // empty section rather than as an error.
    return [];
  }
  if (typeof raw !== "object" || raw === null) return [];

  const out: Workspace[] = [];
  for (const [projectPath, entries] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof entries !== "object" || entries === null) continue;
    for (const [name, entry] of Object.entries(entries as Record<string, unknown>)) {
      out.push({
        project: projectName(projectPath),
        projectPath,
        name,
        // Presentation only, and empty means "use Name" — the same rule the Go
        // side documents on the field.
        displayName: str(entry, "DisplayName") || name,
        path: str(entry, "Path") || projectPath,
        status: str(entry, "Status") || "idle",
        bookmark: str(entry, "Bookmark"),
        prNumber: num(entry, "PRNumber"),
        unread: (entry as { Unread?: unknown })?.Unread === true,
        lastActiveAt: str(entry, "LastActiveAt"),
      });
    }
  }

  // Most recently active first within a project, projects alphabetical. The
  // deck sorts by attention; this is the cheap approximation until tdeck has a
  // notion of what needs you.
  out.sort(
    (a, b) => a.project.localeCompare(b.project) || b.lastActiveAt.localeCompare(a.lastActiveAt),
  );
  return out;
}

// Tell the caller when the file changes, so a status dot moves without polling.
//
// The store is rewritten whole on every update — the hooks fire on tool use, so
// during an active turn that is several times a second — and an editor-style
// write appears as a rename rather than a change. Both are why this notifies
// rather than parsing here, and why the caller debounces.
export function watchWorkspaces(onChange: () => void): FSWatcher | null {
  try {
    return watch(statePath, () => onChange());
  } catch {
    return null;
  }
}
