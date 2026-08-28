// The shell, driven and looked at.
//
// ── why this exists, and what it replaces ─────────────────────────────────
//
// AGENTS.md says it plainly: no gate can tell you the pane is right. Under
// electrobun the answer was Playwright's WebKit, because the window rendered in
// WKWebView and a Chromium screenshot would have been a picture of a different
// renderer. **That argument is gone with the port** — the window is Chromium
// now, and the engine that can be driven headlessly is the engine that ships.
// So the harness is Electron itself, which is stronger again: it is not a
// similar engine, it is the same binary, with the same preloads and the same
// `app://` scheme.
//
// It answers four questions a screenshot cannot, and then takes the screenshot:
//
//   errors      a blank window and a broken window look identical
//   scroll      scrollWidth === clientWidth on both axes. A scrollbar is a
//               layout that has been mis-sized, and it is invisible in a
//               picture of content that fits
//   canvas      present, so "the emulator failed to start" and "the emulator
//               started and drew the wrong thing" are different findings
//   bridge      the native webview, all the way through: made, told to run a
//               script, and the script's answer arriving back on host-message.
//               That chain is three processes and no test reaches it
//
// **`#/` and nothing else.** The fixture attaches to no session. A route naming
// a workspace would make this a client attaching to somebody's terminal, and a
// session takes its size from whoever is looking at it — AGENTS.md records a
// person's window reflowing under them because a probe opened a route.
//
// **One appearance per process.** Both in one run was the first shape and it
// does not work: the second window answered `ERR_FAILED (-2)` on a URL the
// first had just loaded fine, and with a retry in front of it the load simply
// never settled. Rather than time out a teardown nothing here owns, the scheme
// is an argument and `probe:shell` runs the binary twice. It is the same rule
// AGENTS.md already states about the drag probe — one page per gesture, or
// measure nothing — and a fresh process carries no state between the two.
//
// Run it with `bun run probe:shell` from the app, after a build.

import { writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { BrowserWindow, app, ipcMain, nativeTheme } from "electron";
import { CH } from "./channels";
import { RENDERER_URL, declareScheme, serveRenderer } from "./protocol";
import { installWebviews } from "./webviews";

const here = import.meta.dirname;
const shots = process.env.AMOEBA_SHOT_DIR ?? here;

declareScheme();

const wait = (ms: number): Promise<void> => new Promise((done) => setTimeout(done, ms));

/** What one appearance answers. Every field is a check, not a note. */
const look = async (window: BrowserWindow) =>
  window.webContents.executeJavaScript(`(() => {
    const el = document.documentElement;
    const canvas = document.querySelector("canvas");
    const root = document.querySelector("#root")?.firstElementChild;
    return {
      scroll: [el.scrollWidth, el.clientWidth, el.scrollHeight, el.clientHeight],
      canvas: canvas ? [canvas.width, canvas.height] : null,
      rootBg: root ? getComputedStyle(root).backgroundColor : null,
      bridge: typeof window.awpHost === "object",
      drag: [...document.querySelectorAll(".awp-app-region-drag")]
        .map((one) => getComputedStyle(one).webkitAppRegion),
    };
  })()`) as Promise<unknown>;

/**
 * The native webview, from asked-for to answered-back.
 *
 * Driven from the page rather than from here, deliberately: `awpHost` is what
 * the renderer actually has, so a probe calling into the main process directly
 * would be testing a path the panel does not use.
 */
const bridgeRound = async (window: BrowserWindow): Promise<string> =>
  new Promise((done) => {
    const timer = setTimeout(() => done("no host-message inside two seconds"), 2000);
    ipcMain.once(CH.fromGuest, (_event, payload: unknown) => {
      clearTimeout(timer);
      done(JSON.stringify(payload));
    });
    void window.webContents.executeJavaScript(`(async () => {
      const id = await window.awpHost.createWebview({ url: "about:blank" });
      window.awpHost.setWebviewBounds(id, { x: 0, y: 0, width: 10, height: 10 });
      // A gap for the page to exist. about:blank is immediate, and the preload
      // running is what is being checked — asking too early would report the
      // bridge missing when it is only late.
      setTimeout(() => {
        // Hidden and back, because that is the overlay path: a modal opening is
        // the panel asking for the view to stop being drawn, and it is the one
        // call with no visible evidence of its own — the page looks the same
        // afterwards whether or not it ever went away.
        window.awpHost.callWebview(id, "setVisible", false);
        window.awpHost.callWebview(id, "setVisible", true);
        window.awpHost.callWebview(id, "executeJavascript",
          "window.__awpSendToHost({ from: 'awp-annotate', selector: 'probe' })");
        setTimeout(() => window.awpHost.destroyWebview(id), 500);
      }, 300);
      return id;
    })()`);
  });

/** Which appearance this run measures. */
const scheme = process.env.AMOEBA_SHOT_SCHEME === "light" ? "light" : "dark";

void app.whenReady().then(async () => {
  serveRenderer(resolve(here, "..", "renderer"));
  installWebviews(join(here, "preload-guest.cjs"));

  // The window's own preference, so `prefers-color-scheme` and `light-dark()`
  // both follow. A palette that merely happens to be dark passes the dark shot;
  // only the two runs differing proves the preference is being read.
  nativeTheme.themeSource = scheme;

  const errors: Array<string> = [];
  const window = new BrowserWindow({
    width: 1200,
    height: 760,
    show: false,
    titleBarStyle: "hiddenInset",
    webPreferences: {
      preload: join(here, "preload-host.cjs"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  window.webContents.on("console-message", (event) => {
    if (event.level === "error") {
      errors.push(event.message);
    }
  });
  window.webContents.on("render-process-gone", (_event, details) =>
    errors.push(`render process gone: ${details.reason}`),
  );

  // The fixture, and nothing else. See the note above.
  await window.loadURL(`${RENDERER_URL}#/`);
  // The wasm compiles, then the fixture is written, then the render loop
  // paints. Waited for rather than assumed.
  await wait(2500);

  const found = await look(window);
  const round = await bridgeRound(window);
  console.log(scheme, JSON.stringify(found), "webview:", round, "errors:", JSON.stringify(errors));

  const shot = await window.webContents.capturePage();
  await writeFile(join(shots, `shell-${scheme}.png`), shot.toPNG());
  window.destroy();
  app.quit();
});
