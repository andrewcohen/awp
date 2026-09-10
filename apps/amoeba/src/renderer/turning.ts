// The clock a running mark turns on.
//
// ── one timer for the window, not one per row ────────────────────────────
//
// A turn is regularly a dozen tool calls, and an interval per row is a dozen
// timers and a dozen renders every hundred milliseconds. There is one here,
// it runs only while something is actually in flight, and it stops dead when
// nothing is: an idle conversation costs nothing at all.
//
// It also does not run under `prefers-reduced-motion`. That is the window's
// mandate read strictly — reduced motion means none, not slower — and the
// mark falls back to `…`, which is a state rather than an animation.

import { useEffect, useState, useSyncExternalStore } from "react";

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
 * A frame counter while `going`, and nothing when it is over.
 *
 * `undefined` is the whole of the contract: a caller draws the still mark
 * for it, so "nothing is running" and "this machine does not want motion"
 * are one branch rather than two.
 */
export const useTurning = (going: boolean): number | undefined => {
  const calm = useCalm();
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!going || calm) return;
    const timer = setInterval(() => setTick((was) => was + 1), FRAME_MS);
    return () => clearInterval(timer);
  }, [going, calm]);

  return going && !calm ? tick : undefined;
};
