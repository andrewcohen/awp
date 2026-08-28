// amoeba's main process.
//
// Opens one window and points it at the renderer. That is close to all it
// should ever do: Electrobun's RPC is reserved for what only a native process
// can — window, menus, dialogs, keychain, deep links — and the pane's byte
// stream goes straight from the renderer to the daemon's socket instead, so the
// path where latency is felt has one hop and one schema.
//
// In development the window loads Vite's dev server, so a renderer edit is a
// reload rather than a rebuild. In a built app it loads the copied dist through
// Electrobun's own `views://` scheme.

import { BrowserWindow } from "electrobun/bun";
import { installMenu } from "./menu";

/** Where the renderer lives, which differs between dev and a built app. */
const rendererUrl = (): string => {
  const devServer = process.env.AMOEBA_DEV_SERVER;
  return devServer !== undefined && devServer !== "" ? devServer : "views://renderer/index.html";
};

const url = rendererUrl();
console.log(`[amoeba] renderer: ${url}`);

// Kept rather than discarded: closing it, moving it, or opening a second one
// all need the handle — and so does the menu below.
export const mainWindow = new BrowserWindow({
  title: "amoeba",
  url,
  frame: { x: 100, y: 100, width: 1200, height: 800 },
  // ── no title bar, and no traffic lights ────────────────────────────────
  //
  // `hidden` maps to `Titled: false, FullSizeContentView: true`, which takes
  // away every native way to move, close or minimise the window. Two things
  // had to exist first, and both now do:
  //
  //   the Window menu     Close ⌘W, Minimize ⌘M, Zoom — in `menu.ts`
  //   a drag region       the top bar, wearing electrobun's own class
  //
  // The second is the one that nearly shipped broken. `-webkit-app-region:
  // drag` was already on that bar and had never moved anything: electrobun
  // matches on an inline style attribute or on `.electrobun-webkit-app-region-
  // drag`, and StyleX emits a class of its own plus a stylesheet rule. The bar
  // moved the window because `hiddenInset` left a real title bar behind it and
  // AppKit was doing the work. Take the title bar away and there is nothing.
  // See the note in `Bars.tsx`.
  titleBarStyle: "hidden",
});

// After the window, because two of its items act on one: Reload, and the
// Fit-to-Window repair for the Web Inspector leaving the view short. The Edit
// items still have to exist before anything is focused, which they do — this
// runs in the same tick as the window's creation.
installMenu(mainWindow);

// ── a watch on the window's geometry, while the Web Inspector bug is open ──
//
// Nothing here can reproduce that bug: Safari attaching its inspector is not
// something this process is told about, and every measurement taken from a
// clean launch says the window and the view agree. So instead of asking for a
// reading at the moment it happens, this writes one continuously — and only
// when it *changes*, so the log is a list of the transitions rather than a
// tick every two seconds.
//
// Both numbers, because they answer different halves and only their
// disagreement is diagnostic:
//
//   window   what AppKit says the frame is
//   view     what the page says its viewport is
//
// The view has to volunteer its own: `executeJavascript` has no completion, so
// there is no asking it. **And its console does not reach this log** — that was
// the first attempt and it printed nothing, which is worth knowing before
// reaching for `console.log` as a channel out of the renderer again. What does
// work is a request, watched from outside. `AMOEBA_GEOMETRY_LOG` names where.
//
// Remove this with the bug. It is a probe that happens to live in the app
// because the app is the only thing that can see both numbers.
let lastSeen = "";
setInterval(() => {
  const { width, height } = mainWindow.getFrame();
  const now = `${width}x${height}`;
  if (now === lastSeen) {
    return;
  }
  lastSeen = now;
  console.log(`[amoeba] window is now ${now}`);
  const to = process.env.AMOEBA_GEOMETRY_LOG;
  if (to === undefined || to === "") {
    return;
  }
  mainWindow.webview.executeJavascript(
    `fetch('${to}?view=' + innerWidth + 'x' + innerHeight + '&root=' + Math.round(document.getElementById('root')?.getBoundingClientRect().height ?? -1) + '&window=${now}').catch(function () {})`,
  );
}, 1000);
