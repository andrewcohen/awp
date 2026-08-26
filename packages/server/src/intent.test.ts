import { describe, expect, test } from "vitest";
import { findObject, slug, validate } from "./intent";

// The three pure parts, which is where every claim about safety lives.
//
// The model call itself is exercised by `probe:intent`, because it needs a real
// claude and takes about seven seconds. What is tested here is what happens to
// its answer — and the answer is untrusted input, so this is the part that has
// to be right.

describe("findObject", () => {
  test("plain JSON", () => {
    expect(findObject('{"name":"a"}')).toEqual({ name: "a" });
  });

  test("a code fence around it", () => {
    // Measured 2026-08-26 — this is what one real reply looked like.
    expect(findObject('```json\n{"name":"a"}\n```')).toEqual({ name: "a" });
  });

  test("a sentence before it", () => {
    // Also measured, and the reason this function exists rather than
    // JSON.parse. A global CLAUDE.md is in force for a headless call too, so a
    // reply came back beginning "Dearest Mister Duck," before the object.
    expect(findObject('Dearest Mister Duck,\n\n{"name":"a"}')).toEqual({ name: "a" });
  });

  test("no object at all", () => {
    expect(findObject("I cannot help with that.")).toBeUndefined();
  });

  test("something that looks like an object and is not", () => {
    expect(findObject("{ not json }")).toBeUndefined();
  });
});

describe("slug", () => {
  test("a sentence becomes a path", () => {
    expect(slug("Add tiered discounts to checkout")).toBe("add-tiered-discounts-to-checkout");
  });

  test("only the first few words", () => {
    // A directory name wants a handful of words, not a whole sentence.
    expect(slug("one two three four five six seven")).toBe("one-two-three-four-five");
  });

  test("punctuation and case are removed", () => {
    expect(slug("Fix the Sidebar's Cursor (bug!)")).toBe("fix-the-sidebar-s-cursor-bug");
  });

  test("underscores become hyphens and runs collapse", () => {
    expect(slug("a__weird   name")).toBe("a-weird-name");
  });

  test("bounded regardless of word count", () => {
    // One very long word must not produce an unwieldy directory name.
    const found = slug("a".repeat(80));
    expect(found.length).toBeLessThanOrEqual(48);
  });

  test("never ends in a hyphen, even after truncation", () => {
    const found = slug(`${"ab-".repeat(30)}tail`);
    expect(found.endsWith("-")).toBe(false);
  });

  test("text that reduces to nothing gives nothing, not a bad name", () => {
    // The caller decides what to do about it. Returning "workspace" here would
    // hide the case from the one place that can choose better.
    expect(slug("!!! ???")).toBe("");
  });
});

describe("validate", () => {
  const typed = "add tiered discounts to checkout";

  test("a good answer comes through", () => {
    const found = validate(
      { name: "tiered-discounts", label: "Tiered discounts", prompt: "Add tiered discounts." },
      typed,
    );

    expect(found).toEqual({
      name: "tiered-discounts",
      label: "Tiered discounts",
      prompt: "Add tiered discounts.",
    });
  });

  test("the name is re-slugged, not trusted", () => {
    // The whole reason it is safe to act on an answer nobody read first. A
    // model choosing "Tiered Discounts!" would otherwise be a directory name
    // with a capital and an exclamation mark in it.
    const found = validate({ name: "Tiered Discounts!", label: "x", prompt: "y" }, typed);
    expect(found?.name).toBe("tiered-discounts");
  });

  test("a missing label or prompt falls back to what was typed", () => {
    const found = validate({ name: "a-name" }, typed);

    expect(found?.label).toBe(typed);
    expect(found?.prompt).toBe(typed);
  });

  test("a missing name falls back to slugging what was typed", () => {
    const found = validate({ label: "x" }, typed);
    // Five words exactly, so all of it survives the word cap.
    expect(found?.name).toBe("add-tiered-discounts-to-checkout");
  });

  test("an answer that yields no usable name is refused", () => {
    // Refused rather than patched, so the caller fails loudly instead of
    // making a workspace in a directory nobody chose.
    expect(validate({ name: "!!!" }, "???")).toBeUndefined();
  });

  test("not an object at all", () => {
    expect(validate("nope", typed)).toBeUndefined();
    expect(validate(undefined, typed)).toBeUndefined();
    expect(validate(42, typed)).toBeUndefined();
  });

  test("extra fields are ignored, not fatal", () => {
    const found = validate({ name: "a", label: "b", prompt: "c", project: "invented" }, typed);
    expect(found).toEqual({ name: "a", label: "b", prompt: "c" });
  });
});
