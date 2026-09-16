import { describe, expect, it } from "vitest";
import { quoted, withQuote } from "./quote";

// `spotIn` is not here: it reads a live Selection and a range's rectangle,
// neither of which the renderer's vitest environment has. What it does is
// checked in a real window with real pointer events instead — a synthetic
// Range lies about it, twice over. See the commit.

describe("quoted", () => {
  it("makes a blockquote, so markdown reads it as a quotation", () => {
    expect(quoted("the first line\nthe second")).toBe("> the first line\n> the second");
  });

  it("drops trailing whitespace on a blank quoted line", () => {
    // `> ` on its own is two characters of nothing, and a code block quoted
    // whole is full of them.
    expect(quoted("one\n\ntwo")).toBe("> one\n>\n> two");
  });

  it("trims the selection itself", () => {
    // A drag over a paragraph collects the newline after it, which would
    // otherwise become an empty quoted line at the end of every quote.
    expect(quoted("  a claim  \n")).toBe("> a claim");
  });
});

describe("withQuote", () => {
  it("puts the quote first and leaves room to answer it", () => {
    expect(withQuote("", "not that file")).toBe("> not that file\n\n");
  });

  it("keeps what was already typed, after the quote", () => {
    expect(withQuote("try the other one", "not that file")).toBe(
      "> not that file\n\ntry the other one",
    );
  });

  it("stacks two quotes rather than merging them", () => {
    const once = withQuote("", "the first claim");
    expect(withQuote(once, "the second claim")).toBe("> the second claim\n\n> the first claim\n\n");
  });
});
