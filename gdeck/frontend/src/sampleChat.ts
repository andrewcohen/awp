// Sample turns, so the chat can be looked at without an agent behind it.
//
// The bindings only exist inside the Wails window, so the dev server at :9245
// renders the chat as an error — which means every styling question had to be
// answered by asking someone else what it looked like. This is the fixture that
// ends that: `?mock=1` renders the same components against data shaped like a
// real transcript, including the cases that are easy to get wrong — a long
// paragraph, a failed tool, a diff, an empty result.
export const sampleTurns = [
  {
    Kind: "user",
    At: "2026-08-18T14:02:00Z",
    Text: "the block glyph fix looks good. can you check whether the progress bar still moires at small sizes?",
    Thinking: "",
    Tools: [],
  },
  {
    Kind: "assistant",
    At: "2026-08-18T14:02:04Z",
    Text: "Checking at 12px, where the cell is 8x14 and the eighth blocks have the least room to round cleanly.",
    Thinking: "",
    Tools: [
      {
        Name: "Bash",
        Summary: "npm run build && node scripts/render-shades.mjs --size 12",
        Detail: "rendered 8 samples\nshade coverage: 25.0% 50.1% 74.9%\nno inter-cell phase error detected",
        IsError: false,
        File: "",
        Patch: "",
      },
    ],
  },
  {
    Kind: "assistant",
    At: "2026-08-18T14:02:31Z",
    Text: `Clean at 12px — the fills are flat and the eighths step evenly.

One rounding edge remains:

- \`Math.round(ch / 2)\` is computed twice, so an **odd** cell height leaves the bottom half a pixel taller
- visible only where a half block abuts a full one

| size | cell | waste |
| --- | --- | --- |
| 12px | 8x14 | 0.2px |
| 18px | 11x21 | 0.1px |

\`\`\`ts
const halfH = Math.round(ch / 2);
fill(x0, y0 + halfH, cw, ch - halfH);
\`\`\``,
    Thinking: "The half block uses Math.round(ch / 2) for its height but starts at y0, so an odd cell leaves the bottom half a pixel taller than the top. Better to derive the second half from the first rather than rounding twice.",
    Tools: [
      {
        Name: "Edit",
        Summary: "ghostty-web.js",
        Detail: "",
        IsError: false,
        File: "ghostty-web.js",
        Patch: `--- a/ghostty-web.js
+++ b/ghostty-web.js
@@ -1,6 +1,6 @@
 const halfH = Math.round(ch / 2);
 switch (cp) {
-  case 0x2580: fill(x0, y0, cw, Math.round(ch / 2)); return true;
-  case 0x2584: fill(x0, y0 + Math.round(ch / 2), cw, Math.round(ch / 2)); return true;
+  case 0x2580: fill(x0, y0, cw, halfH); return true;
+  case 0x2584: fill(x0, y0 + halfH, cw, ch - halfH); return true;
   case 0x2588: fill(x0, y0, cw, ch); return true;
 }
`,
      },
      {
        Name: "Bash",
        Summary: "npm run test -- glyphs",
        Detail: "FAIL src/glyphs.test.ts\n  ✕ half blocks tile without a seam (14ms)\n\n    expected 7, received 8",
        IsError: true,
        File: "",
        Patch: "",
      },
      { Name: "Read", Summary: "src/glyphs.test.ts", Detail: "", IsError: false, File: "", Patch: "" },
    ],
  },
];


// A transcript-sized fixture. `?mock=300` renders three hundred turns, which is
// what the tail of a real session looks like — the only honest way to measure
// scrolling, since three turns tell you nothing about a view whose cost is
// proportional to how many diffs are mounted.
export function manyTurns(count: number) {
  const out: (typeof sampleTurns)[number][] = [];
  for (let i = 0; i < count; i++) {
    const base = sampleTurns[i % sampleTurns.length];
    out.push({
      ...base,
      At: new Date(Date.now() - (count - i) * 60_000).toISOString(),
      Text: `${base.Text} (${i + 1})`,
    });
  }
  return out;
}
