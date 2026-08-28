import { contextBridge, ipcRenderer } from "electron";
import { CH, type Rect, type WebviewMethod } from "../channels";

// The window's one bridge to the main process.
//
// ── why this is a bridge and not a custom element ─────────────────────────
//
// Electrobun's preload defined `<electrobun-webview>`, and the panel drove a
// DOM node. Copying that shape here does not work, and the way it fails is
// quiet: a preload runs in an **isolated world**, which shares the document but
// not the `window` — so a custom element defined here is defined in a registry
// the page's own scripts never consult. `customElements.get(TAG)` in the
// renderer answers undefined, which is exactly the branch `Web.tsx` uses to say
// "this panel needs the app window", so the panel would report itself
// unavailable inside the app it was written for.
//
// The renderer is ours, so the element goes there instead — `host.ts` in the
// renderer builds and positions the box, and this file is only the wire. That
// is a better division anyway: the part that has to be typed against React and
// StyleX is in the file that has both, and the part crossing the process
// boundary is this, which is thirty lines and no DOM.
//
// Everything exposed is a function, deliberately. `contextBridge` copies plain
// values across once and they never change again; a function is the only shape
// that keeps answering.

export interface HostBridge {
  readonly createWebview: (options: { readonly url?: string | undefined }) => Promise<number>;
  readonly destroyWebview: (id: number) => void;
  readonly setWebviewBounds: (id: number, rect: Rect) => void;
  readonly callWebview: (id: number, method: WebviewMethod, argument?: unknown) => void;
  /** Every event from every view. Answers with the way to stop listening. */
  readonly onWebviewEvent: (
    listener: (message: {
      readonly id: number;
      readonly name: string;
      readonly detail: unknown;
    }) => void,
  ) => () => void;
}

const bridge: HostBridge = {
  createWebview: (options) => ipcRenderer.invoke(CH.create, options) as Promise<number>,
  destroyWebview: (id) => ipcRenderer.send(CH.destroy, id),
  setWebviewBounds: (id, rect) => ipcRenderer.send(CH.bounds, id, rect),
  callWebview: (id, method, argument) => ipcRenderer.send(CH.call, id, method, argument),
  onWebviewEvent: (listener) => {
    const on = (
      _event: unknown,
      message: { readonly id: number; readonly name: string; readonly detail: unknown },
    ) => listener(message);
    ipcRenderer.on(CH.event, on);
    return () => {
      ipcRenderer.off(CH.event, on);
    };
  },
};

contextBridge.exposeInMainWorld("awpHost", bridge);
