#!/usr/bin/env bun
// Does a paste arrive, and does selecting copy?
//
// Two questions a unit test cannot ask, because both are about what a
// *terminal* does: a paste is a pair of escape sequences the terminal wraps
// text in, and a copy is an escape sequence the program writes back. So the
// probe is the terminal — `Bun.Terminal` gives a real pty, the app under it
// sees a tty, and what each side sent the other arrives here as bytes.
//
// Safe anywhere: it renders a composer of its own, opens no socket, names no
// session and spawns nothing but itself.
//
//   bun run probe:paste            drive the pty and report
//   bun run probe:paste --child    the app the pty runs (used by the above)

import { openPty, until, wait } from "./pty";

const ESC = String.fromCodePoint(27);

if (process.argv.includes("--child")) {
  const { createCliRenderer } = await import("@opentui/core");
  const { createRoot } = await import("@opentui/react");
  const { copyOnSelect } = await import("../clipboard");
  const { Toast } = await import("../Toast");
  const { CHROME } = await import("../theme");

  const renderer = await createCliRenderer({ exitOnCtrlC: false, targetFps: 20 });
  copyOnSelect(renderer);
  const App = () => (
    <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
      <text fg={CHROME.text} content="a line worth selecting" selectable />
      <box height={3} backgroundColor={CHROME.raised}>
        <textarea focused flexGrow={1} height={3} backgroundColor={CHROME.raised} />
      </box>
      {/* The notice a copy leaves, which is the only thing on screen that
          says the gesture did anything at all. */}
      <Toast />
    </box>
  );
  createRoot(renderer).render(<App />);
  renderer.start();
} else {
  const pty = openPty(["bun", "src/probe/paste.tsx", "--child"], { cols: 60, rows: 10 });
  // Wait for something the app painted, not for bytes: the first bytes are
  // the terminal being set up, and a paste sent before the textarea exists
  // goes nowhere and reads as a paste that was dropped.
  await until("the first frame", () => pty.saw("a line worth selecting"), 15000);
  await wait(500);

  // ── paste ────────────────────────────────────────────────────────────
  //
  // `?2004h` is the app telling the terminal to wrap a paste in 200~/201~.
  // Without it the terminal sends the text as ordinary keystrokes, and a
  // pasted newline is a Return — which in this app is a send.
  const asked = pty.raw().includes(`${ESC}[?2004h`);
  // ── whitespace is not evidence, and its absence is not either ────────
  //
  // The renderer repaints only the cells that changed, and a space drawn
  // over a space has not changed — so a pasted `pasted one line` reaches
  // this side as `pastedoneline` with the gaps never sent. Reading that as
  // a dropped paste is the mistake this note exists to stop: it looked like
  // a real failure for exactly as long as it took to print the screen.
  const tight = (needle: string) =>
    pty.text().replaceAll(/\s+/gu, "").includes(needle.replaceAll(/\s+/gu, ""));
  pty.send(`${ESC}[200~pasted one line${ESC}[201~`);
  await wait(700);
  const arrived = tight("pasted one line");

  // Two lines in one paste, which is the case that matters: bracketed, the
  // newline is a newline, and the composer must not have sent anything.
  pty.send(`${ESC}[200~first line${String.fromCodePoint(13)}second line${ESC}[201~`);
  await wait(700);
  const both = tight("first line") && tight("second line");

  // ── copy ─────────────────────────────────────────────────────────────
  //
  // A drag, in the mouse reporting the renderer turned on: press at the
  // start of the word, move, release. What comes back is OSC 52 — `]52;`
  // followed by base64 — which is the program asking the terminal to put
  // the text on the clipboard, and the only evidence that a selection was
  // copied rather than merely painted.
  const before = pty.raw().length;
  pty.send(`${ESC}[<0;1;1M`);
  await wait(120);
  pty.send(`${ESC}[<32;12;1M`);
  await wait(120);
  pty.send(`${ESC}[<0;12;1m`);
  await wait(700);
  const osc = pty.raw().slice(before);
  const copied = /\]52;[cp];(?<payload>[A-Za-z\d+/=]+)/u.exec(osc)?.groups?.["payload"];
  const text = copied === undefined ? "" : Buffer.from(copied, "base64").toString("utf8");

  console.log(`  asked for 2004h  ${asked ? "yes" : "NO"}`);
  console.log(`  a paste          ${arrived ? "arrived" : "DROPPED"}`);
  console.log(`  two lines        ${both ? "both arrived" : "LOST ONE"}`);
  console.log(`  a drag copied    ${text === "" ? "NOTHING" : JSON.stringify(text)}`);
  // And said so. A copy leaves the screen exactly as it was, so without this
  // the gesture is indistinguishable from one that did nothing.
  console.log(`  and said so      ${tight("copied 12 characters") ? "yes" : "NO"}`);
  if (process.env["PASTE_DEBUG"] !== undefined) {
    console.log("--- what the screen says ---");
    console.log(pty.text());
  }

  await pty.close();
  process.exit(0);
}
