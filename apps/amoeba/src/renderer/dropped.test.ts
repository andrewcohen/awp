import { describe, expect, it } from "vitest";
import { spliced } from "./dropped";

describe("spliced", () => {
  it("puts the path at the caret and answers where the caret goes", () => {
    const { text, caret } = spliced("look at  please", "/tmp/a.png", 8, 8);
    expect(text).toBe("look at /tmp/a.png please");
    expect(text.slice(0, caret)).toBe("look at /tmp/a.png");
  });

  it("separates from what it lands against, on both sides", () => {
    expect(spliced("read:then", "/tmp/a", 5, 5).text).toBe("read: /tmp/a then");
  });

  it("adds no space it does not need", () => {
    // Both ends of the text and both sides already spaced: four chances to
    // add a stray space, and the reason this is a function rather than a
    // template literal at each call site.
    expect(spliced("", "/tmp/a", 0, 0).text).toBe("/tmp/a");
    expect(spliced("x ", "/tmp/a", 2, 2).text).toBe("x /tmp/a");
    expect(spliced(" y", "/tmp/a", 0, 0).text).toBe("/tmp/a y");
  });

  it("replaces a selection rather than inserting beside it", () => {
    const { text } = spliced("about that file there", "/tmp/a", 11, 15);
    expect(text).toBe("about that /tmp/a there");
  });
});
