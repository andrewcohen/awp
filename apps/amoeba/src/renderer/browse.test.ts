import { describe, expect, it } from "vitest";
import { addressFor } from "./browse";

// What a person typed, as something to navigate to.
//
// The one part of the web panel that can be wrong in a way nobody notices: a
// mis-read address is a navigation to somewhere, and "somewhere" looks like a
// working feature until the page is read.

describe("reading an address bar", () => {
  it("leaves an address that already has a scheme alone", () => {
    expect(addressFor("https://example.com/docs")).toBe("https://example.com/docs");
    expect(addressFor("http://example.com")).toBe("http://example.com");
  });

  it("gives a local address http, not https", () => {
    // A dev server has no certificate, so https fails to connect — and the
    // failure reads as "the panel is broken" rather than "wrong scheme".
    expect(addressFor("localhost:5173")).toBe("http://localhost:5173");
    expect(addressFor("127.0.0.1:8080/health")).toBe("http://127.0.0.1:8080/health");
    expect(addressFor("localhost")).toBe("http://localhost");
  });

  it("gives a bare domain https", () => {
    expect(addressFor("example.com")).toBe("https://example.com");
    expect(addressFor("docs.example.com/v4?q=1")).toBe("https://docs.example.com/v4?q=1");
  });

  it("searches for words, rather than navigating to them", () => {
    // The case that separates this from a one-liner. Something with spaces in
    // it is prose, and prepending a scheme produces a navigation to nowhere.
    expect(addressFor("effect schema v4")).toBe("https://duckduckgo.com/?q=effect%20schema%20v4");
    expect(addressFor("jj")).toBe("https://duckduckgo.com/?q=jj");
  });

  it("treats a colon that is not a port as prose", () => {
    // `note: check this` is a sentence far more often than a URL scheme, and
    // the scheme test requires the `//` for exactly that reason.
    expect(addressFor("note: check this")).toBe("https://duckduckgo.com/?q=note%3A%20check%20this");
  });

  it("answers nothing for nothing, so enter on an empty box does nothing", () => {
    expect(addressFor("")).toBeUndefined();
    expect(addressFor("   ")).toBeUndefined();
  });

  it("trims, because an address pasted from a terminal carries a newline", () => {
    expect(addressFor("  https://example.com  ")).toBe("https://example.com");
  });
});
