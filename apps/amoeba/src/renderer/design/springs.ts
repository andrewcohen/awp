// How this window moves, as physics rather than as durations.
//
// ── why a fifth library, when the stack rule says four ────────────────────
//
// The rule in CLAUDE.md is about UI frameworks — Base UI for behaviour,
// StyleX for appearance — and it is not violated by an animation runtime any
// more than it is by `@pierre/diffs` or the markdown renderer. What Motion
// does that CSS cannot is the two things this window kept wanting and faking:
//
//   a spring     a curve is a guess at how long something takes; a spring is
//                a statement about weight, and it is what "squishy" means.
//                An interrupted spring carries its velocity — a CSS
//                transition restarts from wherever it was, which is the
//                stutter every re-toggled fold in this window had
//   layout       `layoutId` moves one element to where another one is. The
//                selected-tab pill and the sidebar's accent edge were four
//                separate elements appearing and disappearing; now they are
//                one thing that travels
//
// Everything here is a preset rather than a call site inventing numbers,
// for the same reason the durations were: two things in one window that
// disagree about weight read as two applications.

import { useReducedMotion } from "motion/react";

/**
 * The house spring. Soft, and it overshoots just enough to be felt.
 *
 * `bounce` is Motion's own perceptual handle — 0 is critically damped and 1
 * is a rubber ball. 0.28 lands where a control feels sprung rather than
 * loose, and `visualDuration` is how long it takes to *look* settled, which
 * is the number worth tuning against a stopwatch.
 */
export const jelly = { type: "spring", visualDuration: 0.34, bounce: 0.28 } as const;

/** Bigger things, carrying more: a panel, a dialog, a column. */
export const heavy = { type: "spring", visualDuration: 0.46, bounce: 0.18 } as const;

/** A press, a hover, a colour — no overshoot, because nothing travelled. */
export const snap = { type: "spring", visualDuration: 0.18, bounce: 0 } as const;

/** What a control does under the pointer, and under a finger. */
export const squish = {
  whileHover: { y: -1, scale: 1.012 },
  whileTap: { scale: 0.97, y: 0 },
  transition: snap,
} as const;

/** A row arriving in a list: up from under, and settling. */
export const arriving = {
  initial: { opacity: 0, y: 8, scale: 0.985 },
  animate: { opacity: 1, y: 0, scale: 1 },
  transition: jelly,
} as const;

/** What a row is handed: how it enters, or that it does not. */
export type Arriving = typeof arriving | typeof STILL;

/**
 * The same, with nothing moving.
 *
 * Reduced motion means **none** — this window's mandate, stated in
 * CLAUDE.md — so the still variant is not a faster spring, it is the end
 * state applied at once. Everything animated here goes through `calm()`.
 */
export const STILL = {
  initial: false as const,
  animate: {},
  transition: { duration: 0 },
} as const;

/**
 * Props for a thing that arrives, honouring the system preference.
 *
 * A hook because the preference is a subscription: somebody turning it on
 * mid-session should not have to reload to be believed.
 */
export const useArriving = (): Arriving => (useReducedMotion() === true ? STILL : arriving);

/** Props for a control that squishes, or nothing at all. */
export const useSquish = () => (useReducedMotion() === true ? {} : squish);

/**
 * The shape every preset here has, so a caller can ask for one by name.
 *
 * Written out rather than `typeof jelly`, which is the literal 0.34 and 0.28
 * and therefore a type only `jelly` satisfies.
 */
export type Spring = {
  readonly type: "spring";
  readonly visualDuration: number;
  readonly bounce: number;
};

/** The house spring, or an instant, for a caller writing its own animation. */
export const useSpring = (preset: Spring = jelly) =>
  useReducedMotion() === true ? ({ duration: 0 } as const) : preset;

/**
 * What a travelling selection uses: the pill behind a tab, the edge beside
 * a row.
 *
 * Stiffer and flatter than `jelly` on purpose. A fill that overshoots its
 * destination reads as sloppy rather than as sprung, because the thing it
 * is landing on has a hard edge and the eye has both to compare.
 */
export const pill = { type: "spring", visualDuration: 0.26, bounce: 0.12 } as const;
