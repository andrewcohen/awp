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
//     strip does it and `space.titlebar` is that strip's height.
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
  top: {
    height: space.titlebar,
    // Clears the traffic lights. A number rather than a token because it is a
    // fact about macOS, not a decision this design gets to make.
    paddingInlineStart: "5.25rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    // The only way to move a window whose title bar is hidden.
    WebkitAppRegion: "drag",
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

      {/* ── folding a column, from the one place that is never folded ──────

          These used to live on the divider between the columns: a control that
          appeared on hover, and stayed visible once its column was folded
          because it was then the only way back. That worked and was hard to
          find — an affordance that is invisible until the pointer is already
          on a one-pixel rule is one nobody discovers, and it could not be
          pressed at all without a pointer.

          The top bar is the one strip that never folds, so it is where a
          control that folds something else belongs. Both are always here,
          both say which state they are in, and both are reachable by tab. */}
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
