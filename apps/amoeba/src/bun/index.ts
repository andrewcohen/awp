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

/** Where the renderer lives, which differs between dev and a built app. */
const rendererUrl = (): string => {
  const devServer = process.env.AMOEBA_DEV_SERVER;
  return devServer !== undefined && devServer !== "" ? devServer : "views://renderer/index.html";
};

const url = rendererUrl();
console.log(`[amoeba] renderer: ${url}`);

// Kept rather than discarded: closing it, moving it, or opening a second one
// all need the handle, and the main process is about to grow menus that do.
export const mainWindow = new BrowserWindow({
  title: "amoeba",
  url,
  frame: { x: 100, y: 100, width: 1200, height: 800 },
  // Inset controls rather than hidden: there is no custom chrome yet, and a
  // window with no way to close it is a bad first impression.
  titleBarStyle: "hiddenInset",
});
