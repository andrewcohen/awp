// The client side of the socket: find or start the daemon, dial it, and hand
// back something ndJsonStream can wrap.
//
// The awkward part is the handshake. The daemon writes one JSON line before the
// protocol stream, saying whether the adapter is fresh, and that line has to be
// consumed here rather than by the ACP parser — which would reject it. So the
// socket's first chunk is split: the line goes to the caller, the remainder
// becomes the first bytes of the protocol stream.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Socket, SocketHandler } from "bun";
import {
  daemonLogPath,
  ensureRuntimeDir,
  socketPath,
  statePath,
} from "./paths.ts";

const here = dirname(fileURLToPath(import.meta.url));
const daemonScript = join(here, "adapterd.ts");

export type Link = {
  // Whether this adapter has never had a client. False means the agent is
  // already initialized and already has our session, so the caller must skip
  // `initialize` and reuse the session it recorded.
  fresh: boolean;
  readable: ReadableStream<Uint8Array>;
  writable: WritableStream<Uint8Array>;
};

// What the server needs in order to pick a conversation back up. Small enough
// to be a file; the agent owns everything that actually matters.
export type State = { sessionId: string; modes: unknown };

export async function loadState(): Promise<State | null> {
  try {
    return (await Bun.file(statePath).json()) as State;
  } catch {
    return null;
  }
}

export async function saveState(state: State): Promise<void> {
  ensureRuntimeDir();
  await Bun.write(statePath, JSON.stringify(state, null, 2));
}

// A socket with no listener behind it, and a socket that is simply not there,
// are both "no daemon yet". Anything else is a bug here rather than a race, and
// swallowing it costs an hour: an invalid handler set reports as a failed
// connection, which reads exactly like a daemon that never started.
function isAbsent(err: unknown): boolean {
  const code = (err as { code?: unknown } | null)?.code;
  return code === "ENOENT" || code === "ECONNREFUSED";
}

async function dial(
  handlers: SocketHandler<undefined>,
): Promise<Socket<undefined> | null> {
  try {
    return await Bun.connect<undefined>({ unix: socketPath, socket: handlers });
  } catch (err) {
    if (isAbsent(err)) return null;
    throw err;
  }
}

function startDaemon(): void {
  ensureRuntimeDir();
  const log = Bun.file(daemonLogPath);
  Bun.spawn([process.execPath, daemonScript], {
    stdin: "ignore",
    stdout: log,
    stderr: log,
    // The whole point: it must not die when this process does.
    detached: true,
  }).unref();
}

export async function connect(): Promise<Link> {
  let controller: ReadableStreamDefaultController<Uint8Array> | null = null;
  const readable = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
    },
  });

  // Resolved by the first chunk, which always contains the handshake line
  // because the daemon writes it alone before anything else can arrive.
  let announce: (fresh: boolean) => void = () => {};
  const handshake = new Promise<boolean>((resolve) => {
    announce = resolve;
  });

  let sawHandshake = false;
  // Bytes, not a string. Splitting the handshake off by decoding the chunk and
  // re-encoding the remainder would corrupt any multi-byte character that
  // happened to straddle a chunk boundary — a whole class of bug that only
  // shows up once someone puts an emoji in a prompt. The newline is a single
  // byte, so the split can be done without decoding anything but the line.
  let pending = new Uint8Array(0);
  const NEWLINE = 0x0a;

  // Built before dialling, not attached afterwards: the daemon writes its
  // handshake the instant a client opens, so a socket that connects before its
  // data handler exists can drop the first thing it is sent.
  const handlers: SocketHandler<undefined> = {
    data(_s: Socket<undefined>, chunk: Uint8Array) {
      if (sawHandshake) {
        controller?.enqueue(chunk);
        return;
      }

      const merged = new Uint8Array(pending.length + chunk.length);
      merged.set(pending);
      merged.set(chunk, pending.length);
      const newline = merged.indexOf(NEWLINE);
      if (newline < 0) {
        pending = merged;
        return;
      }

      sawHandshake = true;
      pending = new Uint8Array(0);
      let fresh = true;
      try {
        const line = new TextDecoder().decode(merged.subarray(0, newline));
        fresh = Boolean((JSON.parse(line) as { fresh?: unknown }).fresh);
      } catch {
        // A daemon that speaks no handshake is one from an older build.
        // Treating it as fresh fails loudly at `initialize` rather than
        // quietly doing the wrong thing.
      }
      announce(fresh);

      const rest = merged.subarray(newline + 1);
      if (rest.length) controller?.enqueue(rest);
    },
    close() {
      controller?.close();
    },
    error(_s: Socket<undefined>, err: Error) {
      controller?.error(err);
    },
  };

  let socket = await dial(handlers);
  if (!socket) {
    console.log("no adapter daemon; starting one");
    startDaemon();
    // The daemon spawns the adapter before it binds, so the wait covers a
    // process start rather than just a bind.
    for (let attempt = 0; attempt < 100 && !socket; attempt++) {
      await Bun.sleep(100);
      socket = await dial(handlers);
    }
    if (!socket)
      throw new Error(`adapter daemon did not come up; see ${daemonLogPath}`);
  }

  const live = socket;
  const writable = new WritableStream<Uint8Array>({
    write(chunk) {
      live.write(chunk);
      live.flush();
    },
  });

  return { fresh: await handshake, readable, writable };
}
