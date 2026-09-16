import * as stylex from "@stylexjs/stylex";
import { timing } from "../design/tokens.stylex";

// The application's namesake, drawn small enough to sit in a sidebar row —
// and, when nothing is working, drawn as the bullet it replaced.
//
// ── why the working state earned a shape of its own ────────────────────────
//
// Every mark on that strip was a bullet, so five states differed only by a
// hue somebody has to have learned — reported as the status being "pretty
// uninteresting and not helpful", which is the right complaint about a legend
// with no shapes in it. A dot that breathes was the first answer and it says
// only that something is alive; it does not say *what*, and both of the
// states that breathed said it the same way.
//
// So `working` gets a body. It is the one state on the strip that is an agent
// doing something right now, and an amoeba is what this window is called.
//
// ── one element, because two of them can only flick ────────────────────────
//
// The first build swapped a glyph for this element, which is a conditional
// render — and the window's own mandate says a component that is not in the
// tree has nothing to transition. Reported as exactly that. What is here now
// is *always* this element, and the states are values on it:
//
//   at rest      border-radius 50%, scaled down to the bullet's ~6px ink,
//                no keyframes, the bud collapsed to nothing
//   working      radii pulled to 30/70 and wider, scaled out to an oval,
//                the keyframes running, the bud extended
//
// So the two are one transition rather than a swap, and every property that
// moves is a paint property. Nothing here animates a layout property, which
// is also what keeps the row's metrics fixed: the box is a constant square
// and every difference in *apparent* size is a transform.
//
// ── one square box, and the oval is a transform ────────────────────────────
//
// The body was 0.78 × 0.62rem for a while, which is the shape wanted while
// working and the wrong one at rest: a uniform scale of an oval is a smaller
// oval, and the bullet it has to become is a circle. Non-uniform scale in the
// keyframes gets the oval back without the box ever changing.
//
// ── why the silhouette needs a second piece ────────────────────────────────
//
// `border-radius` can only round the corners of the box it is given, so a
// square one is a squircle however far the percentages are pushed — the
// outline never leaves a circle by more than a pixel at this size. Measured
// by looking: at twelve pixels the eye reads the *silhouette*, and a
// silhouette whose extent is a square has already decided what it is. The bud
// is what makes the union lopsided, and it travels on its own slower clock so
// that the shape changes rather than the pair merely moving together.
const styles = stylex.create({
  /**
   * The organism: a box that scales, and paints nothing itself.
   *
   * ── the split, which is the whole of why the states transition ─────────
   *
   * A CSS animation outranks a transition outright, so any property the
   * keyframes touch cannot also be transitioned — and both directions were
   * measured failing on exactly that:
   *
   *   idle → working   the frame the class lands, the 0% keyframe is imposed
   *                    ms 0 sx 1.060 · ms 38 sx 1.060 — no transition at all
   *   working → idle   the animation is removed and the value does not hand
   *                    back: ms 6 sx 0.520, straight from 1.037 mid-wobble
   *
   * So the two are on different elements. This one owns `transform`, which
   * no keyframe here writes, and it is therefore free to spring in both
   * directions. The fill and the wobbling radii are on `::before`, where an
   * animation is welcome to win.
   *
   * What is left snapping is `border-radius` on that pseudo-element, and it
   * is deliberately left: it snaps *while the whole organism is scaling
   * between six and twelve pixels*, which is the movement the eye is
   * following. A corner radius changing under that is not perceptible, and
   * buying it back would mean a second animation runtime on a mark that is
   * eleven pixels across.
   */
  body: {
    position: "relative",
    display: "inline-block",
    width: "0.72rem",
    height: "0.72rem",
    // An inline-block's baseline is its own bottom edge, so left alone the
    // body hangs a glyph's descent below the name beside it. `middle` is the
    // one answer here that is a rule rather than a number somebody tuned on
    // one machine: the box's midpoint against the baseline plus half the
    // parent's x-height, which is very nearly where the bullet's ink was.
    verticalAlign: "middle",
    /**
     * At rest this is the bullet it replaced, to the pixel it occupied.
     *
     * `●` measures 6.05px wide at the 10px the row draws it at, which is what
     * `scale(0.52)` on an 11.5px box comes to. That number is the reason the
     * resting state is expressible at all — a mark that grew when an agent
     * went idle would be a change nobody asked for in the ordinary case.
     */
    transform: "scale(0.52)",
    // Longhands, not the `transition` shorthand — StyleX drops some shorthands
    // in silence, and this one has no gate that would catch it: the states are
    // correct either way and only the movement is missing, which reads as "the
    // animation did not land" rather than as a build problem.
    transitionProperty: "transform",
    transitionDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    // The spring curve, so it arrives rather than stopping. Measured going in:
    // 0.52 → 0.934 → 1.097 → 1.104 → 1.066 → 1.060, which is the overshoot
    // that makes a thing look like it has weight.
    transitionTimingFunction: timing.spring,
    /** The fill, the shape, and — while working — the wobble. */
    "::before": {
      content: "''",
      position: "absolute",
      inset: 0,
      backgroundColor: "currentColor",
      borderRadius: "50%",
      transitionProperty: "background-color, border-width, border-color",
      transitionDuration: {
        default: timing.enter,
        "@media (prefers-reduced-motion: reduce)": "0s",
      },
      transitionTimingFunction: timing.ease,
    },
    /**
     * The pseudopod, retracted.
     *
     * Scaled to nothing rather than hidden, because `display` cannot be
     * animated and a conditional render is the flick this file exists to
     * remove — the same rule one level down. Its `opacity` is what carries
     * it in and out, and opacity is deliberately absent from its keyframes so
     * that the two never contend.
     */
    "::after": {
      content: "''",
      position: "absolute",
      width: "0.34rem",
      height: "0.34rem",
      borderRadius: "50%",
      backgroundColor: "currentColor",
      insetInlineEnd: "-0.06rem",
      insetBlockStart: "-0.05rem",
      opacity: 0,
      transform: "scale(0)",
      transitionProperty: "opacity",
      transitionDuration: {
        default: timing.enter,
        "@media (prefers-reduced-motion: reduce)": "0s",
      },
      transitionTimingFunction: timing.ease,
    },
  },
  /**
   * An agent mid-turn: an oval with something wandering round it.
   *
   * ── why the silhouette needs a second piece ──────────────────────────────
   *
   * `border-radius` can only round the corners of the box it is given, so a
   * square one is a squircle however far the percentages are pushed — the
   * outline never leaves a circle by more than a pixel at this size. Measured
   * by looking: at twelve pixels the eye reads the *silhouette*, and a
   * silhouette whose extent is a square has already decided what it is. The
   * bud is what makes the union lopsided, and it travels on its own slower
   * clock so that the shape changes rather than the pair merely moving.
   *
   * ── one square box, and the oval is a transform ──────────────────────────
   *
   * The body was 0.78 × 0.62rem for a while, which is the shape wanted while
   * working and the wrong one at rest: a uniform scale of an oval is a
   * smaller oval, and the bullet it has to become is a circle.
   */
  crawling: {
    transform: "scale(1.06, 0.84)",
    "::before": {
      // Pairs at 30/70 and wider, and the two axes deliberately disagree. The
      // first set ran 35–68%, every corner within a sixth of a circle's,
      // which is a rounded box rather than an organism.
      animationName: stylex.keyframes({
        "0%, 100%": { borderRadius: "62% 38% 32% 68% / 58% 30% 70% 42%" },
        "27%": { borderRadius: "30% 70% 68% 32% / 45% 62% 38% 55%" },
        "53%": { borderRadius: "72% 28% 55% 45% / 32% 68% 30% 70%" },
        "79%": { borderRadius: "35% 65% 70% 30% / 65% 35% 62% 38%" },
      }),
      // Held back by exactly the morph's own duration, so the shape has
      // finished growing before it starts changing. Two movements at once on
      // an eleven pixel mark is one movement nobody can read.
      animationDelay: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
      // Slow. A fast wriggle on a list of eight rows is a christmas tree, and
      // this has to survive being on screen all day — the same reason the
      // breathing dot beside it is 2.6s, and slower still because a shape
      // changing draws more of the eye than an opacity does.
      animationDuration: { default: "5.4s", "@media (prefers-reduced-motion: reduce)": "0s" },
      animationTimingFunction: "cubic-bezier(0.45, 0, 0.55, 1)",
      // Reduced motion means none, not slower — the window's mandate, read
      // strictly. What is left is still an oval with a bud on it, so the state
      // is legible without a single frame of movement.
      animationIterationCount: {
        default: "infinite",
        "@media (prefers-reduced-motion: reduce)": "1",
      },
    },
    "::after": {
      opacity: 1,
      // The same value the keyframes begin and end on. Without it the bud
      // sits at the base style's `scale(0)` through the delay and then pops
      // the instant the animation starts.
      transform: "translate(0, 0) scale(1)",
      animationName: stylex.keyframes({
        "0%, 100%": { transform: "translate(0, 0) scale(1)" },
        "30%": { transform: "translate(-0.14rem, 0.4rem) scale(0.8)" },
        "60%": { transform: "translate(-0.58rem, 0.28rem) scale(1.1)" },
        "82%": { transform: "translate(-0.46rem, -0.06rem) scale(0.86)" },
      }),
      animationDelay: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
      animationDuration: { default: "7.1s", "@media (prefers-reduced-motion: reduce)": "0s" },
      animationTimingFunction: "cubic-bezier(0.5, 0, 0.5, 1)",
      animationIterationCount: {
        default: "infinite",
        "@media (prefers-reduced-motion: reduce)": "1",
      },
    },
  },
  /**
   * Not looked at yet: a wall around a hole, which is `◉` with a body.
   *
   * The fill goes rather than a second element going in, so there is nothing
   * to paint the hole *with* — the row's own ground shows through, and a
   * selected row has a different one. An inner span filled with `base` would
   * be a plug that is the wrong colour on exactly the row somebody is on.
   *
   * The bud goes with it. Two overlapping rings at this size is not a bud on
   * a body, it is a smudge with a hole in it — and the ring is already
   * carrying "not looked at" on its own.
   */
  hollow: {
    "::before": {
      backgroundColor: "transparent",
      // 2px and not the 1.5 this was first written at. Chromium reports a used
      // border width snapped to whole device pixels, so a hairline ring is at
      // the mercy of the display it lands on — and the one thing this shape
      // has to keep doing is saying "not looked at" at a glance, in a column
      // somebody is scanning.
      borderWidth: "2px",
      borderStyle: "solid",
      borderColor: "currentColor",
    },
    "::after": { opacity: 0 },
  },
  /** The ring, at rest, is `◉` — which is wider than `●` and drawn as such. */
  hollowRest: { transform: "scale(0.78)" },
});

/**
 * An agent's state, as a shape rather than as a hue alone.
 *
 * `currentColor` throughout, so the caller keeps deciding the state's colour
 * and this only decides its form. Carries no label: it sits inside a span
 * that already has `role="img"` and the state's own name on it, and a second
 * announcement there would read the row twice.
 */
export const Amoeba = ({
  /** An agent is mid-turn. Anything else is the bullet. */
  crawling,
  unread,
}: {
  readonly crawling: boolean;
  readonly unread: boolean;
}) => (
  <span
    {...stylex.props(
      styles.body,
      crawling && styles.crawling,
      unread && styles.hollow,
      unread && !crawling && styles.hollowRest,
    )}
  />
);
