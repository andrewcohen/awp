import type { Job } from "@awp-kit/jobs";
import type { SessionInfo, WorkspaceFacts } from "@awp-kit/protocol";
import { SidebarSimpleIcon } from "@phosphor-icons/react/SidebarSimple";
import * as stylex from "@stylexjs/stylex";
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

/**
 * The narrowest the corner strip is allowed to get.
 *
 * The traffic lights own the first 5.25rem and two fold controls follow them,
 * so a strip narrower than this puts a button under a light. It is a floor and
 * not a width: while the sidebar is open the strip matches it, and a seam that
 * moved when the sidebar was dragged would read as a rendering fault.
 */
const STRIP_FLOOR = "9.5rem";

// ── how a window with no title bar is actually moved ──────────────────────
//
// `-webkit-app-region: drag` is emitted by StyleX and does nothing. Electrobun
// does not read CSS for it; its preload matches on the DOM, and on two things
// only:
//
//   target.closest('[style*="app-region"][style*="drag"]')   an INLINE style
//   target.closest(".electrobun-webkit-app-region-drag")     its own class
//
// StyleX produces neither — it produces a class of its own and a rule in a
// stylesheet. So the property has never moved this window. It moved because
// `hiddenInset` leaves a real title bar behind the strip, and AppKit was doing
// the work; the note in AGENTS.md claiming otherwise was checking that the CSS
// was emitted, which it is, rather than that anything reads it.
//
// That mattered the moment the title bar went away. With `titleBarStyle:
// "hidden"` there is no native region left, so without the class below the
// window cannot be moved at all.
//
// The CSS property is kept as well as the class. It costs nothing, it is the
// standard spelling, and it is what a different host would read.
const DRAG = "electrobun-webkit-app-region-drag";
const NO_DRAG = "electrobun-webkit-app-region-no-drag";

