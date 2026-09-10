#!/usr/bin/env bun
// What the command menu draws, and whether a bar reaches both edges.
//
// Two questions a live screen cannot answer. The menu's interesting states
// need an agent that has advertised fifty-odd skills and a highlight walked
// past the sixth row, and the bar question is about a *background* — which is
// invisible in a screenshot of a phrase that happens to be wide.
//
// No daemon, no tty, no socket: `testRender` draws into a buffer and the
// spans come back with their colours, which is the only way to tell a bar
// from a coloured phrase.

import { testRender } from "@opentui/react/test-utils";
import { agentCommands, matching } from "@awp-kit/protocol/commands";
import { Menu } from "../Chat";
import { CHROME } from "../theme";

/** What an adapter pushes: `available_commands_update`, 57 of them here. */
const THEIRS = agentCommands(
  [
    { name: "/bro", description: "Restate the last message in plain human language" },
    { name: "/commit", description: "Create a Conventional Commit message" },
    { name: "/review", description: "Review the current change" },
    { name: "/pr", description: "Open a pull request", hint: "[base]" },
    { name: "/usage", description: "What this subscription has spent" },
    { name: "/init", description: "Write a CLAUDE.md for this repository" },
    { name: "/simplify", description: "Reuse, simplify, and apply the fixes" },
    { name: "/loop", description: "Run a prompt on an interval", hint: "<interval>" },
  ].map((one) => ({ ...one })),
);

const WIDTH = 72;
const at = Number(process.argv[3] ?? 0);

const App = () => (
  <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
    <box height={1} backgroundColor={CHROME.accent}>
      <text fg={CHROME.base} wrapMode="none" content=" thicket/lantern · idle " />
    </box>
    <box flexGrow={1} />
    <Menu commands={matching(process.argv[2] ?? "/", THEIRS)} at={at} width={WIDTH} />
    <text height={1} content=" " />
    <box height={1} backgroundColor={CHROME.raised}>
      <text fg={CHROME.accent} wrapMode="none" content={`> ${process.argv[2] ?? "/"}`} />
    </box>
    <text height={1} content=" " />
    <box height={1} backgroundColor={CHROME.bar}>
      <text fg={CHROME.muted} wrapMode="none" content=" tab to complete · return to run" />
    </box>
  </box>
);

const { renderer, captureCharFrame, renderOnce } = await testRender(<App />, {
  width: WIDTH,
  height: 16,
});
await renderOnce();
console.log(captureCharFrame());

const hex = (c: { r: number; g: number; b: number }) =>
  `#${[c.r, c.g, c.b]
    .map((one) =>
      Math.round(one * 255)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;

const lines = renderer.currentRenderBuffer.getSpanLines();
/** The colour of the last cell on a row: a bar reaches it, a phrase does not. */
const rightEdge = (row: number): string => {
  const line = lines[row];
  if (line === undefined) return "no row";
  const span = line.spans.at(-1);
  return span === undefined ? "empty" : hex(span.bg);
};

const rows = lines.map((line) => line.spans.map((span) => span.text).join(""));
const drawn = rows.filter((row) => row.trimEnd().startsWith(" /")).length;

console.log(`\n  command rows   ${String(drawn)} (cap is 6)`);
console.log(`  the count      ${rows.some((row) => row.includes("more")) ? "yes" : "no"}`);
// The highlighted row is a *band* or it is a coloured phrase: what tells
// them apart is the far right cell, which is past every character on it.
const marked = rows.findIndex((row) => row.trimEnd().startsWith(" /"));
console.log(
  `  highlight edge ${rightEdge(marked + at - Math.max(0, at - 5))}  wanted ${CHROME.bar}`,
);
console.log(`  header edge    ${rightEdge(0)}  wanted ${CHROME.accent}`);
console.log(`  status edge    ${rightEdge(15)}  wanted ${CHROME.bar}`);
console.log(`  blank above    ${rows[12]?.trim() === "" ? "yes" : "NO"}`);
console.log(`  blank below    ${rows[14]?.trim() === "" ? "yes" : "NO"}`);
console.log(`  no overrun     ${rows.every((row) => row.length <= WIDTH) ? "yes" : "NO"}`);

process.exit(0);
