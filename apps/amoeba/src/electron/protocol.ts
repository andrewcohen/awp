import { existsSync } from "node:fs";
import { join, normalize, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";
import { net, protocol } from "electron";

// Where a built renderer is served from.
//
// ── why not file:// ────────────────────────────────────────────────────────
//
// Electrobun served the copied renderer over `views://`, and the shape of that
// choice carries over rather than the scheme name. `file://` is the obvious
// substitute and it is the wrong one, for a reason that is invisible until the
// diff panel is opened:
//
//   @pierre/diffs tokenizes in a worker pool  →  new Worker(url, { type: "module" })
//   a module worker from file://              →  refused, opaque origin
//
// AGENTS.md already records what a missing worker pool looks like — the same
// pixels, later — so this would have presented as "the built app feels slow"
// and nothing else. A registered standard scheme has a real origin, so workers,
// fetch and the module graph all behave as they do against the dev server.
//
// The privileges are the whole reason the scheme exists, so they are declared
// before `app.ready` — Electron refuses `registerSchemesAsPrivileged` after it,
// and a scheme registered without them is `file://` with a different name.

export const APP_SCHEME = "app";

/** `app://renderer/index.html` — one host, mirroring electrobun's `views://renderer/`. */
export const RENDERER_URL = `${APP_SCHEME}://renderer/index.html`;

export const declareScheme = (): void => {
  protocol.registerSchemesAsPrivileged([
    {
      scheme: APP_SCHEME,
      privileges: {
        standard: true,
        secure: true,
        supportFetchAPI: true,
        corsEnabled: true,
        stream: true,
      },
    },
  ]);
};

/**
 * Serve the built renderer.
 *
 * @param root  the directory Vite wrote, which is `dist/renderer`.
 *
 * A deep path answers with index.html rather than 404, which is what a history
 * fallback is — the router uses a hash and does not need one, and it is here
 * because the cost of not having it is a white window with a console message,
 * and the cost of having it is one `existsSync`.
 */
export const serveRenderer = (root: string): void => {
  protocol.handle(APP_SCHEME, (request) => {
    const url = new URL(request.url);
    if (url.hostname !== "renderer") {
      return new Response("not found", { status: 404 });
    }
    // Refuse to leave the directory. `..` in a URL path is normalised by the
    // URL parser, and this is the second line rather than the first: an encoded
    // separator that survives it must not become a path that escapes.
    const wanted = normalize(decodeURIComponent(url.pathname)).replace(/^[/\\]+/u, "");
    const file = resolve(root, wanted);
    if (file !== root && !file.startsWith(root + sep)) {
      return new Response("forbidden", { status: 403 });
    }
    const on = existsSync(file) ? file : join(root, "index.html");
    return net.fetch(pathToFileURL(on).toString());
  });
};
