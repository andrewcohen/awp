import { describe, expect, it } from "vitest";
import { contains, excerpt, flatten, fuzzy, pieces } from "./fuzzy";

/** What a highlight would draw, as a string a failure can be read out of. */
const marked = (text: string, spans: ReadonlyArray<readonly [number, number]>): string =>
  pieces(text, spans)
    .map((piece) => (piece.hit ? `[${piece.text}]` : piece.text))
    .join("");

describe("fuzzy", () => {
  it("finds the letters in order", () => {
    expect(fuzzy("fzp", "Fuzzy search over the tasks panel")).toBeDefined();
  });

  it("refuses a letter that is not there", () => {
    expect(fuzzy("fzx", "Fuzzy search over the tasks panel")).toBeUndefined();
  });

  it("refuses letters that are there in the wrong order", () => {
    expect(fuzzy("zf", "fz")).toBeUndefined();
  });

  // The backward pass, which is the whole reason this is not one loop. Greedy
  // alone takes the `t` of "the" and the highlight reads as a bug.
  it("gathers the run where a reader would put it", () => {
    const text = "the tasks panel";
    expect(marked(text, fuzzy("tsk", text) ?? [])).toBe("the [t]a[sk]s panel");
  });

  it("runs of adjacent hits are one span", () => {
    expect(fuzzy("task", "one task")).toStrictEqual([[4, 8]]);
  });

  it("ignores case in both directions", () => {
    expect(fuzzy("PANEL", "the tasks panel")).toBeDefined();
    expect(fuzzy("panel", "THE TASKS PANEL")).toBeDefined();
  });

  // A space is a pause between two remembered words, not a character being
  // claimed to appear in the subject.
  it("drops whitespace from the query", () => {
    expect(fuzzy("ta sk", "one task")).toStrictEqual([[4, 8]]);
  });

  it("an empty query matches everything and highlights nothing", () => {
    expect(fuzzy("", "anything")).toStrictEqual([]);
  });
});

describe("contains", () => {
  it("finds every occurrence, not only the first", () => {
    expect(contains("port", "the port, and the port again")).toStrictEqual([
      [4, 8],
      [18, 22],
    ]);
  });

  it("is a substring and not a subsequence", () => {
    expect(contains("prt", "the port")).toBeUndefined();
  });

  it("ignores case", () => {
    expect(contains("PORT", "the port")).toStrictEqual([[4, 8]]);
  });
});

describe("excerpt", () => {
  const body = flatten(`
    A long body, of the kind a task here actually has, with the remembered
    phrase — a fuzzy filter — sitting well inside it and a good deal more
    prose after it that nobody typed and nobody wants drawn.
  `);

  it("keeps the match inside the window", () => {
    const spans = contains("fuzzy filter", body) ?? [];
    const cut = excerpt(body, spans);
    expect(marked(cut.text, cut.spans)).toContain("[fuzzy filter]");
  });

  it("says both ends were cut", () => {
    const cut = excerpt(body, contains("fuzzy filter", body) ?? []);
    expect(cut.text.startsWith("…")).toBe(true);
    expect(cut.text.endsWith("…")).toBe(true);
  });

  it("does not shift the spans when nothing was cut from the front", () => {
    const text = "fuzzy filter at the front";
    const cut = excerpt(text, contains("fuzzy", text) ?? []);
    expect(cut.text.startsWith("…")).toBe(false);
    expect(marked(cut.text, cut.spans)).toBe("[fuzzy] filter at the front");
  });

  // The bug this shape exists to make impossible: collapsing whitespace after
  // the window is cut moves every offset past the first run of spaces, which
  // is most bodies, because most of them have a blank line before the match.
  it("lights the match and not its neighbours, in a body with paragraphs", () => {
    const raw = "A heading\n\n    and then, after a blank line, the phrase to find.";
    const flat = flatten(raw);
    const cut = excerpt(flat, contains("the phrase", flat) ?? []);
    expect(marked(cut.text, cut.spans)).toContain("[the phrase]");
  });
});
