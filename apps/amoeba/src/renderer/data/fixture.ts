// Bytes chosen to fail visibly if a specific renderer fix is missing.
//
// Each block below corresponds to one of the four fixes in
// `patches/ghostty-web@0.4.0.patch`. Looking at this pane is how the patch is
// verified — a screenshot nobody diffs proves nothing, but a row of box
// junctions that visibly do not meet proves one thing exactly.

const ESC = "";
const sgr = (...codes: number[]) => `${ESC}[${codes.join(";")}m`;
const reset = sgr(0);

const line = (text: string) => `${text}\r\n`;

export const rendererFixture = [
  line(`${sgr(1)}amoeba — renderer check${reset}`),
  line(""),

  // 1. Row height from the font's line box, not one glyph's ink.
  //    Measuring "M" — a letter with no descender — gave an 18px font a 17px
  //    row, and the next row's background fill sliced the descenders off.
  line(`${sgr(2)}descenders   ${reset}gjpqy_ ${sgr(2)}— tails must not be clipped${reset}`),

  // 2. A glyph confined to its cell.
  //    Bold and fallback glyphs are routinely wider than a cell measured from
  //    regular "M", and only dirty rows repaint, so any spill stays on screen.
  line(
    `${sgr(2)}wide glyphs  ${reset}${sgr(1)}BOLD${reset} 漢字 🙂 ${sgr(2)}— no bleeding into neighbours${reset}`,
  ),

  // 3. Stems thickened by a second offset fill.
  //    Core Text dilates stems and Canvas2D does not, so canvas text reads thin
  //    against every other app. The obvious strokeText spelling of this cost
  //    p50 734ms per keystroke in WKWebView.
  line(
    `${sgr(2)}weight       ${reset}iIlL1 ${sgr(1)}iIlL1${reset} ${sgr(2)}— should not look washed out${reset}`,
  ),

  // 4. Block, shade and box-drawing drawn as snapped rectangles.
  //    Ghostty rasterises U+2500-U+259F itself. Taken from the font instead,
  //    shades moiré and junctions fail to meet.
  line(""),
  line(
    `${sgr(2)}shades       ${reset}░░░░░░░░▒▒▒▒▒▒▒▒▓▓▓▓▓▓▓▓████████ ${sgr(2)}— flat, no hatching${reset}`,
  ),
  line(`${sgr(2)}box joins    ${reset}┌───┬───┐`),
  line(`             │   │   │`),
  line(`             ├───┼───┤ ${sgr(2)}— corners must meet${reset}`),
  line(`             │   │   │`),
  line(`             └───┴───┘`),
  line(""),

  // Colour, so the palette is visible at the same time.
  line(
    `${sgr(2)}palette      ${reset}${[31, 32, 33, 34, 35, 36]
      .map((c) => `${sgr(c)}███${reset}`)
      .join("")} ${[91, 92, 93, 94, 95, 96].map((c) => `${sgr(c)}███${reset}`).join("")}`,
  ),
].join("");
