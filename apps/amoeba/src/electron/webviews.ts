import { BrowserWindow, WebContentsView, ipcMain, shell } from "electron";
import type { IpcMainEvent, IpcMainInvokeEvent } from "electron";
import { CH, type Rect, type WebviewMethod } from "./channels";

// The web panel's native view, and everything about owning one.
//
// ── what this replaces, and what it keeps ─────────────────────────────────
//
// Electrobun's `<electrobun-webview>` was a custom element defined by its
// preload, and the panel drove it by calling methods on the DOM node. Electron
// has no such element — its own `<webview>` tag is discouraged in its own docs
// — so the equivalent is a `WebContentsView` positioned over the window by
// hand, which is what `<webview>` is underneath anyway.
//
// The property AGENTS.md states about the old one holds exactly as before, and
// is the reason `overlays.ts` exists: **a native view does not stack.** It is
// drawn by the compositor over the whole page, so no `z-index` in the renderer
// is in front of it, and a modal is made visible by not drawing it at all.
//
// ── the orphan, which is a shape and not an electrobun bug ────────────────
//
// `patches/electrobun@1.18.1.patch` existed for a webview that outlived every
// handle to it: the element was removed during the async gap between asking for
// a native view and being handed one, its `disconnectedCallback` found nothing
// to remove, and the view arrived attached to a detached element.
//
// Nothing here is immune to that shape — `create` is an `invoke`, so there is
// still a gap, and StrictMode still rehearses mount/unmount inside one frame.
// What is different is where the guard can live: the renderer owns the element
// now (see `host.ts` there), so `destroy` can arrive for an id whose view has
// not finished being made. That is why `pending` is a set of ids rather than
// the map being the only record: a destroy for an id still in flight is
// recorded, and `create` throws its own view away when it finds it.

interface Owned {
  readonly view: WebContentsView;
  /** The window it was made for, so an event can be routed back to it. */
  readonly owner: number;
}

const views = new Map<number, Owned>();
/** Ids asked for and not yet built. See the orphan note above. */
const cancelled = new Set<number>();
let next = 1;

/** The window a call came from, or nothing if it has gone. */
const windowOf = (event: IpcMainEvent | IpcMainInvokeEvent): BrowserWindow | null =>
  BrowserWindow.fromWebContents(event.sender);

const tell = (owner: number, id: number, name: string, detail: unknown): void => {
  const window = BrowserWindow.fromId(owner);
  if (window === null || window.isDestroyed() || window.webContents.isDestroyed()) {
    return;
  }
  window.webContents.send(CH.event, { id, name, detail });
};

const drop = (id: number): void => {
  const owned = views.get(id);
  if (owned === undefined) {
    // Still being made. Recorded so `create` discards it on arrival.
    cancelled.add(id);
    return;
  }
  views.delete(id);
  const window = BrowserWindow.fromId(owned.owner);
  if (window !== null && !window.isDestroyed()) {
    window.contentView.removeChildView(owned.view);
  }
  if (!owned.view.webContents.isDestroyed()) {
    owned.view.webContents.close();
  }
};

/**
 * @param guestPreload  the script the guest page gets. It defines one function
 *                      and nothing else — see preload/guest.ts.
 */
