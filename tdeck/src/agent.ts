// The ACP half: one connection to the agent, many conversations over it, and
// the client handlers the agent calls back into.
//
// The adapter is `@agentclientprotocol/claude-agent-acp`, which wraps the Claude
// Agent SDK and speaks ACP over a stream. On a Bun host it could be *imported*
// rather than run as a process — that is the argument that decided ACP over raw
// stream-json — but importing it would put the agent inside the process whose
// restarts we are trying to survive. So it lives in the daemon (adapterd.ts) and
// this talks to it down a socket.
//
// One connection, N sessions, and that is measured rather than assumed:
// experiments/acp-chat/probe-concurrent.mjs prompted two sessions milliseconds
// apart and got 14 interleaves across 5.8 seconds of overlap, with the second
// finishing first. If they had serialised, this file would instead be spawning
// an adapter per chat.

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
import { EventLog, type Command, type PermissionOption } from "./events.ts";
import { connect, loadState, saveState, type SessionRecord } from "./link.ts";

export type Mode = { id: string; name: string };
export type Modes = { availableModes: Mode[]; currentModeId: string | null };

// Everything the agent lets you change about a session, as the agent describes
// it: permission mode, model, reasoning effort, fast mode, and whatever it adds
// next. ACP calls these config options and they are one generic mechanism, so
// this passes them through rather than naming any of them.
//
// The alternative was a hand-built control per setting — which is what a first
// pass did, and it invented a /effort slash command on the grounds that ACP had
// no concept of reasoning effort. It has one. The client just was not asking.
// A conversation the agent has on disk but tdeck does not have open.
export type PastSession = {
  sessionId: string;
  cwd: string;
  title: string;
  updatedAt: string;
  open: boolean;
  // Filled in by the route from transcript activity: someone is talking to this
  // conversation right now, most likely a terminal agent.
  live?: boolean;
};

export type ConfigOption = {
  id: string;
  name: string;
  description?: string;
  category?: string;
  type: "select" | "boolean";
  value: string | boolean | null;
  // Flattened from ACP's options-or-groups union, keeping the group as a label
  // so a grouped model list still reads as one.
  options: { value: string; name: string; group?: string }[];
};

// One chat. Owns its own event log, so a viewer subscribes to a conversation
// rather than to the process and a reload rebuilds only what it is looking at.
export class Conversation {
  readonly log = new EventLog();
  title: string;
  busy = false;
  private pendingPermission: ((optionId: string) => void) | null = null;

  constructor(
    readonly sessionId: string,
    readonly cwd: string,
    public modes: Modes | null,
    title?: string,
    public config: ConfigOption[] = [],
  ) {
    this.title = title ?? "new chat";
  }

  // Parks the agent's permission request until a browser answers it. The
  // promise living here rather than on the connection is what lets two
  // conversations wait on different questions at the same time.
  park(resolve: (optionId: string) => void): void {
    this.pendingPermission = resolve;
  }

  // A no-op when nothing is waiting: a stale click from a reloaded page is not
  // an error.
  permit(optionId: string): void {
    const resolve = this.pendingPermission;
    this.pendingPermission = null;
    resolve?.(optionId);
  }

  summary() {
    return {
      sessionId: this.sessionId,
      title: this.title,
      cwd: this.cwd,
      busy: this.busy,
      modes: this.modes,
      config: this.config,
    };
  }
}

// The client side of the protocol: what the agent is allowed to ask of us.
//
// Every callback carries a sessionId, which is what makes one connection able
// to serve many chats — the routing is the protocol's, not ours.
class ChatClient implements Client {
  commands: Command[] = [];

  constructor(private readonly find: (sessionId: string) => Conversation | undefined) {}

