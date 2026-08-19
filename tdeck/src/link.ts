// Dialling the daemon, and speaking the protocol in protocol.ts to it.
//
// Much smaller than it was. When the UI server held the ACP client this file
// had to split a handshake off the front of a byte stream, decide whether the
// adapter was fresh, and keep a state file so sessions could be reattached.
// All of that existed to survive a restart; the daemon now owns the
// conversations, so a restart loses nothing and none of it is needed.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { openSync } from "node:fs";
import type { Socket, SocketHandler } from "bun";
import { SocketWriter } from "./socketwrite.ts";
import { daemonLogPath, ensureRuntimeDir, socketPath } from "./paths.ts";
import { isPush, type Command, type Frame } from "./protocol.ts";
import type { UiEvent } from "./events.ts";

const here = dirname(fileURLToPath(import.meta.url));
const daemonScript = join(here, "adapterd.ts");

// A socket with no listener behind it, and a socket that is simply not there,
// are both "no daemon yet". Anything else is a bug here rather than a race, and
// swallowing it costs an hour: an invalid handler set reports as a failed
// connection, which reads exactly like a daemon that never started.
function isAbsent(err: unknown): boolean {
  const code = (err as { code?: unknown } | null)?.code;
  return code === "ENOENT" || code === "ECONNREFUSED";
}

function startDaemon(): void {
  ensureRuntimeDir();
  // Appended, via a raw fd, rather than Bun.file(path) — which writes from
  // offset zero. A new daemon that logs fewer bytes than the last one leaves
  // the previous run's tail sitting after its own, so `tail` shows two
  // processes' output as though it were one story.
  const log = openSync(daemonLogPath, "a");
  Bun.spawn([process.execPath, daemonScript], {
    stdin: "ignore",
    stdout: log,
    stderr: log,
    // Verified: a detached child survives a normal parent exit. It does NOT
    // survive `bun --watch`, which kills the process group on reload, so in
    // development the daemon is started separately (`bun run daemon`).
    detached: true,
    // Inherited so TDECK_INSTANCE reaches the daemon: a development instance
    // that auto-started a default-instance daemon would talk to the agents it
    // was supposed to be isolated from.
    env: process.env,
  }).unref();
}

export class Daemon {
  private next = 1;
  private readonly waiting = new Map<
    number,
    { resolve: (data: unknown) => void; reject: (err: Error) => void }
  >();
  // One set of handlers per subscribed conversation.
  private readonly listeners = new Map<string, Set<(event: UiEvent) => void>>();
  // What each subscribed conversation has shown since this process attached.
  //
  // The daemon replays on subscribe, but only for the first subscriber: the
  // second one here joins an existing set and no command goes down the wire, so
  // without this a page opened beside another — or a tab switched away from and
  // back — shows an empty conversation until the next token arrives. Found by
  // watching exactly that happen.
  //
  // Grows with the conversation, which is the same thing the daemon's own log
  // does, and is dropped when the last subscriber leaves.
  private readonly seen = new Map<string, UiEvent[]>();
  private onSessions: () => void = () => {};

  private constructor(
    private readonly out: SocketWriter,
    readonly closed: Promise<void>,
  ) {}

  static async connect(): Promise<Daemon> {
    let hangUp: () => void = () => {};
    const closed = new Promise<void>((resolve) => {
      hangUp = resolve;
    });

    let daemon!: Daemon;
    let pending = "";

    const handlers: SocketHandler<undefined> = {
      data(_socket: Socket<undefined>, chunk: Uint8Array) {
        pending += new TextDecoder().decode(chunk);
        const lines = pending.split("\n");
        pending = lines.pop() ?? "";
        for (const line of lines) {
          if (!line.trim()) continue;
          try {
            daemon.receive(JSON.parse(line) as Frame);
          } catch {
            console.error("unparseable frame from the daemon; ignoring");
          }
        }
      },
      drain() {
        daemon.drain();
      },
      close() {
        hangUp();
      },
      error(_socket: Socket<undefined>, err: Error) {
        console.error(`daemon socket error: ${err.message}`);
        hangUp();
      },
    };

    const dial = async (): Promise<Socket<undefined> | null> => {
      try {
        return await Bun.connect<undefined>({ unix: socketPath, socket: handlers });
      } catch (err) {
        if (isAbsent(err)) return null;
        throw err;
      }
    };

    let socket = await dial();
    if (!socket) {
      console.log("no adapter daemon; starting one");
      startDaemon();
      for (let attempt = 0; attempt < 100 && !socket; attempt++) {
        await Bun.sleep(100);
        socket = await dial();
      }
      if (!socket) throw new Error(`adapter daemon did not come up; see ${daemonLogPath}`);
    }

    daemon = new Daemon(new SocketWriter(socket), closed);
    return daemon;
  }

  drain(): void {
    this.out.drain();
  }

  private receive(frame: Frame): void {
    if (isPush(frame)) {
      if (frame.push === "sessions") {
        this.onSessions();
        return;
      }
      this.seen.get(frame.session)?.push(frame.event);
      for (const listener of this.listeners.get(frame.session) ?? []) listener(frame.event);
      return;
    }
    const waiter = this.waiting.get(frame.id);
    if (!waiter) return;
    this.waiting.delete(frame.id);
    if (frame.ok) waiter.resolve(frame.data);
    else waiter.reject(new Error(frame.error));
  }

  request<T>(command: Command): Promise<T> {
    const id = this.next++;
    return new Promise<T>((resolve, reject) => {
      this.waiting.set(id, { resolve: resolve as (data: unknown) => void, reject });
      this.out.write(new TextEncoder().encode(JSON.stringify({ id, ...command }) + "\n"));
    });
  }

  // Told when a conversation is opened, closed, or moves between busy and idle,
  // so the UI's list can be re-read without polling for it.
  onSessionsChanged(fn: () => void): void {
    this.onSessions = fn;
  }

  // Follow one conversation. Returns an unsubscribe.
  //
  // Subscriptions are counted, because two browser tabs on the same chat are an
  // ordinary thing and the first one to close must not silence the second.
  subscribe(session: string, onEvent: (event: UiEvent) => void): () => void {
    let listeners = this.listeners.get(session);
    if (!listeners) {
      listeners = new Set();
      this.listeners.set(session, listeners);
      this.seen.set(session, []);
      void this.request({ cmd: "subscribe", session });
    } else {
      // A later subscriber gets what the earlier one already received. Sent
      // synchronously, before any live event can arrive, so the order the
      // caller sees is the order things happened.
      for (const event of this.seen.get(session) ?? []) onEvent(event);
    }
    listeners.add(onEvent);

    return () => {
      const set = this.listeners.get(session);
      if (!set) return;
      set.delete(onEvent);
      if (set.size > 0) return;
      this.listeners.delete(session);
      this.seen.delete(session);
      void this.request({ cmd: "unsubscribe", session });
    };
  }
}
