// tdeck's UI host: a Bun server that serves the page and relays to the daemon.
//
// It owns nothing. Every conversation, every in-flight turn and the ACP
// connection itself live in the daemon, so this process can be restarted on
// every saved file — which `bun --watch` does — without a turn noticing. That
// is the whole reason for the split; see protocol.ts.
//
// The UI is a served page rather than something a desktop shell embeds
// directly, which keeps the shell a later decision.

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdirSync } from "node:fs";
import { Daemon } from "./link.ts";
import { dropDir, instanceName, port } from "./paths.ts";
import { readWorkspaces } from "./workspaces.ts";
import { liveAgents } from "./live.ts";
import type { UiEvent } from "./events.ts";
import type { Command } from "./protocol.ts";

const here = dirname(fileURLToPath(import.meta.url));

// The built frontend if there is one, else the throwaway page the experiment
// shipped with. Two reasons to keep the fallback rather than requiring a build:
// the backend stays runnable on a fresh checkout, and a broken frontend build
// leaves something that can still talk to the agent.
const bundle = join(here, "..", "frontend", "dist");
const fallbackPage = join(here, "..", "public", "index.html");
const built = await Bun.file(join(bundle, "index.html")).exists();
const page = built ? join(bundle, "index.html") : fallbackPage;

// Where dropped files land. Counter rather than a timestamp so the names are
// stable to read in a log and unique within a run.
mkdirSync(dropDir, { recursive: true });
let dropCount = 1;

const daemon = await Daemon.connect();

// Exit rather than serve a corpse. Without the daemon there is no agent, and a
// server that kept answering would look healthy while every request failed.
void daemon.closed.then(() => {
  console.error("the adapter daemon went away; exiting so it is obvious");
  process.exit(1);
});

type SessionSummary = {
  sessionId: string;
  cwd: string;
  busy: boolean;
  waitingOn: string | null;
};

// A fresh daemon has no conversations. Opening one here means the page always
// has something to show without a "new chat" click being the first thing
// anybody does — and only when there are none, so a server restart no longer
// adds a conversation each time.
const existing = await daemon.request<SessionSummary[]>({ cmd: "sessions" });
if (existing.length === 0) await daemon.request({ cmd: "open", cwd: process.cwd() });

console.log(
  `${built ? "serving the built frontend" : "serving the fallback page (run: bun run build)"} — ` +
    `tdeck [${instanceName}] on http://localhost:${port}`,
);

async function jsonBody(req: Request): Promise<Record<string, unknown>> {
  try {
    return (await req.json()) as Record<string, unknown>;
  } catch {
    return {};
  }
}

const str = (v: unknown): string => (typeof v === "string" ? v : "");

// Relays one command and answers with whatever the daemon said. The daemon owns
// the vocabulary, so most routes below are this and nothing else.
async function relay(command: Command): Promise<Response> {
  try {
    return Response.json(await daemon.request(command));
  } catch (err) {
    // 409 rather than 500: the interesting failures are "that session is gone"
    // and "that conversation refused", which are the caller's situation rather
    // than the server falling over.
    return Response.json(
      { error: err instanceof Error ? err.message : String(err) },
      { status: 409 },
    );
  }
}

