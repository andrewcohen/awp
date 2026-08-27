import type { Job } from "@awp-kit/jobs";
import type { SessionInfo, WorkspaceFacts } from "@awp-kit/protocol";
import { SidebarSimpleIcon } from "@phosphor-icons/react/SidebarSimple";
import * as stylex from "@stylexjs/stylex";
import { AppearanceToggle } from "./Appearance";
import type { Collapsed } from "./columns";
import { colors, space, text } from "./tokens.stylex";
import { tally } from "./useJobs";

// The two strips the columns sit between.
//
// The columns used to run edge to edge, top to bottom, which meant anything the
// window had to say about *itself* — which session is open, whether the daemon
// is there, what the jobs are doing — had to be borrowed from a column that was
// already spoken for. The sidebar ended up carrying the appearance toggle in a
// footer for exactly that reason.
//
// Two consequences worth stating, because both replaced something:
//
//   - **The top bar clears the traffic lights, so no column has to.** The
//     window is `hiddenInset`, so the controls float over the top-left of the
//     content; every column used to start with `space.titlebar` of padding to
//     stay out of their way, in three places, none of which knew why. Now one
//     strip does it — and it clears them by going *under* them rather than
//     starting to their right, which is what lets the leftmost control have
//     the window's real left edge. See `top` below for the six pixels that
//     forced the change.
//   - **The top bar is the window's drag handle.** With the controls hidden
//     there is no title bar to grab, and a strip of chrome with nothing
//     interactive in it is the natural place — which is also why nothing
//     interactive goes in it.
//
// Neither bar scrolls or grows. They are `flex-shrink: 0` in a column layout,
// so the middle row is the only thing that flexes and the no-top-level-scroll
// rule in global.css still holds when the window gets short.

const styles = stylex.create({
  bar: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.6rem",
    paddingInline: "0.6rem",
    fontSize: text.small,
    color: colors.muted,
    whiteSpace: "nowrap",
  },
  // The top bar goes *under* the traffic lights rather than beside them.
  //
  // It used to start 5.25rem in, to clear them sideways. That worked and put
  // our leftmost control six pixels from the OS's rightmost one, so the eye
  // grouped it with close/minimise/zoom — a fourth traffic light that folds a
  // column. Measured, because "it looks cramped" is not a number:
  //
  //   traffic lights end   78px
  //   our toggle starts    84px      ← six pixels of daylight
  //
  // There is no fixing that in the same row. The accessory's toggle can sit at
  // its column's outer edge because that edge is the window's right side and
  // nothing is there; the mirror of that for the sidebar is the window's left
  // side, which AppKit owns. So the bar takes the whole width and starts below
  // the band instead, and the leftmost control gets the real edge it was after.
  //
  // The band is empty on purpose. It is the drag handle, and a strip of chrome
  // with nothing interactive in it is exactly what a drag handle should be.
  // ── one row, and the title is centred on the window ─────────────────────
  //
  // This was two bands stacked: a 28px strip under the traffic lights holding
  // the title, and a 28px row below it holding the fold controls. That was
  // 57px of chrome to say one word, and the title was centred in the *upper
  // half* rather than in the bar — which is what "center it vertically" was
  // about, and the two-band split is the only reason it could not be.
  //
  //   before  ┌──────────────┐        after  ┌───────────────┐
  //           │  no session  │ 28            │ ▣ no session ▣│ 44
  //           │ ▣          ▣ │ 28            └───────────────┘
  //           └──────────────┘ 57
  //
  // **The left control still cannot reach the window's edge.** The window is
  // `hiddenInset`, so AppKit floats the traffic lights over the first 78px and
  // this bar's start padding is what clears them — six pixels of daylight at
  // 84px. That constraint is why the two bands existed; what changed is the
  // answer to it. Giving the control the real edge cost a whole row of chrome
  // and put the title off centre, and the edge was worth neither.
  top: {
    position: "relative",
    alignItems: "center",
    height: space.titlebar,
    paddingInlineStart: space.lightsInline,
    paddingInlineEnd: "0.6rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    // The only way to move a window whose title bar is hidden.
    WebkitAppRegion: "drag",
  },
  // The title, centred on the **window** rather than on the space left over.
  //
  // Absolutely positioned for exactly that: the bar's start padding clears the
  // traffic lights and its end padding does not, so a title laid out in the
  // flow would sit visibly right of centre. This is the one element here whose
  // position is about the window rather than about its siblings.
  //
  // `pointer-events: none` so it does not interrupt the drag region it sits
  // in — there is nothing to click, and a word that stops a window being
  // dragged is a word in the way.
  title: {
    position: "absolute",
    insetInlineStart: "50%",
    transform: "translateX(-50%)",
    maxWidth: `calc(100% - 2 * ${space.lightsInline})`,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    pointerEvents: "none",
  },

  bottom: {
    height: "1.6rem",
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: colors.border,
  },
  spacer: { flex: 1 },
  // The window's subject. The only thing in the chrome at `title` weight, on
  // the principle that a screen has one subject and everything else is about
  // it — a second strong thing makes neither one strong.
  strong: { color: colors.text, fontSize: text.title, fontWeight: text.medium },
  live: { color: colors.live },
  warn: { color: colors.warn },
  // Truncation goes on the one field that can be arbitrarily long. Everything
  // else in a bar is a word or a count, and a bar that ellipsises its counts to
  // make room for a name has lost the thing it was for.
  name: { overflow: "hidden", textOverflow: "ellipsis", minWidth: 0 },

  // ── the column toggles ──────────────────────────────────────────────────
  //
  // `no-drag`, and it has to be said: the bar around these is the window's drag
  // handle, and a button inside a drag region is a button that moves the window
  // instead of being pressed.
  //
  // This is the one exception to "nothing interactive goes in the top bar", and
  // it is a narrow one — two 22px targets at the right end, with the whole
  // middle of the bar still grabbable.
  toggle: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.4rem",
    height: "1.4rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: {
      default: colors.muted,
      ":hover": colors.text,
    },
    cursor: "pointer",
    WebkitAppRegion: "no-drag",
  },
  // On when the column is showing. A toggle that dims when its panel is open
  // reads backwards — the lit state is the state the thing is in.
  toggleOn: { color: colors.text },
  // The accessory's icon is the sidebar's, mirrored. One glyph, two meanings,
  // and the mirror is what says which edge is meant.
  mirrored: { transform: "scaleX(-1)" },
});

