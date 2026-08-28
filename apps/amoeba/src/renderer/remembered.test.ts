import { beforeEach, describe, expect, test } from "vitest";
import {
  rememberPage,
  rememberPanel,
  rememberedPages,
  rememberedPanels,
  readStored,
} from "./remembered";

// The two preferences that are filed under a thread rather than under the
// window, and the rules they have to keep.
//
// Written against a real Storage rather than a mock of one, because the thing
// worth checking is the round trip through JSON: both of these are a map in one
// key, and a map that does not survive being stringified is a preference that
// works until the window is closed.

const store = new Map<string, string>();

beforeEach(() => {
  store.clear();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => void store.set(key, value),
      removeItem: (key: string) => void store.delete(key),
    },
  });
});

describe("the panel each thread had open", () => {
  test("two threads keep two answers", () => {
    // The whole reason it moved off one value for the window: the diff is what
    // you want while reviewing one piece of work and the browser is what you
    // want while building another.
    rememberPanel("20260828-aaaa", "diff");
    rememberPanel("20260828-bbbb", "web");

    expect(rememberedPanels()).toEqual({ "20260828-aaaa": "diff", "20260828-bbbb": "web" });
  });

  test("a session no thread claims files nothing", () => {
    // Not filed under a placeholder. That would hand this choice to the next
    // unclaimed session as though somebody had made it there.
    rememberPanel(undefined, "web");
    expect(rememberedPanels()).toEqual({});
  });

  test("choosing again replaces, rather than appending", () => {
    rememberPanel("20260828-aaaa", "diff");
    rememberPanel("20260828-aaaa", "jobs");
    expect(rememberedPanels()).toEqual({ "20260828-aaaa": "jobs" });
  });
});

describe("the page each thread was showing", () => {
  test("two threads keep two pages", () => {
    rememberPage("20260828-aaaa", "https://example.test/one");
    rememberPage("20260828-bbbb", "https://example.test/two");

    expect(rememberedPages()).toEqual({
      "20260828-aaaa": "https://example.test/one",
      "20260828-bbbb": "https://example.test/two",
    });
  });

  test("clearing one leaves the others", () => {
    rememberPage("20260828-aaaa", "https://example.test/one");
    rememberPage("20260828-bbbb", "https://example.test/two");
    rememberPage("20260828-aaaa", undefined);

    expect(rememberedPages()).toEqual({ "20260828-bbbb": "https://example.test/two" });
  });

  test("an unreadable record is a preference nobody set", () => {
    // Written by an older version — this key held a bare URL before it held a
    // map — or by hand. Throwing here would take the panel out with it.
    store.set("amoeba.page", "https://example.test/from-the-old-shape");
    expect(rememberedPages()).toEqual({});
  });

  test("the two are separate keys, so neither can overwrite the other", () => {
    rememberPanel("20260828-aaaa", "web");
    rememberPage("20260828-aaaa", "https://example.test/one");

    expect(rememberedPanels()["20260828-aaaa"]).toBe("web");
    expect(rememberedPages()["20260828-aaaa"]).toBe("https://example.test/one");
    expect(readStored("amoeba.panels")).not.toBe(readStored("amoeba.page"));
  });
});
