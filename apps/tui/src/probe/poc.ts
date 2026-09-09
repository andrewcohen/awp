#!/usr/bin/env bun
// The whole POC, driven: threads → the agent → a terminal → out.
//
// Every screen is checked against what the child painted, and the row it opens
// is this repository's own workspace — never somebody else's work. Opening a
// conversation is a read, and the terminal is a plain shell in a directory, so
// nothing here starts, steers or resizes an agent.
//
// Assertions are on short tokens rather than whole phrases, deliberately: a
// TUI writes the cells that changed, so a footer painted over another footer
// reaches this stream with the unchanged middle missing.

import { openPty, until, wait } from "./pty";

const WORKSPACE = "awp/opentui-terminal-poc";
const say = (label: string, value: unknown) =>
  process.stderr.write(
    `  ${label.padEnd(14)}${typeof value === "string" ? value : JSON.stringify(value)}\n`,
  );

const tui = openPty(["bun", "src/main.tsx"], { cols: 110, rows: 28 });

try {
  await until("the thread list", () => tui.saw("threads"), 20_000);
  say("threads", tui.saw("enter agent · s terminal") ? "listed, with the keys offered" : "MISSING");
  say("rows", tui.text().includes("▸") ? "threads and their checkouts" : "MISSING");

  // Down one row, onto this repository's own workspace, and open the agent.
  //
  // An arrow, never 0x0A: that byte is ctrl+j *and* Enter, so the first draft
  // of this line opened a conversation on somebody else's checkout — which is
  // a real adapter started in a real directory, one row above the intended one.
  tui.send(`${String.fromCodePoint(27)}[B`);
  await wait(400);
  tui.send("\r");
  await until("the conversation", () => tui.saw("ctrl-\\ back"), 20_000);
  say("chat", tui.saw(WORKSPACE) ? `open on ${WORKSPACE}` : "MISSING");
  await wait(2500);
  say(
    "transcript",
    /[·✓✗]/u.test(tui.text().slice(-4000)) ? "the conversation is drawn" : "nothing said yet",
  );
  say("composer", tui.saw("say something to the agent") ? "placeholder shown" : "MISSING");

  // Back, then a terminal in the same checkout.
  tui.send(String.fromCodePoint(28));
  const leftChatAt = tui.text().length;
  await until("the list again", () => tui.text().slice(leftChatAt).includes("▸"), 10_000);
  say("back", "the list came back");

  tui.send("s");
  // Waited on by asking the shell a question rather than by reading the
  // header. A TUI paints the cells that changed, so the directory in the
  // header arrived here as `…poc/openui-terminal-poc` — the two screens'
  // text interleaved. Only what a program writes in one go is safe to match.
  await wait(2500);
  tui.send("basename \"$PWD\" | tr -d '\\n' | sed 's/^/HERE:/;s/$/:END/'\r");
  await until("the shell to answer", () => tui.saw("HERE:opentui-terminal-poc:END"), 20_000);
  say("terminal", "a shell, running in the checkout's own directory");

  const leftAt = tui.text().length;
  tui.send(String.fromCodePoint(28));
  await wait(900);
  // `▸` is content the repaint has to write; the footer may not be rewritten
  // at all when the new one shares its opening characters with the old.
  say("back", tui.text().slice(leftAt).includes("▸") ? "the list came back" : "MISSING");

  tui.send("q");
  await wait(1000);
  const settled = tui.bytes();
  await wait(800);
  say("quit", tui.bytes() === settled ? "painting stopped" : "STILL PAINTING");
} finally {
  await tui.close();
  say("closed", "the process exited");
}
