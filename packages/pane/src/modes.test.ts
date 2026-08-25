import { describe, expect, it } from "vitest";
import {
  WHEEL_DOWN,
  WHEEL_UP,
  applyModes,
  encodeWheel,
  initialModes,
  noCarry,
  wheelLines,
} from "./modes";

// The wheel handler itself needs a canvas and cannot be tested here. This can
// be, and it is where the decisions are: what the program asked for, and how a
// turn of the wheel is spelled back to it.

const ESC = "\u001B";
const after = (...chunks: ReadonlyArray<string>) =>
  chunks.reduce((modes, chunk) => applyModes(modes, chunk), initialModes);

describe("reading private modes off the stream", () => {
  it("starts assuming a plain shell", () => {
    expect(initialModes).toEqual({
      alternateScreen: false,
      mouseTracking: false,
      sgrMouse: false,
    });
  });

  it("sees a full-screen program arrive and leave", () => {
    expect(after(`${ESC}[?1049h`).alternateScreen).toBe(true);
    expect(after(`${ESC}[?1049h`, `${ESC}[?1049l`).alternateScreen).toBe(false);
  });

  // 1049 is the modern spelling, but 47 and 1047 are still emitted — by vim
  // among others — and a pane that only knew the new one would send arrows into
  // an editor.
  it.each([47, 1047, 1049])("recognises %i as the alternate screen", (mode) => {
    expect(after(`${ESC}[?${mode}h`).alternateScreen).toBe(true);
  });

  // Any of the tracking modes means the wheel is wanted. A program that asked
  // for motion reporting has certainly asked for buttons.
  it.each([1000, 1002, 1003])("takes %i as asking for the mouse", (mode) => {
    expect(after(`${ESC}[?${mode}h`).mouseTracking).toBe(true);
  });

  it("reads several modes from one sequence", () => {
    const modes = after(`${ESC}[?1049;1002;1006h`);
    expect(modes).toEqual({ alternateScreen: true, mouseTracking: true, sgrMouse: true });
  });

  it("carries what it learned across chunks", () => {
    // The real stream arrives in pieces; the modes are not re-read from
    // scratch each time.
    const modes = after(`${ESC}[?1049h`, "some output\r\n", `${ESC}[?1006h`);
    expect(modes.alternateScreen).toBe(true);
    expect(modes.sgrMouse).toBe(true);
  });

  it("ignores modes it has no opinion about", () => {
    // Bracketed paste, cursor visibility, wrap. Present in almost every
    // stream, and none of them change what a wheel should do.
    expect(after(`${ESC}[?2004h${ESC}[?25l${ESC}[?7h`)).toEqual(initialModes);
  });

  it("is not fooled by the digits appearing in ordinary output", () => {
    expect(after("mode 1049h was set, apparently")).toEqual(initialModes);
  });
});

describe("spelling a wheel turn back to the program", () => {
  it("uses SGR when the program asked for it", () => {
    const modes = after(`${ESC}[?1000;1006h`);
    expect(encodeWheel(modes, WHEEL_UP, 12, 34)).toBe(`${ESC}[<64;12;34M`);
    expect(encodeWheel(modes, WHEEL_DOWN, 12, 34)).toBe(`${ESC}[<65;12;34M`);
  });

  it("falls back to the original encoding when it did not", () => {
    const modes = after(`${ESC}[?1000h`);
    // Each field is one byte, offset by 32: 64+32 = 96 = "`", 12+32 = 44 = ",".
    expect(encodeWheel(modes, WHEEL_UP, 12, 34)).toBe(`${ESC}[M\`,B`);
  });

  // The reason SGR matters, stated as a test rather than as a comment: this
  // pane is regularly wider than 223 columns, and the old encoding cannot say
  // so. Clamping keeps the report at the edge instead of wrapping it to a
  // column near zero, which would look like a deliberate click somewhere else.
  it("clamps a column the old encoding cannot express", () => {
    const legacy = after(`${ESC}[?1000h`);
    expect(encodeWheel(legacy, WHEEL_UP, 400, 10)).toBe(
      `${ESC}[M\`${String.fromCharCode(223 + 32)}${String.fromCharCode(42)}`,
    );
    // SGR has no such limit and says the real number.
    const sgr = after(`${ESC}[?1000;1006h`);
    expect(encodeWheel(sgr, WHEEL_UP, 400, 10)).toBe(`${ESC}[<64;400;10M`);
  });
});

