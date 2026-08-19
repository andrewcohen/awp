// The agent host: one long-lived process that owns the adapter, the ACP
// connection, and every conversation.
//
// It started as a byte pipe — the adapter's stdio bridged to a socket, with the
// UI server holding the ACP client. That survived a server restart in the sense
// that the agent kept working and its output was buffered, but not in the sense
// that mattered: a turn is a JSON-RPC request, its reply is addressed to the
// process that asked, and a restarted server is not that process. Every restart
// mid-turn produced a conversation that streamed to its end and then span
// forever, because the `done` had been addressed to somebody who no longer
// existed.
//
// Buffering harder cannot fix that. The request has to be owned by something
// that does not die. So the ACP client moved in here and the UI server became a
// subscriber over the small protocol in protocol.ts.
//
// What that bought, beyond the fix: the handshake is gone, the initialize-twice
// problem is gone, the state file whose only job was surviving a restart is
// gone, and several clients can watch the same conversation without fighting
// over one JSON-RPC stream. Conversations now live exactly as long as the
// daemon — which is exactly as long as the agents behind them do, so there is
// nothing left to reconcile.
//
// zmx is still the shape being copied, and still rejected as the mechanism: it
// keeps a terminal grid rather than a byte stream, so a 245-byte JSON-RPC line
// came back as 249 with carriage returns inserted at column boundaries.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { unlinkSync } from "node:fs";
import type { Socket } from "bun";
import { ndJsonStream } from "@agentclientprotocol/sdk";
import { AgentHost, type Conversation } from "./agent.ts";
import { ensureRuntimeDir, instanceName, socketPath } from "./paths.ts";
import { SocketWriter } from "./socketwrite.ts";
import type { Frame, Request } from "./protocol.ts";

// How recently a conversation must have been touched to count as being written
// to. Long enough to cover an agent thinking between tool calls, short enough
// that something abandoned an hour ago is not called live.
const liveWindowMs = 3 * 60 * 1000;

const here = dirname(fileURLToPath(import.meta.url));
const adapterBin = join(here, "..", "node_modules", ".bin", "claude-agent-acp");

ensureRuntimeDir();

// One daemon per instance, enforced by asking.
//
// A socket file outlives the process that made it, so a crash leaves one behind
// and the next bind fails on an address nobody is listening to — which is why
// this unlinks before binding. But unlinking is stealing the address, and doing
// it unconditionally orphans a daemon that is alive and well: it keeps running
// on an unlinked inode, holding its adapter and every conversation in it, while
// being unreachable by anything. Three of them accumulated in an afternoon
// before anyone noticed, each with its own agent process idling.
//
// An earlier version of this comment claimed the case was "excluded by the lock
// below". There was no lock. So there is one now, and it is the only kind that
// cannot go stale: connect, and see whether anybody answers.
async function alreadyListening(): Promise<boolean> {
  try {
    const probe = await Bun.connect<undefined>({
      unix: socketPath,
      socket: { data() {}, error() {} },
    });
    probe.end();
    return true;
  } catch {
    return false;
  }
}

if (await alreadyListening()) {
  console.error(
    `a daemon is already listening on ${socketPath}\n` +
      `stop it first, or run a separate instance with TDECK_INSTANCE=<name>`,
  );
  process.exit(1);
}

try {
  unlinkSync(socketPath);
} catch {
  // Nothing there, which is the normal case for a first start.
}

const adapter = Bun.spawn([adapterBin], {
  stdin: "pipe",
  stdout: "pipe",
  stderr: "inherit",
  onExit(_proc, code) {
    // The adapter is the whole point of this process. Outliving it would leave
    // a socket that accepts connections and answers nothing.
    console.log(`adapter exited (${code}); daemon stopping`);
    process.exit(code ?? 0);
  },
});

const host = await AgentHost.start(
  ndJsonStream(
    new WritableStream<Uint8Array>({
      write(chunk) {
        adapter.stdin.write(chunk);
        adapter.stdin.flush();
      },
    }),
    adapter.stdout,
  ),
);

// Attached UIs. Several are fine now — they are subscribers, not co-owners of a
// JSON-RPC stream — which is what makes a second window, or a reloaded page
// beside an open one, a non-event.
type Viewer = {
  out: SocketWriter;
  // Which conversations this viewer is watching, so a page showing one chat is
  // not sent the tokens of five others.
  watching: Set<string>;
  // Unsubscribes from each conversation's log, by session id.
  stop: Map<string, () => void>;
};
const viewers = new Map<Socket<undefined>, Viewer>();

const encoder = new TextEncoder();
function send(viewer: Viewer, frame: Frame): void {
  viewer.out.write(encoder.encode(JSON.stringify(frame) + "\n"));
}

host.onSessionsChanged = () => {
  for (const viewer of viewers.values()) send(viewer, { push: "sessions" });
};

function chatOr(sessionId: string): Conversation {
  const chat = host.get(sessionId);
  if (!chat) throw new Error(`no such session: ${sessionId}`);
  return chat;
}

