// The agent host: one long-lived process that owns the ACP adapter and lends it
// out over a unix socket.
//
// Why this exists, and why it exists *now* rather than in a later phase:
//
// An agent that is a child of the UI process dies with the UI process. Restart
// the server — which happens every few seconds during development, and once per
// update forever after — and an agent three tool calls into something loses the
// work. `loadSession` makes restarting *cheap*, because the conversation is on
// disk, but it does not make it *free*: what was in flight is gone.
//
// That is exactly what zmx protects for terminal programs, and zmx was measured
// and rejected for this: it keeps a terminal grid rather than a byte stream, so
// a 245-byte JSON-RPC line came back as 249 bytes with four carriage returns
// inserted at column boundaries and no longer parsed. Durability for a protocol
// needs every byte, once, in order — which a socket gives and a screen does not.
//
// So: zmx's architecture with a different payload. The adapter's stdio is
// bridged to a unix socket, the UI dials in, and closing the UI detaches rather
// than kills.
//
// Deliberately dumb. It moves bytes and does not parse them — no JSON-RPC
// awareness, no session tracking, no restart policy. Supervision and multi-client
// fan-out wait for evidence they are needed; what could not wait is the seam,
// because starting on stdio bakes in "agents die with the window" and moving a
// transport afterwards is the expensive kind of change.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { unlinkSync } from "node:fs";
import type { Socket } from "bun";
import { ensureRuntimeDir, socketPath } from "./paths.ts";
import { SocketWriter } from "./socketwrite.ts";

const here = dirname(fileURLToPath(import.meta.url));
const adapterBin = join(here, "..", "node_modules", ".bin", "claude-agent-acp");

ensureRuntimeDir();

// A socket file outlives the process that made it, so a crash leaves one behind
// and the next bind fails on an address nobody is listening to. Removing it
// first is safe because a *live* daemon is excluded by the lock below.
try {
  unlinkSync(socketPath);
} catch {
  // Nothing there, which is the normal case.
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

// Exactly one client at a time.
//
// The alternative — fanning one ACP connection out to several — is not a
// buffering problem but a protocol one: JSON-RPC replies are addressed to
// whoever asked, and two clients sharing a stream would each see the other's
// answers. A newcomer therefore replaces the incumbent, the same way `zmx
// attach` from inside a session moves the client rather than nesting.
let client: Socket<undefined> | null = null;
let out: SocketWriter | null = null;

// What the agent said while nobody was listening.
//
// This is the durability the whole file is for. Close the window mid-turn and
// the agent keeps working; its output lands here and is delivered when a client
// comes back. Dropping it instead would make the socket a reconnect convenience
// rather than a guarantee, which is not worth a daemon.
let detachedOutput: Uint8Array[] = [];

// Whether a client has ever attached.
//
// Stands in for "has this adapter been initialized", which is the one piece of
// protocol state a byte pipe cannot avoid caring about: ACP's `initialize` is
// sent once per connection, and a reconnecting client that sends it again gets
// an error. The daemon tells the client which case it is in and lets the client
// decide; that keeps the protocol knowledge on the protocol side.
let used = false;

adapter.stdout.pipeTo(
  new WritableStream<Uint8Array>({
    write(chunk) {
      if (out) out.write(chunk);
      // Copied on the way into the buffer: `write` hands over a view that the
      // stream is free to reuse for the next read, and this one is held across
      // an arbitrary detachment. A live client is written synchronously, so it
      // does not need the copy.
      else detachedOutput.push(new Uint8Array(chunk));
    },
  }),
).catch((err: unknown) => {
  console.log(`adapter stdout closed: ${err instanceof Error ? err.message : String(err)}`);
});

Bun.listen<undefined>({
  unix: socketPath,
  socket: {
    open(socket) {
      if (client) {
        console.log("second client attached; dropping the first");
        client.end();
      }
      client = socket;
      out = new SocketWriter(socket);

      // The handshake, ahead of the protocol stream: one JSON line saying
      // whether this adapter is fresh. The client reads it, then treats the
      // rest of the socket as ACP.
      out.write(new TextEncoder().encode(JSON.stringify({ fresh: !used }) + "\n"));
      used = true;

      if (detachedOutput.length) {
        console.log(`flushing ${detachedOutput.length} chunks buffered while detached`);
        for (const chunk of detachedOutput) out.write(chunk);
        detachedOutput = [];
      }
      console.log("client attached");
    },

    drain() {
      out?.drain();
    },

    data(_socket, chunk) {
      adapter.stdin.write(chunk);
      adapter.stdin.flush();
    },

    close(socket) {
      if (client === socket) {
        client = null;
        out = null;
        console.log("client detached; the agent keeps working");
      }
    },

    error(_socket, err) {
      console.log(`client error: ${err.message}`);
    },
  },
});

console.log(`adapterd listening on ${socketPath} (pid ${process.pid})`);
