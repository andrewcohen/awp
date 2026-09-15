import type { SessionInfo, Thread, WorkspaceFacts, WorkspaceStatus } from "@awp-kit/protocol";
import { CaretRightIcon } from "@phosphor-icons/react/CaretRight";
import * as stylex from "@stylexjs/stylex";
import { AnimatePresence, motion } from "motion/react";
import { pill, useSpring } from "./springs";
import { useMemo, useState } from "react";
import { useThreadMenu } from "./ArchiveThread";
import { More, RightClick } from "./menus";
import { Rename } from "./Rename";
import { useWorkspaceMenu } from "./ReclaimWorkspace";
import { Amoeba } from "./Amoeba";
import { type Facts, factsKey } from "./useFacts";
import {
  rememberFolded,
  rememberLooseOpen,
  rememberedFolded,
  rememberedLooseOpen,
} from "./remembered";
import { typeset } from "./typeset";
import { colors, space, text, timing } from "./tokens.stylex";
import {
  PRIMARY,
  type ThreadGroup,
  type Workspace,
  groupByThread,
  headingless,
  groupByWorkspace,
  openable,
  prIn,
} from "./workspaces";

/**
 * What is known about a workspace, if anything is.
 *
 * A workspace with no session carrying an identity has no key to look up —
 * which is every session someone else started, and is why this can answer
 * undefined rather than taking the two halves as arguments.
 */
const factsFor = (facts: Facts, workspace: Workspace): WorkspaceFacts | undefined => {
  const pair = workspace.pair;
  return pair === undefined ? undefined : facts.get(factsKey(pair.project, pair.workspace));
};

// The list of workspaces, and which of them can be opened.
//
// Two lines per row, and the rules below are the Go deck's — see
// `archive/internal/deckui/sidebar.go`, which is around sixty percent prose
// about exactly this strip. They are worth taking rather than rediscovering,
// and each one is here because something was tried and read badly.
//
//   ● pr-2340-lantern-header-header-allowlist
//     rowan · agent
//
//   ● effect-ts-tabular-export-timemachine
//     rowan · agent editor action_dev
//
// **Two lines, always.** A row has two unrelated facts to carry — which
// workspace, and what is in it — and on one line they compete: the kinds are
// short and go last, so the name is what truncates, and a truncated name is the
// one field you cannot work out from the others. Given a line to itself the
// name gets the whole column. The cadence has to be fixed to be a cadence, so
// the second line always says something; a name with nothing under it reads as
// a one-line row and the rhythm is gone.
//
// **Colour marks structure, not content.** One dot per row carries a hue and
// nothing else on the row does. The second line is the line there is one of per
// row, so a colour on it is a colour repeated down the whole column — and
// emphasis spent everywhere is emphasis nowhere.
//
// **A workspace called `default` is the repository's**, and the word says
// nothing: six projects with one workspace each would render as six rows
// reading `default`. So the project is the name and `default` goes below it,
// which is the same trade in both directions — line two is whichever half of
// project/workspace line one did not use.
//
// ── threads sit above all of this ──────────────────────────────────────────
// The strip is a list of threads, each holding its workspaces, and one group at
// the end for everything no thread has claimed. The nesting is one level and
// stays one level: a workspace row already carries two lines, and a third level
// of indent would spend the name's column on structure.
//
// A thread heading is a heading, not a row — it does not open anything, because
// a thread has nothing to open. What it has is a `+`, which is the only way to
// make a workspace from this window.
//
// The loose group is not a thread with a blank name. It has no `+` and no
// title to edit, and `ThreadGroup.thread === undefined` is what makes that a
// type error rather than something to remember.

