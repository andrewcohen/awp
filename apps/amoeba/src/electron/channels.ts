// The names on the wire between the window and the main process.
//
// One file, imported by the main process and by both preloads, for the same
// reason `reviewKey` and `reviewOf` sit together in the daemon: a string
// composed in one file and matched in another drifts by a colon, and the
// failure is silence rather than an error.

export const CH = {
  /** Make a native view. Answers with its id. */
  create: "awp:webview:create",
  /** Take one down. Sent, not invoked — a teardown that awaits is a teardown that races a reload. */
  destroy: "awp:webview:destroy",
  /** Where on screen it goes, in CSS pixels of the window's content area. */
  bounds: "awp:webview:bounds",
  /** loadURL · reload · goBack · goForward · setVisible · executeJavascript */
  call: "awp:webview:call",
  /** main → window: did-navigate · dom-ready · host-message. */
  event: "awp:webview:event",
  /** the guest page → main, on its way to the window that owns it. */
  fromGuest: "awp:webview:from-guest",
  /**
   * window → main: put the keyboard back in the window's own web contents.
   *
   * A native view is a separate `webContents`, and clicking in one gives it
   * the keyboard. So a control the renderer draws *because* of that click —
   * the annotator's note box — can call `focus()` on itself, become
   * `document.activeElement`, and still receive nothing: the keys are going
   * to the page. Only the main process can move focus between the two.
   */
  focus: "awp:window:focus",
} as const;

/** A rectangle in the window's content area, in CSS pixels. */
export interface Rect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export type WebviewMethod =
  | "loadURL"
  | "reload"
  | "goBack"
  | "goForward"
  | "setVisible"
  | "executeJavascript";
