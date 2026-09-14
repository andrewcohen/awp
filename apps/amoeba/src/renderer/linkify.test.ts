import { describe, expect, it } from "vitest";
import { linkify } from "./linkify";

// What makes this safe to run over somebody's own words.
//
// Your message is drawn as text rather than as markdown precisely so the window
// cannot edit what you said — see the note in `Chat.tsx`. Linkifying is allowed
// past that rule on one promise: it never changes a character. So the first
// test here is not about links at all, and it runs over every case.

const whole = (text: string): string =>
  linkify(text)
    .map((piece) => piece.text)
    .join("");

const links = (text: string): ReadonlyArray<string> =>
  linkify(text).flatMap((piece) => (piece.href === undefined ? [] : [piece.href]));

const cases = [
  "",
  "nothing here at all",
  "https://example.invalid/build/412",
  "look at https://example.invalid/build/412 when you can",
  "look at https://example.invalid/build/412.",
  "(see https://example.invalid/build/412)",
  "https://example.invalid/wiki/Foo_(bar)",
  "https://example.invalid/wiki/Foo_(bar).",
  "two https://a.invalid/x and https://b.invalid/y here",
  "http://plain.invalid/ok",
  "a bare https:// on its own",
  "trailing punctuation https://example.invalid/x, then more",
  "see index.ts and www.example.invalid — neither is a link",
  "multi\nline https://example.invalid/x\nand after",
];

describe("linkify", () => {
  // The promise, over every case above. A regression here is the window
  // silently rewriting a message, which is the one thing drawing your text as
  // text was protecting against.
  it.each(cases)("puts every character back: %j", (text) => {
    expect(whole(text)).toBe(text);
  });

  it("finds nothing in text with no url", () => {
    expect(links("nothing here at all")).toEqual([]);
  });

  it("finds a url that is the whole message", () => {
    expect(links("https://example.invalid/build/412")).toEqual([
      "https://example.invalid/build/412",
    ]);
  });

  // A url at the end of a sentence is followed by punctuation that is prose.
  it("leaves a trailing full stop out of the link", () => {
    expect(links("look at https://example.invalid/build/412.")).toEqual([
      "https://example.invalid/build/412",
    ]);
  });

  it("leaves a closing bracket the sentence owns out of the link", () => {
    expect(links("(see https://example.invalid/build/412)")).toEqual([
      "https://example.invalid/build/412",
    ]);
  });

  // ── and keeps the one the url owns ───────────────────────────────────────
  //
  // The case that makes this balancing rather than stripping. Every wiki puts
  // parentheses inside urls, and a rule that dropped the trailing one would
  // break the link while leaving it looking perfectly fine on screen.
  it("keeps a bracket that belongs to the url", () => {
    expect(links("https://example.invalid/wiki/Foo_(bar)")).toEqual([
      "https://example.invalid/wiki/Foo_(bar)",
    ]);
  });

  it("keeps the url's bracket and drops the sentence's full stop", () => {
    expect(links("https://example.invalid/wiki/Foo_(bar).")).toEqual([
      "https://example.invalid/wiki/Foo_(bar)",
    ]);
  });

  it("finds both of two urls", () => {
    expect(links("two https://a.invalid/x and https://b.invalid/y here")).toEqual([
      "https://a.invalid/x",
      "https://b.invalid/y",
    ]);
  });

  it("takes http as well as https", () => {
    expect(links("http://plain.invalid/ok")).toEqual(["http://plain.invalid/ok"]);
  });

  // ── what is deliberately not a link ──────────────────────────────────────
  //
  // A host with no scheme is a guess about what to prepend, and a guess inside
  // an anchor is a link going somewhere the text did not say. `index.ts` is the
  // reason a bare-host rule cannot work at all: it is indistinguishable from a
  // sentence containing a filename.
  it("leaves a scheme-less host alone", () => {
    expect(links("see index.ts and www.example.invalid — neither is a link")).toEqual([]);
  });

  it("does not make a link out of a bare scheme", () => {
    expect(links("a bare https:// on its own")).toEqual([]);
  });

  it("stops a url at a newline", () => {
    expect(links("multi\nline https://example.invalid/x\nand after")).toEqual([
      "https://example.invalid/x",
    ]);
  });
});
