// tdeck's backend: a Bun server that owns the agent and serves the UI.
//
// The UI is a served page rather than something a desktop shell embeds
// directly, which keeps the shell a later decision — Electrobun wraps this
// without the frontend knowing, and if Electrobun disappoints nothing here
// changes.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdirSync } from "node:fs";
import { AgentHost, type Conversation } from "./agent.ts";
import { runtimeDir } from "./paths.ts";
import { readWorkspaces } from "./workspaces.ts";

const here = dirname(fileURLToPath(import.meta.url));
const PORT = 4317;

// The built frontend if there is one, else the throwaway page the experiment
// shipped with. Two reasons to keep the fallback rather than requiring a build:
// the backend stays runnable on a fresh checkout, and a broken frontend build
// leaves something that can still talk to the agent.
const bundle = join(here, "..", "frontend", "dist");
const fallbackPage = join(here, "..", "public", "index.html");
const built = await Bun.file(join(bundle, "index.html")).exists();
const page = built ? join(bundle, "index.html") : fallbackPage;
console.log(built ? "serving the built frontend" : "serving the fallback page (run: bun run build)");

// Where dropped files land. Counter rather than a timestamp so the names are
// stable to read in a log and unique within a run.
const dropDir = join(runtimeDir, "drops");
mkdirSync(dropDir, { recursive: true });
let dropCount = 1;

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

    // awp's workspaces, annotated with which of them a conversation is already
    // open on. The annotation is the only thing tdeck adds: everything else is
    // awp's state, read and passed through.
    "/workspaces": async () => {
      const open = new Map(host.list().map((chat) => [chat.cwd, chat.sessionId]));
      const workspaces = (await readWorkspaces()).map((workspace) => ({
        ...workspace,
        sessionId: open.get(workspace.path),
      }));
      return Response.json(workspaces);
    },

    // A dropped file, turned into somewhere the agent can look.
    //
    // gdeck got real OS paths from the window manager, because a native drop
    // carries one. A browser drop does not: it hands the page a File with the
    // bytes and no location, and an agent with a Read tool needs a location. So
    // the bytes come here and a path goes back.
    //
    // Under ~/.awp/tdeck/drops rather than the system temp directory, because
    // these outlive the request — the agent reads the file at some later point
    // in the turn, and a cleaner that runs in between would make a dropped
    // screenshot intermittently unreadable.
    "/upload": {
      POST: async (req) => {
        const form = await req.formData();
        const paths: string[] = [];
        for (const entry of form.getAll("file")) {
          if (!(entry instanceof File)) continue;
          // The name comes from the browser, so it is untrusted: basename it,
          // and prefix a counter so two screenshots taken in the same second do
          // not overwrite each other.
          const safe = (entry.name.split("/").pop() ?? "file").replace(/[^\w.\-]/g, "_");
          const path = join(dropDir, `${dropCount++}-${safe}`);
          await Bun.write(path, entry);
          paths.push(path);
        }
        return Response.json({ paths });
      },
    },

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

    // One route for every setting the agent exposes — model, effort, permission
    // mode, fast mode. They are one mechanism in the protocol, so they are one
    // route here rather than a hand-built endpoint per setting.
    "/config": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        const chat = chatFrom(payload);
        if (!chat) return noSuchSession();
        const value = payload.value;
        if (typeof value !== "string" && typeof value !== "boolean") {
          return Response.json({ error: "value must be a string or a boolean" }, { status: 400 });
        }
        await host.setConfig(chat, str(payload.configId), value);
        return Response.json(chat.summary());
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
  // Anything not matched above is a bundle asset — the hashed JS, CSS and font
  // files Vite emits. Resolved against the build directory and nothing else, so
  // a crafted path cannot walk out of it.
  fetch: async (req) => {
    if (!built) return new Response("not found", { status: 404 });
    const name = new URL(req.url).pathname;
    const path = join(bundle, name);
    if (!path.startsWith(bundle)) return new Response("not found", { status: 404 });
    const file = Bun.file(path);
    return (await file.exists()) ? new Response(file) : new Response("not found", { status: 404 });
  },
});
