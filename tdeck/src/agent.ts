// The ACP half: one connection to the agent, many conversations over it, and
// the client handlers the agent calls back into.
//
// This runs *inside the daemon*, over the adapter's own stdio. It used to run in
// the UI server and reach the adapter down a socket, which cost a `done` every
// time the server restarted: a turn is a JSON-RPC request, its reply is
// addressed to whoever asked, and a restarted server is not that process. All
// the output arrived and the completion did not.
//
// Owning the connection here fixes that by construction and deletes a pile of
// machinery with it — the fresh/reattach handshake, the initialize-twice
// problem, and a state file whose whole job was surviving a restart that no
// longer loses anything. Conversations now live exactly as long as the daemon,
// which is also exactly as long as the agents do.
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
  type Stream,
  type ReadTextFileRequest,
} from "@agentclientprotocol/sdk";
import { homedir } from "node:os";
import { join } from "node:path";
import { EventLog, type Command, type PermissionOption } from "./events.ts";

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
  // Messages typed while a turn was running, waiting for it to end.
  //
  // They used to be dropped: say() returned early when busy and the caller was
  // told `said: true`, so a message typed mid-turn vanished and nothing said
  // so. Queueing matches what Claude Code does with input during a turn, and
  // the queue is visible in the conversation so it is obvious the message is
  // waiting rather than lost.
  readonly queued: string[] = [];
  private pendingPermission: ((optionId: string) => void) | null = null;
  // What the agent is currently asking permission for, if anything.
  //
  // This is the attention signal the whole fleet view wants, and it is exact:
  // the agent is stopped, waiting for a person, and the protocol said so. awp's
  // hooks approximate this from outside the agent because nothing was speaking
  // to it; where tdeck is the client there is nothing to approximate.
  waitingOn: string | null = null;

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
  park(title: string, resolve: (optionId: string) => void): void {
    this.pendingPermission = resolve;
    this.waitingOn = title;
  }

  // A no-op when nothing is waiting: a stale click from a reloaded page is not
  // an error.
  permit(optionId: string): void {
    const resolve = this.pendingPermission;
    this.pendingPermission = null;
    this.waitingOn = null;
    resolve?.(optionId);
  }

  summary() {
    return {
      sessionId: this.sessionId,
      title: this.title,
      cwd: this.cwd,
      busy: this.busy,
      // Distinct from busy on purpose. Busy means the agent is working and you
      // can look away; waiting means it has stopped and cannot continue without
      // you. Collapsing them into one flag is how a fleet view ends up unable to
      // say which of eighteen workspaces needs a person.
      waitingOn: this.waitingOn,
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

  constructor(
    private readonly find: (sessionId: string) => Conversation | undefined,
    // Rung when something the fleet view cares about changes — a conversation
    // starting to wait on a person, or stopping. The permission handler is the
    // only place that knows, and it is not on AgentHost.
    private readonly changed: () => void,
  ) {}

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
    const title = toolCall?.title ?? "run a tool";
    chat.log.emit({ kind: "permission", title, options: rendered });
    this.changed();
    return new Promise((resolve) => {
      chat.park(title, (optionId) => {
        this.changed();
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
  ) {}

  // Told when the set of conversations changes, so the daemon can nudge every
  // attached UI. One callback rather than an event emitter: there is exactly one
  // listener and it is the process this object lives in.
  onSessionsChanged: () => void = () => {};

  static async start(stream: Stream): Promise<AgentHost> {
    let host!: AgentHost;
    const client = new ChatClient(
      (id) => host.chats.get(id),
      () => host.onSessionsChanged(),
    );
    const conn = new ClientSideConnection(() => client, stream);
    host = new AgentHost(conn, client);

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
      this.onSessionsChanged();
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
    if (gone) this.onSessionsChanged();
    return gone;
  }

  list(): Conversation[] {
    return [...this.chats.values()];
  }

  get(sessionId: string): Conversation | undefined {
    return this.chats.get(sessionId);
  }

  async open(cwd: string): Promise<Conversation> {
    // Started the way awp starts an agent, when awp has an opinion about this
    // directory.
    //
    // awp runs `claude --append-system-prompt-file ~/.awp/dev-loop/<slug>.md`,
    // and that file is where the working discipline lives — one committable unit
    // at a time, gates green before anything is marked done. An agent tdeck
    // started without it is a visibly worse agent in the same repo, which is a
    // confusing thing to discover an hour in.
    //
    // `_meta.systemPrompt` takes `{append}` on top of the claude_code preset,
    // so this is the supported seam rather than a flag smuggled past the
    // adapter.
    const append = await devLoopPrompt(cwd);
    const session = await this.conn.newSession({
      cwd,
      mcpServers: [],
      ...(append ? { _meta: { systemPrompt: { append } } } : {}),
    });

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
    this.onSessionsChanged();
    return chat;
  }

  // One turn at a time per conversation, with anything sent meanwhile queued.
  //
  // Whether the agent would accept a genuinely concurrent prompt on one session
  // is untested — ACP models a turn as a request, and a second in flight is at
  // best undefined. What is certain is that dropping the message was wrong.
  //
  // Queued messages go in at the next turn boundary, which is Claude Code's own
  // behaviour for input typed during a turn. It is not interruption: a queued
  // "stop" arrives after the thing it wanted to stop. That is what the stop
  // button is for, and the UI says which is which.
  async say(chat: Conversation, text: string): Promise<void> {
    if (!text) return;
    if (chat.busy) {
      chat.queued.push(text);
      chat.log.emit({ kind: "queued", text });
      this.onSessionsChanged();
      return;
    }
    chat.busy = true;
    this.onSessionsChanged();
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
      this.onSessionsChanged();
    }

    // Whatever was typed while that ran, in the order it was typed. Sent after
    // the flag is cleared so this is an ordinary turn rather than a special
    // case, and one at a time so a queue of three does not become three
    // concurrent turns.
    const next = chat.queued.shift();
    if (next !== undefined) {
      chat.log.emit({ kind: "unqueued", text: next });
      await this.say(chat, next);
    }
  }

  // Stop the turn in progress.
  //
  // A notification, not a request: there is no reply to wait for, and the turn
  // ends by the prompt returning with a stopReason of "cancelled" through the
  // ordinary path. So nothing here marks the conversation idle — say() already
  // owns that, and a second writer of the same flag is how they disagree.
  async cancel(chat: Conversation): Promise<void> {
    if (!chat.busy) return;
    try {
      await this.conn.cancel({ sessionId: chat.sessionId });
    } catch (err) {
      chat.log.emit({ kind: "error", message: messageOf(err) });
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
      this.onSessionsChanged();
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
      this.onSessionsChanged();
    } catch (err) {
      chat.log.emit({ kind: "error", message: messageOf(err) });
    }
  }

  // Whatever the agent last advertised. Empty until the first turn, since the
  // agent sends the list as part of one rather than at initialize.
  get commands(): Command[] {
    return this.client.commands;
  }

}

// awp names its dev-loop files after the directory: leading slash dropped, the
// remaining separators turned into dashes, and dots left alone — the real files
// read "Users-acohen-go-src-github.com-andrewcohen-awp.md", with github.com
// intact. Flattening the dots too would miss every file, silently, and the only
// symptom would be an agent that behaves slightly worse than the one next to it.
//
// Absent for a directory awp has never seen, which is the ordinary case for a
// scratch chat.
async function devLoopPrompt(cwd: string): Promise<string> {
  const slug = cwd.replace(/^\//, "").replaceAll("/", "-");
  const path = join(homedir(), ".awp", "dev-loop", `${slug}.md`);
  try {
    return await Bun.file(path).text();
  } catch {
    return "";
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
