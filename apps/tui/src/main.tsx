#!/usr/bin/env bun
// The POC: threads, an agent, a terminal.
//
//   bun src/main.tsx
//
// ── one way out, and the signals use it ────────────────────────────────────
//
// opentui installs handlers for the exit signals that call `renderer.destroy()`
// and nothing else, so on a SIGTERM the screen is torn down while the process
// carries on — timers painting into a dead renderer, once a second, forever.
// That is what made the first probe of this POC hang: `kill` then `await exit`,
// and the exit never came.

import { createCliRenderer } from "@opentui/core";
import { createRoot } from "@opentui/react";
import { App } from "./App";
import { copyOnSelect } from "./clipboard";

const renderer = await createCliRenderer({
  // ctrl+c belongs to whatever is running in the pty, and to the composer
  // everywhere else. ctrl-q is the way out and it is written in the footer.
  exitOnCtrlC: false,
  targetFps: 30,
});

// Selecting copies. The renderer owns the mouse, so a drag is opentui's
// gesture and the terminal never sees one — see `clipboard.ts`.
copyOnSelect(renderer);

const root = createRoot(renderer);

const quit = (code: number) => {
  root.unmount();
  if (!renderer.isDestroyed) renderer.destroy();
  process.exit(code);
};

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"] as const) {
  process.on(signal, () => quit(0));
}

root.render(<App onQuit={() => quit(0)} />);

// The render loop, and it is not implied by `render`. Without it the first
// frame is painted and nothing after it is: state arrived, React re-rendered,
// and the screen went on saying `0 threads` with nine of them in hand.
renderer.start();