async function run(viewer: Viewer, request: Request): Promise<unknown> {
  switch (request.cmd) {
    case "sessions":
      return host.list().map((chat) => chat.summary());

    case "open":
      return (await host.open(request.cwd)).summary();

    case "resume":
      return (await host.resume(request.sessionId, request.cwd, request.title)).summary();

    case "close":
      return { closed: await host.close(request.sessionId) };

    case "say":
      // Not awaited. A turn runs for minutes and the reply to this command is
      // "I have told the agent", not "the agent has finished" — which arrives
      // as events, to every viewer watching, including ones that connect
      // halfway through.
      void host.say(chatOr(request.session), request.text);
      return { said: true };

    case "cancel":
      await host.cancel(chatOr(request.session));
      return { cancelled: true };

    case "permit":
      chatOr(request.session).permit(request.optionId);
      return { permitted: true };

    case "mode":
      await host.setMode(chatOr(request.session), request.modeId);
      return chatOr(request.session).summary();

    case "config":
      await host.setConfig(chatOr(request.session), request.configId, request.value);
      return chatOr(request.session).summary();

    case "history": {
      // Liveness comes from the protocol, not from the filesystem.
      //
      // This used to stat transcripts under ~/.claude/projects to find the one
      // being appended to. `session/list` already carries `updatedAt`, and it is
      // the same signal from the same underlying record: measured against this
      // repo, the live conversation read 3 seconds old and the next 2500. So the
      // filesystem version was a Claude-specific reimplementation of a field the
      // agent hands over, and it is gone.
      //
      // Recency is a heuristic either way, and it decides whether to *ask*
      // before attaching, never whether to allow.
      const past = await host.history(request.cwd);
      const now = Date.now();
      return past.map((entry) => {
        const at = Date.parse(entry.updatedAt);
        return {
          ...entry,
          // A conversation this daemon is running is live by definition, with no
          // guessing involved — that case is exact and the timestamp is not.
          live: entry.open ? host.get(entry.sessionId)?.busy === true
            : Number.isFinite(at) && now - at < liveWindowMs,
        };
      });
    }

    case "commands":
      return host.commands;

    case "subscribe": {
      if (viewer.watching.has(request.session)) return { subscribed: true };
      const chat = chatOr(request.session);
      viewer.watching.add(request.session);
      // subscribe() replays what the conversation has already shown before
      // following it, so a page that has just loaded gets the conversation
      // rather than the next token of it.
      viewer.stop.set(
        request.session,
        chat.log.subscribe((line) => {
          // The log speaks in SSE lines because that is what the browser wants;
          // here the payload is what matters, so it is unwrapped and re-framed.
          const payload = line.slice("data: ".length).trimEnd();
          if (payload) {
            viewer.out.write(
              encoder.encode(
                `{"push":"event","session":${JSON.stringify(request.session)},"event":${payload}}\n`,
              ),
            );
          }
        }),
      );
      return { subscribed: true };
    }

    case "unsubscribe": {
      viewer.stop.get(request.session)?.();
      viewer.stop.delete(request.session);
      viewer.watching.delete(request.session);
      return { unsubscribed: true };
    }
  }
}

Bun.listen<undefined>({
  unix: socketPath,
  socket: {
    open(socket) {
      viewers.set(socket, {
        out: new SocketWriter(socket),
        watching: new Set(),
        stop: new Map(),
      });
      console.log(`viewer attached (${viewers.size})`);
    },

    drain(socket) {
      viewers.get(socket)?.out.drain();
    },

    data(socket, chunk) {
      const viewer = viewers.get(socket);
      if (!viewer) return;
      // Requests are newline-delimited JSON and can be split across reads, so
      // the tail is held until its newline arrives.
      pending.set(socket, (pending.get(socket) ?? "") + new TextDecoder().decode(chunk));
      const buffered = pending.get(socket) ?? "";
      const lines = buffered.split("\n");
      pending.set(socket, lines.pop() ?? "");
      for (const line of lines) {
        if (!line.trim()) continue;
        let request: Request;
        try {
          request = JSON.parse(line) as Request;
        } catch {
          console.log("unparseable command from a viewer; ignoring");
          continue;
        }
        void run(viewer, request).then(
          (data) => send(viewer, { id: request.id, ok: true, data }),
          (err: unknown) =>
            send(viewer, {
              id: request.id,
              ok: false,
              error: err instanceof Error ? err.message : String(err),
            }),
        );
      }
    },

    close(socket) {
      const viewer = viewers.get(socket);
      // Every subscription this viewer held, released — otherwise a reloaded
      // page leaks a listener per conversation it had open, and a day of
      // reloads is a conversation fanning out to a hundred dead sockets.
      for (const stop of viewer?.stop.values() ?? []) stop();
      viewers.delete(socket);
      pending.delete(socket);
      console.log(`viewer detached (${viewers.size}); the agents keep working`);
    },

    error(_socket, err) {
      console.log(`viewer error: ${err.message}`);
    },
  },
});

// Partial reads, per socket.
const pending = new Map<Socket<undefined>, string>();

console.log(`adapterd [${instanceName}] listening on ${socketPath} (pid ${process.pid})`);
