// amoeba's main process.
//
// Opens one window and points it at the renderer. That is close to all it
// should ever do: the IPC here is reserved for what only a native process can —
// the window, the menus, the native webview the web panel needs — and the
// pane's byte stream goes straight from the renderer to the daemon's socket
// instead, so the path where latency is felt has one hop and one schema.
//
// In development the window loads Vite's dev server, so a renderer edit is a
// reload rather than a rebuild. In a built app it loads the copied dist over
// `app://`, which is a registered standard scheme rather than `file://` — see
// protocol.ts for the worker pool that decides that.
//
// ── the daemon is still a separate process ────────────────────────────────
//
// It runs under Bun and this runs under Electron's Node, so embedding it was
// never on the table — but that is not the reason it stays out. A daemon that
// is a child of the window cannot outlive it, and the whole point of zmx owning
// the sessions is that closing a window is not the same as ending the work.

import { join, resolve } from "node:path";
import { BrowserWindow, app, shell } from "electron";
import { installMenu } from "./menu";
import { RENDERER_URL, declareScheme, serveRenderer } from "./protocol";
import { forgetWindow, installWebviews } from "./webviews";

const here = import.meta.dirname;

/**
 * How long `app.quit()` is given before the process is ended outright.
 *
 * Long enough that an ordinary teardown — a few hundred milliseconds at worst,
 * measured — is never cut short, and short enough that nobody reaches for Force
 * Quit. See the note where it is used for why abruptness is cheap here.
 */
const QUIT_GRACE_MS = 3000;

/** Where the renderer lives, which differs between dev and a built app. */
const rendererUrl = (): string => {
  const devServer = process.env.AMOEBA_DEV_SERVER;
  return devServer !== undefined && devServer !== "" ? devServer : RENDERER_URL;
};

// Declared before `app.ready`, which is the only moment Electron accepts it.
declareScheme();

