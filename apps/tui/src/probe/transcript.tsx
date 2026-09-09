#!/usr/bin/env bun
// What a run of tool calls, an edit and a status row actually draw.
//
// No daemon and no tty: `createTestRenderer` renders into a buffer and
// `captureCharFrame` hands back the characters, so this prints the frame the
// terminal would have shown. Which is the only way to answer any of it — the
// interesting states are the ones a live agent happens not to be in, and the
// three here are all of that kind:
//
//   an edit          a patch under the row, in the same tree-sitter a fence
//                    uses. Before it was on the wire, this row was a title and
//                    a tick over the one call that changed something
//   a run of them    rolled up, with a count for the tail — and the edit NOT
//                    rolled up, which is the whole difference from the
//                    window's grouping
//   the status row   the session's own facts, where a chord list used to be
//
// Safe anywhere: it never opens a socket, never names a session and never
// spawns anything.

import { testRender } from "@opentui/react/test-utils";
import { type Item, grouped } from "../conversation";
import { Call, Calls, Message } from "../Items";
import { CHROME } from "../theme";

// A real patch, out of `createTwoFilesPatch`, because a hand-written one is
// nearly always wrong in the one way that matters: `<diff>` parses the hunk
// counts, and a header claiming seven lines over six draws
//
//   Error parsing diff: Added line count did not match for hunk at line 5
//
// which is the renderable doing exactly the right thing with a bad fixture.
const PATCH = [
  "Index: lines.ts",
  "===================================================================",
  "--- lines.ts",
  "+++ lines.ts",
  "@@ -1,6 +1,6 @@",
  " export const wrap = (text: string, width: number) => {",
  "   if (width <= 0) return [text];",
  "-  const out: string[] = [];",
  "+  const out: Array<string> = [];",
  '   for (const paragraph of text.split("\\n")) {',
  '     let line = "";',
  '     for (const word of paragraph.split(" ")) {',
  "",
].join("\n");

const receipt = (id: string, title: string, kind = "execute"): Item => ({
  kind: "tool",
  id,
  title,
  status: "completed",
  output: "",
  toolKind: kind,
});

const ITEMS: ReadonlyArray<Item> = [
  { kind: "said", role: "user", text: "make wrap return an Array", turn: 1 },
  { kind: "said", role: "agent", text: "Looking at how it is written now.", turn: 1 },
  receipt("t1", "rg -n 'const out' apps/tui/src"),
  receipt("t2", "apps/tui/src/lines.ts", "read"),
  receipt("t3", "apps/tui/src/Items.tsx", "read"),
  receipt("t4", "bun run typecheck"),
  receipt("t5", "bun run lint"),
  {
    kind: "tool",
    id: "t6",
    title: "Edit lines.ts",
    status: "completed",
    output: "",
    toolKind: "edit",
    diffs: [{ path: "/repo/apps/tui/src/lines.ts", patch: PATCH }],
  },
  receipt("t7", "bun run test"),
  { kind: "said", role: "agent", text: "Done — one line, and the gates are green.", turn: 1 },
];

const WIDTH = 76;

const App = () => (
  <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
    {grouped(ITEMS).map((block, at) =>
      block.kind === "calls" ? (
        <Calls key={at} items={block.items} inner={WIDTH} />
      ) : block.item.kind === "said" ? (
        <Message key={at} item={block.item} inner={WIDTH} />
      ) : (
        <Call key={at} item={block.item} inner={WIDTH} />
      ),
    )}
    <text
      height={1}
      bg={CHROME.bar}
      fg={CHROME.muted}
      wrapMode="none"
      content=" Manual · Opus · medium · fast: off · 14% context"
    />
  </box>
);

// `testRender` rather than a bare root: the reconciler commits inside React's
// own `act`, so the first frame captured is a frame with the tree in it. A
// plain `createRoot(...).render()` returns before any of that has happened,
// and what it captures is 34 rows of nothing — which is also exactly what a
// component that threw would capture.
const { renderer, captureCharFrame, renderOnce } = await testRender(<App />, {
  width: WIDTH,
  height: 34,
});
await renderOnce();
// Highlighting is asynchronous — the parser is loaded and the tree walked off
// the render path — so a frame captured straight after the first one is a
// frame of unhighlighted text. Measured: the same `+` line reads #ffffff at
// once and the palette's own colours a second later, and only the second is
// what a person sees.
await new Promise((resolve) => setTimeout(resolve, 1500));
await renderOnce();

const frame = captureCharFrame();
console.log(frame);

// What the frame has to contain, said out loud — a picture of a terminal is
// easy to glance at and call correct.
const has = (what: string) => (frame.includes(what) ? "yes" : "NO");
console.log(`\n  rolled up      ${has("earlier call")}`);
console.log(`  the edit drawn ${has("Edit lines.ts")}`);
console.log(`  its patch      ${has("Array<string>")}`);
console.log(`  line numbers   ${has(" 3 -") && has(" 3 +") ? "yes" : "NO"}`);
console.log(`  patch header   ${frame.includes("+++ lines.ts") ? "STILL THERE" : "not drawn"}`);
console.log(`  status row     ${has("% context")}`);

// ── and the colour, which the characters cannot show ────────────────────
//
// The first version of this drew the patch with `<code filetype="diff">` and
// captured a frame that looked plausible — because opentui ships four
// grammars and diff is not among them, so every line came out one colour and
// nothing said so. `<diff>` is the renderable for this, and what it puts the
// add and the remove in is a **background**: the foreground is the code's own
// highlighting, which is the point of handing it a filetype.
const hex = (c: { r: number; g: number; b: number }) =>
  `#${[c.r, c.g, c.b]
    .map((one) =>
      Math.round(one * 255)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;

// Per line, not per span: once the code is highlighted a row is a dozen small
// spans and no single one holds a phrase. The row's colour is its background;
// the code's colour is whatever token `const` came out as.
const paint = (needle: string, token: string): string => {
  for (const line of renderer.currentRenderBuffer.getSpanLines()) {
    const text = line.spans.map((span) => span.text).join("");
    if (!text.includes(needle)) continue;
    // The keyword's own cell, and not the first span on the row: the line
    // number sits in a gutter whose background is deliberately transparent,
    // so sampling there reads the page and reports every row as unmarked.
    const cell = line.spans.find((span) => span.text.trim() === token);
    return cell === undefined
      ? `no \`${token}\` on the row`
      : `bg ${hex(cell.bg)}  ${token} ${hex(cell.fg)}`;
  }
  return "not found";
};

console.log(`\n  the + row      ${paint("const out: Array", "const")}`);
console.log(`  the - row      ${paint("const out: string", "const")}`);
console.log(`  a context row  ${paint("if (width <= 0)", "if")}`);
console.log(`  wanted         added ${CHROME.addedBg} · removed ${CHROME.removedBg}`);

// Not `renderer.destroy()`: the unmount it triggers is a React update outside
// `act`, and React says so at length in the middle of the output.
process.exit(0);
