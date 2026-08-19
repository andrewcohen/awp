// The seam between the page and the Bun server.
//
// These types are hand-mirrored from src/events.ts on the backend. They are not
// generated, which is a deliberate trade: gdeck generated its bindings from Go
// and the generator was a step everyone forgot to run, so the types were
// authoritative and stale at the same time. Two small files that disagree
// loudly at the first render beat one that lies.

export type PermissionOption = { id: string; name: string; kind?: string };

export type UiEvent =
  | { kind: "user"; text: string }
  | { kind: "text"; text: string }
  | { kind: "thought"; text: string }
  | { kind: "tool"; id: string; title: string; status: string }
  | { kind: "tool_update"; id: string; status?: string; content?: unknown }
  | { kind: "plan"; entries: unknown[] }
  | { kind: "permission"; title: string; options: PermissionOption[] }
  | { kind: "permission_resolved"; optionId: string }
  | { kind: "mode"; modeId: string }
  | { kind: "config"; options: ConfigOption[] }
  | { kind: "usage"; used: number; size: number; cost?: number }
  | { kind: "title"; title: string }
  | { kind: "done"; stopReason: string }
  | { kind: "error"; message: string }
  | { kind: "other"; update: unknown };

export type Mode = { id: string; name: string };
export type Modes = { availableModes: Mode[]; currentModeId: string | null };

// Whatever the agent lets you change about a session, described by the agent:
// model, reasoning effort, permission mode, fast mode, and anything it adds
// later. One generic shape, so the UI renders settings it has never heard of.
export type ConfigOption = {
  id: string;
  name: string;
  description?: string;
  category?: string;
  type: "select" | "boolean";
  value: string | boolean | null;
  options: { value: string; name: string; group?: string }[];
};

export type SessionSummary = {
  sessionId: string;
  title: string;
  cwd: string;
  busy: boolean;
  modes: Modes | null;
  config: ConfigOption[];
};

export type Command = { name: string; description: string };

// One of awp's workspaces, read from awp's own state. tdeck does not compute
// any of this — see src/workspaces.ts on the backend for why that matters.
export type PastSession = {
  sessionId: string;
  cwd: string;
  title: string;
  updatedAt: string;
  open: boolean;
  // Someone is appending to this conversation right now — almost always a
  // terminal agent. Resuming it would make tdeck a second writer.
  live?: boolean;
};

export type Workspace = {
  project: string;
  projectPath: string;
  name: string;
  displayName: string;
  path: string;
  status: string;
  bookmark: string;
  prNumber: number;
  unread: boolean;
  lastActiveAt: string;
  // Set when a tdeck conversation is already open on this directory.
  sessionId?: string;
  // A zmx agent is running in this directory. Opening a chat here would put a
  // second agent in the same checkout.
  terminalAgent?: boolean;
};

async function post(
  path: string,
  body: Record<string, unknown>,
): Promise<Response> {
  return fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
}

export const api = {
  sessions: (): Promise<SessionSummary[]> =>
    fetch("/sessions").then((r) => r.json()),

  open: (cwd?: string): Promise<SessionSummary> =>
    post("/sessions", cwd ? { cwd } : {}).then((r) => r.json()),

  commands: (): Promise<Command[]> => fetch("/commands").then((r) => r.json()),

  workspaces: (): Promise<Workspace[]> =>
    fetch("/workspaces").then((r) => r.json()),

  // Conversations the agent has on disk for a directory, including ones started
  // from a terminal.
  history: (cwd: string): Promise<PastSession[]> =>
    fetch(`/history?cwd=${encodeURIComponent(cwd)}`).then((r) => r.json()),

  resume: async (past: PastSession): Promise<SessionSummary> => {
    const res = await post("/resume", {
      sessionId: past.sessionId,
      cwd: past.cwd,
      title: past.title,
    });
    if (!res.ok) {
      const body = (await res.json().catch(() => ({}))) as { error?: string };
      throw new Error(body.error ?? "could not resume that conversation");
    }
    return (await res.json()) as SessionSummary;
  },

  // Local only: the agent keeps the conversation, this window stops holding it.
  close: (sessionId: string) => post("/close", { sessionId }),

  say: (session: string, text: string) => post("/say", { session, text }),

  permit: (session: string, optionId: string) =>
    post("/permit", { session, optionId }),

  setMode: (session: string, modeId: string) =>
    post("/mode", { session, modeId }),

  setConfig: (
    session: string,
    configId: string,
    value: string | boolean,
  ): Promise<SessionSummary> =>
    post("/config", { session, configId, value }).then((r) => r.json()),

  // Everything the agent says for one conversation. The backend replays what it
  // has already shown before streaming what is new, so a reload or a session
  // switch rebuilds the conversation rather than starting from blank.
  events: (
    session: string,
    onEvent: (event: UiEvent) => void,
  ): (() => void) => {
    const source = new EventSource(
      `/events?session=${encodeURIComponent(session)}`,
    );
    source.onmessage = (message) => {
      try {
        onEvent(JSON.parse(message.data) as UiEvent);
      } catch (err) {
        // One malformed frame should not take the stream down with it.
        console.error("bad event", err, message.data);
      }
    };
    return () => source.close();
  },
};