const create = (): BrowserWindow => {
  const window = new BrowserWindow({
    title: "amoeba",
    x: 100,
    y: 100,
    width: 1200,
    height: 800,
    // Painted before the renderer's first frame, to the same hue `global.css`
    // uses for `colors.base` in dark. Without it a launch flashes white.
    backgroundColor: "#1e2030",
    show: false,
    // ── inset controls, and the reason `hidden` was tried and reverted ──────
    //
    // `hidden` works — the lights go and the top bar starts at the window's own
    // edge — and it was reverted within a minute, because **a tiling window
    // manager stops managing the window**. AeroSpace and the rest pick windows
    // out through the accessibility API and skip anything that is not a
    // *standard* window; an untitled one is not. On a 3440x1440 display:
    //
    //   hiddenInset   3424x1393    tiled to the screen, less the gaps
    //   hidden        whatever it was last dragged to
    //
    // Three circles are a small price for being a window somebody's tiler will
    // manage, and the person running the tiler is the person using this.
    titleBarStyle: "hiddenInset",
    // ── the lights on the same line as the bar's own control ───────────────
    //
    // AppKit places them for a standard title bar, which centres them about
    // 14pt down. This window's top bar is `space.titlebar` — 2.5rem, 40px —
    // and centres its content at 20px, so by default the sidebar's fold button
    // sits below the lights and the row reads as two rows.
    //
    // Electron's knob is not electrobun's: `trafficLightOffset` was a *delta*
    // from where AppKit would put them, and `trafficLightPosition` is the
    // position itself, as the top-left of the group. The buttons are 16px
    // across, so a group centred at 20px starts at 12.
    trafficLightPosition: { x: 19, y: 12 },
    webPreferences: {
      preload: join(here, "preload-host.cjs"),
      sandbox: true,
      nodeIntegration: false,
      contextIsolation: true,
      // The web panel is a WebContentsView positioned by this process — see
      // webviews.ts. Electron's own <webview> tag is discouraged in its own
      // docs and is not what any of this uses.
      webviewTag: false,
      // The pane's canvas is the one surface where a dropped frame is visible,
      // and an occluded or backgrounded window throttling timers is what makes
      // a terminal redraw in bursts after being switched back to.
      backgroundThrottling: false,
    },
  });

  // Shown once there is something to show, rather than on creation. A window
  // that appears empty and fills in is the launch flash by another route.
  window.once("ready-to-show", () => window.show());

  // A window that failed to load is a white rectangle, and a white rectangle is
  // also what a renderer that threw looks like. Said out loud so the two are
  // different findings — the sub-frame check keeps an iframe's own failure from
  // reading as the window's.
  window.webContents.on("did-fail-load", (_event, code, description, url, isMainFrame) => {
    if (isMainFrame) {
      console.error(`[amoeba] the renderer did not load: ${description} (${code}) at ${url}`);
    }
  });

  // ── the renderer's errors, in this process' log ──────────────────────────
  //
  // Under electrobun the view's console did not reach the main process at all,
  // which is worth stating because it was tried: `console.log` was the first
  // channel reached for out of the renderer and it printed nothing. Electron
  // gives the window's console back, and only the error level is taken —
  // everything else belongs in devtools, and a main-process log that echoes
  // every render is one nobody reads.
  window.webContents.on("console-message", (event) => {
    if (event.level === "error") {
      console.error(`[renderer] ${event.message} (${event.sourceId}:${event.lineNumber})`);
    }
  });

  // The renderer is an application surface and never navigates. Anything that
  // tries is a link somebody clicked in a diff or a PR body, and it belongs in
  // the person's browser rather than replacing the window.
  window.webContents.setWindowOpenHandler(({ url }) => {
    void shell.openExternal(url).catch(() => undefined);
    return { action: "deny" };
  });

  // ── the beachball, and what is actually known about it ──────────────────
  //
  // Reported once: the window was closed and the app hung, beachballed, and
  // went only to a SIGKILL. It has not been reproduced since — three ways,
  // none of which hung, and each quit within four milliseconds of the close:
  //
  //   a window with a child WebContentsView, torn down on "closed"   exit 0
  //   the same, torn down BEFORE window.close()                      exit 0
  //   the real renderer — wasm, daemon socket, worker pool, a guest
  //   view pointed at a refused connection                           exit 0
  //
  // So nothing below is a diagnosis, and it is important not to read it as
  // one. It is two things: teardown moved somewhere strictly safer, and a
  // floor under the one property this process must have.
  //
  // **Views go on `close`, not on `closed`.** By `closed` the window is already
  // destroyed, so the branch that removes a child view is skipped and the view
  // is only closed by its own webContents. Doing it a moment earlier means the
  // window is still there to remove them from, which is the order Electron's
  // own examples use. `closed` keeps a second sweep, because a window can be
  // destroyed without `close` ever firing.
  window.on("close", () => forgetWindow(window.id));
  window.on("closed", () => forgetWindow(window.id));

  // **The renderer going quiet is the other half of the beachball**, and it is
  // the half nothing here could see. A wedged renderer and a wedged main
  // process look identical from outside — a window that will not answer — and
  // they are different bugs in different files. Said out loud so the next
  // occurrence leaves a line behind instead of needing a force quit to end and
  // a guess to explain.
  window.webContents.on("unresponsive", () =>
    console.error("[amoeba] the renderer stopped responding"),
  );
  window.webContents.on("responsive", () =>
    console.log("[amoeba] the renderer is answering again"),
  );
  window.webContents.on("render-process-gone", (_event, details) =>
    console.error(`[amoeba] the renderer is gone: ${details.reason} (${String(details.exitCode)})`),
  );

  const url = rendererUrl();
  console.log(`[amoeba] renderer: ${url}`);
  void window.loadURL(url);

  return window;
};