/**
 * Which session is open, and whether there is a daemon behind it.
 *
 * The identity rather than the session name: a name is an address —
 * `awp.thicket.effect-ts-tiered-discou-f500.agent` — and the shortening that
 * makes it fit is exactly what makes it unreadable. `identity` is the
 * unshortened truth and is on the wire for this reason.
 *
 * ── and the display name rather than the slug ──────────────────────────────
 *
 * The unshortened truth was still three slugs — `effect-ts-tiered-discount-
 * timemachine · thicket · agent` — which is an address written out in full
 * rather than a title. The title bar of a window says what the window is
 * about, and what this window is about is a piece of work somebody described
 * in a sentence.
 *
 *   before   effect-ts-tiered-discount-timemachine · thicket · agent
 *   after    tiered discount timemachine
 *
 * And only that. The project and the kind went with the slug, because a title
 * bar has one job and three fields is not one: both are already on the sidebar
 * row that is selected, so repeating them here makes the address the subject
 * of a line that is supposed to be about the work.
 *
 * The slug does not move down the way it does on a sidebar row. A row is a
 * list entry and the slug is how you find the directory; a title bar is one
 * line about one thing.
 */
export function TopBar({
  session,
  facts,
  connected,
  collapsed,
  onFold,
}: {
  readonly session: SessionInfo | undefined;
  /** What is known about the open session's workspace, if anything is. */
  readonly facts: WorkspaceFacts | undefined;
  readonly connected: boolean;
  readonly collapsed: Collapsed;
  readonly onFold: (which: keyof Collapsed) => void;
}) {
  return (
    <header {...stylex.props(styles.bar, styles.top)}>
      {/* Just the name.
      
        It was `displayName · project · kind`, which is three fields where a
        title bar has one job. The project and the kind are both already on the
        sidebar row that is selected — a title bar says what the window is
        about, and repeating the address under it makes the address the
        subject. */}
      <span {...stylex.props(styles.strong, styles.title)}>
        {session === undefined
          ? "no session"
          : (facts?.displayName ??
            session.identity?.label ??
            session.identity?.workspace ??
            session.name)}
      </span>

      {/* ── folding a column, from the one place that is never folded ──────

          These used to live on the divider between the columns: a control that
          appeared on hover, and stayed visible once its column was folded
          because it was then the only way back. That worked and was hard to
          find — an affordance that is invisible until the pointer is already
          on a one-pixel rule is one nobody discovers, and it could not be
          pressed at all without a pointer.

          The top bar is the one strip that never folds, so it is where a
          control that folds something else belongs.

          **Each one sits at the edge it folds.** Both were at the right end
          together, which put the sidebar's control as far from the sidebar as
          the window allows and left the two of them to be told apart by a
          mirrored glyph alone. Now the icon's mirror and its position say the
          same thing, and neither is carrying the distinction by itself.

          The left one cannot go further left than this: the window is
          `hiddenInset`, so the traffic lights float over the first 5.25rem and
          the bar's own start padding is what clears them. */}
      <button
        type="button"
        aria-label={collapsed.sidebar ? "show the sidebar" : "hide the sidebar"}
        aria-pressed={!collapsed.sidebar}
        title={collapsed.sidebar ? "show the sidebar" : "hide the sidebar"}
        onClick={() => onFold("sidebar")}
        {...stylex.props(styles.toggle, !collapsed.sidebar && styles.toggleOn)}
      >
        <SidebarSimpleIcon size={15} aria-hidden />
      </button>

      <span {...stylex.props(styles.spacer)} />
      {/* Nothing at all while it is working.
      
          A green "daemon" sitting there permanently is a status light that is
          on by definition — it teaches the eye to skip that corner, which costs
          exactly the one moment it exists for. The same argument the footer
          already makes about `0 running · 0 failed`.
      
          So the connected state is silence, and the disconnected state is a
          word rather than a dot: "no daemon" is the sentence, and a red circle
          would need to be hovered before it said anything. */}
      {!connected && <span {...stylex.props(styles.warn)}>no daemon</span>}

      {/* The other half of the pair — see the sidebar's, at the far left. */}
      <button
        type="button"
        aria-label={collapsed.accessory ? "show the panels" : "hide the panels"}
        aria-pressed={!collapsed.accessory}
        title={collapsed.accessory ? "show the panels" : "hide the panels"}
        onClick={() => onFold("accessory")}
        {...stylex.props(styles.toggle, !collapsed.accessory && styles.toggleOn)}
      >
        <SidebarSimpleIcon size={15} aria-hidden {...stylex.props(styles.mirrored)} />
      </button>
    </header>
  );
}