// One rule of the Go strip is deliberately **not** taken: it drops a
// `pr-1234-` prefix from a name because the number is on the line below. Here
// there is no PR number on any line, so dropping it would lose the only place
// that information appears.
//
// Air between rows is a margin rather than a blank row. The Go strip paid a
// third of its height for that separation because a terminal has no smaller
// unit than a line; this one does not have to.

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%" },
  list: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    // Never sideways. A horizontal scrollbar in a column of names means a name
    // that should have been truncated was not, so scrolling to read it is the
    // wrong repair — this makes the mistake show up as a clipped name, which is
    // findable, rather than as a scrollbar, which reads as intentional. See the
    // keyboard-and-layout rules in AGENTS.md.
    overflowX: "hidden",
    // A gutter at both ends now: the sticky footing used to carry the one at
    // the bottom, and without it the last row sat flush against the window.
    padding: `${space.row} 0`,
  },
  empty: { padding: `0.5rem ${space.gutter}`, color: colors.muted },
  failure: {
    padding: `${space.gutter}`,
    color: colors.muted,
    lineHeight: 1.6,
  },
  head: { marginBottom: "0.75rem" },
  quiet: { fontSize: text.small, opacity: 0.8 },
  gap: { marginTop: "0.75rem" },

  // The band is the row, both lines of it, edge to edge — the gutter is inside
  // the row rather than around it, so a selected workspace is a strip and not a
  // floating rectangle.
  row: {
    // `relative`, so the travelling accent edge has this row to sit in.
    position: "relative",
    padding: `${space.row} ${space.gutter}`,
    marginBottom: "0.3rem",
    // Rounded where the accent edge is, square where the column ends. The band
    // runs into the divider rather than stopping short of it in a curve — a
    // rounded trailing corner reads as a card floating in the strip, where
    // what this is is a row *of* the strip. On the selected row it is also the
    // difference between the fill agreeing with the 2px edge and arguing with
    // it.
    borderStartStartRadius: "0.35rem",
    borderEndStartRadius: "0.35rem",
    borderStartEndRadius: 0,
    borderEndEndRadius: 0,
    // A strip of rows that does nothing under the pointer reads as a
    // listing rather than as a set of things to open. Cheap, and it is the
    // only feedback this column gives before a click.
    transitionProperty: "background-color, transform",
    transitionDuration: { default: timing.quick, "@media (prefers-reduced-motion: reduce)": "0s" },
    transitionTimingFunction: timing.ease,
    ":hover": { backgroundColor: colors.surface },
  },
  // The one level of indent. Enough that the eye finds the thread's left edge,
  // not so much that the name loses its column.
  nested: { paddingInlineStart: "1.5rem" },

  /**
   * The box whose height the fold animates.
   *
   * `overflow: hidden` is the whole mechanism: without it the rows are drawn
   * over the group below for the length of the transition, which reads as the
   * list tearing rather than folding.
   */
  folding: { overflow: "hidden" },

  group: { marginBottom: "0.5rem" },
  heading: {
    display: "flex",
    // `center`, not `baseline`. The caret is an icon in a flex box now, and a
    // flex container's baseline is its last line box — so under `baseline` the
    // mark sat low against the title instead of centred on it.
    alignItems: "center",
    gap: "0.4rem",
    padding: `0.15rem ${space.gutter}`,
  },
  // The heading and its ⋯, side by side. A menu trigger is a button and cannot
  // be nested inside the fold button, so the two are siblings in a row rather
  // than one control.
  headingRow: {
    display: "flex",
    alignItems: "center",
    // The end padding is the row's, so the ⋯ sits at the same edge every other
    // control in the column does; the fold button keeps the start padding it
    // already had.
    paddingInlineEnd: space.gutter,
  },
  /** `minWidth: 0` with it, or a long title pushes the ⋯ off the column. */
  grow: { flex: 1, minWidth: 0 },
  // A section header, and dressed as one by weight and spacing — not by
  // colour.
  //
  // It was the accent for a while, and that was the accent being spent rather
  // than used: an orange on every heading down the strip is a second body
  // colour, and then nothing is left to mark the one row that matters. The
  // accent is now on exactly two things, both of which answer "this, here" —
  // the selected row's edge and the selected tab.
  //
  // Not `text-transform: uppercase`, because a thread title is a person's own
  // sentence and shouting it back at them is a different thing from labelling
  // a section. The weight and the spacing do the structural work; the words
  // stay as they were typed.
  threadName: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
    letterSpacing: "0.03em",
  },
  loose: { color: colors.muted, fontWeight: text.medium, letterSpacing: "normal" },
  headingButton: {
    width: "100%",
    borderStyle: "none",
    backgroundColor: "transparent",
    font: "inherit",
    textAlign: "left",
    cursor: "pointer",
  },
  /**
   * The disclosure mark. A box the icon is centred in, not a text slot.
   *
   * `display: flex` so the rotation turns about the icon's own centre — an
   * inline span is as tall as the line box, so a rotation inside one pivots
   * about a point above the glyph and the caret appears to swing rather than
   * to turn.
   */
  caret: {
    flexShrink: 0,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "0.9rem",
    color: colors.muted,
  },
  // How much is behind the fold. A disclosure that will not say how much it is
  // hiding is one nobody opens.
  count: { flexShrink: 0, fontSize: text.small, color: colors.muted },
  plus: {
    flexShrink: 0,
    padding: "0 0.3rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  // The selected row: a fill *and* an edge, and the edge is the accent.
  //
  // A fill alone was the whole mark, in the same value as every rule in the
  // window — so "selected" and "there is a divider here" were the same colour,
  // and the row a person is looking at was the least distinguished thing on
  // the strip. The bar is the mark; the fill is what makes it read as a row
  // rather than as a line beside one.
  //
  // Inset with a box shadow rather than a border, so gaining it does not move
  // the row's contents by a pixel — which a real border would, on the one row
  // whose position a person is tracking.
  rowOn: {
    backgroundColor: colors.raised,
    // The accent bar is `edge` now, a single element that slides between
    // rows. What stays here is the fill, which does not travel: two rows
    // briefly filled during the slide reads as the selection being handed
    // over, which is what is happening.
  },
  /** The travelling accent edge. See the note at its `layoutId`. */
  edge: {
    position: "absolute",
    insetBlockStart: 0,
    insetBlockEnd: 0,
    insetInlineStart: 0,
    width: "2px",
    borderStartEndRadius: "2px",
    borderEndEndRadius: "2px",
    backgroundColor: colors.accent,
  },

  // Line one is a button and line two is not, which is why the padding lives on
  // the row: a button carrying it would put the band on one line of two.
  title: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.5rem",
    // `flex: 1` with `minWidth: 0`, and not `width: 100%`.
    //
    // This was `width: 100%` from when it was the row's only child. Putting the
    // fold menu beside it made that an overflow of exactly the menu's width:
    // a full-width child plus a sibling is wider than the row, and the sidebar
    // grew a horizontal scrollbar — 236px of column against 240px of content.
    //
    // `minWidth: 0` is the half that is easy to leave off. A flex item will not
    // shrink below its content by default, so the name would push the row wide
    // again the moment it was long enough.
    flex: 1,
    minWidth: 0,
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.text,
    font: "inherit",
    // The one weight change that does most of the work. A name and the caption
    // under it were the same size and the same weight, so the only thing
    // separating them was a colour that was itself failing contrast.
    textAlign: "left",
    cursor: "pointer",
  },
  titleShut: { color: colors.muted, cursor: "default", opacity: 0.55 },
  /**
   * The whole band is the target, not just line one.
   *
   * A row is two lines and the name is the top one, so two thirds of a row
   * somebody is aiming at did nothing — and line two is where the slug, the
   * phase and the project are, which is the half a person reads to decide
   * *which* row they want. Pointing at the thing you just read and having it
   * not respond is the shape of a control that looks broken.
   *
   * An empty `::after` over the row rather than a click handler on the row's
   * `div`, because the button is what carries the semantics: the role, the
   * disabled state, the tooltip, and `data-nav-item`, which is what ctrl+j
   * and ctrl+k step through. A div with an `onClick` has none of those and
   * would have to restate all four.
   *
   * It positions against `row`, which is already `relative` for the accent
   * edge — so the target is exactly the band that lights up on hover.
   *
   * What it costs: the `title` on line two's pull request number and phase.
   * The overlay is above them, so the row's own tooltip — the address — is
   * what a hover reports now. Both facts are still drawn; it is the second
   * reading of them that goes.
   *
   * Not applied to a shut row. A disabled button swallows the press rather
   * than passing it on, so an overlay there would be a dead sheet over the
   * one row that has chips underneath worth reaching.
   */
  stretch: {
    "::after": {
      content: '""',
      position: "absolute",
      insetBlockStart: 0,
      insetBlockEnd: 0,
      insetInlineStart: 0,
      insetInlineEnd: 0,
    },
  },
  // Line one is the name and the menu beside it. The name's button still takes
  // the width it can, so most of the row is still one target.
  titleRow: { display: "flex", alignItems: "baseline", gap: "0.25rem" },
  label: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },

  // A fixed width, so the second line starts under the first letter of the name
  // and not under the dot. One level of structure on this strip, one left edge
  // for everything that is not the dot.
  // A bullet rather than text, so it is sized by eye against the row's name
  // rather than from the type scale — but it still has to move when that scale
  // does, which is why this number changed with it.
  dot: {
    width: "0.85rem",
    flexShrink: 0,
    fontSize: 10,
    color: colors.muted,
    // The hue is the state, so a state change is a hue change — and five
    // colours that cut straight to one another is the flick this strip was
    // reported for. It is the one property here the mark itself cannot carry:
    // `Amoeba` draws in `currentColor` precisely so this stays the caller's.
    transitionProperty: "color",
    transitionDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    transitionTimingFunction: timing.ease,
  },
  // One per state, and named for the state rather than the colour so a theme
  // can move them. `exited` deliberately has none: a session that ended is what
  // the muted default already says, and giving it a hue would put a colour on
  // the strip for the one thing nobody needs to look at.
  dotWorking: { color: colors.live },
  dotWaiting: { color: colors.waiting },
  /**
   * A dot that is doing something, breathing.
   *
   * ── waiting only, since `working` grew a body ──────────────────────────
   *
   * This strip is a set of hues that are all equally still, so `working`
   * and `idle` differed only by a colour somebody has to have learned. A
   * mark that moves is the one thing on the strip that cannot be a
   * screenshot, and it was spent on both of the states that change on their
   * own — an agent working, and an agent waiting for a person.
   *
   * Which made the two of them move *identically*, so the thing that could
   * not be a screenshot still could not be told apart. `working` is an
   * `Amoeba` now and this is what is left: a person is being waited on, and
   * that is the one state on the strip somebody has to act on.
   *
   * Opacity and scale rather than a colour cycle: the hue is already
   * carrying the state, and a hue that changes would be a second claim.
   *
   * 2.6s, which is slow — a fast pulse on a list of eight rows is a
   * christmas tree, and this has to survive being on screen all day.
   */
  breathing: {
    animationName: stylex.keyframes({
      "0%, 100%": { opacity: 1, transform: "scale(1)" },
      "50%": { opacity: 0.45, transform: "scale(0.88)" },
    }),
    animationDuration: { default: "2.6s", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.45, 0, 0.55, 1)",
    animationIterationCount: {
      default: "infinite",
      "@media (prefers-reduced-motion: reduce)": "1",
    },
  },
  dotError: { color: colors.warn },
  dotIdle: { color: colors.muted },
  // A workspace nothing has reported on, with an unread mark. There is a state
  // to draw and no hue for it, so the unread colour is the whole signal — which
  // is the only case it may be, and the reason this is not applied over a known
  // status. The first version did apply it over one, and the measurement said
  // so before a screenshot could have:
  //
  //   "working, unread"   rgb(138, 173, 244)   ← the ready blue, not the green
  //
  // Two facts on one mark only works if they use different channels. Colouring
  // by unread spends the channel the state was using and leaves a strip where
  // every row that needs attention is the same colour whatever it needs.
  dotUnknownUnread: { color: colors.ready },

  meta: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.35rem",
    paddingInlineStart: "1.25rem",
    fontSize: text.small,
    color: colors.muted,
    lineHeight: 1.5,
    overflow: "hidden",
  },
  ident: { flexShrink: 0 },
  // The pull request number, in the accent. It is the one thing on line two
  // that points somewhere outside this window, and the hue is what says so.
  // The pull request number keeps the accent, and it is the exception that
  // shows the rule: it is the one thing on the strip that points somewhere
  // outside this window, which is a different kind of fact from everything
  // beside it. One use, on a handful of rows, is an accent. On every heading
  // it was a palette.
  pr: { flexShrink: 0, color: colors.accent, fontFamily: text.mono },
  // Where the work is in the configured dev loop. Never truncated — `impl…`
  // says nothing that `implement` does not, and the whole word is nine
  // characters.
  phase: { flexShrink: 0, whiteSpace: "nowrap" },
  // The one thing on line two allowed to truncate. A project name is short and
  // a kind is shorter; a slug is the long one, and it is also the one whose
  // beginning carries the information.
  // Monospace, because it is an address — a directory to `cd` into and half a
  // bookmark to push. The name above it is prose and is not.
  slug: {
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    fontFamily: text.mono,
  },
  // The separator, not a word. Present so the two halves of the line do not run
  // together, muted so it is not one of them.
  sep: { flexShrink: 0, opacity: 0.5 },
  kinds: { display: "flex", gap: "0.3rem", overflow: "hidden" },
  kind: {
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "inherit",
    whiteSpace: "nowrap",
  },
  kindOn: { color: colors.text },
  // Above the row's stretched target — see `stretch`. Only the chips that are
  // *buttons* are raised: a lone kind is a span saying what is running, and
  // raising that would punch a hole in the row for a word nobody presses.
  kindPick: { position: "relative", zIndex: 1, cursor: "pointer" },
  reason: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
});

