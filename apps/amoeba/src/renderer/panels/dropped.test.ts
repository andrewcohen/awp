import { describe, expect, it, vi } from "vitest";
import { acceptsFilesInto, spliced } from "./dropped";

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

const event = (prevented: boolean) =>
  ({
    defaultPrevented: prevented,
    preventDefault: vi.fn(),
    dataTransfer: null,
  }) as unknown as React.DragEvent<HTMLElement> & { preventDefault: ReturnType<typeof vi.fn> };

describe("a drop on the panel rather than on the box", () => {
  // The bridge is the half a browser cannot supply, so what a path *becomes*
  // is checked by driving a real window — see the note in `dropped.ts`. What
  // is checked here is the pair of decisions taken before a path is wanted,
  // because both of them fail silently and neither is visible in a screenshot.

  it("leaves a drop the composer already took alone", () => {
    // Both handlers see the same event: the textarea's runs first and does not
    // stop propagation, so this one has to notice it was handled. Without the
    // check the paths would be spliced twice — once at the caret and once at
    // the end.
    const onValue = vi.fn();
    const one = event(true);
    acceptsFilesInto("hello", onValue).onDrop(one);

    expect(one.preventDefault).not.toHaveBeenCalled();
    expect(onValue).not.toHaveBeenCalled();
  });

  it("cancels a drop that carries no path it can use", () => {
    // The cancel is what makes this safe rather than what makes it work: an
    // uncancelled drop is Chromium navigating the window to the file, which
    // replaces the renderer and leaves no way back but a reload. So it is
    // cancelled whether or not a path came back — in a plain browser, where
    // the bridge is absent, that is every drop.
    const onValue = vi.fn();
    const one = event(false);
    acceptsFilesInto("hello", onValue).onDrop(one);

    expect(one.preventDefault).toHaveBeenCalled();
    expect(onValue).not.toHaveBeenCalled();
  });
});
