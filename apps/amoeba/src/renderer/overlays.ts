import { useEffect, useSyncExternalStore } from "react";

// A count of the modal overlays that are open, and a way to watch it.
//
// ── why anything needs to know ─────────────────────────────────────────────
//
// The web panel is a native webview: another process draws it, over the top of
// this one, at a rectangle it is told to occupy. It is not a DOM element, so it
// does not stack — nothing rendered by React can be in front of it, whatever
// its `z-index` says. A dialog opened while the web tab is selected is drawn
// underneath the page and cannot be seen at all.
//
// Nothing about that reads as a stacking problem from the dialog's side. The
// backdrop dims, focus moves into the popup, Escape closes it — every part
// works except the one that shows it to a person.
//
// ── hide, not mask ─────────────────────────────────────────────────────────
//
// The tag offers both:
//
//   toggleHidden(true)   the whole webview stops being drawn
//   addMaskSelector(s)   holes are cut out of it where `s` matches, and the
//                        rectangles are recomputed every 10ms while moving
//
// A mask is the right answer for something small that overlaps a corner of the
// page. A modal is not that: it makes the rest of the window inert, so there is
// nothing left for the page underneath to be useful for. Masking it would cost
// a per-frame `querySelectorAll` to arrive at a hole the size of the panel.
//
// ── a count, not a flag ────────────────────────────────────────────────────
//
// Two overlays can be open at once — a dialog, and a select inside it that
// portals out of it. With a boolean the inner one closing clears the outer
// one's claim, and the page comes back on top of a dialog that is still there.
// A count cannot get that wrong.
//
// ── modal only ─────────────────────────────────────────────────────────────
//
// A row's `⋯` menu is not registered here, deliberately. It is a few hundred
// pixels in another column and hiding the whole browser for it would read as a
// bug in the browser. What earns hiding is an overlay that owns the window.

let open = 0;

const listeners = new Set<() => void>();

const changed = (): void => {
  for (const listener of listeners) {
    listener();
  }
};

const subscribe = (listener: () => void): (() => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

/** Whether anything modal is open right now. */
export const overlaysOpen = (): boolean => open > 0;

/**
 * Say that a modal overlay is open, and take it back when it closes.
 *
 * Returned rather than paired, so a caller cannot claim without releasing.
 */
export const holdOverlay = (): (() => void) => {
  open += 1;
  changed();
  let released = false;
  return () => {
    // Guarded because a cleanup can run twice — StrictMode rehearses mount and
    // unmount, and a claim released twice is a count that goes negative and
    // then never reaches zero again for the overlay that is still open.
    if (released) {
      return;
    }
    released = true;
    open -= 1;
    changed();
  };
};

/** Hold a claim for as long as `shown` is true. */
export const useOverlay = (shown: boolean): void => {
  useEffect(() => {
    if (!shown) {
      return;
    }
    return holdOverlay();
  }, [shown]);
};

/**
 * Whether anything modal is open, as a value a component re-renders on.
 *
 * `useSyncExternalStore` rather than `useState` in an effect, for the same
 * reason `useColorScheme` is: an effect reads a frame late, and a frame late
 * here is one frame of a dialog behind a web page.
 */
export const useOverlaysOpen = (): boolean => useSyncExternalStore(subscribe, overlaysOpen);

/** Reset, for tests. Nothing in the window calls this. */
export const forgetOverlays = (): void => {
  open = 0;
  changed();
};