/** `stylex.props`, with one of electrobun's drag classes appended. */
const withRegion = (
  region: string,
  props: {
    readonly className?: string | undefined;
    readonly style?: Readonly<Record<string, unknown>> | undefined;
  },
): {
  readonly className: string;
  readonly style?: Readonly<Record<string, unknown>> | undefined;
} => ({
  ...props,
  className: props.className === undefined ? region : `${props.className} ${region}`,
});

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
  // ── and then it stopped spanning the window ──────────────────────────────
  //
  // "let the panes go all the way to the top again". A full-width strip is
  // forty pixels the agent and the panels do not get, spent on a constraint
  // that is only true of the window's left corner: AppKit floats the traffic
  // lights over the first 84px, and nothing else up there needs clearing.
  //
  //   before  ┌───────────────────────────────┐   after  ┌────┬──────┬─────┐
  //           │ ▣          no session       ▣ │          │ ▣▣ │agent │tabs │
  //           ├────────┬──────────┬───────────┤          ├────┤      │     │
  //           │sidebar │ agent    │ panels    │          │side│      │     │
  //
  // So the strip is as wide as the sidebar and no wider, and the other two
  // columns start at the top of the window. It is `position: absolute` rather
  // than a row: a row is a row across the whole window by construction, and
  // what this is now is a corner.
  //
  // **It never folds, and that is why it is not inside the sidebar.** Both
  // fold controls live here, so putting the strip in the column it can hide
  // would take the way back with it. Folded, it holds its floor width — enough
  // for the lights and two buttons — and the columns simply begin to its right.
  //
  // The title went to the footer. There is no room for it here, and the footer
  // is already the strip that says what the window is doing; a name that is
  // read once when the selection changes belongs with the other status, not in
  // a corner competing with two controls for 152 pixels.
  top: {
    position: "absolute",
    insetBlockStart: 0,
    insetInlineStart: 0,
    zIndex: 2,
    alignItems: "center",
    height: space.titlebar,
    // Clearing the traffic lights, which are back: `titleBarStyle: "hidden"`
    // was tried and reverted because a tiling window manager stops managing an
    // untitled window. See the note in the main process.
    paddingInlineStart: space.lightsInline,
    paddingInlineEnd: "0.35rem",
    // Its own ground, because it is absolute and whatever is beneath it would
    // otherwise show through the buttons. It matters most folded, when the
    // strip sits over the agent column rather than over the sidebar.
    backgroundColor: colors.base,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    // The only way to move a window whose title bar is hidden. It applies to
    // the whole strip: everything interactive in here sets it back to `no-drag`
    // itself, so the draggable part is exactly the part with nothing in it.
    WebkitAppRegion: "drag",
  },
  /** How wide the strip is: the sidebar's width, or its floor when folded. */
  wide: (px: number) => ({ width: `max(${px}px, ${STRIP_FLOOR})` }),
  // Truncated rather than allowed to push the counts off the bar. A name is
  // long — a sentence somebody typed — and this row is 1.6rem tall with three
  // other things in it.
  title: {
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  // The header's two halves, as one line that can be clipped from the right.
  // `minWidth: 0` with the `flex`, or a long title pushes the counts off the
  // bar rather than being ellipsised — the pair AGENTS.md names.
  named: {
    display: "flex",
    alignItems: "baseline",
    flex: 1,
    minWidth: 0,
    // No gap. The slash is the separator, and space either side of it would
    // make it a third thing on the line rather than the join between two.
    gap: 0,
  },
  /** An address: the project, and the slash that ends it. */
  where: {
    flexShrink: 0,
    color: colors.muted,
    fontFamily: text.mono,
    fontSize: text.title,
  },

  // The agent column's header. Same height as the corner strip and the panels'
  // tab strip, so the three line up across the window — they are one band of
  // chrome drawn in three pieces, and a pixel of disagreement reads as a
  // rendering fault rather than as three columns.
  agentBar: {
    height: space.titlebar,
    flexShrink: 0,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
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
  width,
  collapsed,
  onFold,
}: {
  /** The sidebar's current width. The strip matches it, down to its floor. */
  readonly width: number;
  readonly collapsed: Collapsed;
  readonly onFold: (which: keyof Collapsed) => void;
}) {
  return (
    <header {...withRegion(DRAG, stylex.props(styles.bar, styles.top, styles.wide(width)))}>
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
          the bar's own start padding is what clears them. Removing them was
          tried and cost tiling-window-manager support — see the note in the
          main process. */}
      <button
        type="button"
        aria-label={collapsed.sidebar ? "show the sidebar" : "hide the sidebar"}
        aria-pressed={!collapsed.sidebar}
        title={collapsed.sidebar ? "show the sidebar" : "hide the sidebar"}
        onClick={() => onFold("sidebar")}
        {...withRegion(NO_DRAG, stylex.props(styles.toggle, !collapsed.sidebar && styles.toggleOn))}
      >
        <SidebarSimpleIcon size={15} aria-hidden />
      </button>
    </header>
  );
}

/**
 * The agent column's own header: what is open, and what the window is doing.
 *
 * ── why the footer went away ──────────────────────────────────────────────
 *
 * A window with a bar top and bottom spends two strips on chrome and gives the
 * columns what is left. Once the top bar became a corner, the footer was the
 * only full-width strip still doing that — for a name, a toggle and two
 * counts. So each column carries its own header instead, and this is the
 * middle one's.
 *
 * ── and why the counts are here rather than anywhere else ─────────────────
 *
 * **This column cannot be folded.** `Collapsed` has a `sidebar` and an
 * `accessory` and no third field, which makes the agent's header the one strip
 * in the window that is always on screen and always full width — the property
 * the footer had, and the reason it held the job summary at all:
 *
 *   a job that fails while its column is collapsed is a job nobody hears
 *   about, and the whole reason the jobs system exists is so that stops
 *   being true
 *
 * The same argument puts "no daemon" here.
 *
 * Nothing is said when there is nothing to say. A strip that always reads
 * `0 running · 0 failed` teaches the eye to skip it, which costs exactly the
 * one moment it exists for.
 */
