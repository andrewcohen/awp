import type { ElectrobunConfig } from "electrobun";

// The desktop shell, last on purpose.
//
// Everything above it works as a page served on localhost, and it stays that
// way: the shell starts the UI server in its own process and points a window at
// it. Nothing in the frontend knows it is in a desktop app, so the browser
// remains a first-class way to run tdeck — useful while developing, and the
// fallback if Electrobun disappoints.
//
// Electrobun rather than Electron because the resource budget belongs to agent
// processes, not to a second copy of Chromium. It uses the system webview and a
// Bun backend, which is what tdeck already is.
export default {
  app: {
    name: "tdeck",
    identifier: "dev.awp.tdeck",
    version: "0.1.0",
    description: "awp's agents, in a window",
  },
  build: {
    // The shell imports the server, so bundling this entrypoint pulls in the
    // whole backend. The daemon is deliberately not part of it — it has to
    // outlive the window, so it stays a separate process.
    bun: { entrypoint: "src/desktop/index.ts" },
    // The built frontend has to travel with the app. Without it the server
    // falls back to the experiment's throwaway page, which is a working chat
    // and very obviously not the one anybody wants — a failure quiet enough to
    // ship by accident.
    //
    // Destinations are relative to the app directory, which already contains
    // bun/. shell.js therefore lands in app/bun/ and resolves ../frontend/dist
    // to app/frontend/dist — the same layout as the source tree, so the server
    // needs no special case for being bundled.
    copy: { "frontend/dist": "frontend/dist", "public": "public" },
    buildFolder: "build",
  },
} satisfies ElectrobunConfig;