Bun.serve({
  port,
  // Bun closes a request that has been idle for 10 seconds. For an event stream
  // watching an agent think, ten seconds of silence is normal — so the default
  // hangs up on exactly the conversations worth watching, and the page goes
  // quiet with no error anywhere.
  idleTimeout: 0,
  routes: {
    "/": () => new Response(Bun.file(page)),

    "/sessions": {
      GET: () => relay({ cmd: "sessions" }),
      POST: async (req) =>
        relay({ cmd: "open", cwd: str((await jsonBody(req)).cwd) || process.cwd() }),
    },

    "/events": (req) => {
      const session = new URL(req.url).searchParams.get("session") ?? "";
      if (!session) return Response.json({ error: "session is required" }, { status: 400 });

      // Server-sent events rather than a websocket: the traffic is one-way and
      // the reconnect behaviour is free. What comes back the other way is a
      // handful of POSTs, which do not need a socket to carry them.
      let unsubscribe = () => {};
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          const encoder = new TextEncoder();
          unsubscribe = daemon.subscribe(session, (event: UiEvent) => {
            try {
              controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`));
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

    "/commands": () => relay({ cmd: "commands" }),

    "/history": (req) => {
      const cwd = new URL(req.url).searchParams.get("cwd") ?? "";
      if (!cwd) return Response.json({ error: "cwd is required" }, { status: 400 });
      return relay({ cmd: "history", cwd });
    },

    "/resume": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        return relay({
          cmd: "resume",
          sessionId: str(payload.sessionId),
          cwd: str(payload.cwd),
          title: str(payload.title) || "resumed",
        });
      },
    },

    "/close": {
      POST: async (req) => relay({ cmd: "close", sessionId: str((await jsonBody(req)).sessionId) }),
    },

    "/say": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        return relay({ cmd: "say", session: str(payload.session), text: str(payload.text) });
      },
    },

    "/cancel": {
      POST: async (req) => relay({ cmd: "cancel", session: str((await jsonBody(req)).session) }),
    },

    "/permit": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        return relay({
          cmd: "permit",
          session: str(payload.session),
          optionId: str(payload.optionId),
        });
      },
    },

    "/mode": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        return relay({ cmd: "mode", session: str(payload.session), modeId: str(payload.modeId) });
      },
    },

    "/config": {
      POST: async (req) => {
        const payload = await jsonBody(req);
        const value = payload.value;
        if (typeof value !== "string" && typeof value !== "boolean") {
          return Response.json({ error: "value must be a string or a boolean" }, { status: 400 });
        }
        return relay({
          cmd: "config",
          session: str(payload.session),
          configId: str(payload.configId),
          value,
        });
      },
    },

    // A dropped file, turned into somewhere the agent can look.
    //
    // A browser drop hands the page a File with the bytes and no location, and
    // an agent with a Read tool needs a location. So the bytes come here and a
    // path goes back. Under the runtime directory rather than the system temp
    // directory, because these outlive the request — the agent reads the file
    // later in the turn, and a cleaner running in between would make a dropped
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

    // A file on disk, for the page to show.
    //
    // An agent citing a screenshot writes an absolute path, and a browser cannot
    // open file:// from an http page — it fails silently, which is the worst way
    // for an image to be missing. This is the only party that can read the disk.
    //
    // Deliberately narrow. Media only, by content type, because the job is
    // showing a picture rather than exposing a filesystem over HTTP: without
    // that check this route would serve ~/.ssh/id_rsa to anything that could
    // reach the port. It is still a real widening of what the port does, which
    // is why it is worth saying out loud — the port is localhost-only, and the
    // people who can reach it can already read the files.
    "/file": async (req) => {
      const path = new URL(req.url).searchParams.get("path") ?? "";
      if (!path.startsWith("/")) {
        return Response.json({ error: "an absolute path is required" }, { status: 400 });
      }
      const file = Bun.file(path);
      if (!(await file.exists())) return new Response("not found", { status: 404 });
      const type = file.type.split(";")[0] ?? "";
      if (!/^(image|video|audio)\//.test(type)) {
        return Response.json({ error: `refusing to serve ${type || "unknown"}` }, { status: 415 });
      }
      return new Response(file, { headers: { "content-type": type } });
    },

    // awp's workspaces, annotated with what tdeck and the terminal are each
    // holding. The annotations are the only thing tdeck adds; the rest is awp's
    // state, read and passed through.
    "/workspaces": async () => {
      const [spaces, agents, sessions] = await Promise.all([
        readWorkspaces(),
        liveAgents(),
        daemon.request<SessionSummary[]>({ cmd: "sessions" }),
      ]);
      const open = new Map(sessions.map((chat) => [chat.cwd, chat]));
      const busyDirs = new Set(agents.map((agent) => agent.dir));
      return Response.json(
        spaces.map((workspace) => {
          const chat = open.get(workspace.path);
          return {
            ...workspace,
            sessionId: chat?.sessionId,
            terminalAgent: busyDirs.has(workspace.path),
            // Where tdeck holds the conversation, the protocol is the truth and
            // awp's stored status is a stale echo of a different agent. Where it
            // does not, the store is all there is — those are agents nobody is
            // speaking ACP to, which is what the hooks were written for.
            status: chat
              ? chat.waitingOn
                ? "waiting"
                : chat.busy
                  ? "working"
                  : "idle"
              : workspace.status,
            waitingOn: chat?.waitingOn ?? null,
          };
        }),
      );
    },
  },

  // Anything not matched above is a bundle asset — the hashed JS, CSS and font
  // files Vite emits. Resolved against the build directory and nothing else, so
  // a crafted path cannot walk out of it.
  fetch: async (req) => {
    if (!built) return new Response("not found", { status: 404 });
    const name = new URL(req.url).pathname;

    // Client-side routes — /s/<session>, /w/<project>/<workspace> — are views,
    // not files. Reloading on one has to serve the page rather than 404, or a
    // link only works when followed from inside the app, which is the opposite
    // of the point of having links.
    if (name.startsWith("/s/") || name.startsWith("/w/")) return new Response(Bun.file(page));

    const path = join(bundle, name);
    if (!path.startsWith(bundle)) return new Response("not found", { status: 404 });
    const file = Bun.file(path);
    return (await file.exists()) ? new Response(file) : new Response("not found", { status: 404 });
  },
});