// Two facts on one mark: the *hue* is the state, and the *shape* is whether it
// has been read. That pairing is deliberate — a colour alone excludes anyone
// who cannot see the difference between the amber and the green, and a shape
// alone would need five of them, which is a legend nobody has.
//
// Both are `Amoeba` now, and there is no glyph table left. `●` and `◉` were
// literals here for as long as the mark was text; a state change was then a
// change of *text node*, which nothing can transition. The same two shapes are
// a border-radius and a fill on one element, which can.

const DOT: Record<WorkspaceStatus, { readonly style: stylex.StyleXStyles; readonly say: string }> =
  {
    working: { style: styles.dotWorking, say: "working" },
    waiting: { style: styles.dotWaiting, say: "waiting for you" },
    error: { style: styles.dotError, say: "error" },
    idle: { style: styles.dotIdle, say: "idle" },
    exited: { style: styles.dotIdle, say: "exited" },
  };

const Dot = ({
  live,
  status,
  unread,
}: {
  /** A session is running, which is all this knew before facts existed. */
  readonly live: boolean;
  readonly status: WorkspaceStatus | undefined;
  readonly unread: boolean;
}) => {
  // The fallback is not "idle". A workspace nothing has ever reported on is a
  // different thing from one an agent has finished in, and drawing them alike
  // would claim knowledge this does not have — so an unknown state keeps the
  // one fact that is certain, which is whether anything is running.
  const known = status === undefined ? undefined : DOT[status];
  const style = known?.style ?? (live ? styles.dotWorking : styles.dotIdle);
  const say = known?.say ?? (live ? "running" : "not running");
  // An agent working right now, and only that. A workspace nothing has
  // reported on falls back to "something is live", which is most rows on a
  // real machine — see `Amoeba` on why the shape is not spent there.
  const crawling = status === "working";
  // What is left of the pair that used to move. See `breathing`.
  const moving = status === "waiting";

  return (
    <span
      // The name, not the glyph. A screen reader reading "●" says "black
      // circle", which is a description of the ink rather than of the row.
      role="img"
      aria-label={unread ? `${say}, unread` : say}
      {...stylex.props(
        styles.dot,
        style,
        moving && styles.breathing,
        known === undefined && unread && styles.dotUnknownUnread,
      )}
    >
      <Amoeba crawling={crawling} unread={unread} />
    </span>
  );
};

