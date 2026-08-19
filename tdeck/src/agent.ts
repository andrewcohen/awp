// The ACP half: the connection to the agent, and the client handlers it calls
// back into.
//
// The adapter is `@agentclientprotocol/claude-agent-acp`, which wraps the Claude
// Agent SDK and speaks ACP over a stream. On a Bun host it could be *imported*
// rather than run as a process — that is the argument that decided ACP over raw
// stream-json — but importing it would put the agent inside the process whose
// restarts we are trying to survive. So it lives in the daemon (adapterd.ts) and
// this talks to it down a socket.

import {
  ClientSideConnection,
  ndJsonStream,
  PROTOCOL_VERSION,
  type Agent,
  type Client,
  type RequestPermissionRequest,
  type RequestPermissionResponse,
  type SessionNotification,
  type ReadTextFileRequest,
} from "@agentclientprotocol/sdk";
import type { Command, EventLog, PermissionOption } from "./events.ts";
import { connect, loadState, saveState } from "./link.ts";

export type Mode = { id: string; name: string };
export type Modes = { availableModes: Mode[]; currentModeId: string | null };

// The client side of the protocol: what the agent is allowed to ask of us.
//
// Permissions are the interesting one. The agent parks a request here and the
// promise stays unresolved until the browser posts a choice back — which is the
// whole reason ACP is worth an adapter, and the thing a log-scraping chat could
// never do.
class ChatClient implements Client {
  private pending: ((optionId: string) => void) | null = null;
  commands: Command[] = [];

  constructor(private readonly log: EventLog) {}

  async sessionUpdate({ update }: SessionNotification): Promise<void> {
    switch (update.sessionUpdate) {
      case "agent_message_chunk":
        this.log.emit({ kind: "text", text: textOf(update.content) });
        break;
      case "agent_thought_chunk":
        this.log.emit({ kind: "thought", text: textOf(update.content) });
        break;
      case "tool_call":
        this.log.emit({
          kind: "tool",
          id: update.toolCallId,
          title: update.title ?? update.kind ?? "tool",
          status: update.status ?? "pending",
        });
        break;
      case "tool_call_update":
        this.log.emit({
          kind: "tool_update",
          id: update.toolCallId,
          status: update.status ?? undefined,
          // Content arrives as blocks, and a diff is its own kind — the thing a
          // terminal can only transcribe and this can render.
          content: update.content ?? undefined,
        });
        break;
      case "plan":
        this.log.emit({ kind: "plan", entries: update.entries ?? [] });
        break;
      case "usage_update":
        // Context used against the window, and cost. A status bar wants both,
        // and there is nowhere else to get them: the transcript records usage
        // per message after the fact, this arrives while the turn is running.
        this.log.emit({
          kind: "usage",
          used: numberOf(update, "used"),
          size: numberOf(update, "size"),
          cost: costOf(update),
        });
        break;
      case "session_info_update":
        // The agent names the conversation. Free sidebar labels — better than
        // the first 40 characters of whatever the user typed.
        this.log.emit({ kind: "title", title: stringOf(update, "title") });
        break;
      case "available_commands_update":
        // Held, not broadcast. This is ~9KB of slash-command definitions and it
        // arrives twice per turn; shipping it down the event stream would be
        // most of the bytes a conversation costs, to deliver something that
        // changes approximately never. The page fetches it from /commands.
        this.commands = commandsOf(update);
        break;
      default:
        this.log.emit({ kind: "other", update });
    }
  }

  async requestPermission({
    toolCall,
    options,
  }: RequestPermissionRequest): Promise<RequestPermissionResponse> {
    const rendered: PermissionOption[] = options.map((o) => ({
      id: o.optionId,
      name: o.name,
      kind: o.kind,
    }));
    this.log.emit({ kind: "permission", title: toolCall?.title ?? "run a tool", options: rendered });
    return new Promise((resolve) => {
      this.pending = (optionId) => {
        this.pending = null;
        this.log.emit({ kind: "permission_resolved", optionId });
        resolve({ outcome: { outcome: "selected", optionId } });
      };
    });
  }

  // Answers a parked permission request. A no-op when nothing is waiting, since
  // a stale click from a reloaded page is not an error.
  permit(optionId: string): void {
    this.pending?.(optionId);
  }

  async readTextFile({ path }: ReadTextFileRequest) {
    return { content: await Bun.file(path).text() };
  }

  // Not enabled: the agent edits files through its own tools, and this
  // capability is for edits the client mediates. Refusing is more honest than
  // silently succeeding, and the capability is advertised false to match.
  async writeTextFile(): Promise<never> {
    throw new Error("tdeck does not mediate writes; the agent uses its own tools");
  }
}

export class AgentHost {
  private constructor(
    private readonly conn: Agent,
    private readonly client: ChatClient,
    private readonly log: EventLog,
    readonly sessionId: string,
    readonly modes: Modes | null,
  ) {}