describe("turning wheel events into lines", () => {
  const cell = { height: 20, rows: 30 };

  const drag = (deltas: ReadonlyArray<number>, deltaMode = 0) => {
    let carry = noCarry;
    let lines = 0;
    for (const deltaY of deltas) {
      const step = wheelLines(carry, { deltaY, deltaMode }, cell);
      carry = step.carry;
      lines += step.lines;
    }
    return { lines, carry };
  };

  // The bug this exists for. A trackpad reports a stream of two- and
  // three-pixel events; the first version took at least one line from each, so
  // resting a finger on the surface scrolled at sixty lines a second.
  it("does not turn every stray pixel into a line", () => {
    expect(drag([2]).lines).toBe(0);
    expect(drag([2, 3, 2]).lines).toBe(0);
  });

  // And it must not do the opposite either: rounding without a floor loses
  // every small event and the surface never moves at all.
  it("delivers a line once a line's worth of movement has happened", () => {
    // Ten events of two pixels is one cell exactly.
    expect(drag([2, 2, 2, 2, 2, 2, 2, 2, 2, 2]).lines).toBe(1);
  });

  it("keeps the remainder rather than dropping it", () => {
    // Three lines' worth plus a bit, delivered in awkward pieces.
    const { lines, carry } = drag([25, 25, 12]);
    expect(lines).toBe(3);
    expect(carry.pixels).toBe(2);
  });

  it("scrolls as far as the hand moved, however the events were split", () => {
    // The property that matters: the same distance in one event or in twenty.
    expect(drag([200]).lines).toBe(drag(Array.from({ length: 20 }, () => 10)).lines);
  });

  it("goes up for a negative delta and down for a positive one", () => {
    expect(drag([-40]).lines).toBe(-2);
    expect(drag([40]).lines).toBe(2);
  });

  // A carry left pointing the other way would make the first movement of a
  // reversal shorter than asked for.
  it("changes direction without owing anything to the old one", () => {
    let carry = noCarry;
    const down = wheelLines(carry, { deltaY: 30, deltaMode: 0 }, cell);
    carry = down.carry;
    expect(down.lines).toBe(1);
    // 10px of carry down, then 30px up: net 20px up, exactly one line.
    const up = wheelLines(carry, { deltaY: -30, deltaMode: 0 }, cell);
    expect(up.lines).toBe(-1);
  });

  // deltaMode is the browser saying what unit deltaY is in. Treating a line
  // delta as pixels is the difference between one line and one pixel.
  it("reads line and page deltas in their own units", () => {
    expect(wheelLines(noCarry, { deltaY: 3, deltaMode: 1 }, cell).lines).toBe(3);
    expect(wheelLines(noCarry, { deltaY: 1, deltaMode: 2 }, cell).lines).toBe(cell.rows);
  });

  // macOS momentum keeps delivering after a hard flick, and a pane nobody is
  // watching still receives it. Past a screenful it is scrolling no one asked
  // to see arrive.
  // A page-mode delta is a whole screen by definition, so the cap has to sit
  // above one screen or it would quietly break page scrolling — which is how
  // the first version of it was found.
  it("delivers a full page for a page-mode delta", () => {
    expect(wheelLines(noCarry, { deltaY: 1, deltaMode: 2 }, cell).lines).toBe(cell.rows);
  });

  it("bounds one absurd delta without banking the rest", () => {
    const flick = wheelLines(noCarry, { deltaY: 100_000, deltaMode: 0 }, cell);
    expect(flick.lines).toBe(cell.rows * 3);
    // And the excess is discarded rather than carried, or the pane would keep
    // moving long after the hand stopped.
    expect(Math.abs(flick.carry.pixels)).toBeLessThan(cell.height);
  });

  it("survives a cell height of zero rather than dividing by it", () => {
    expect(wheelLines(noCarry, { deltaY: 40, deltaMode: 0 }, { height: 0, rows: 30 }).lines).toBe(
      40,
    );
  });
});
