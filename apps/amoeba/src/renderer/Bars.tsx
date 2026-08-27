import type { Job } from "@awp-kit/jobs";
import type { SessionInfo } from "@awp-kit/protocol";
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
  top: {
    flexDirection: "column",
    alignItems: "stretch",
    paddingInline: 0,
    gap: 0,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    // The only way to move a window whose title bar is hidden.
    WebkitAppRegion: "drag",
  },
  // The band AppKit draws its buttons in, and where the title goes.
  //
  // It was empty when the bar first moved under the lights, and empty is what
  // a person notices: the session's identity was sitting in the row below,
  // immediately right of the fold button, where it reads as a label *for* the
  // button. It is a title, and this is where a title belongs on macOS.
  //
  // **Padded by the same amount on both sides**, so the title centres in the
  // window rather than in the space left over — a title centred in the
  // remainder sits visibly off to the right — and so it can never reach the
  // window controls however narrow the window gets.
  lights: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    gap: "0.45rem",
    height: space.lights,
    flexShrink: 0,
    paddingInline: space.lightsInline,
    minWidth: 0,
  },
  // What was the top bar: one row, now running the full width of the window.
  topRow: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.6rem",
    paddingInline: "0.6rem",
    // The same height as the band above, and not `space.titlebar`. That token
    // meant "tall enough to clear the traffic lights", which this row no longer
    // does — the band does it. What is left here is two 22px buttons and the
    // occasional word, so matching the band both fits and reads as deliberate:
    // one strip of chrome in two equal halves rather than a tall one and a
    // short one that look like an accident.
    height: space.lights,
    minWidth: 0,
  },
  bottom: {
    height: "1.6rem",
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: colors.border,
  },
  spacer: { flex: 1 },
  strong: { color: colors.text },
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
 */
export function TopBar({
  session,
  connected,
  collapsed,
  onFold,
}: {
  readonly session: SessionInfo | undefined;
  readonly connected: boolean;
  readonly collapsed: Collapsed;
  readonly onFold: (which: keyof Collapsed) => void;
}) {
  return (
    <header {...stylex.props(styles.bar, styles.top)}>
      <div {...stylex.props(styles.lights)}>
        {session === undefined ? (
          <span>no session</span>
        ) : (
          <>
            <span {...stylex.props(styles.strong, styles.name)}>
              {session.identity?.workspace ?? session.name}
            </span>
            {session.identity !== undefined && <span>{session.identity.project}</span>}
            <span>{session.identity?.kind}</span>
          </>
        )}
      </div>

      <div {...stylex.props(styles.topRow)}>
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
      </div>
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