  async sessionUpdate({ sessionId, update }: SessionNotification): Promise<void> {
    const chat = this.find(sessionId);
    // An update for a session we do not know about is not a crash. It happens
    // on a reattach whose state file has been trimmed, and dropping it loses
    // one message rather than the process.
    if (!chat) return;
    const log = chat.log;

    switch (update.sessionUpdate) {
      case "agent_message_chunk":
        log.emit({ kind: "text", text: textOf(update.content) });
        break;
      case "agent_thought_chunk":
        log.emit({ kind: "thought", text: textOf(update.content) });
        break;
      case "tool_call":
        log.emit({
          kind: "tool",
          id: update.toolCallId,
          title: update.title ?? update.kind ?? "tool",
          status: update.status ?? "pending",
        });
        break;
      case "tool_call_update":
        log.emit({
          kind: "tool_update",
          id: update.toolCallId,
          status: update.status ?? undefined,
          // Content arrives as blocks, and a diff is its own kind — the thing a
          // terminal can only transcribe and this can render.
          content: update.content ?? undefined,
        });
        break;
      case "plan":
        log.emit({ kind: "plan", entries: update.entries ?? [] });
        break;
      case "usage_update":
        // Context used against the window, and cost. A status bar wants both,
        // and there is nowhere else to get them: the transcript records usage
        // per message after the fact, this arrives while the turn is running.
        log.emit({
          kind: "usage",
          used: numberOf(update, "used"),
          size: numberOf(update, "size"),
          cost: costOf(update),
        });
        break;
      case "session_info_update": {
        // The agent names the conversation. Free sidebar labels — better than
        // the first 40 characters of whatever the user typed.
        const title = stringOf(update, "title");
        if (title) chat.title = title;
        log.emit({ kind: "title", title });
        break;
      }
      case "config_option_update":
        // The agent's own account of what is currently set. Worth taking rather
        // than tracking locally: changing the model rebuilds the effort options,
        // because which levels exist depends on the model — so a client that
        // remembered its own answers would show levels the agent has withdrawn.
        chat.config = configOptionsOf(update);
        log.emit({ kind: "config", options: chat.config });
        break;
      case "available_commands_update":
        // Held, not broadcast. This is ~9KB of slash-command definitions and it
        // arrives twice per turn; shipping it down the event stream would be
        // most of the bytes a conversation costs, to deliver something that
        // changes approximately never. The page fetches /commands.
        this.commands = commandsOf(update);
        break;
      default:
        log.emit({ kind: "other", update });
    }
  }

