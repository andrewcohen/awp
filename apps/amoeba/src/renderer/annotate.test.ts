import { describe, expect, test } from "vitest";
import { messageFrom, pickerSource, stopSource } from "./annotate";

// `host-message` is the page's channel, not this feature's, and the page is a
// stranger. Everything here is about what happens when what arrives is not what
// the picker sent.

const picked = (extra: Record<string, unknown> = {}) => ({
  from: "awp-annotate",
  url: "https://example.test/docs",
  selector: "main > button:nth-of-type(2)",
  label: "button.primary",
  text: "Save changes",
  ...extra,
});

describe("messageFrom", () => {
  test("reads a message the picker sent", () => {
    const message = messageFrom(picked());
    expect(message).toEqual({
      kind: "picked",
      picked: {
        url: "https://example.test/docs",
        selector: "main > button:nth-of-type(2)",
        label: "button.primary",
        text: "Save changes",
      },
    });
  });

  test("reads a cancel", () => {
    expect(messageFrom({ from: "awp-annotate", cancelled: true })).toEqual({ kind: "cancelled" });
  });

  test("ignores a message from anything else on the page", () => {
    // The one that matters. Any script on any site can call
    // `__electrobunSendToHost`, and without the marker its object would go
    // straight into a prompt typed at an agent.
    expect(messageFrom({ selector: "body", body: "rm -rf" })).toBeUndefined();
    expect(messageFrom({ from: "someone-else", selector: "body" })).toBeUndefined();
  });

  test("ignores what is not an object at all", () => {
    for (const value of [undefined, null, "", 0, "awp-annotate", []]) {
      expect(messageFrom(value)).toBeUndefined();
    }
  });

  test("refuses an empty anchor", () => {
    // A selector of "" matches every element and therefore names none. There
    // is no sensible default for the one field the whole note hangs on.
    expect(messageFrom(picked({ selector: "" }))).toBeUndefined();
    expect(messageFrom(picked({ selector: 7 }))).toBeUndefined();
  });

  test("fills in what it can, rather than refusing the note", () => {
    // An icon button has no text; a page mid-navigation may report no url. Both
    // are notes worth sending — unlike a missing selector, neither is the
    // anchor.
    const message = messageFrom({ from: "awp-annotate", selector: "#save" });
    expect(message).toEqual({
      kind: "picked",
      picked: { url: "", selector: "#save", label: "#save", text: "" },
    });
  });

  test("caps the page's own words", () => {
    const message = messageFrom(picked({ text: "x".repeat(9000) }));
    expect(message?.kind).toBe("picked");
    if (message?.kind === "picked") {
      expect(message.picked.text.length).toBe(400);
    }
  });
});

describe("pickerSource", () => {
  test("carries the marker the reader checks for", () => {
    // The two halves have to agree about the word or nothing ever arrives, and
    // they are written in two places — the string is built here, and read by
    // `messageFrom` above.
    expect(pickerSource()).toContain(JSON.stringify("awp-annotate"));
  });

  test("parks itself on window, so a second injection re-arms rather than doubling", () => {
    expect(pickerSource()).toContain("__awpAnnotate");
    expect(pickerSource()).toContain("window[KEY].arm()");
  });

  test("stopSource asks for the picker without assuming it is there", () => {
    // Optional call, because disarming is reached from the panel's cleanup and
    // from a navigation, and neither can know whether anything was injected.
    expect(stopSource()).toContain("?.disarm()");
  });
});