export const installWebviews = (guestPreload: string): void => {
  ipcMain.handle(CH.create, (event, options: { readonly url?: string | undefined }) => {
    const window = windowOf(event);
    if (window === null) {
      return -1;
    }
    const id = next++;

    const view = new WebContentsView({
      webPreferences: {
        // The guest is an arbitrary website. Everything here is the strict
        // reading, and none of it is negotiable: `sandbox` on, node off,
        // context isolation on, and a preload that exposes exactly one
        // function through the bridge.
        preload: guestPreload,
        sandbox: true,
        nodeIntegration: false,
        contextIsolation: true,
        webviewTag: false,
        // Its own session would lose every login the person already has in
        // this app's profile, which is the point of a browser beside an agent.
        // The default partition is deliberate.
      },
    });
    // The page paints its own ground before it has one, and the default is
    // white — a dark window flashing white on every navigation.
    view.setBackgroundColor("#00000000");

    // A window opened by the page becomes a navigation in it. A real popup
    // would be a second native view with no rectangle to live in; an external
    // link is the one case worth handing to the system browser, and there is
    // no way to tell them apart here, so the page keeps its own traffic.
    view.webContents.setWindowOpenHandler(({ url }) => {
      if (url.startsWith("http://") || url.startsWith("https://")) {
        void view.webContents.loadURL(url);
      } else {
        void shell.openExternal(url).catch(() => undefined);
      }
      return { action: "deny" };
    });

    const owner = window.id;
    const navigated = (url: string) => tell(owner, id, "did-navigate", { url });
    view.webContents.on("did-navigate", (_event, url) => navigated(url));
    // A single-page site changes its address without a document load, and an
    // address bar that only followed the first would be lying for the rest of
    // the visit.
    view.webContents.on("did-navigate-in-page", (_event, url, isMainFrame) => {
      if (isMainFrame) {
        navigated(url);
      }
    });
    // `dom-ready` and not `did-navigate`: the picker appends a highlight to
    // `document.body`, and a committed navigation is before there is one.
    view.webContents.on("dom-ready", () => tell(owner, id, "dom-ready", {}));

    // ── a page that did not load has to say so ───────────────────────────────
    //
    // A native view draws over the column whatever happened to it, so a refused
    // connection is a blank rectangle — and this file's own note says why that
    // is not good enough: an empty box is also what a working page with nothing
    // on it looks like. Reported from a real window, pointed at a dev server
    // that was not running:
    //
    //   Failed to load URL: http://localhost:5173/ — ERR_CONNECTION_REFUSED
    //
    // That sentence went to the main process log, where nobody using the app
    // can see it. It belongs in the panel.
    //
    // **`-3` is not a failure.** It is `ERR_ABORTED`, which every navigation
    // superseded by another one reports — typing a second address while the
    // first is still resolving, or a page redirecting. Showing it would put an
    // error on the screen for the ordinary case of changing your mind.
    view.webContents.on("did-fail-load", (_event, code, description, url, isMainFrame) => {
      if (isMainFrame && code !== -3) {
        tell(owner, id, "did-fail-load", { code, description, url });
      }
    });

    if (cancelled.delete(id)) {
      // Removed while it was being made. Nothing ever holds this one.
      view.webContents.close();
      return id;
    }

    window.contentView.addChildView(view);
    view.setBounds({ x: 0, y: 0, width: 0, height: 0 });
    views.set(id, { view, owner });

    if (options.url !== undefined && options.url !== "") {
      void view.webContents.loadURL(options.url).catch(() => undefined);
    }
    return id;
  });

  ipcMain.on(CH.destroy, (_event, id: number) => drop(id));

  ipcMain.on(CH.bounds, (_event, id: number, rect: Rect) => {
    const owned = views.get(id);
    if (owned === undefined) {
      return;
    }
    owned.view.setBounds({
      x: Math.round(rect.x),
      y: Math.round(rect.y),
      width: Math.max(0, Math.round(rect.width)),
      height: Math.max(0, Math.round(rect.height)),
    });
  });

  ipcMain.on(CH.call, (_event, id: number, method: WebviewMethod, argument: unknown) => {
    const owned = views.get(id);
    if (owned === undefined) {
      return;
    }
    const contents = owned.view.webContents;
    if (contents.isDestroyed()) {
      return;
    }
    switch (method) {
      case "loadURL":
        if (typeof argument === "string" && argument !== "") {
          void contents.loadURL(argument).catch(() => undefined);
        }
        return;
      case "reload":
        contents.reload();
        return;
      case "goBack":
        // `navigationHistory` rather than the deprecated `goBack()`, which
        // Electron 36 removed outright.
        if (contents.navigationHistory.canGoBack()) {
          contents.navigationHistory.goBack();
        }
        return;
      case "goForward":
        if (contents.navigationHistory.canGoForward()) {
          contents.navigationHistory.goForward();
        }
        return;
      case "setVisible":
        owned.view.setVisible(argument !== false);
        return;
      case "executeJavascript": {
        if (typeof argument === "string") {
          // Fire and forget, like electrobun's — the picker volunteers what it
          // finds through `__awpSendToHost` rather than being asked. Electron
          // *would* return a promise here, and taking it would put a second
          // channel beside the one annotate.ts is written against.
          //
          // This runs in the page's MAIN world, which is what makes the bridged
          // function reachable from the injected script.
          void contents.executeJavaScript(argument, true).catch(() => undefined);
        }
      }
    }
  });

  // The page's own channel back. Anything on any site can put anything down it
  // — Electrobun's `__electrobunSendToHost` had exactly this property — so the
  // payload is forwarded verbatim and `messageFrom` in the renderer is what
  // decides whether it is ours.
  ipcMain.on(CH.fromGuest, (event, payload: unknown) => {
    for (const [id, owned] of views) {
      if (owned.view.webContents.id === event.sender.id) {
        tell(owned.owner, id, "host-message", payload);
        return;
      }
    }
  });
};

/** Every view belonging to a window that is going away. */
export const forgetWindow = (windowId: number): void => {
  for (const [id, owned] of views) {
    if (owned.owner === windowId) {
      drop(id);
    }
  }
};