  async requestPermission({
    sessionId,
    toolCall,
    options,
  }: RequestPermissionRequest): Promise<RequestPermissionResponse> {
    const chat = this.find(sessionId);
    if (!chat) return { outcome: { outcome: "cancelled" } };

    const rendered: PermissionOption[] = options.map((o) => ({
      id: o.optionId,
      name: o.name,
      kind: o.kind,
    }));
    chat.log.emit({ kind: "permission", title: toolCall?.title ?? "run a tool", options: rendered });
    return new Promise((resolve) => {
      chat.park((optionId) => {
        chat.log.emit({ kind: "permission_resolved", optionId });
        resolve({ outcome: { outcome: "selected", optionId } });
      });
    });
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
  private readonly chats = new Map<string, Conversation>();

  private constructor(
    private readonly conn: Agent,
    private readonly client: ChatClient,
    // Resolves when the daemon goes away, so the caller can decide what to do
    // about it rather than finding out at the next request.
    readonly closed: Promise<void>,
  ) {}

  static async start(): Promise<AgentHost> {
    const link = await connect();
    let host!: AgentHost;
    const client = new ChatClient((id) => host.chats.get(id));
    const conn = new ClientSideConnection(() => client, ndJsonStream(link.writable, link.readable));
    host = new AgentHost(conn, client, link.closed);

    // A reconnect picks up an adapter that is already initialized and already
    // holds our sessions. Sending `initialize` again is an error, and asking
    // for new sessions would abandon conversations that may be mid-turn — the
    // work the daemon exists to protect. So the two paths genuinely differ, and
    // the daemon's handshake is what tells them apart.
    if (!link.fresh) {
      const state = await loadState();
      // `sessions?` rather than `sessions.` — a state file written by an older
      // build has a single `sessionId` and no list at all, and reading through
      // it would throw on the one path whose whole job is recovery.
      if (state?.sessions?.length) {
        for (const record of state.sessions) {
          host.chats.set(
            record.sessionId,
            new Conversation(
              record.sessionId,
              record.cwd,
              record.modes as Modes | null,
              record.title,
              (record.config as ConfigOption[]) ?? [],
            ),
          );
        }
        console.log(`reattached to ${state.sessions.length} session(s)`);
        // Known consequence, visible in the log as "Got response to unknown
        // request N": a turn in flight was requested by the client that died,
        // so its JSON-RPC reply arrives addressed to nobody. All the agent's
        // output is delivered — that is the buffer's job — but this connection
        // never sees the `done` for a turn it did not start, so a reattached UI
        // streams a turn to its end and then sits there looking busy.
        //
        // Fixing it properly means the daemon tracking outstanding requests,
        // which is the first protocol awareness it does not have.
        return host;
      }
      // A daemon in use with nothing recorded: something else is driving it, or
      // the state file was removed. Falling through will fail at `initialize`
      // with the agent's own error, which is more useful than a guess here.
      console.log("daemon is in use but no sessions were recorded; trying a fresh handshake");
    }

    const init = await conn.initialize({
      protocolVersion: PROTOCOL_VERSION,
      // writeTextFile stays false; see ChatClient.writeTextFile.
      //
      // configOptions.boolean has to be advertised or the agent withholds every
      // boolean setting — fast mode, among others — from the list it sends. An
      // empty object is the whole of the opt-in.
      clientCapabilities: {
        fs: { readTextFile: true, writeTextFile: false },
        session: { configOptions: { boolean: {} } },
      },
    });
    console.log(`initialized: protocol ${init.protocolVersion}`);
    return host;
  }

  // Conversations the agent knows about, which is a larger set than the ones
  // tdeck has open — it includes everything Claude Code has ever run in that
  // directory, from this window or from a terminal.
  //
  // Filtered by cwd, which is what makes this the join with a workspace: a
  // workspace is a directory, and its history is the sessions that ran there.
  async history(cwd: string): Promise<PastSession[]> {
    if (!this.conn.listSessions) return [];
    try {
      const res = await this.conn.listSessions({ cwd });
      return (res.sessions ?? []).map((session) => ({
        sessionId: session.sessionId,
        cwd: session.cwd,
        title: session.title ?? "untitled",
        updatedAt: session.updatedAt ?? "",
        // Already open here, so the UI offers "switch to" rather than "resume".
        open: this.chats.has(session.sessionId),
      }));
    } catch (err) {
      console.log(`listing sessions failed: ${messageOf(err)}`);
      return [];
    }
  }

  // Pick up a conversation that already exists.
  //
  // The agent replays it as it loads — user and agent message chunks, in order —
  // which is why the Conversation is registered *before* the call: the replay
  // arrives as ordinary session updates addressed to a session id, and an
  // unknown id is dropped on the floor.
  //
  // A live session refuses, and that is Claude Code protecting a conversation
  // that already has a writer. Not a case to work around: the error is the
  // correct answer, and the caller shows it.
  async resume(sessionId: string, cwd: string, title: string): Promise<Conversation> {
    const existing = this.chats.get(sessionId);
    if (existing) return existing;
    if (!this.conn.loadSession) throw new Error("this agent cannot load past sessions");

    const chat = new Conversation(sessionId, cwd, null, title);
    this.chats.set(sessionId, chat);
    try {
      const res = await this.conn.loadSession({ sessionId, cwd, mcpServers: [] });
      chat.modes = res?.modes
        ? {
            availableModes: res.modes.availableModes.map((m) => ({ id: m.id, name: m.name ?? m.id })),
            currentModeId: res.modes.currentModeId ?? null,
          }
        : null;
      chat.config = configOptionsOf(res);
      console.log(`resumed session ${sessionId} (${this.chats.size} open)`);
      await this.persist();
      return chat;
    } catch (err) {
      // Registered above, so it has to come back out — otherwise a failed
      // resume leaves a chat in the sidebar that no agent is behind.
      this.chats.delete(sessionId);
      throw err;
    }
  }

  // Let go of a conversation. Local only: the agent keeps it, the daemon keeps
  // running, this window just stops holding it.
  //
  // Needed the moment resume exists, because resume can attach to a session
  // that is already being driven somewhere else — see the note on resume — and
  // the fix for that is to be able to put it back down.
  async close(sessionId: string): Promise<boolean> {
    const gone = this.chats.delete(sessionId);
    if (gone) await this.persist();
    return gone;
  }

  list(): Conversation[] {
    return [...this.chats.values()];
  }

  get(sessionId: string): Conversation | undefined {
    return this.chats.get(sessionId);
  }

  async open(cwd: string): Promise<Conversation> {
    const session = await this.conn.newSession({ cwd, mcpServers: [] });

    // Modes come from the agent. A client that invented its own "auto" would
    // only ever see the prompts the agent bothered to send, and would diverge
    // from what the same setting means everywhere else in awp.
    const modes: Modes | null = session.modes
      ? {
          availableModes: session.modes.availableModes.map((m) => ({ id: m.id, name: m.name ?? m.id })),
          currentModeId: session.modes.currentModeId ?? null,
        }
      : null;

    const chat = new Conversation(
      session.sessionId,
      cwd,
      modes,
      undefined,
      configOptionsOf(session),
    );
    this.chats.set(chat.sessionId, chat);
    console.log(`session ${chat.sessionId} (${this.chats.size} open)`);
    await this.persist();
    return chat;
  }

  // One prompt at a time per conversation. The agent would accept a second, but
  // two overlapping turns in one chat is not a thing the UI has a shape for —
  // unlike two conversations, which genuinely run at once.
  async say(chat: Conversation, text: string): Promise<void> {
    if (chat.busy || !text) return;
    chat.busy = true;
    chat.log.emit({ kind: "user", text });
    try {
      const res = await this.conn.prompt({
        sessionId: chat.sessionId,
        prompt: [{ type: "text", text }],
      });
      chat.log.emit({ kind: "done", stopReason: res.stopReason });
    } catch (err) {
      chat.log.emit({ kind: "error", message: messageOf(err) });
    } finally {
      chat.busy = false;
      await this.persist();
    }
  }

  async setMode(chat: Conversation, modeId: string): Promise<void> {
    // Optional on the Agent interface — an agent that advertises no modes has
    // no method to call, so this is a real branch rather than a type dance.
    if (!this.conn.setSessionMode) {
      chat.log.emit({ kind: "error", message: "this agent does not support session modes" });
      return;
    }
    try {
      await this.conn.setSessionMode({ sessionId: chat.sessionId, modeId });
      if (chat.modes) chat.modes = { ...chat.modes, currentModeId: modeId };
      chat.log.emit({ kind: "mode", modeId });
      await this.persist();
    } catch (err) {
      chat.log.emit({ kind: "error", message: messageOf(err) });
    }
  }

  // Set one of the agent's own settings — model, effort, permission mode, fast
  // mode. One method for all of them, because ACP made them one thing.
  //
  // The response carries the whole option list back, and it is taken rather than
  // patched locally: the sets are interdependent, so choosing a model can change
  // which effort levels exist.
  async setConfig(chat: Conversation, configId: string, value: string | boolean): Promise<void> {
    if (!this.conn.setSessionConfigOption) {
      chat.log.emit({ kind: "error", message: "this agent has no configurable options" });
      return;
    }
    try {
      const request =
        typeof value === "boolean"
          ? { sessionId: chat.sessionId, configId, type: "boolean" as const, value }
          : { sessionId: chat.sessionId, configId, value };
      const res = await this.conn.setSessionConfigOption(request);
      chat.config = configOptionsOf(res);
      chat.log.emit({ kind: "config", options: chat.config });
      await this.persist();
    } catch (err) {
      chat.log.emit({ kind: "error", message: messageOf(err) });
    }
  }

  // Whatever the agent last advertised. Empty until the first turn, since the
  // agent sends the list as part of one rather than at initialize.
  get commands(): Command[] {
    return this.client.commands;
  }

  private async persist(): Promise<void> {
    const sessions: SessionRecord[] = this.list().map((chat) => ({
      sessionId: chat.sessionId,
      cwd: chat.cwd,
      title: chat.title,
      modes: chat.modes,
      config: chat.config,
    }));
    await saveState({ sessions });
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

// ACP's config options, flattened into something a picker can render without
// knowing the union. Select values may arrive as a flat list or as groups; the
// group survives as a label rather than as a second level of structure, since a
// grouped model list still reads as one list.
function configOptionsOf(source: unknown): ConfigOption[] {
  const list = field(source, "configOptions");
  if (!Array.isArray(list)) return [];
  return list.map((raw) => {
    const type = stringOf(raw, "type") === "boolean" ? "boolean" : "select";
    const value = field(raw, "currentValue");
    const options: ConfigOption["options"] = [];
    const values = field(raw, "options");
    if (Array.isArray(values)) {
      for (const entry of values) {
        const grouped = field(entry, "options");
        if (Array.isArray(grouped)) {
          const group = stringOf(entry, "name");
          for (const inner of grouped) {
            options.push({ value: stringOf(inner, "value"), name: stringOf(inner, "name"), group });
          }
          continue;
        }
        options.push({ value: stringOf(entry, "value"), name: stringOf(entry, "name") });
      }
    }
    return {
      id: stringOf(raw, "id"),
      name: stringOf(raw, "name"),
      description: stringOf(raw, "description") || undefined,
      category: stringOf(raw, "category") || undefined,
      type,
      value: typeof value === "string" || typeof value === "boolean" ? value : null,
      options,
    };
  });
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