/**
 * One workspace: its name, and whatever line one did not already say.
 *
 * Line one is always a button and opens the workspace's primary session, so
 * every row has a full-width target. Where a workspace has more than one
 * session the kinds on line two are buttons too — that is the only way to
 * reach the editor without reaching the agent first — and putting them on the
 * second line is what keeps them out of the name's way.
 */
function Row({
  workspace,
  facts,
  title,
  selected,
  at,
  onSelect,
  onOpen,
  thread,
  onThreadsChanged,
}: {
  readonly workspace: Workspace;
  /** What is known about this workspace, or nothing has reported on it. */
  readonly facts: WorkspaceFacts | undefined;
  /**
   * The name to show, when the row is standing in for its whole thread.
   *
   * A thread holding one workspace draws no heading — see `Group` — so the
   * thread's title has nowhere else to be, and it is the truest name there
   * is: the sentence a person typed, kept exactly, in this window's own
   * store. Absent when the row is one of several under a heading, where
   * repeating the group's name on every child would say nothing.
   */
  readonly title: string | undefined;
  readonly selected: string | undefined;
  /**
   * The workspace the window is looking at, whether or not a session is open
   * in it.
   *
   * Beside `selected` rather than instead of it, because the two answer
   * different questions: this decides which *row* is marked, and the name
   * decides which of a row's kind chips is. A row whose only session has
   * exited has no name to match and is still the row somebody is on — see
   * `sessionAt`, which stopped answering for an ended session.
   */
  readonly at: { readonly project: string; readonly workspace: string } | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  /**
   * Open the workspace itself, when there is no session to open.
   *
   * The row is an address and not a session — see `unstarted` in
   * workspaces.ts. A workspace a thread holds and nothing is running in is
   * still openable: its conversation, its diff and its pull request are all
   * questions about the checkout, and none of them needs a terminal.
   */
  readonly onOpen: (project: string, workspace: string) => void;
  /** The thread holding this workspace, if any. */
  readonly thread: Thread | undefined;
  readonly onThreadsChanged: () => void;
}) {
  // Hover is tracked here rather than done in CSS, because the control lives in
  // a child component and `:hover` on a parent cannot reach across one. Focus
  // is left to CSS — see `trigger` in MoveToThread.
  const [hovered, setHovered] = useState(false);
  const pair = workspace.pair;
  const active =
    pair !== undefined && at !== undefined
      ? pair.project === at.project && pair.workspace === at.workspace
      : workspace.sessions.some((session) => session.name === selected);
  const live = workspace.sessions.some((session) => !session.ended);
  const primary = openable(workspace);
  const several = workspace.sessions.length > 1;
  // Nothing is running here, and it is one of ours. A foreign row IS its
  // session, so it has nothing else to be and stays shut.
  const stopped = workspace.sessions.length === 0 && workspace.pair !== undefined;
  const shut = primary === undefined && !stopped;

  // Whichever half of project/workspace the name did not use. A `default`
  // workspace is the repository's, so the project is the name and `default`
  // goes below; anything else names itself and the project goes below.
  const other = workspace.foreign ? "elsewhere" : (workspace.otherIdent ?? "");

  /**
   * Which pull request this row is about, from either thing that knows.
   *
   * ── two sources, and only one of them used to be read here ───────────────
   *
   * `facts.pr` is the agent's own hooks writing what it is working on into
   * `~/.awp/workspace-state.json` — so it exists for a checkout something has
   * reported on, and for no other. awp's own record is the thread's link,
   * which is written when a review is started and, since the adoption pass,
   * whenever the inbox recognises a pull request opened from a checkout.
   *
   * Only the first was drawn, and AGENTS.md argued a third copy of the number
   * would be duplication — true while every linked thread was a *review*
   * thread, whose title already begins `#2418`. It stopped being true the
   * moment a thread named after the work could hold a link: reported as the
   * link not showing at all, on a thread the daemon had linked minutes
   * earlier. The number was on the record, in the window, and drawn nowhere.
   *
   * The hook's reading wins when both are there. It is about the checkout as
   * it stands; the link is about the work, and the two only disagree while
   * somebody is doing something the record has not caught up with.
   */
  const pr = facts?.pr ?? prIn(thread, pair?.project)?.number;

  // ── the label takes line one, and the slug moves down ────────────────────
  //
  // `effect-ts-tabular-export-timemachine` is a slug because it has to be a
  // directory, a jj workspace and half a bookmark. What the person typed was a
  // sentence, and line one is the line they read.
  //
  //   before   ● effect-ts-tabular-export-timemachine
  //              thicket
  //
  //   after    ● Tabular export time machine
  //              thicket · effect-ts-tabular-export-timemachine
  //
  // The slug is not dropped. It is the directory someone will `cd` into and
  // the bookmark they will push, so a strip that only showed the sentence
  // would make the workspace unfindable from anywhere outside this window.
  //
  // Only when they differ. Every workspace made before `awp_label` has no
  // label at all and falls back to the slug on line one — repeating it on line
  // two would give nearly every row today the same word twice.
  // Three sources, in the order they were meant.
  //
  //   facts.displayName   the Go implementation's, and nineteen workspaces on
  //                       this machine have one
  //   workspace.label     `awp_label`, which amoeba writes for what it makes.
  //                       Terse and legal rather than exact — zmx validates a
  //                       label value, so `Review: Inbox UI` is written as
  //                       `Review-Inbox-UI`. See `labelValue`.
  //   workspace.name      the slug, which every workspace has
  //
  // The exact sentence is the thread's title. Where the row stands in for its
  // whole thread it arrives as `title` and wins outright — it is the only one
  // of the four that is neither shortened, sanitized nor second-hand. Where
  // the row is one of several under a heading, the title is already on screen
  // above it, so the row says which *workspace* it is instead.
  //
  // The Go one first and not last, which is the opposite of the obvious
  // ranking. amoeba's own label is the better long-term home — it travels with
  // the session and needs no second file — but a workspace with both got them
  // from the same sentence, and a workspace with only one of them is the
  // ordinary case either way. First is where the data actually is.
  const shown = title ?? facts?.displayName ?? workspace.label ?? workspace.name;
  const slug = shown === workspace.name ? undefined : workspace.name;

  // Shown when worth showing, which is not whenever it exists. Eighteen of
  // twenty-one rows are one agent, and eighteen lines each ending in the word
  // "agent" is one word repeated down a column while the names it crowds out
  // are the part being read. A lone session names itself only when it is *not*
  // the agent — a captain, an editor on its own.
  const listed = several
    ? workspace.sessions
    : workspace.sessions.filter((session) => session.identity?.kind !== PRIMARY);

  // The reason takes the whole of line two when there is one. It is the most
  // important thing the row has to say, and giving it a third line would break
  // the cadence the two lines exist to keep.
  const refusal = several ? undefined : workspace.sessions[0]?.refusal;

  // ── which menu this row gets, and it is one or the other ─────────────────
  //
  // A row standing in for its whole thread — `title` is set, which is how
  // `Group` says so — carries the thread's menu, because that thread draws no
  // heading and has nowhere else to put it. A row under a heading does not:
  // the heading above already carries it, and one per sibling would offer to
  // archive the thread four times.
  // Renaming from the row, which happens only where the row stands in for its
  // whole thread — under a heading the title is the heading's, and two places
  // offering to rename one thing is one of them being wrong about what it owns.
  const [renaming, setRenaming] = useState(false);
  const asThread = useThreadMenu({
    thread: title === undefined ? undefined : thread,
    onChanged: onThreadsChanged,
    onRename: title === undefined || thread === undefined ? undefined : () => setRenaming(true),
  });
  // The other half. A row under a heading can be taken back on its own —
  // `thread` is the claim that makes that expressible, and `pair` is the
  // member. Both hooks always run and one of them always answers nothing.
  const asCheckout = useWorkspaceMenu({
    thread: title === undefined ? thread?.id : undefined,
    member: title === undefined ? pair : undefined,
    onChanged: onThreadsChanged,
  });
  const menu = title === undefined ? asCheckout : asThread;
  // Named apart from the row's own `onOpen`, which opens a workspace. This
  // resets the menu's `copy link` label — see `useThreadMenu`.
  const resetMenu = title === undefined ? undefined : asThread.onOpen;

  return (
    <RightClick
      items={menu.items}
      onOpen={resetMenu}
      style={[styles.row, active && styles.rowOn]}
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
    >
      {/* ── the edge travels down the strip ──────────────────────────────
          One accent bar with a `layoutId`, so moving between rows slides it
          from the row you left to the row you are on. As an inset shadow it
          was two rows changing colour, which says the same thing and shows
          none of the movement — and this strip is the one place in the
          window where "which row" is the whole question.

          Absolutely positioned rather than a border, so gaining it moves
          the row's contents by nothing at all. */}
      {active && (
        <motion.span layoutId="sidebar-edge" {...stylex.props(styles.edge)} transition={pill} />
      )}
      <div {...stylex.props(styles.titleRow)}>
        {renaming && thread !== undefined && title !== undefined ? (
          <Rename thread={thread.id} title={title} onDone={() => setRenaming(false)} />
        ) : (
          <button
            type="button"
            disabled={shut}
            // What ctrl+j and ctrl+k step through in this column. The row's
            // title, and not the chips or the hover controls beside it — a list
            // that moved through those is a list nobody can predict. See
            // navigation.ts.
            data-nav-item
            // The reason is the tooltip as well as line two. A row that will not
            // say why it is disabled is worse than no row at all.
            title={refusal ?? workspace.address}
            onClick={() => {
              if (primary !== undefined) {
                onSelect(primary);
              } else if (pair !== undefined) {
                onOpen(pair.project, pair.workspace);
              }
            }}
            {...stylex.props(
              typeset.heading,
              styles.title,
              !shut && styles.stretch,
              shut && styles.titleShut,
            )}
          >
            <Dot live={live} status={facts?.status} unread={facts?.unread === true} />
            <span
              onDoubleClick={(event) => {
                // The row's own click opens the workspace, and a double click
                // has already done that once. Stopping this one keeps the second
                // from re-selecting underneath a field that is about to open.
                event.stopPropagation();
                if (title !== undefined && thread !== undefined) {
                  setRenaming(true);
                }
              }}
              {...stylex.props(styles.label)}
            >
              {shown}
            </span>
          </button>
        )}
        {/* Which menu this is, is decided above — see `menu`. Every row has
            one; what differs is whether its items are the thread's or this
            one checkout's. */}
        <More label={`more for ${shown}`} shown={hovered} items={menu.items} onOpen={resetMenu} />
      </div>

      <div {...stylex.props(styles.meta)}>
        {refusal === undefined ? (
          <>
            {other !== "" && <span {...stylex.props(styles.ident)}>{other}</span>}
            {other !== "" && slug !== undefined && (
              <span aria-hidden {...stylex.props(styles.sep)}>
                ·
              </span>
            )}
            {slug !== undefined && <span {...stylex.props(styles.slug)}>{slug}</span>}
            {/* Fixed-width and first among the details, in that order for the
                reason the two-line layout exists at all: a number and a phase
                cannot truncate usefully, so they take their space and the slug
                above takes what is left. */}
            {pr !== undefined && (
              <span {...stylex.props(styles.pr)} title={`pull request #${pr}`}>
                {`#${pr}`}
              </span>
            )}
            {facts?.phase !== undefined && (
              <span
                {...stylex.props(styles.phase)}
                // Counted where there is a count. `3/7` beside `implement` is
                // the difference between knowing the work is underway and
                // knowing how far — and it is one of the few facts on this
                // strip that changes while somebody watches it.
                title={
                  facts.done !== undefined && facts.total !== undefined
                    ? `${facts.phase} · ${facts.done} of ${facts.total}`
                    : facts.phase
                }
              >
                {facts.done !== undefined && facts.total !== undefined
                  ? `${facts.phase} ${facts.done}/${facts.total}`
                  : facts.phase}
              </span>
            )}
            {/* Said, because the row is otherwise identical to a running one
                and the dot alone is a colour somebody has to have learned.
                The kinds chip would say nothing here — there are none. */}
            {stopped && (
              <>
                {(other !== "" || slug !== undefined) && (
                  <span aria-hidden {...stylex.props(styles.sep)}>
                    ·
                  </span>
                )}
                <span {...stylex.props(styles.kind)}>no session</span>
              </>
            )}
            {(other !== "" || slug !== undefined) && listed.length > 0 && (
              <span aria-hidden {...stylex.props(styles.sep)}>
                ·
              </span>
            )}
            <span {...stylex.props(styles.kinds)}>
              {listed.map((session) => {
                const kind = session.identity?.kind ?? "";
                if (kind === "") {
                  return null;
                }
                const chip = stylex.props(
                  styles.kind,
                  session.name === selected && styles.kindOn,
                  several && session.refusal === undefined && styles.kindPick,
                );
                return several ? (
                  <button
                    key={session.name}
                    type="button"
                    disabled={session.refusal !== undefined}
                    title={session.refusal ?? session.cmd}
                    onClick={() => onSelect(session)}
                    {...chip}
                  >
                    {kind}
                  </button>
                ) : (
                  <span key={session.name} {...chip}>
                    {kind}
                  </span>
                );
              })}
            </span>
          </>
        ) : (
          <span {...stylex.props(styles.reason)}>{refusal}</span>
        )}
      </div>
      {menu.dialogs}
    </RightClick>
  );
}

