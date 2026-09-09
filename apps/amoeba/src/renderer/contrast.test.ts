import { describe, expect, test } from "vitest";
import { channels, hexOf, ratio, verdict } from "./contrast";

// The arithmetic, against the two ratios everybody knows: black on white is
// 21, and a colour against itself is 1. Anything wrong with the curve shows up
// in the first of those immediately.

describe("reading a painted colour", () => {
  test("rgb and rgba are both read", () => {
    expect(channels("rgb(30, 32, 48)")).toEqual([30, 32, 48]);
    expect(channels("rgba(30, 32, 48, 0.5)")).toEqual([30, 32, 48]);
  });

  test("a hex is read too, in both spellings", () => {
    // A candidate palette and the terminal's slots are literals, not variables
    // this window can sample off an element — and they still have to be
    // measured against the ground they would live on.
    expect(channels("#1e2030")).toEqual([30, 32, 48]);
    expect(channels("#FFF")).toEqual([255, 255, 255]);
    expect(ratio("#000000", "#ffffff")).toBeCloseTo(21, 2);
  });

  test("a colour space this cannot read reports as unmeasurable, not as a number", () => {
    // The failure that matters: a plausible wrong ratio would send somebody to
    // darken a token that was fine. `lab()` is what would arrive if a token
    // were ever written in one.
    expect(channels("lab(39 -11 -5)")).toBeUndefined();
    expect(ratio("lab(39 -11 -5)", "rgb(0, 0, 0)")).toBeUndefined();
    expect(verdict(undefined)).toBe("unmeasurable");
  });
});

describe("the ratio", () => {
  test("black on white is 21", () => {
    expect(ratio("rgb(0, 0, 0)", "rgb(255, 255, 255)")).toBeCloseTo(21, 2);
  });

  test("a colour against itself is 1", () => {
    expect(ratio("rgb(76, 79, 105)", "rgb(76, 79, 105)")).toBeCloseTo(1, 5);
  });

  test("order does not matter — the lighter one is always the numerator", () => {
    expect(ratio("rgb(0, 0, 0)", "rgb(255, 255, 255)")).toBe(
      ratio("rgb(255, 255, 255)", "rgb(0, 0, 0)"),
    );
  });
});

describe("the verdict", () => {
  test("the thresholds are named where they are crossed", () => {
    expect(verdict(7.01)).toContain("AAA");
    expect(verdict(4.6)).toContain("AA");
    // Between the mark floor and the text floor: legible as a dot, not as a
    // word. `live` is both in this window, on the same row.
    expect(verdict(3.2, 3)).toContain("mark");
    expect(verdict(2.4, 3)).toContain("FAIL");
  });
});

describe("the hex", () => {
  test("a painted colour reads back as one", () => {
    expect(hexOf("rgb(30, 32, 48)")).toBe("#1e2030");
    // Rounded, because a computed colour can arrive fractional.
    expect(hexOf("rgba(255, 255, 255, 1)")).toBe("#ffffff");
  });

  test("what cannot be read has no hex either", () => {
    expect(hexOf("lab(39 -11 -5)")).toBeUndefined();
  });
});

describe("a colour nobody painted", () => {
  test("fully transparent is unmeasurable, not black", () => {
    // The whole style guide read `FAIL` on every hue because this answered
    // [0, 0, 0]: `getComputedStyle` gives `rgba(0, 0, 0, 0)` for an element
    // with no background of its own, and every ink was then measured against
    // black in both themes. A wrong number is worse than no number — it sends
    // somebody to darken a token that was already fine.
    expect(channels("rgba(0, 0, 0, 0)")).toBeUndefined();
    expect(channels("rgba(30, 32, 48, 0)")).toBeUndefined();
    expect(ratio("rgb(76, 79, 105)", "rgba(0, 0, 0, 0)")).toBeUndefined();
    expect(hexOf("rgba(0, 0, 0, 0)")).toBeUndefined();
  });

  test("the modern spelling too", () => {
    expect(channels("rgb(0 0 0 / 0)")).toBeUndefined();
    expect(channels("rgb(30 32 48 / 0.5)")).toEqual([30, 32, 48]);
  });
});
