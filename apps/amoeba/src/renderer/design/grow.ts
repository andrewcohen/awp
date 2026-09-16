import * as stylex from "@stylexjs/stylex";
import { useCallback, useEffect, useRef } from "react";

// A box that starts at one line and grows with what is in it.
//
// Lifted out of Composer.tsx when the new-thread brief needed the same
// behaviour. Two textareas that disagree about how tall an empty one is read
// as two applications — the same argument as one fold duration for the whole
// window — and the arithmetic below is subtle enough in two places to drift.

/**
 * The floor and the cap, in pixels.
 *
 * The floor is a safety net rather than the answer: one line is *measured*,
 * because it is a property of the font **and of the padding**, and a constant
 * gets one of those wrong. The new-thread brief is the worked example — 16px
 * text at 1.5 with 4px of padding either side needs 32, and a floor of 23 or
 * 24 clipped the line it was holding, with `scrollHeight` reporting 32 against
 * an offsetHeight of 24 and nothing on screen saying so.
 *
 * The cap is about eight lines, past which it scrolls: what is above the box
 * is the thing being read, and a composer that can eat the panel is one that
 * hides the answer somebody is typing about.
 */
export const LEAST = 23;
export const MOST = 184;

export const growth = stylex.create({
  /**
   * How the height moves, as a dynamic style.
   *
   * ── an identifier in a static style is resolved by StyleX ──────────────
   *
   * `${FOLD_MS}ms` written inside a `create` value is a **build error about
   * theming rules** — StyleX resolves identifiers in static styles and demands
   * they come from a `.stylex.ts` file. AGENTS.md records this twice already,
   * and it was walked into a third time in Composer.tsx: nothing in fmt, lint,
   * typecheck, test or doctor sees it, because only Vite runs the StyleX Babel
   * pass. What it produces is a module that answers 500 and a page that
   * renders nothing.
   *
   * A dynamic style takes the value at runtime and asks no such question, so
   * the one duration and the one curve stay in columns.ts where the rest of
   * the window reads them.
   *
   * Reduced motion means none, not less — every eased style in this window
   * carries the query, and one without it is a bug.
   */
  eased: (ms: number) => ({
    transitionProperty: "height",
    transitionDuration: {
      default: `${String(ms)}ms`,
      "@media (prefers-reduced-motion: reduce)": "0s",
    },
    transitionTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  }),
});

/**
 * The holder for a textarea whose height follows its value.
 *
 * Measured from `scrollHeight`, which is the only thing that knows how tall
 * the text is — and it has to be read with the height *released*, or what is
 * measured is the height this code set last time and the box never shrinks
 * again. So it is an effect on the value rather than anything during render,
 * and `height` is what animates: `max-height` cannot, because the content's
 * height is not a number CSS knows.
 *
 * The caller attaches the returned *callback* and applies
 * `growth.eased(FOLD_MS)`. A callback rather than the ref itself because
 * writing `.current` on something a hook returned is a react-doctor error —
 * fairly: the holder belongs to the hook, and the caller usually has its own
 * business with the node too (focus it, hand it upwards), which a callback
 * composes with and a ref object does not.
 *
 * @param least  a lower bound, for the case where the element cannot be
 *               measured yet. What one line actually is comes from
 *               `scrollHeight` with the height released.
 */
export const useGrow = (
  value: string,
  least: number = LEAST,
): ((node: HTMLTextAreaElement | null) => void) => {
  const held = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    const node = held.current;
    // `node.value !== value` is a real guard rather than a nod to the linter:
    // what is measured below is only true of the text the element is actually
    // holding, and React has written it by the time an effect runs. If the two
    // ever disagree, measuring would size the box to the previous message.
    if (node === null || node.value !== value) {
      return;
    }
    // Released, read, then set — on every value including the empty one.
    // Without the release `scrollHeight` answers the height this code set last
    // time and the box never shrinks again; and measuring the empty case
    // rather than assuming it is what makes the floor a property of the
    // element instead of a number written here.
    node.style.height = "auto";
    const wanted = Math.min(node.scrollHeight, MOST);
    node.style.height = `${String(Math.max(wanted, least))}px`;
  }, [value, least]);
  return useCallback((node: HTMLTextAreaElement | null) => {
    held.current = node;
  }, []);
};
