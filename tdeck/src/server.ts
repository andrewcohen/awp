// tdeck's backend: a Bun server that owns the agent and serves the UI.
//
// The UI is a served page rather than something a desktop shell embeds
// directly, which keeps the shell a later decision — Electrobun wraps this
// without the frontend knowing, and if Electrobun disappoints nothing here
// changes.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { AgentHost } from "./agent.ts";
import { EventLog } from "./events.ts";

const here = dirname(fileURLToPath(import.meta.url));
const page = join(here, "..", "public", "index.html");
const PORT = 4317;

const log = new EventLog();
const host = await AgentHost.start(log, process.cwd());

async function jsonBody(req: Request): Promise<Record<string, unknown>> {
  try {
    return (await req.json()) as Record<string, unknown>;
  } catch {
    return {};
  }
}

const str = (v: unknown): string => (typeof v === "string" ? v : "");

Bun.serve({
  port: PORT,
  routes: {
    "/": () => new Response(Bun.file(page)),

    "/events": () => {
      // Server-sent events rather than a websocket: the traffic is one-way and
      // the reconnect behaviour is free. What comes back the other way is a
      // handful of POSTs, which do not need a socket to carry them.
      let unsubscribe = () => {};
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          const encoder = new TextEncoder();
          unsubscribe = log.subscribe((line) => {
            try {
              controller.enqueue(encoder.encode(line));
            } catch {
              // The viewer went away mid-write; cancel() will clean up.
            }
          });
        },
        cancel() {
          unsubscribe();
        },
      });
      return new Response(body, {
        headers: {
          "content-type": "text/event-stream",
          "cache-control": "no-cache",
          connection: "keep-alive",
        },
      });
    },

    "/modes": () => Response.json(host.modes ?? { availableModes: [], currentModeId: null }),

    "/commands": () => Response.json(host.commands),

    "/say": {
      POST: async (req) => {
        // Not awaited: a prompt runs for minutes and the POST is a doorbell,
        // not the conversation. What the agent says arrives on /events.
        void host.say(str((await jsonBody(req)).text));
        return new Response(null, { status: 204 });
      },
    },

    "/permit": {
      POST: async (req) => {
        host.permit(str((await jsonBody(req)).optionId));
        return new Response(null, { status: 204 });
      },
    },

    "/mode": {
      POST: async (req) => {
        void host.setMode(str((await jsonBody(req)).modeId));
        return new Response(null, { status: 204 });
      },
    },
  },
  fetch: () => new Response("not found", { status: 404 }),
});

console.log(`open http://localhost:${PORT}`);