  static async start(log: EventLog, cwd: string): Promise<AgentHost> {
    const link = await connect();
    const client = new ChatClient(log);
    const conn = new ClientSideConnection(() => client, ndJsonStream(link.writable, link.readable));

    // A reconnect picks up an adapter that is already initialized and already
    // holds our session. Sending `initialize` again is an error, and asking for
    // a `newSession` would abandon a conversation that may be mid-turn — the
    // work this daemon exists to protect. So the two paths genuinely differ,
    // and the daemon's handshake is what tells them apart.
    if (!link.fresh) {
      const state = await loadState();
      if (state) {
        // Known consequence, visible in the log as "Got response to unknown
        // request N": a turn that was in flight was requested by the client
        // that died, so its JSON-RPC *reply* arrives addressed to nobody. The
        // agent's output is all delivered — that is the buffer's job, and it
        // works — but this connection never sees the `done` for a turn it did
        // not start, so a reattached UI shows a conversation that streams to
        // its end and then sits there looking busy.
        //
        // Fixing it properly means the daemon knowing which requests are
        // outstanding, which is the first piece of protocol awareness it does
        // not have. Deferred until the UI exists to be annoyed by it; a
        // client-side "no output for N seconds after a reattach" would do.
        console.log(`reattached to session ${state.sessionId}`);
        return new AgentHost(conn, client, log, state.sessionId, state.modes as Modes | null);
      }
      // A daemon with no state file: something else was driving it, or the file
      // was removed. Falling through to a fresh handshake will fail at
      // `initialize` with the agent's own error, which is more useful than a
      // guess made here.
      console.log("daemon is in use but no session was recorded; trying a fresh handshake");
    }

    const init = await conn.initialize({
      protocolVersion: PROTOCOL_VERSION,
      // writeTextFile stays false; see ChatClient.writeTextFile.
      clientCapabilities: { fs: { readTextFile: true, writeTextFile: false } },
    });
    console.log(`initialized: protocol ${init.protocolVersion}`);

    const session = await conn.newSession({ cwd, mcpServers: [] });
    console.log(`session ${session.sessionId}`);

    // Modes come from the agent. A client that invented its own "auto" would
    // only ever see the prompts the agent bothered to send, and would diverge
    // from what the same setting means everywhere else in awp.
    const modes: Modes | null = session.modes
      ? {
          availableModes: session.modes.availableModes.map((m) => ({ id: m.id, name: m.name ?? m.id })),
          currentModeId: session.modes.currentModeId ?? null,
        }
      : null;
    if (modes) {
      console.log(`modes: ${modes.availableModes.map((m) => m.id).join(", ")} (current ${modes.currentModeId})`);
    }

    await saveState({ sessionId: session.sessionId, modes });
    return new AgentHost(conn, client, log, session.sessionId, modes);
  }

  // One prompt at a time. The agent would accept a second, but two overlapping
  // turns in one conversation is not a thing the UI has a shape for — unlike
  // two *sessions*, which genuinely run at once and are the next unit.
  private busy = false;

  async say(text: string): Promise<void> {
    if (this.busy || !text) return;
    this.busy = true;
    this.log.emit({ kind: "user", text });
    try {
      const res = await this.conn.prompt({ sessionId: this.sessionId, prompt: [{ type: "text", text }] });
      this.log.emit({ kind: "done", stopReason: res.stopReason });
    } catch (err) {
      this.log.emit({ kind: "error", message: messageOf(err) });
    } finally {
      this.busy = false;
    }
  }

  permit(optionId: string): void {
    this.client.permit(optionId);
  }

  // Whatever the agent last advertised. Empty until the first turn, since the
  // agent sends the list as part of one rather than at initialize.
  get commands(): Command[] {
    return this.client.commands;
  }

  async setMode(modeId: string): Promise<void> {
    // Optional on the Agent interface — an agent that advertises no modes has
    // no method to call, so this is a real branch rather than a type dance.
    if (!this.conn.setSessionMode) {
      this.log.emit({ kind: "error", message: "this agent does not support session modes" });
      return;
    }
    try {
      await this.conn.setSessionMode({ sessionId: this.sessionId, modeId });
      this.log.emit({ kind: "mode", modeId });
    } catch (err) {
      this.log.emit({ kind: "error", message: messageOf(err) });
    }
  }
}

// The SDK types these updates loosely enough that reading a field is a cast
// either way; doing it through one set of guards keeps the casts in one place.
function field(obj: unknown, key: string): unknown {
  return typeof obj === "object" && obj !== null ? (obj as Record<string, unknown>)[key] : undefined;
}

function numberOf(obj: unknown, key: string): number {
  const value = field(obj, key);
  return typeof value === "number" ? value : 0;
}

function stringOf(obj: unknown, key: string): string {
  const value = field(obj, key);
  return typeof value === "string" ? value : "";
}

function costOf(update: unknown): number | undefined {
  const amount = field(field(update, "cost"), "amount");
  return typeof amount === "number" ? amount : undefined;
}

function commandsOf(update: unknown): Command[] {
  const list = field(update, "availableCommands");
  if (!Array.isArray(list)) return [];
  return list.map((c) => ({ name: stringOf(c, "name"), description: stringOf(c, "description") }));
}

function textOf(content: unknown): string {
  return typeof content === "object" && content !== null && "text" in content
    ? String((content as { text: unknown }).text ?? "")
    : "";
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