// ── a watch on the window's geometry ───────────────────────────────────────
//
// Carried over from the electrobun shell, where it was a probe for a Web
// Inspector bug that left the view short. It is cheaper here and worth keeping
// for the same reason: only this process can see both numbers, and their
// *disagreement* is the whole diagnostic.
//
//   window   what the platform says the frame is
//   content  the area inside it the web contents were given
//   view     what the page says its viewport is
//
// Three rather than two, because the pair that disagrees names the side: a
// window and a content area that differ is the native inset, and a content area
// and a viewport that differ is the page.
//
// One thing genuinely improved in the port. Electrobun's `executeJavascript`
// had no completion, so the page had to volunteer its viewport through a
// `fetch` at a URL named by `AMOEBA_GEOMETRY_LOG`. Electron's returns a
// promise, so the reading is a question and the env var is gone with it.
//
// Logged only when it *changes*, so this is a list of transitions rather than a
// tick every second.
const watchGeometry = (window: BrowserWindow): void => {
  let lastSeen = "";
  const timer = setInterval(() => {
    if (window.isDestroyed()) {
      clearInterval(timer);
      return;
    }
    const [width = 0, height = 0] = window.getSize();
    const [inner = 0, tall = 0] = window.getContentSize();
    const now = `${width}x${height}, content ${inner}x${tall}`;
    if (now === lastSeen) {
      return;
    }
    lastSeen = now;
    void window.webContents
      .executeJavaScript("innerWidth + 'x' + innerHeight")
      .then((view: unknown) => console.log(`[amoeba] window ${now}, view ${String(view)}`))
      .catch(() => console.log(`[amoeba] window ${now}`));
  }, 1000);
  window.on("closed", () => clearInterval(timer));
};

// One window at a time. A second instance would be a second menu bar claiming
// cmd+V and a second client attaching to the same sessions — and a session
// takes its size from whoever is looking at it, so two windows on one session
// is two clients fighting over one geometry. AGENTS.md describes the deliberate
// second instance, and it is a second *daemon* on its own port, not this.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => {
    const [existing] = BrowserWindow.getAllWindows();
    if (existing !== undefined) {
      if (existing.isMinimized()) {
        existing.restore();
      }
      existing.focus();
    }
  });

  void app.whenReady().then(() => {
    // `dist/electron/main.js` sits beside the renderer's own output.
    serveRenderer(resolve(here, "..", "renderer"));
    installWebviews(join(here, "preload-guest.cjs"));

    const window = create();
    // After the window, because two of the View items act on one. The Edit
    // items have to exist before anything is focused, which they do — this is
    // the same tick as the window's creation.
    installMenu(window);
    watchGeometry(window);

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) {
        const again = create();
        installMenu(again);
        watchGeometry(again);
      }
    });
  });

  // Closing the window ends the app, on every platform. That is what the
  // electrobun shell did, and it is right here for a reason of its own: the
  // work does not live in this process. The sessions are zmx's and the records
  // are the daemon's, so a windowless app in the dock would be holding nothing.
  //
  // ── and it ends, whether or not the graceful path finishes ──────────────
  //
  // `app.quit()` is a request: it fires `before-quit`, closes what is left and
  // unwinds. Everything in this app cooperates with that, and it was still
  // watched hanging once — see the note where the window is created.
  //
  // The asymmetry is what settles it. A shell that quits a little abruptly
  // costs nothing, because **nothing durable lives in this process**: the
  // sessions are zmx's, the threads and jobs are the daemon's, and the window's
  // own state is a hash in the address bar and a few keys in localStorage. A
  // shell that will not quit costs a force quit, and it looks like the
  // application is broken.
  //
  // So the graceful path gets a bounded amount of time and then the process
  // ends. `unref` matters: this timer must never be the reason the loop is
  // still alive, only the thing that fires if something else is.
  app.on("window-all-closed", () => {
    app.quit();
    const giveUp = setTimeout(() => {
      console.error(`[amoeba] quit did not finish in ${QUIT_GRACE_MS}ms — exiting`);
      app.exit(0);
    }, QUIT_GRACE_MS);
    giveUp.unref();
  });
}
