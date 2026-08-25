import { describe, expect, it } from "vitest";
import { WHEEL_DOWN, WHEEL_UP, wheelLines, wheelReport } from "./wheel";

// Whether a wheel event should reach the program at all is the terminal's
// question — `buffer.active.type` and `hasMouseTracking()` — and needs a canvas
// to ask. What is testable here is the arithmetic and the spelling.

const ESC = "\u001B";
const cell = { height: 20, rows: 30 };
const pixels = (deltaY: number) => wheelLines({ deltaY, deltaMode: 0 }, cell);

describe("how far one event asks to scroll", () => {
  // Always at least one line. Rounding alone drops every small movement — a
  // trackpad reports a stream of two- and three-pixel events — and a surface
  // that ignores gentle movement reads as broken rather than as precise.
  it("moves at least a line, however small the movement", () => {
    expect(pixels(1)).toBe(1);
    expect(pixels(2)).toBe(1);
    expect(pixels(-2)).toBe(1);
  });

  it("scales with the distance moved", () => {
    expect(pixels(20)).toBe(1);
    expect(pixels(60)).toBe(3);
  });

  // The direction is the caller's business — it picks the button. This answers
  // "how far", and a distance has no sign.
  it("reports a distance, not a direction", () => {
    expect(pixels(-60)).toBe(pixels(60));
  });

  // deltaMode is the browser saying what unit deltaY is in. Treating a line
  // delta as pixels is the difference between one line and one pixel.
  it("reads line and page deltas in their own units", () => {
    expect(wheelLines({ deltaY: 3, deltaMode: 1 }, cell)).toBe(3);
    // A page is a screen, and this cap sits above one deliberately — a cap at a
    // screen would quietly break page scrolling.
    expect(wheelLines({ deltaY: 1, deltaMode: 2 }, { height: 20, rows: 8 })).toBe(8);
  });

  it("does not hand a program hundreds of reports from one flick", () => {
    expect(pixels(100_000)).toBe(10);
  });

  it("survives a cell height of zero rather than dividing by it", () => {
    expect(wheelLines({ deltaY: 40, deltaMode: 0 }, { height: 0, rows: 30 })).toBe(10);
  });
});

describe("spelling a wheel turn for the program", () => {
  it("writes one SGR report per line, in one string", () => {
    expect(wheelReport(WHEEL_UP, 12, 34, 1)).toBe(`${ESC}[<64;12;34M`);
    expect(wheelReport(WHEEL_DOWN, 12, 34, 3)).toBe(`${ESC}[<65;12;34M`.repeat(3));
  });

  // SGR unconditionally, and the reason is the column. The original encoding
  // puts each field in a single byte offset by 32, so it cannot say 400 at all
  // — and this pane is regularly wider than 223 cells.
  it("says a column the original encoding could not", () => {
    expect(wheelReport(WHEEL_UP, 400, 10, 1)).toBe(`${ESC}[<64;400;10M`);
  });

  it("says nothing for no lines", () => {
    expect(wheelReport(WHEEL_UP, 1, 1, 0)).toBe("");
  });
});
