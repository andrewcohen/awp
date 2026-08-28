// The native webview, from this side of the bridge.
//
// ── what this is, and why it is here rather than in a preload ─────────────
//
// The web panel needs a real browser view, not an iframe: most of what a person
// wants beside an agent — a docs site, a dashboard, an issue tracker — sends
// `X-Frame-Options` or a `frame-ancestors` policy and renders as a blank
// rectangle with a console error nobody sees.
//
// Electrobun supplied `<electrobun-webview>` from its preload and the panel
// drove the DOM node. Electron cannot: a preload runs in an isolated world, so
// a custom element defined there is invisible to the page's own scripts. So the
// element is built here, in the renderer, over a bridge that carries ids and
// rectangles — see `src/electron/preload/host.ts` for the other half.
//
// ── the one property everything else follows from ────────────────────────
//
// **A native view does not stack.** It is drawn over the window by the
// compositor at the rectangle it is told to occupy, so nothing rendered here
// can be in front of it and no `z-index` wins. That is why `overlays.ts` counts
// open modals and this panel hides on the count, and why a column folded to
// nothing has to hide it too — a view whose box has no size keeps whatever
// rectangle it last had.
//
// ── the orphan ───────────────────────────────────────────────────────────
//
// `create` is a round trip, and StrictMode rehearses mount and unmount inside
// one frame. A view asked for and released inside that gap has no handle left
// to release it with: it floats at its birth rectangle for the life of the
// process, over everything, unmovable. Electrobun had exactly this bug and it
// needed a patch to its preload, because the guard had to live inside an
// element nothing outside could reach.
//
// Here the guard is one field on an object this file owns — `gone` — checked
// after the await. The main process holds the matching half: a `destroy` for an
// id still in flight is remembered, and the view is discarded on arrival.

/** How often the native view is told where its box is, when nothing else says. */
const POLL_MS = 100;

export type WebviewEvent = "did-navigate" | "dom-ready" | "host-message" | "did-fail-load";

export interface HostWebview {
  loadURL(url: string): void;
  reload(): void;
  goBack(): void;
  goForward(): void;
  /** Stop drawing it, for a modal over the top or a column folded away. */
  toggleHidden(hidden: boolean): void;
  /** Push the box's rectangle now rather than at the next poll. */
  syncDimensions(force?: boolean): void;
  /** Fire and forget: the page volunteers what it finds, it is not asked. */
  executeJavascript(js: string): void;
  on(event: WebviewEvent, listener: (detail: unknown) => void): void;
  off(event: WebviewEvent, listener: (detail: unknown) => void): void;
  /** Take the native view down. Nothing else does. */
  destroy(): void;
}

interface Rect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

interface HostBridge {
  readonly createWebview: (options: { readonly url?: string | undefined }) => Promise<number>;
  readonly destroyWebview: (id: number) => void;
  readonly setWebviewBounds: (id: number, rect: Rect) => void;
  readonly callWebview: (id: number, method: string, argument?: unknown) => void;
  readonly onWebviewEvent: (
    listener: (message: {
      readonly id: number;
      readonly name: string;
      readonly detail: unknown;
    }) => void,
  ) => () => void;
}

declare global {
  interface Window {
    readonly awpHost?: HostBridge;
  }
}

const bridge = (): HostBridge | undefined =>
  typeof window === "undefined" ? undefined : window.awpHost;

/**
 * Whether a native webview can be made at all.
 *
 * False in a plain browser — `bun run dev:renderer` opened in a tab, and every
 * Playwright probe. The panel says so in words rather than showing an empty
 * box, because an empty box is also what a page that failed to load looks like.
 */
export const hostWebviewAvailable = (): boolean => bridge() !== undefined;

const same = (a: Rect | undefined, b: Rect): boolean =>
  a !== undefined && a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;

/**
 * @param box  the element whose rectangle the native view is drawn at. It has
 *             no children of its own — what fills it is another process.
 */
export const createWebview = (
  box: HTMLElement,
  options: { readonly url?: string | undefined },
): HostWebview => {
  const host = bridge();
  if (host === undefined) {
    throw new Error("no native host: createWebview needs the app window");
  }

  let id: number | undefined;
  let gone = false;
  let hidden = false;
  let last: Rect | undefined;
  /** Calls made before the id arrived, replayed in order once it has. */
  const queued: Array<() => void> = [];
  const listeners = new Map<WebviewEvent, Set<(detail: unknown) => void>>();

  const when = (run: () => void): void => {
    if (gone) {
      return;
    }
    if (id === undefined) {
      queued.push(run);
      return;
    }
    run();
  };

  const call = (method: string, argument?: unknown): void => {
    when(() => host.callWebview(id as number, method, argument));
  };

  const push = (force: boolean): void => {
    if (id === undefined || gone) {
      return;
    }
    const rect = box.getBoundingClientRect();
    const now: Rect = {
      x: rect.left,
      y: rect.top,
      width: hidden ? 0 : rect.width,
      height: hidden ? 0 : rect.height,
    };
    if (!force && same(last, now)) {
      return;
    }
    last = now;
    host.setWebviewBounds(id, now);
  };

  // Position is not size, and only one of them has an observer. A divider drag
  // moves this box without resizing it, and a folding column resizes an
  // ancestor. So both: the observer for the common case, and a poll for
  // everything layout can do without telling anyone. Electrobun's own tag
  // polled at the same interval.
  const watch = new ResizeObserver(() => push(false));
  watch.observe(box);
  const timer = window.setInterval(() => push(false), POLL_MS);

  const stopListening = host.onWebviewEvent((message) => {
    if (message.id !== id) {
      return;
    }
    const set = listeners.get(message.name as WebviewEvent);
    if (set === undefined) {
      return;
    }
    for (const listener of set) {
      listener(message.detail);
    }
  });

  void host
    .createWebview({ url: options.url })
    .then((made) => {
      if (gone) {
        // Released while it was being made. This is the orphan, and saying so
        // is the whole fix — the main process is holding a view nothing here
        // has a handle to.
        host.destroyWebview(made);
        return;
      }
      id = made;
      push(true);
      for (const run of queued.splice(0)) {
        run();
      }
    })
    .catch((error: unknown) => {
      console.warn("[amoeba] the native webview could not be made:", error);
    });

  return {
    loadURL: (url) => call("loadURL", url),
    reload: () => call("reload"),
    goBack: () => call("goBack"),
    goForward: () => call("goForward"),
    toggleHidden: (value) => {
      hidden = value;
      call("setVisible", !value);
      if (!value) {
        // Forced on the way back rather than waiting to be noticed: while
        // hidden the box's rectangle was pushed as zero, and the poll only
        // fires every 100ms, which reads as the panel being slow to wake up.
        push(true);
      }
    },
    syncDimensions: (force) => push(force !== false),
    executeJavascript: (js) => call("executeJavascript", js),
    on: (event, listener) => {
      const set = listeners.get(event) ?? new Set();
      set.add(listener);
      listeners.set(event, set);
    },
    off: (event, listener) => {
      listeners.get(event)?.delete(listener);
    },
    destroy: () => {
      gone = true;
      watch.disconnect();
      window.clearInterval(timer);
      stopListening();
      listeners.clear();
      if (id !== undefined) {
        host.destroyWebview(id);
        id = undefined;
      }
    },
  };
};