/**
 * One thread and the workspaces it claimed, or the group for what none did.
 *
 * The heading is a heading and not a row: a thread has nothing to open, so
 * making it look pressable would be a lie about what a click does. The nesting
 * is one level and stays one level — a workspace row already carries two lines,
 * and a third level of indent spends the name's column on structure.
 */
function Group({
  group,
  facts,
  selected,
  at,
  onSelect,
  onOpen,
  folded,
  onFold,
  alone,
  onThreadsChanged,
}: {
  readonly group: ThreadGroup;
  readonly facts: Facts;
  readonly selected: string | undefined;
  /** The workspace the window is looking at. See Row's own. */
  readonly at: { readonly project: string; readonly workspace: string } | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  /** Open a workspace that has no session. See Row's own. */
  readonly onOpen: (project: string, workspace: string) => void;
  readonly onThreadsChanged: () => void;
  readonly folded: boolean;
  /**
   * Undefined where there is nothing to fold into.
   *
   * A thread holding one workspace draws no heading at all — see below — and a
   * fold with no heading has no control to live on.
   */
  readonly onFold: (() => void) | undefined;
  /**
   * The only group there is, so its heading names nothing.
   *
   * See the note over the loose heading: a grouping is a statement about which
   * of several a row is in, and with one group there is no several.
   */
  readonly alone: boolean;
}) {
  // Hover on the heading, tracked here for the same reason `Row` tracks its
  // own: the control is in a child component, and `:hover` on a parent cannot
  // reach across one. Focus reveals it on its own — see `trigger`.
  const [hovered, setHovered] = useState(false);

  // ── a fold is a flick, not a panel ───────────────────────────────────────
  //
  // `heavy` first, on the reasoning that a group of rows is the largest thing
  // this column moves. Wrong: weight is about what is being moved, and what a
  // person is doing here is *glancing* — folding a thread away to see the one
  // under it, several times in a row. At 0.46s that reads as the column
  // thinking about it.
  //
  // `pill` is the crispest preset with any give at all, and the caret's
  // rotation takes the same one: two halves of one gesture that disagreed
  // about how long it takes would read as two controls.
  const folding = useSpring(pill);

  // The thread's own menu — its items, and the two dialogs they open. Called
  // unconditionally with a possibly-absent thread, because the loose group has
  // none and a hook cannot be skipped.
  // Renaming in place, and the state lives here rather than in the hook: the
  // field replaces the fold button, which is this component's to draw.
  const [renaming, setRenaming] = useState(false);
  const menu = useThreadMenu({
    thread: group.thread,
    onChanged: onThreadsChanged,
    onRename: group.thread === undefined ? undefined : () => setRenaming(true),
  });

  // ── the only group names nothing, so it draws no heading ────────────────
  //
  // `not in a thread` is a distinction, and a distinction needs something to
  // be distinct *from*. With no threads at all every workspace is loose, so
  // the heading, the count and the caret are three pieces of chrome over a
  // list that is simply the sidebar — and `not in a thread` reads as a fault
  // rather than as a category, because there is no thread anywhere to be in.
  //
  // The same argument as the rule below it, one level up: the grouping is
  // drawn where there is grouping to see. A single thread keeps its heading —
  // a title is the name of the work and says something a row cannot — and this
  // is only ever the derived group, which has no name of its own.
  //
  // **Forced open, and that is not a detail.** The loose group is shut until
  // opened and remembers that across launches, so dropping its heading without
  // this hides every row behind a control that is no longer drawn: an empty
  // sidebar, on the one machine state where it holds everything. The caller
  // passes `folded={false}` for the same reason; both halves are needed
  // because either alone is the bug.
  if (alone) {
    return (
      <div {...stylex.props(styles.group)}>
        {group.workspaces.map((workspace) => (
          <Row
            key={workspace.key}
            workspace={workspace}
            facts={factsFor(facts, workspace)}
            title={undefined}
            selected={selected}
            at={at}
            onSelect={onSelect}
            onOpen={onOpen}
            thread={undefined}
            onThreadsChanged={onThreadsChanged}
          />
        ))}
      </div>
    );
  }

  // ── one workspace is not a group ─────────────────────────────────────────
  //
  // Thread and workspace are one-to-one today, so every thread drew a heading
  // above a single row and the two said the same thing twice:
  //
  //   before                        after
  //   ▸ Review: Inbox UI            ◉ Review: Inbox UI
  //     ◉ Review-Inbox-UI             review-inbox · andrew/review-inbox
  //       review-inbox · …
  //
  // Two lines of chrome to name one thing, and an indent implying a structure
  // with one member. So the heading appears when it earns its line — when the
  // thread actually holds more than one workspace — and is absent when it
  // would only be repeating the row beneath it.
  //
  // **Not a change to the data.** The thread is still there, still holds the
  // pair, still groups the moment a second workspace joins it. What changes is
  // that the grouping is drawn only where there is grouping to see, which is
  // the same reason `slug` is hidden when it equals the name.
  //
  // The empty thread keeps its heading: it has no row to collapse into, and a
  // thread waiting for its job to finish is exactly the thing a person is
  // watching for.
  // It is also the one group with no fold: a heading that would hide exactly
  // one row is a control that costs a line to save a line.
  if (group.thread !== undefined && group.workspaces.length === 1) {
    const only = group.workspaces[0];
    return only === undefined ? null : (
      <div {...stylex.props(styles.group)}>
        <Row
          workspace={only}
          facts={factsFor(facts, only)}
          title={group.title}
          selected={selected}
          at={at}
          onSelect={onSelect}
          onOpen={onOpen}
          thread={group.thread}
          onThreadsChanged={onThreadsChanged}
        />
      </div>
    );
  }

  // ── every heading folds, and it is the same control ──────────────────────
  //
  // Only the loose group used to, on the argument that a thread is small and
  // is the point. That was true while a thread held one workspace — and it is
  // the *other* rule above, not this one, that was carrying it: a one-workspace
  // thread draws no heading, so there was never anything to fold.
  //
  // A thread across three repositories is three two-line rows under a heading,
  // and two of those fill the column. So the fold appears exactly where the
  // heading does, which is exactly where there is more than one thing behind
  // it.
  //
  // What stays different is the default, not the control: the loose group is
  // the archive and is shut until opened, where a thread is the work and is
  // open until somebody puts it away. See `rememberedFolded`.
  const loose = group.thread === undefined;

  // A button, because it does something. A menu trigger is a button too, and a
  // button cannot be nested inside one — which is why the heading row holds
  // two controls rather than being one.
  const fold = (
    <button
      type="button"
      aria-expanded={!folded}
      onClick={onFold}
      {...stylex.props(styles.heading, styles.headingButton, styles.grow)}
    >
      {/* ── one caret, rotated, rather than two glyphs swapped ───────────

            It was `▸` and `▾`, which are two problems. They are *characters*,
            so their size and weight are the text face's rather than this
            control's — measured against a 13px `text.small` they came out
            noticeably lighter and smaller than every other mark in the
            column, and there is no size at which a geometric shape from a
            prose font matches an icon set drawn for the purpose.

            And swapping one character for another is a pop. A disclosure's
            natural motion is the caret turning, which says the same thing the
            two glyphs said and says it *continuously* — so it also reads
            during the fold rather than only at its ends. */}
      <motion.span
        aria-hidden
        {...stylex.props(styles.caret)}
        animate={{ rotate: folded ? 0 : 90 }}
        initial={false}
        transition={folding}
      >
        <CaretRightIcon size={14} weight="bold" />
      </motion.span>
      <span
        onDoubleClick={(event) => {
          // The fold has already had this row's single clicks, which is fine:
          // two of them are a fold and an unfold, so the column is where it
          // was. What must not also happen is a third from the double.
          event.stopPropagation();
          if (group.thread !== undefined) {
            setRenaming(true);
          }
        }}
        {...stylex.props(typeset.subhead, styles.threadName, loose && styles.loose)}
      >
        {group.title}
      </span>
      <span {...stylex.props(styles.count)}>{group.workspaces.length}</span>
    </button>
  );

  // ── the heading IS the menu's area ───────────────────────────────────────
  //
  // Right-clicking a thread opens what its ⋯ opens, so the menu owns the row
  // rather than sitting in it — `ThreadMenu` renders the heading element
  // itself and takes the style it would have had. A wrapper would put a div
  // between the row and the column's flex.
  //
  // Only a real thread has one: the derived group the sidebar makes for
  // workspaces nobody has claimed has nothing to archive, nothing to add a
  // project to, and no id to link to.
  const heading =
    renaming && group.thread !== undefined ? (
      <div {...stylex.props(styles.headingRow)}>
        <Rename thread={group.thread.id} title={group.title} onDone={() => setRenaming(false)} />
      </div>
    ) : group.thread === undefined ? (
      <div {...stylex.props(styles.headingRow)}>{fold}</div>
    ) : (
      <RightClick
        items={menu.items}
        onOpen={menu.onOpen}
        style={[styles.headingRow]}
        onPointerEnter={() => setHovered(true)}
        onPointerLeave={() => setHovered(false)}
      >
        {fold}
        <More
          label={`more for ${group.title}`}
          shown={hovered}
          items={menu.items}
          onOpen={menu.onOpen}
        />
      </RightClick>
    );

  return (
    <div {...stylex.props(styles.group)}>
      {heading}
      {/* ── it folds, rather than disappearing ──────────────────────────────

          Nothing pops: the window's mandate. A conditional render has nothing
          to transition — a component that is not in the tree cannot animate —
          so the body is kept mounted through the fold and its *height* is
          what moves, which is the one property that can carry a list of rows
          collapsing.

          `overflow: hidden` on the folding box is what makes a height
          animation possible at all; without it the rows are drawn over the
          group below for the length of the transition. The vertical padding
          is on the rows rather than here for the same reason: padding on a box
          animating to zero leaves a gap that never closes. */}
      <AnimatePresence initial={false}>
        {!folded && (
          <motion.div
            key="body"
            {...stylex.props(styles.folding)}
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={folding}
          >
            {/* Said rather than left blank. A thread with nothing in it is the row
          waiting to be filled, and an empty space under a heading reads as a
          rendering fault. */}
            {group.workspaces.length === 0 && (
              <div {...stylex.props(styles.empty, styles.nested)}>nothing yet</div>
            )}

            {group.workspaces.map((workspace) => (
              <div key={workspace.key} {...stylex.props(styles.nested)}>
                <Row
                  workspace={workspace}
                  facts={factsFor(facts, workspace)}
                  // No title: the heading above already says it, and repeating
                  // it on every child would name the group four times.
                  title={undefined}
                  selected={selected}
                  at={at}
                  onSelect={onSelect}
                  onOpen={onOpen}
                  thread={group.thread}
                  onThreadsChanged={onThreadsChanged}
                />
              </div>
            ))}
          </motion.div>
        )}
      </AnimatePresence>
      {menu.dialogs}
    </div>
  );
}

