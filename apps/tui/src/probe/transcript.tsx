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

import { createMockMouse } from "@opentui/core/testing";
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

const receipt = (id: string, title: string, name = "Bash", purpose = ""): Item => ({
  kind: "tool",
  id,
  title,
  status: "completed",
  output: "",
  // What the call was for, which Bash's schema requires and the adapter
  // forwards. The row draws this and keeps the command underneath — the
  // whole reason a heredoc is no longer forty characters of nothing.
  ...(purpose === "" ? {} : { purpose }),
  // The tool's own name, which is what a row is labelled with — the kind is
  // ten words for fifty tools and put `execute` on most of this fixture.
  toolName: name,
  // The turn is what decides whether a run is drawn whole or rolled up, so
  // every fixture row carries one — a row without it is treated as finished.
  turn: 1,
});

const ITEMS: ReadonlyArray<Item> = [
  { kind: "said", role: "user", text: "make wrap return an Array", turn: 1 },
  {
    kind: "said",
    role: "agent",
    // Everything being drawn as `text` used to throw away: a heading, a
    // list, a table, bold, and inline code. The fence below it is the half
    // that must NOT be wrapped, and is why `segments` still splits.
    text: [
      "## What is written now",
      "",
      "`wrap` returns a **string array**, and the two callers that matter are",
      "in `Items.tsx` and in the composer.",
      "",
      "- the composer, which counts the lines to size itself",
      "- the message body, which draws them",
      "",
      "| caller   | wants     |",
      "| -------- | --------- |",
      "| composer | a count   |",
      "| message  | the lines |",
      "",
      "```ts",
      "const rows: string[] = []; // a fence, whose breaks are the content",
      "```",
    ].join("\n"),
    turn: 1,
  },
  receipt("t1", "const out", "Grep"),
  receipt("t2", "apps/tui/src/lines.ts", "Read"),
  receipt("t3", "apps/tui/src/Items.tsx", "Read"),
  receipt("t4", "bun run typecheck", "Bash", "Check the types"),
  receipt("t5", "bun run lint"),
  {
    kind: "tool",
    id: "t6",
    title: "Edit lines.ts",
    status: "completed",
    output: "",
    toolName: "Edit",
    turn: 1,
    diffs: [{ path: "/repo/apps/tui/src/lines.ts", patch: PATCH }],
  },
  receipt("t7", "bun run test"),
  // Still going, which is the state the turning mark exists for — and the
  // one a live agent is in exactly when nobody is running a probe.
  {
    kind: "tool",
    id: "t8",
    title: "bun run build",
    purpose: "Build the renderer",
    status: "in_progress",
    output: "",
    toolName: "Bash",
    turn: 1,
  },
  // ── the case where the command must be on screen ──────────────────────
  //
  // A call waiting on a permission, drawn as its purpose. Approving
  // "Remove the build output" without seeing `rm -rf` is exactly the
  // decision nobody should be asked to make from a description, so a call
  // that stands alone shows both.
  {
    kind: "tool",
    id: "t9",
    title: "rm -rf dist .tsbuild",
    purpose: "Remove the build output",
    status: "asking",
    output: "",
    toolName: "Bash",
    turn: 1,
    ask: {
      request: "permission-0",
      options: [
        { id: "allow", label: "Allow once" },
        { id: "deny", label: "Deny" },
      ],
    },
  },
  // An MCP tool, because the shortening is a rule with a name in it:
  // `mcp__awp__awp_tasks` is drawn as `awp_tasks`, the server being in the
  // title already.
  receipt("t8", "awp_tasks", "mcp__awp__awp_tasks"),
  { kind: "said", role: "agent", text: "Done — one line, and the gates are green.", turn: 1 },
];

const WIDTH = 76;

// Which turn is still going, as an argument, because the two states of a run
// of calls are the whole of what changed here: open while the agent is
// working, one line once it has stopped. `bun src/probe/transcript.tsx live`
// draws the first.
const live = process.argv[2] === "live" ? 1 : undefined;