/**
 * The appearance toggle, and what the jobs are doing.
 *
 * The jobs summary is here rather than only in the panel because the panel is
 * in a column that folds away. A job that fails while its column is collapsed
 * is a job nobody hears about, and the whole reason the jobs system was built
 * was so that would stop being true.
 *
 * Nothing is said when there is nothing to say. A status bar that always reads
 * `0 running · 0 failed` teaches the eye to skip it, which costs exactly the
 * one moment it exists for.
 */
export function BottomBar({
  jobs,
  session,
}: {
  readonly jobs: ReadonlyArray<Job>;
  readonly session: SessionInfo | undefined;
}) {
  const counted = tally(jobs);

  return (
    <footer {...stylex.props(styles.bar, styles.bottom)}>
      <AppearanceToggle />
      {counted.running > 0 && (
        <span {...stylex.props(styles.strong)}>{counted.running} running</span>
      )}
      {counted.failed > 0 && <span {...stylex.props(styles.warn)}>{counted.failed} failed</span>}
      {counted.dirty > 0 && (
        // Said separately from `failed`, and never folded into it: a failed job
        // wants a retry, and a dirty one wants a person to look at what its
        // rollback could not undo.
        <span {...stylex.props(styles.warn)}>{counted.dirty} needs cleaning up</span>
      )}
      <span {...stylex.props(styles.spacer)} />
      <span {...stylex.props(styles.name)}>{session?.name}</span>
    </footer>
  );
}
