import { describe, expect, it } from "vitest";
import { WHEEL_DOWN, WHEEL_UP, applyModes, encodeWheel, initialModes } from "./modes";

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
