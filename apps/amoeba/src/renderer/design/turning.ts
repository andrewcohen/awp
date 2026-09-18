// The clock a running mark turns on.
//
// ── one timer for the window, and one RENDER per turning mark ────────────
//
// A turn is regularly a dozen tool calls, and an interval per row is a dozen
// timers going off every hundred milliseconds. There is one here, it runs
// only while something is in flight, and it stops dead when nothing is: an
// idle conversation costs nothing at all.
//
// The first version got that right and paid for it in the other currency. It
// was a `useState` in the component holding the timer, so the *caller*
// re-rendered ten times a second — and the caller was `Transcript`, which
// draws the whole conversation. Ten full reconciliations of every message,
// every tool row and every code fence, per second, for as long as an agent
// was working, and the cost of each one grows with the conversation. The
// spinner is one cell.
//
// So the tick is a store and the subscription is the hook. A leaf that draws
// a turning mark subscribes; nothing above it re-renders at all, because
// nothing above it asked. The interval is shared and reference-counted, so a
// dozen rows still cost one timer — which is the property the first version
// was written for and this one keeps.
//
// It also does not run under `prefers-reduced-motion`. That is the window's
// mandate read strictly — reduced motion means none, not slower — and the
// mark falls back to `…`, which is a state rather than an animation.

import { useCallback, useSyncExternalStore } from "react";

/** As fast as a spinner reads as turning rather than as flickering. */
const FRAME_MS = 100;

const LESS = "(prefers-reduced-motion: reduce)";

/**
 * Whether the system has asked for less motion, as a subscription.
 *
 * `useSyncExternalStore` rather than `useState` + an effect, which is the
 * rule the appearance hook already follows: the second reads a frame late,
 * and a frame late here is a spinner that starts and then stops.
 */
const useCalm = (): boolean =>
  useSyncExternalStore(
    (fire) => {
      const query = globalThis.matchMedia(LESS);
      query.addEventListener("change", fire);
      return () => query.removeEventListener("change", fire);
    },
    () => globalThis.matchMedia(LESS).matches,
    () => false,
  );

/**
 * The shared frame, and the one interval that moves it.
 *
 * Reference-counted rather than always running: the last mark to go takes the
 * timer with it, so an idle window has no interval at all — the property the
 * component-local version had, kept.
 */
let frame = 0;
let timer: ReturnType<typeof setInterval> | undefined;
const watching = new Set<() => void>();

const join = (fire: () => void): (() => void) => {
  watching.add(fire);
  timer ??= setInterval(() => {
    frame += 1;
    for (const listener of watching) listener();
  }, FRAME_MS);
  return () => {
    watching.delete(fire);
    if (watching.size === 0 && timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
};

/** Nothing to subscribe to, for a mark that is not turning. */
const idle = (): (() => void) => () => {};

/**
 * A frame counter while `going`, and nothing when it is over.
 *
 * `undefined` is the whole of the contract: a caller draws the still mark
 * for it, so "nothing is running" and "this machine does not want motion"
 * are one branch rather than two.
 *
 * **Call this at the leaf that draws the mark**, never above a list. See the
 * note at the top — a subscription re-renders whoever subscribed, so where
 * this is called decides how much of the window moves with it.
 */
export const useTurning = (going: boolean): number | undefined => {
  const calm = useCalm();
  const live = going && !calm;
  // The identity has to change with `live`, or a row that stops turning keeps
  // its subscription and the timer never reaches zero.
  const watch = useCallback((fire: () => void) => (live ? join(fire) : idle()), [live]);
  const tick = useSyncExternalStore(
    watch,
    () => frame,
    () => 0,
  );
  return live ? tick : undefined;
};
