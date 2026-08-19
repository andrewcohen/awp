// tdeck as a desktop window.
//
// Deliberately thin. It starts the UI server inside its own process and opens a
// window on it — that is the entire shell. The frontend is unchanged and does
// not know it is in an app, so `bun run start` and a browser tab remain a
// first-class way to run tdeck: useful while developing the UI, and the fallback
// if this layer disappoints.
//
// What the window buys over a tab: a dock icon, no browser chrome, no port in an
// address bar, and somewhere for native file drop to land later.
//
// The daemon is not started here and must not be. It owns the agents and has to
// outlive the window — closing the app is meant to leave the work running, which
// is the whole argument of adapterd.ts. The server auto-starts one if none is
// listening, which is right for a launch and still leaves it detached.

// Named index.ts, in a directory of its own, because Electrobun's runtime loads
// exactly `app/bun/index.js` — the bundler names the output after the
// entrypoint, so a file called shell.ts produced shell.js and the launcher
// found nothing to run. It logged "Loading app code from flat files" and then
// sat there, which is a failure that looks like a slow start.
import { BrowserWindow } from "electrobun/bun";
import { instanceName, port } from "../paths.ts";

// Importing the server *is* starting it: it is a script, not a module with an
// entry function. The top-level await inside it means the window below is not
// created until the daemon connection is up, which is the order you want —
// a window that opens before its backend shows a connection error for a second
// and then repaints.
await import("../server.ts");

new BrowserWindow({
  title: instanceName === "default" ? "tdeck" : `tdeck (${instanceName})`,
  url: `http://localhost:${port}`,
  frame: { x: 80, y: 60, width: 1280, height: 860 },
  // Inset rather than hidden: hidden means drawing our own window controls, and
  // the page has no titlebar of its own to put them in. Inset keeps the traffic
  // lights and gives the page the rest of the strip.
  titleBarStyle: "hiddenInset",
});
