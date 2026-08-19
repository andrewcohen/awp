// tdeck's backend: a Bun server that owns the agent and serves the UI.
//
// The UI is a served page rather than something a desktop shell embeds
// directly, which keeps the shell a later decision — Electrobun wraps this
// without the frontend knowing, and if Electrobun disappoints nothing here
// changes.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { AgentHost, type Conversation } from "./agent.ts";

const here = dirname(fileURLToPath(import.meta.url));
const page = join(here, "..", "public", "index.html");
const PORT = 4317;

const host = await AgentHost.start();

// Exit rather than serve a corpse. An ACP connection whose stream has closed
// keeps looking healthy — routes answer, the page loads — until something tries
// to reach the agent, and then every request fails with "ACP connection closed"
// while the server sits there claiming to be up. Dying is the honest signal, and
// the next start brings the daemon back.
void host.closed.then(() => {
  console.error("the adapter daemon went away; exiting so it is obvious");
  process.exit(1);
});

// A fresh start has no conversations; a reattach brings its own. Opening one
// here means the page always has something to show without a "new chat" click
// being the first thing anybody does.
if (host.list().length === 0) await host.open(process.cwd());
console.log(`open http://localhost:${PORT}`);

async function jsonBody(req: Request): Promise<Record<string, unknown>> {
  try {
    return (await req.json()) as Record<string, unknown>;
  } catch {
    return {};
  }
}

const str = (v: unknown): string => (typeof v === "string" ? v : "");

// Every route below addresses one conversation. Naming the session in the
// request rather than keeping a "current" one on the server is what lets two
// browser tabs — or two panes of one window — watch different chats at once.
function chatFrom(payload: Record<string, unknown>): Conversation | null {
  return host.get(str(payload.session)) ?? null;
}

const noSuchSession = () => Response.json({ error: "no such session" }, { status: 404 });

Bun.serve({
  port: PORT,
  // Bun closes a request that has been idle for 10 seconds. For an event stream
  // watching an agent think, ten seconds of silence is normal — so the default
  // hangs up on exactly the conversations worth watching, and the page goes
  // quiet with no error anywhere. Measured: a session whose first token took
  // longer than that appeared to produce nothing at all.
  idleTimeout: 0,
  routes: {
    "/": () => new Response(Bun.file(page)),

    "/sessions": {
      GET: () => Response.json(host.list().map((chat) => chat.summary())),
      POST: async (req) => {
        const cwd = str((await jsonBody(req)).cwd) || process.cwd();
        const chat = await host.open(cwd);
        return Response.json(chat.summary());
      },
    },

    "/events": (req) => {
      const wanted = new URL(req.url).searchParams.get("session") ?? "";
      const chat = host.get(wanted) ?? host.list()[0];
      if (!chat) return noSuchSession();

      // Server-sent events rather than a websocket: the traffic is one-way and
      // the reconnect behaviour is free. What comes back the other way is a
      // handful of POSTs, which do not need a socket to carry them.
      let unsubscribe = () => {};
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          const encoder = new TextEncoder();
          unsubscribe = chat.log.subscribe((line) => {
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

    "/commands": () => Response.json(host.commands),

    "/say": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        const chat = chatFrom(payload);
        if (!chat) return noSuchSession();
        // Not awaited: a prompt runs for minutes and the POST is a doorbell,
        // not the conversation. What the agent says arrives on /events.
        void host.say(chat, str(payload.text));
        return new Response(null, { status: 204 });
      },
    },

    "/permit": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        const chat = chatFrom(payload);
        if (!chat) return noSuchSession();
        chat.permit(str(payload.optionId));
        return new Response(null, { status: 204 });
      },
    },

    "/mode": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        const chat = chatFrom(payload);
        if (!chat) return noSuchSession();
        void host.setMode(chat, str(payload.modeId));
        return new Response(null, { status: 204 });
      },
    },
  },
  fetch: () => new Response("not found", { status: 404 }),
});
