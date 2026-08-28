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
  // ── inset controls, and the reason `hidden` was tried and reverted ──────
  //
  // `hidden` maps to `Titled: false, FullSizeContentView: true`, and it works:
  // the lights go, the top bar starts at the window's own edge, the Window
  // menu covers close and minimise, and the drag region moves it.
  //
  // **A tiling window manager stops managing it.** Reported within a minute of
  // the flag landing — "my aerospace seems to not have resized it" — and it is
  // not a bug in either program. AeroSpace, yabai and the rest pick windows out
  // through the accessibility API and skip anything that is not a *standard*
  // window; an untitled one is not, so it is left floating. On a 3440x1440
  // display the difference is the whole point of the display:
  //
  //   hiddenInset   3424x1393    tiled to the screen, less the gaps
  //   hidden        whatever it was last dragged to
  //
  // Three circles are a small price for being a window somebody's tiler will
  // manage, and the person running the tiler is the person using this. So the
  // title bar stays.
  //
  // `trafficLightOffset: { x, y }` is the knob that is actually available here
  // if they are in the way — it moves them, it cannot remove them.
  //
  // What the attempt was worth keeping: it found that the top bar's drag
  // region had never worked. `-webkit-app-region: drag` is emitted by StyleX
  // as a class and a stylesheet rule, and electrobun reads neither — only an
  // inline style attribute or its own class name. The bar moved the window
  // because this very flag leaves a real title bar behind it. `Bars.tsx` now
  // wears electrobun's class as well, so the region works on its own merits.
  titleBarStyle: "hiddenInset",
  // ── putting the lights on the same line as the bar's own control ────────
  //
  // AppKit places them for a standard 28pt title bar, so their centre is about
  // 14pt down. This window's top bar is `space.titlebar` — 2.5rem, 40px — and
  // centres its content at 20px, so the sidebar's fold button sat six pixels
  // below the lights and the row read as two rows.
  //
  // The button's position is ours and the lights' is not, so the lights move.
  // `trafficLightOffset` is a delta from where AppKit would put them; zero is
  // the default.
  //
  //   40px bar, centre at 20      the button
  //   28pt bar, centre at ~14     the lights, by default
  //   ────────────────────────
  //   6                           this
  //
  // **The sign is unverified.** It goes straight to the native side with no
  // documentation on either end, and AppKit's window coordinates are flipped —
  // so if they move up rather than down, negate it. That is the whole
  // adjustment, and it is why the number is derived and named rather than
  // buried in the call.
  trafficLightOffset: { x: 0, y: 6 },
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