/**
 * `<project>/<title>`, or the honest thing when there is no project to name.
 *
 * A session awp did not create has no identity, so there is no project and no
 * display name — only the name zmx knows it by. That is an address rather than
 * a title, so it is drawn as one: whole, in mono, and not pretending to be
 * prose it is not.
 */
function Title({
  session,
  facts,
}: {
  readonly session: SessionInfo | undefined;
  readonly facts: WorkspaceFacts | undefined;
}) {
  if (session === undefined) {
    return <span {...stylex.props(styles.strong, styles.title)}>no session</span>;
  }

  const project = session.identity?.project;
  if (project === undefined) {
    return <span {...stylex.props(styles.where, styles.title)}>{session.name}</span>;
  }

  // The best name there is, in the order they are worth: what a person called
  // it, what the model called it, and the directory as the name of last
  // resort. The slug is a fallback rather than a field — a workspace whose
  // display name is its slug says the slug once, not twice.
  const title =
    facts?.displayName ?? session.identity?.label ?? session.identity?.workspace ?? session.name;

  return (
    <span {...stylex.props(styles.named)}>
      <span {...stylex.props(styles.where)}>{project}/</span>
      <span {...stylex.props(styles.strong, styles.title)}>{title}</span>
    </span>
  );
}

export function AgentBar({
  jobs,
  session,
  facts,
  connected,
  collapsed,
  onFold,
}: {
  readonly jobs: ReadonlyArray<Job>;
  readonly session: SessionInfo | undefined;
  /** What is known about the open session's workspace, if anything is. */
  readonly facts: WorkspaceFacts | undefined;
  readonly connected: boolean;
  readonly collapsed: Collapsed;
  readonly onFold: (which: keyof Collapsed) => void;
}) {
  const counted = tally(jobs);

  return (
    <header {...stylex.props(styles.bar, styles.agentBar)}>
      {/* ── where the work is, then what it is ───────────────────────────

          `<project>/<title>`, and nothing else. It was `displayName · project
          · kind` once — three fields where a header has one job — and then
          just the title, which lost the one piece of context a person
          switching between two workspaces actually needs.

          The order is not decoration: the project narrows, the title names.
          Read left to right it answers "where am I" before "what is this",
          which is the order somebody arriving at the window asks them in.

          Two families, per the rule. A project is an address — a directory
          somebody types elsewhere — so it is mono; a title is prose and is
          ui. The slash carries the join, which is why there is no chip, no
          rule and no extra gap around it: a separator that is doing its job
          does not need furniture.

          The project does not truncate and the title does. A clipped title is
          still a title; a clipped project is a different project. */}
      <Title session={session} facts={facts} />

      <span {...stylex.props(styles.spacer)} />

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

      {/* Nothing at all while it is working.
      
          A green "daemon" sitting there permanently is a status light that is
          on by definition — it teaches the eye to skip that corner, which costs
          exactly the one moment it exists for. So the connected state is
          silence, and the disconnected state is a word rather than a dot. */}
      {!connected && <span {...stylex.props(styles.warn)}>no daemon</span>}

      {/* ── the way back, and only the way back ─────────────────────────────
      
          The control that folds the panels lives *on* the panels — at the end
          of their own tab strip, which is the edge it acts on. That leaves one
          hole, and it is the hole every self-hosted control has: folded away,
          it takes itself with it.
      
          So the pair is one control rendered at the boundary between the two
          columns. Open, it is the panels' last tab-strip item. Closed, it is
          this header's last item — the same place on screen, because the
          panels are no longer occupying it. */}
      {collapsed.accessory && (
        <button
          type="button"
          aria-label="show the panels"
          title="show the panels"
          aria-pressed={false}
          onClick={() => onFold("accessory")}
          {...stylex.props(styles.toggle)}
        >
          <SidebarSimpleIcon size={15} aria-hidden {...stylex.props(styles.mirrored)} />
        </button>
      )}
    </header>
  );
}
