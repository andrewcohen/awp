import { beforeEach, describe, expect, it } from "vitest";
import { forgetOverlays, holdOverlay, overlaysOpen } from "./overlays";

// The count that keeps a native webview from covering a dialog.
//
// Tested as a module rather than through the component, because what can go
// wrong here is arithmetic — a claim released twice, or two overlaps where the
// inner one closing clears the outer one's claim — and none of it is visible in
// rendered output.

describe("open overlays", () => {
  beforeEach(forgetOverlays);

  it("is closed to begin with", () => {
    expect(overlaysOpen()).toBe(false);
  });

  it("opens while something holds it", () => {
    const release = holdOverlay();
    expect(overlaysOpen()).toBe(true);
    release();
    expect(overlaysOpen()).toBe(false);
  });

  it("stays open while the outer one is still there", () => {
    // A select inside a dialog portals out of it, so both are open at once and
    // the inner one closes first. With a boolean this reads false here, and the
    // web page comes back on top of a dialog that has not gone anywhere.
    const dialog = holdOverlay();
    const select = holdOverlay();
    select();
    expect(overlaysOpen()).toBe(true);
    dialog();
    expect(overlaysOpen()).toBe(false);
  });

  it("ignores a claim released twice", () => {
    // StrictMode rehearses mount and unmount, so a cleanup running twice is the
    // ordinary case rather than a mistake. Counting it twice makes the count
    // negative, and a negative count never reaches zero for the overlay that is
    // still open — the page would then stay hidden for the life of the window.
    const dialog = holdOverlay();
    const other = holdOverlay();
    dialog();
    dialog();
    expect(overlaysOpen()).toBe(true);
    other();
    expect(overlaysOpen()).toBe(false);
  });

  it("tells a watcher when it changes", () => {
    const seen: boolean[] = [];
    // The panel subscribes through useSyncExternalStore; this is what that
    // reads underneath.
    const release = holdOverlay();
    seen.push(overlaysOpen());
    release();
    seen.push(overlaysOpen());
    expect(seen).toEqual([true, false]);
  });
});
