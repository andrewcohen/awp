import { describe, expect, it } from "vitest";
import { pressed } from "./chords";

const key = (over: Partial<KeyboardEvent> = {}): KeyboardEvent =>
  ({
    code: "KeyI",
    metaKey: true,
    ctrlKey: false,
    altKey: false,
    repeat: false,
    ...over,
  }) as KeyboardEvent;

describe("pressed", () => {
  it("takes either modifier, because the window runs in a browser too", () => {
    expect(pressed(key({ metaKey: true, ctrlKey: false }), "KeyI")).toBe(true);
    expect(pressed(key({ metaKey: false, ctrlKey: true }), "KeyI")).toBe(true);
    expect(pressed(key({ metaKey: false, ctrlKey: false }), "KeyI")).toBe(false);
  });

  it("refuses alt, which is a different binding rather than a sloppier one", () => {
    expect(pressed(key({ altKey: true }), "KeyI")).toBe(false);
  });

  it("ignores a repeat, which is what stops a toggle flapping under a held key", () => {
    // The bug this exists for: `cmd+I` and `cmd+shift+R` toggle, so fifteen
    // keydowns a second left the dialog wherever the release happened to land.
    expect(pressed(key({ repeat: true }), "KeyI")).toBe(false);
  });

  it("says nothing about shift, because two chords are told apart by it", () => {
    expect(pressed(key({ shiftKey: true } as Partial<KeyboardEvent>), "KeyI")).toBe(true);
  });

  it("compares the physical key", () => {
    expect(pressed(key({ code: "KeyN" }), "KeyI")).toBe(false);
  });
});