const App = () => (
  <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
    {grouped(ITEMS, live).map((block, at) =>
      block.kind === "calls" ? (
        <Calls key={at} items={block.items} inner={WIDTH} live={block.live} />
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
  height: 46,
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

// ── and the summary opens again ─────────────────────────────────────────
//
// A rolled-up run is an offer to look, so the row is a control. There is no
// chord for it — a transcript has no focus model, and every row would have
// to be reachable before one row could be — so it is a click, which is
// something this app hears at all only because opentui owns the mouse.
//
// Driven rather than reasoned about: `createMockMouse` puts a real press
// through the renderer's own hit testing, which is the half that a call to
// the handler would skip.
const rolled = captureCharFrame()
  .split("\n")
  .findIndex((row) => row.includes("ran 5 tools"));
if (rolled >= 0 && live === undefined) {
  const mouse = createMockMouse(renderer);
  await mouse.click(6, rolled);
  await renderOnce();
  const opened = captureCharFrame();
  console.log(`  a click opens  ${opened.includes("bun run lint") ? "yes" : "NO"}`);
  await mouse.click(6, rolled);
  await renderOnce();
  console.log(`  and shuts      ${captureCharFrame().includes("bun run lint") ? "NO" : "yes"}`);
}

// What the frame has to contain, said out loud — a picture of a terminal is
// easy to glance at and call correct.
const has = (what: string) => (frame.includes(what) ? "yes" : "NO");
console.log(`\n  rolled up      ${has("ran 5 tools")}`);
console.log(`  the edit drawn ${has("Edit lines.ts")}`);
console.log(`  its patch      ${has("Array<string>")}`);
console.log(`  line numbers   ${has(" 3 -") && has(" 3 +") ? "yes" : "NO"}`);
console.log(`  patch header   ${frame.includes("+++ lines.ts") ? "STILL THERE" : "not drawn"}`);
console.log(`  status row     ${has("% context")}`);
// A row drawn as what the call was FOR, with the command still under it.
// Approving `rm -rf` from a description alone is the decision nobody should
// be asked to make, so both have to be on screen.
console.log(`  the purpose    ${has("Remove the build output")}`);
console.log(`  the command    ${has("rm -rf dist .tsbuild")}`);
// Only visible in the live run: a finished run is one line, which is the
// other half of this fixture. Asserting it either way would be a check that
// passes for the wrong reason half the time.
console.log(
  `  a receipt's    ${live === undefined ? "rolled up — run with 'live'" : has("Check the types")}`,
);

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
    const cell =
      line.spans.find((span) => span.text.trim() === token) ??
      line.spans.find((span) => span.text.includes(token));
    return cell === undefined
      ? `no \`${token}\` on the row`
      : `bg ${hex(cell.bg)}  ${token} ${hex(cell.fg)}`;
  }
  return "not found";
};

// ── and the message, which is markdown now ─────────────────────────────
//
// The characters alone cannot tell a rendered heading from a paragraph that
// happens to say the same words: what says it is the colour and the weight.
// `##` being gone is the other half — a marker left on screen is what a
// renderer that did not parse looks like.
console.log(`\n  no literal ##  ${frame.includes("## What") ? "STILL THERE" : "parsed"}`);
console.log(`  the heading    ${paint("What is written now", "What")}`);
console.log(`  inline code    ${paint("wrap returns a string", "wrap")}`);
console.log(`  a table drawn  ${frame.includes("│caller") ? "yes" : "NO"}`);
console.log(
  `  the fence      ${frame.includes("// a fence, whose breaks are the content") ? "unwrapped" : "WRAPPED"}`,
);

console.log(`\n  the + row      ${paint("const out: Array", "const")}`);
console.log(`  the - row      ${paint("const out: string", "const")}`);
console.log(`  a context row  ${paint("if (width <= 0)", "if")}`);
console.log(`  wanted         added ${CHROME.addedBg} · removed ${CHROME.removedBg}`);

// Not `renderer.destroy()`: the unmount it triggers is a React update outside
// `act`, and React says so at length in the middle of the output.
process.exit(0);