export function Sidebar({
  sessions,
  facts,
  threads,
  selected,
  at,
  onSelect,
  onOpen,
  onThreadsChanged,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  /** What each workspace's agent is doing. See useFacts. */
  readonly facts: Facts;
  readonly threads: ReadonlyArray<Thread>;
  readonly selected: string | undefined;
  /** The workspace the window is looking at. See Row's own. */
  readonly at: { readonly project: string; readonly workspace: string } | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  /** Open a workspace that has no session. See Row's own. */
  readonly onOpen: (project: string, workspace: string) => void;
  /** Open the new-thread modal. The window owns it — see App.tsx. */
  /** A workspace changed threads, so the list App holds is out of date. */
  readonly onThreadsChanged: () => void;
  readonly failure: string | undefined;
}) {
  // What orders the column: when each checkout was last worked in. The same
  // table the dots are read from, so there is no second source to disagree
  // with them — see `activeAt` in workspaces.ts for why a thread's own
  // `createdAt` is the wrong field to sort on.
  const when = useMemo(
    () =>
      new Map(
        [...facts.values()].flatMap((one) =>
          one.lastActiveAt === undefined
            ? []
            : [[factsKey(one.project, one.workspace), one.lastActiveAt.getTime()] as const],
        ),
      ),
    [facts],
  );
  const groups = groupByThread(
    threads,
    groupByWorkspace(sessions),
    (workspace) => factsFor(facts, workspace)?.status,
    when,
  );
  // Whether the one group there is draws a heading — see `headingless`.
  const bare = headingless(groups);
  const [looseOpen, setLooseOpen] = useState(rememberedLooseOpen);

  // ── the folded threads, by id ────────────────────────────────────────────
  //
  // A set of what is *shut*, so a thread nobody has touched is open and a
  // thread this window has never seen needs no entry. The loose group keeps
  // its own boolean beside this rather than joining it: it has no id, and its
  // default is the opposite. Two things with two defaults are two values.
  const [folded, setFolded] = useState(rememberedFolded);
  const toggleFold = (id: string) => () => {
    setFolded((was) => {
      const next = new Set(was);
      if (!next.delete(id)) {
        next.add(id);
      }
      rememberFolded(next);
      return next;
    });
  };

  // The two states of the column, chosen before the markup rather than inside
  // it. A daemon that is not running is the ordinary case during development,
  // so it gets a sentence and the command, not an empty list.
  const body =
    failure === undefined ? (
      <>
        {groups.length === 0 && <div {...stylex.props(styles.empty)}>no workspaces</div>}
        {groups.map((group) => {
          const id = group.thread?.id;
          const isLoose = id === undefined;
          return (
            <Group
              key={group.key}
              group={group}
              facts={facts}
              selected={selected}
              at={at}
              onSelect={onSelect}
              onOpen={onOpen}
              onThreadsChanged={onThreadsChanged}
              alone={bare}
              // Never folded when it is the only group: its heading is not
              // drawn, so there would be no control left to open it with.
              folded={isLoose ? !bare && !looseOpen : folded.has(id)}
              onFold={
                isLoose
                  ? () =>
                      setLooseOpen((open) => {
                        rememberLooseOpen(!open);
                        return !open;
                      })
                  : toggleFold(id)
              }
            />
          );
        })}
      </>
    ) : (
      <div {...stylex.props(styles.failure)}>
        <div {...stylex.props(styles.head)}>no daemon</div>
        <div {...stylex.props(styles.quiet)}>{failure}</div>
        <div {...stylex.props(styles.quiet, styles.gap)}>
          start it with <code>bun run daemon</code>
        </div>
      </div>
    );

  return (
    <div {...stylex.props(styles.column)}>
      <div {...stylex.props(styles.list)}>{body}</div>
    </div>
  );
}
