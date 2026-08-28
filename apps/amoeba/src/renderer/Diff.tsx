import type { CommentSide, ReviewComment, Revision } from "@awp-kit/protocol";
import { CaretDownIcon } from "@phosphor-icons/react/CaretDown";
import { CaretRightIcon } from "@phosphor-icons/react/CaretRight";
import { ArrowsInLineVerticalIcon } from "@phosphor-icons/react/ArrowsInLineVertical";
import { ArrowsOutLineVerticalIcon } from "@phosphor-icons/react/ArrowsOutLineVertical";
import { CaretUpIcon } from "@phosphor-icons/react/CaretUp";
import { CodeView, type CodeViewItem } from "@pierre/diffs/react";
import type { CodeViewLineSelection, DiffLineAnnotation, SelectedLineRange } from "@pierre/diffs";
import * as stylex from "@stylexjs/stylex";
import { parsePatchFiles } from "@pierre/diffs";
import { useCallback, useEffect, useRef, useState } from "react";
import { listRevisions, readDiff, watchWorkspace } from "./daemon";
import { THEME } from "./highlighting";
import { contentOf, statOf, subjectOf, versionOf } from "./patch";
import {
  DEFAULT_SPLIT,
  rememberSplit,
  rememberViewed,
  rememberedSplit,
  rememberedViewed,
} from "./remembered";
import { useReview } from "./review";
import type { ColorScheme } from "@awp-kit/pane";
import { colors, text } from "./tokens.stylex";

// What the workspace on screen has changed, and what a person has to say about
// it.
//
// ── two questions, not one ─────────────────────────────────────────────────
//
//   revisions   the working copy, and every commit back to the main line
//   diff        the patch for whichever of those is selected
//
// The list is the commit-by-commit view. It is not a separate mode with a
// switch beside it, because a mode is a thing to be in and this is a thing to
// point at: the working copy is one row among the commits, sitting where it
// belongs — at the top, above the commit it is built on.
//
// ── the row at the top is not the same as the commit it names ──────────────
//
// This is the one thing in this file worth reading twice. jj's working copy is
// a real commit with a real change id, so the top row *could* ask for its diff
// by name — and doing so would be wrong. Only a request that names no revision
// snapshots the files on disk first, and without that snapshot a workspace
// where an agent has written six files and run no jj command diffs as empty.
// That is precisely the state this panel exists to show, so the top row asks
// for `undefined` and every other row asks by change id. See `Diff` in the
// contract, and `DiffOf.snapshot` in the daemon's jj service.
//
// ── two questions means two requests ───────────────────────────────────────
//
// They used to go out together, and clicking a revision therefore re-fetched
// the list of revisions it was clicked in. That list is a property of the
// *workspace*: picking a different row out of it cannot change it. Measured at
// 40ms a call against a real repository — small, and paid on every click for an
// answer already on screen. Now `askRevisions` follows the directory and
// `askPatch` follows the pair, which is what each one is actually about.
//
// The rest of what "clicking a revision is slow" was is not here at all: it was
// every file being tokenized on the main thread, because nothing had put a
// worker pool in the tree. See highlighting.tsx.
//
// ── nothing polls, and nothing has to be pressed ──────────────────────────
//
// A timer here would snapshot someone's working copy every few seconds, which
// writes an operation to the repository they are standing in — for a panel that
// may be sitting behind another tab. So the daemon watches the files instead
// and says when they change, and this asks then.
//
// Three moments, and each is one where the answer plausibly differs: a tick
// from the watcher, opening the tab (Base UI unmounts a hidden panel, so
// showing it remounts this), and the window regaining focus — which covers the
// stretch when the daemon was not running to watch anything.
//
// There used to be a refresh button. A control whose whole meaning is "what is
// on screen may be a lie" is an admission rather than a feature, and it was the
// one thing in this panel a person had to remember to do.
//
// ── @pierre/diffs, and which of its components ─────────────────────────────
//
// `CodeView` rather than `PatchDiff`, and not as a preference: `PatchDiff`
// throws outright on a patch containing more than one file — `getSingularPatch`
// is exactly what its name says. A commit touches several files roughly always.

/** The row for the working copy, which is not addressed by change id. */
const WORKING_COPY = "@";

/** One empty list, shared. See where it is used. */
const NO_FOLDS: ReadonlyArray<string> = [];

/**
 * A revision nothing is ever at.
 *
 * `undefined` cannot serve as "not yet decided" in this panel, because it
 * already means the working copy — see the fold state below.
 */
const NEVER = "";

/** How short the revision list is allowed to be dragged before it collapses. */
const MIN_SPLIT = 44;

/**
 * The collapsed height, when the list's own rows cannot be measured.
 *
 * Collapsing used to mean `display: none`, and what that lost is the answer to
 * "which revision am I looking at" — the one thing the list is for when it is
 * not being browsed. So it collapses to a single row instead, and that row is
 * scrolled to the selected one.
 *
 * Measured from the first row rather than computed from the tokens, because
 * the height is the line box plus padding and both move with the type scale.
 * This number is only ever used before there is a row to measure.
 */
const ROW_FALLBACK = 24;

/**
 * What the working copy's row is called in the DOM.
 *
 * It addresses itself as `undefined` everywhere else in this panel — see the
 * note at the top — and an empty attribute value cannot be selected for, so
 * the one place that needs to find it by name gives it one.
 */
const WORKING_COPY_ROW = "working-copy";

/**
 * Injected into the renderer's shadow root, because that is where the gutter is.
 *
 * StyleX cannot reach it — a shadow root is exactly what a stylesheet does not
 * cross, which is the property that makes the diff renderer's own styling safe
 * to live beside this app's. `unsafeCSS` is the library's own door through it,
 * and this is the only thing put through: a cursor, so the one part of a line
 * that can be grabbed says so.
 */
const GUTTER_CSS = `
[data-column-number] { cursor: pointer; }

/* The same scrollbars as the rest of the window — see global.css, which cannot
   reach in here. A shadow root is exactly what a stylesheet does not cross, so
   the diff's own scrollport would otherwise be the one place in the window
   still wearing the system's. Kept as a copy rather than imported, because the
   only way in is a string. */
::-webkit-scrollbar { width: 11px; height: 11px; }
::-webkit-scrollbar-track, ::-webkit-scrollbar-corner { background: transparent; }
::-webkit-scrollbar-thumb {
  background-color: light-dark(rgba(0, 0, 0, 0.22), rgba(255, 255, 255, 0.16));
  background-clip: padding-box;
  border: 3px solid transparent;
  border-radius: 8px;
}
::-webkit-scrollbar-thumb:hover {
  background-color: light-dark(rgba(0, 0, 0, 0.38), rgba(255, 255, 255, 0.3));
}
::-webkit-scrollbar-thumb:vertical { min-height: 28px; }
`;

/**
 * What a line annotation carries: an existing comment, or the box to write one.
 *
 * One type for both, because both are drawn by `renderAnnotation` and the
 * library gives an item exactly one annotation renderer. A separate mechanism
 * for the composer would mean a second thing that has to know how to position
 * itself against a diff line, which is the whole problem the annotation slot
 * already solves.
 */
type Note =
  | { readonly kind: "comment"; readonly comment: ReviewComment }
  // The draft carries its own span rather than the renderer reading it back off
  // the selection. This closure is handed to the library and called later, so
  // `selection` inside it is whatever it was when the closure was made — and
  // TypeScript says as much, refusing to narrow it out of `null`. Putting the
  // answer on the annotation makes the value travel with the thing it describes.
  | { readonly kind: "draft"; readonly line: number; readonly endLine: number };

/**
 * A selection, as the side and the two line numbers a comment is filed under.
 *
 * The one subtlety, and it is the reason this is a function rather than three
 * property reads: **a range whose ends are on different sides is not a range.**
 * A unified diff numbers each side separately, so a drag from a removed line to
 * an added one reports `start` counted in the old file and `end` counted in the
 * new one. Storing that as 12–40 would be a span over two different files.
 *
 * The end wins, because the end is where the pointer finished and where the
 * composer appears. A crossing selection therefore comments on one line, which
 * is honest, rather than on a block that does not exist.
 */
const spanOf = (
  range: SelectedLineRange,
): { readonly side: CommentSide; readonly line: number; readonly endLine: number } => {
  const side = range.endSide ?? range.side ?? "additions";
  const crossed = (range.side ?? side) !== side;
  const first = crossed ? range.end : Math.min(range.start, range.end);
  const last = crossed ? range.end : Math.max(range.start, range.end);
  return { side, line: first, endLine: last };
};

const styles = stylex.create({
  panel: {
    display: "flex",
    flexDirection: "column",
    height: "100%",
    minHeight: 0,
    overflow: "hidden",
  },

  // ── the revisions ────────────────────────────────────────────────────────
  //
  // A height in pixels, dragged and remembered, rather than the fixed 30% this
  // replaced — and the reason the fixed split existed is still true and still
  // honoured. What it was fixing was a boundary that *moved on its own*:
  // sizing the list to its contents meant selecting a revision could change how
  // many rows were listed, so the line the eye uses to separate "which commit"
  // from "what changed" jumped while being looked at.
  //
  // A dragged boundary moves only when someone moves it, which is the opposite
  // property. What 30% cost was the case it could not express: three revisions
  // and most of the band empty, or fifty and a diff peering out from under
  // them.
  //
  // `maxHeight` and not `flexShrink`, because the point is a boundary that
  // stays put. Shrinking would let a short window quietly renegotiate it and
  // then leave it there. A cap is a rule about the window, not about the drag.
  revisions: (height: number) => ({
    flexGrow: 0,
    flexShrink: 0,
    flexBasis: `${height}px`,
    maxHeight: "60%",
    minHeight: 0,
    overflowY: "auto",
    overflowX: "hidden",
  }),
  // No `display: none` any more. A collapsed list is one row tall and still
  // shows which revision is selected; hiding it outright answered the question
  // it exists to answer with nothing.
  shutOverflow: { overflowY: "hidden" },
  revision: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.4rem",
    width: "100%",
    padding: "0.2rem 0.6rem",
    backgroundColor: {
      default: "transparent",
      ":hover": colors.border,
    },
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    textAlign: "start",
    cursor: "pointer",
  },
  on: { backgroundColor: colors.border, color: colors.text },
  // The change id is a fixed-width address and the subject is prose. Giving
  // the room to the subject is the same decision the jobs panel makes about a
  // job's title, and for the same reason: it is the field that cannot be
  // reconstructed from anything else on the row.
  id: { flexShrink: 0, opacity: 0.7 },
  subject: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  mark: { flexShrink: 0, color: colors.live },
  // ── a bookmark on a revision row ─────────────────────────────────────────
  //
  // The accent, and monospace, because a bookmark is an address: it is the
  // string somebody types at jj, and the family is what says so. One of the
  // few places the accent is spent — see the note in AGENTS.md — and it earns
  // it the same way the pull request number does, by pointing at something
  // outside this panel.
  //
  // `flexShrink: 0` against the subject's `flex: 1`, so a long commit message
  // is what gets truncated. The bookmark is the shorter and the more findable
  // of the two, and half a bookmark is not an address.
  bookmark: {
    flexShrink: 0,
    maxWidth: "12rem",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.accent,
    fontFamily: text.mono,
    fontSize: text.small,
  },

  // ── the boundary between the two ─────────────────────────────────────────
  //
  // The same shape as the column dividers — a thin rule with a hit area over
  // it — turned ninety degrees. Not the same component: `Divider` places its
  // handle against the titlebar and speaks in the vocabulary of a column that
  // folds a whole side of the window away. What is shared is the idea, which is
  // cheaper to restate in twenty lines than to generalise into a prop.
  //
  // The caret used to live *inside* this bar, and it was in the wrong place:
  // a button drawn over a `row-resize` strip has to fight the strip's own
  // gesture for the same pixels, and it had to stop the pointerdown to do it.
  // It is now in the head row below, where the rest of the panel's controls
  // are, and this bar does one thing.
  // Space, and no rule at all.
  //
  // This was a 14px band of `colors.base` with a rule under it, then a rule
  // with 7px of space above it — and the second was reported the same way as
  // the first: "borders and then spacing outside the borders, which is weird".
  // It is, and it is the general fault: a line *and* a gap doing one job,
  // where the gap alone would have done it.
  //
  // So the boundary between the list and the head is now empty. What separates
  // them is that the head is filled — see `head` — and this is the room around
  // it. Dragging is still here; it just draws nothing until it is being used.
  splitter: {
    position: "relative",
    flexShrink: 0,
    height: "0.45rem",
    backgroundColor: "transparent",
    cursor: "row-resize",
    // The gesture is captured here, so it must not also read as a page scroll.
    touchAction: "none",
  },
  // Only while it is being dragged. A boundary that is invisible at rest has
  // to appear under the hand, or there is no feedback that the drag started.
  held: { backgroundColor: colors.border },
  // Sized to match the icon buttons at the other end of the head row, so the
  // two ends of the row read as one set of controls rather than as a caret
  // that happens to be nearby.
  peg: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
    // The same box as `icon` at the other end of the row. Without the match
    // the row has a 1.1rem control at one end and a 1.35rem one at the other,
    // and `alignItems: center` then centres two different heights — which is
    // the misalignment, and it is invisible until the two are compared.
    width: "1.5rem",
    height: "1.35rem",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: {
      default: colors.muted,
      ":hover": colors.text,
    },
    cursor: "pointer",
  },

  // ── the header over the patch ────────────────────────────────────────────
  head: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    flexShrink: 0,
    // A floor rather than only padding, so the row is the same height whether
    // it is carrying buttons or a sentence. Without it the panel shifted every
    // time a patch went from "no changes" to a stat, which reads as the layout
    // twitching rather than as content arriving.
    minHeight: "1.75rem",
    // Symmetric, and no vertical padding at all — the floor above sets the
    // height and `alignItems: center` places the content in it. The asymmetric
    // version this replaced pulled the caret four pixels closer to the left
    // edge than the buttons were to the right, which is small enough to read
    // as the whole row being off rather than as one control being wrong.
    paddingBlock: 0,
    paddingInline: "0.35rem",
    // **Filled, not ruled.** Two rules were tried here first — one above and
    // one below — and both were wrong for the same reason: the row is a band
    // between two scrolling things, and a band is a shape, not a pair of
    // lines. One step off the window's base is enough to say so, and it says
    // it on all four sides at once instead of on two.
    //
    // It also does the job a rule was there for. The patch scrolls under this
    // row, and what stops the code appearing to be part of it is that the row
    // is a different colour, which stays true while it moves.
    backgroundColor: colors.surface,
    color: colors.muted,
    fontSize: text.small,
  },
  /** What the head says when there is no stat to show. */
  headSaid: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  // `overflow: hidden` and not just `minWidth: 0`. A flex item will not shrink
  // below its content, so `flex: 1` alone lets "17 files +1348 −171" push the
  // send button off its own row — which is what it did, with the numbers ending
  // up underneath the button. The stat is the part that can be clipped: the
  // button is a control and the count is a detail.
  stat: {
    display: "flex",
    gap: "0.35rem",
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    whiteSpace: "nowrap",
  },
  statPart: { flexShrink: 0 },
  // Green and red, from the two signal colours the palette already has rather
  // than two new ones. `live` and `warn` mean "went well" and "wants your
  // attention" everywhere else in this window, which is close enough to
  // added/removed that a third pair would be three names for two colours.
  added: { color: colors.live },
  removed: { color: colors.warn },
  button: {
    flexShrink: 0,
    padding: "0.1rem 0.4rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  // A button whose whole content is a glyph. Two arrows meeting, and two
  // parting: the pair says fold and unfold without a word, and the words were
  // costing more room in this bar than they were worth. `title` and
  // `aria-label` keep the sentence for anyone who wants it.
  icon: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.5rem",
    height: "1.35rem",
    padding: 0,
  },
  // The one button here that does something outward-facing. It is the only
  // control in the panel that another process finds out about.
  send: { borderColor: colors.live, color: colors.live },
  busy: { opacity: 0.5, cursor: "default" },

  // ── where the scrollbar goes, and why it is not obvious ─────────────────
  //
  // This said "CodeView scrolls itself" and left the overflow off. It does
  // not, quite: it scrolls **its own root** — the element this file hands it —
  // and it is the caller's job to make that element a scrollport. Its
  // constructor is unambiguous once read rather than assumed:
  //
  //   this.root.addEventListener("scroll", this.handleScroll, …)
  //
  // Without `overflowY` on that root there is no scroll event, so nothing
  // scrolls *and* the virtualizer never advances its window — one missing
  // declaration costing both. Measured before the fix, on a ten-file patch:
  //
  //   Diff__panel     overflow hidden    h=727   sh=19220   ← clipped 18k px
  //   Diff__patch     overflow visible   h=443   sh=18936
  //   Diff__view      overflow visible   h=443   sh=18928   ← the root
  //
  // `patch` only has to give it a bounded height to scroll inside.
  // `minHeight: 0` is what makes "bounded" true in a flex column — without it
  // the box grows to its content and the clipping moves up a level rather
  // than going away.
  patch: { flex: 1, minHeight: 0 },
  view: { height: "100%", overflowY: "auto" },

  // ── the file header, made into a control ────────────────────────────────
  //
  // `CodeViewItem.collapsed` is a field on the item, not a behaviour: the
  // library folds a file when told to and offers no header click of its own.
  // So the toggle is ours, rendered into the header through
  // `renderHeaderPrefix`, and the state lives here.
  //
  // An icon, not a text glyph, and that is the fix for a complaint made twice.
  // `fontSize` was raised once and the caret stayed tiny, which is the tell
  // that font size was never what governed it: `▸` is drawn by whatever font
  // the library's own header CSS resolves inside its shadow root, and a
  // triangle in a text font has no stems or counters to carry it at small
  // sizes — it is a few pixels of ink whatever the em box says. An SVG with an
  // explicit pixel size is not a glyph and cannot be re-sized by inheriting
  // anything.
  fold: {
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
  },

  // ── the viewed mark ─────────────────────────────────────────────────────
  //
  // A real `<input type="checkbox">` with a `<label>` around it, not a styled
  // button pretending. It is a checkbox in every way that matters — it is
  // tabbable, space toggles it, a screen reader calls it one, and the state is
  // announced — and every bit of that would have to be rebuilt by hand for a
  // div. The same argument Base UI is a dependency for.
  //
  // The label is the hit area, and it is bigger than the box inside it: a
  // 13px checkbox is a 13px target, and this one sits at the end of a header
  // row that is otherwise all text. The word "viewed" beside it was doing the
  // same job less well — it made the target wide and told a reader what a
  // checkbox already says. `aria-label` keeps the sentence where it is needed.
  viewed: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.75rem",
    height: "1.75rem",
    cursor: "pointer",
    userSelect: "none",
  },
  // Scaled rather than rebuilt. `transform` and not `width`, because a native
  // checkbox draws its tick at its own size and a widened one is a rectangle
  // with a small tick in it — the same reason the caret became an icon.
  box: {
    margin: 0,
    transform: "scale(1.5)",
    accentColor: colors.live,
    cursor: "pointer",
  },

  // ── a comment, and the box that writes one ──────────────────────────────
  note: {
    display: "flex",
    flexDirection: "column",
    gap: "0.25rem",
    margin: "0.15rem 0.4rem",
    padding: "0.35rem 0.5rem",
    backgroundColor: colors.base,
    borderLeftWidth: 2,
    borderLeftStyle: "solid",
    borderLeftColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    fontSize: text.small,
  },
  draft: { borderLeftColor: colors.live },
  noteRow: { display: "flex", alignItems: "baseline", gap: "0.4rem" },
  noteBody: { flex: 1, minWidth: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" },
  noteWhen: { flexShrink: 0, color: colors.muted, fontSize: text.small },
  noteWhere: {
    flexShrink: 0,
    color: colors.muted,
    fontSize: text.small,
    fontVariantNumeric: "tabular-nums",
  },
  write: {
    width: "100%",
    minHeight: "3.5rem",
    padding: "0.3rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    resize: "vertical",
  },
  hint: { color: colors.muted, fontSize: text.small },

  said: { padding: "0.5rem 0.6rem", color: colors.muted, fontSize: text.small },
  warn: { color: colors.warn },
});

export function Diff({
  dir,
  project,
  workspace,
  scheme,
}: {
  /** A directory in the workspace — a session's `startDir`. */
  readonly dir: string | undefined;
  /** The workspace's identity, or absent for a session that is not one of ours. */
  readonly project: string | undefined;
  readonly workspace: string | undefined;
  readonly scheme: ColorScheme;
}) {
  const [revisions, setRevisions] = useState<ReadonlyArray<Revision>>([]);
  // The change id of the commit being shown, or absent for the working copy.
  // Absent is the initial state and the one the panel returns to whenever the
  // workspace changes, because the working copy is the row someone opened this
  // to look at.
  const [at, setAt] = useState<string | undefined>();
  const [patch, setPatch] = useState<string | undefined>();
  const [failure, setFailure] = useState<string | undefined>();

  // ── the workspace changed under the panel ────────────────────────────────
  // Derived state, and the reason it is not an effect that calls setAt: the
  // selection belongs to a directory, so it is reset by *rendering a different
  // one*, not by noticing afterwards that one arrived. React's own name for
  // this is adjusting state during render.
  const [shownFor, setShownFor] = useState(dir);
  if (shownFor !== dir) {
    setShownFor(dir);
    setAt(undefined);
    setPatch(undefined);
    setFailure(undefined);
  }

  // Which patch request the answer on screen belongs to. A ref rather than a
  // flag closed over by each effect, because asking is not only something an
  // effect does — the watcher and the focus listener call the same function,
  // and a reply that arrives after a newer request has gone out must lose in
  // every one of those cases alike.
  const newest = useRef(0);

  // The revision on screen, readable from a callback that outlives the render
  // that made it. The watcher below is opened once per directory and must keep
  // asking about whatever row is selected *now*, not the row that was selected
  // when the subscription was made.
  //
  // Written from an effect rather than during render. A ref assignment in the
  // render body is a write React cannot see, and the compiler says so; here it
  // would also be a write that a discarded render could leave behind.
  const latest = useRef<string | undefined>(at);
  useEffect(() => {
    latest.current = at;
  }, [at]);

  const askRevisions = useCallback((where: string) => {
    listRevisions(where)
      .then(setRevisions)
      .catch(() => {
        // Silent, deliberately. The diff below has its own failure and it is
        // the one worth showing; two messages about the same unreachable
        // daemon is one more than says anything.
        setRevisions([]);
      });
  }, []);

  const askPatch = useCallback((where: string, revision: string | undefined) => {
    newest.current += 1;
    const mine = newest.current;

    readDiff(where, revision)
      .then((answer) => {
        // The revision is checked as well as the counter. The reply carries
        // what it was for precisely so a stale one can be told apart from a
        // current one, and the two guards answer different questions: the
        // counter is "is this request still wanted", the revision is "is this
        // the row on screen".
        if (mine === newest.current && answer.revision === (revision ?? WORKING_COPY)) {
          setPatch(answer.patch);
          // Cleared here rather than when the request went out, so a panel
          // showing an error keeps showing it until there is something to put
          // in its place.
          setFailure(undefined);
        }
      })
      .catch((error: unknown) => {
        if (mine === newest.current) {
          setPatch(undefined);
          setFailure(error instanceof Error ? error.message : String(error));
        }
      });
  }, []);

  useEffect(() => {
    if (dir !== undefined) {
      askRevisions(dir);
    }
  }, [dir, askRevisions]);

  useEffect(() => {
    if (dir !== undefined) {
      askPatch(dir, at);
    }
  }, [dir, at, askPatch]);

  // ── the daemon says when, so nobody has to ask ──────────────────────────
  //
  // A tick per burst of writes under the workspace. Both questions are asked on
  // one, and not only the patch: a commit made in the terminal beside this
  // panel changes the *list*, and the files it touched are what says so.
  //
  // This is what the refresh button used to be for. It is gone — a control
  // whose whole meaning is "the thing on screen may be a lie" is an admission
  // rather than a feature, and it was the one thing here a person had to
  // remember to do.
  //
  // The subscription follows `dir` and not `at`, so changing revision does not
  // tear down a watch and open another. `askPatch` is read through a ref for
  // the same reason — see `again`.
  useEffect(() => {
    if (dir === undefined) {
      return;
    }
    return watchWorkspace(dir, () => {
      askRevisions(dir);
      askPatch(dir, latest.current);
    });
  }, [dir, askRevisions, askPatch]);

  // Coming back to the window is still worth asking on, and costs nothing to
  // detect. The watcher covers what happens *here*; this covers what happened
  // while the daemon was not running, or in a repository operation that touched
  // no file this window is watching.
  useEffect(() => {
    if (dir === undefined) {
      return;
    }
    const again = () => {
      askRevisions(dir);
      askPatch(dir, at);
    };
    window.addEventListener("focus", again);
    return () => window.removeEventListener("focus", again);
  }, [dir, at, askRevisions, askPatch]);

  const review = useReview(project, workspace);

  // ── the boundary between the list and the patch ─────────────────────────
  const [split, setSplit] = useState(rememberedSplit);
  const [dragging, setDragging] = useState(false);
  const panel = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);

  /**
   * One row, measured off the list itself.
   *
   * The height is a line box plus padding and both move with the type scale, so
   * a constant here is a number that is right until somebody edits a token.
   * `ROW_FALLBACK` covers the render before there is a row to measure.
   */
  const [rowHeight, setRowHeight] = useState(ROW_FALLBACK);
  useEffect(() => {
    // The list is the dependency and it is read, not merely listed: no rows
    // means nothing to measure, and the fallback stands.
    if (revisions.length === 0) {
      return;
    }
    const first = list.current?.firstElementChild;
    if (first === null || first === undefined) {
      return;
    }
    const measured = Math.round(first.getBoundingClientRect().height);
    if (measured > 0) {
      setRowHeight(measured);
    }
  }, [revisions]);

  // **Zero still means collapsed.** Storing the measured row height instead
  // was the first shape and it is worse in a way that only shows up later:
  // `folded` would then be a comparison against a number that moves with the
  // type scale, so a row one pixel taller than the threshold reads as open
  // while looking shut — and the remembered value from an older build would
  // decide it. The stored value keeps meaning "collapsed"; what changed is
  // only what collapsed *looks* like.
  const folded = split === 0;

  const resize = (height: number) => {
    // Dragged past the floor is a collapse, not a two-pixel list. The floor is
    // where the gesture already wanted to go.
    const next = height < MIN_SPLIT ? 0 : Math.round(height);
    setSplit(next);
    rememberSplit(next);
  };

  // Put the selected revision under the one row that is left.
  //
  // `block: "nearest"` and not `"center"`: with a taller list open this must
  // do nothing at all, and centring would yank a list somebody is reading. It
  // fires on the collapse and on a change of selection, which are the only two
  // moments the visible row can be the wrong one.
  //
  // The row is found by its address rather than held in a ref, and that is not
  // a style choice: a ref is not a dependency React can watch, so the effect
  // would have had to list `at` without reading it — which the lint calls an
  // extra dependency and is right to.
  useEffect(() => {
    if (!folded) {
      return;
    }
    list.current
      ?.querySelector<HTMLElement>(`[data-revision="${at ?? WORKING_COPY_ROW}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [folded, at]);

  // ── which files are folded, and which revision that was true of ──────────
  //
  // The revision is stored *with* the set rather than cleared when it changes.
  // Clearing was the first version and is a state write inside an effect —
  // which react-doctor refuses, and rightly: what a stale set means is a
  // question that can be answered during render, and answering it in an effect
  // means one frame drawn with the wrong folds before it is.
  //
  // It has to be answered at all because item ids are paths: two revisions that
  // both touch `Sidebar.tsx` produce the same id, so a set carried across would
  // silently fold a file in the new patch because its namesake was folded in
  // the old one.
  const [folds, setFolds] = useState<{
    readonly at: string | undefined;
    readonly ids: ReadonlyArray<string>;
    // `NEVER` and not `undefined`, and the difference is the whole bug it
    // fixed. `undefined` is not "no revision yet" here — it is the *working
    // copy*, which is the revision the panel opens on. So a freshly mounted
    // panel compared `folds.at === at` as `undefined === undefined`, decided
    // its empty fold list was current, and threw away the seed taken from the
    // viewed marks. What that looked like: tick a file viewed, reload, and the
    // tick came back while the file sat open with every line showing.
    //
    // The empty string is a value no change id and no working copy ever has.
  }>({ at: NEVER, ids: [] });

  // ── which files have been looked at ─────────────────────────────────────
  //
  // Held with the patch it belongs to, exactly as the folds are, and for the
  // same reason: item ids are paths, so a set carried across revisions would
  // mark a file viewed in the new patch because its namesake was viewed in the
  // old one. `for` is the pair the marks were read for; when it disagrees with
  // what is on screen, the answer is re-read rather than corrected in an
  // effect.
  const [viewed, setViewed] = useState<{
    readonly for: string;
    readonly paths: ReadonlyArray<string>;
  }>({ for: "", paths: [] });

  const marksFor =
    project === undefined || workspace === undefined
      ? undefined
      : `${project}/${workspace}/${at ?? WORKING_COPY}`;

  if (marksFor !== undefined && viewed.for !== marksFor) {
    setViewed({
      for: marksFor,
      paths: rememberedViewed(project ?? "", workspace ?? "", at ?? WORKING_COPY),
    });
  }

  const seen = new Set(viewed.for === marksFor ? viewed.paths : []);

  const markViewed = (path: string, yes: boolean) => {
    if (project === undefined || workspace === undefined || marksFor === undefined) {
      return;
    }
    const paths = yes
      ? [...new Set([...viewed.paths, path])]
      : viewed.paths.filter((one) => one !== path);
    setViewed({ for: marksFor, paths });
    rememberViewed(project, workspace, at ?? WORKING_COPY, paths);

    // Marking a file viewed folds it, and unmarking opens it again. That is
    // what the mark is *for*: the reason to say "I have looked at this" is to
    // get it out of the way, and a checkbox that ticks and leaves eight hundred
    // lines on screen has not done the thing it was pressed for.
    setFolds((prev) => {
      const ids = prev.at === at ? prev.ids : shutIds;
      const mine = parsed.filter((one) => one.fileDiff.name === path).map((one) => one.id);
      return {
        at,
        ids: yes ? [...new Set([...ids, ...mine])] : ids.filter((one) => !mine.includes(one)),
      };
    });
  };

  // Where a comment is being written, and what is in the box. One at a time,
  // because the selection it is anchored to is one at a time.
  const [selection, setSelection] = useState<CodeViewLineSelection | null>(null);
  const [writing, setWriting] = useState("");

  // The live one, which is a different thing from the settled one and exists
  // for a reason that is not obvious: **passing `selectedLines` at all is what
  // makes the selection controlled.** The React wrapper reads
  // `controlledSelection = selectedLines !== undefined`, and in controlled mode
  // the renderer stops painting its own highlight — it proposes a range and
  // waits to be told what the answer is.
  //
  // So the first fix left the drag working and invisible. This one is fed every
  // intermediate range, and the composer still opens only on the settled one:
  //
  //   live       every pointermove   →  selectedLines  →  the blue band
  //   selection  pointerup only      →  the annotation →  a new item version
  //
  // Which is the whole trick. A re-render is cheap; a re-render that changes an
  // item's `version` rebuilds its DOM, and that is what a gesture cannot
  // survive.
  const [live, setLive] = useState<CodeViewLineSelection | null>(null);

  // ── the composer waits for the gesture to finish ─────────────────────────
  //
  // A drag down the number column selected exactly one line, every time, and
  // the fault is here rather than in the library. The composer is an annotation
  // on the item; an item that gains one gets a new `version`; a new version
  // rebuilds that item's DOM. So the gesture destroyed the thing it was
  // tracking, one move in:
  //
  //   pointerdown line 4   selection 4–4  →  composer  →  the item rebuilds
  //   pointermove line 9   nothing left to track
  //   pointerup            "line 4"
  //
  // None of which reads as a bug in the drag. Shift-click worked throughout,
  // because its two halves are separate gestures with a settled render between
  // them — which is exactly what made this look like the library lacking
  // multi-line support rather than us tearing it down.
  //
  //   drag the numbers    line 4      ← before
  //   click, shift-click  lines 4–9
  //   drag the +          lines 4–5   ← the rebuild caught it two lines in
  //
  // So the selection held here is the *settled* one, taken from
  // `onLineSelectionEnd` and from nowhere else. `onSelectedLinesChange` cannot
  // be the source: it fires at pointerdown, before the gesture has said what it
  // is, and a flag raised in `onLineSelectionStart` is raised one call too late
  // — the library's wrapper calls `onSelectedLinesChange` *first* and the
  // bracket callback after it. That was the first fix and it changed nothing,
  // which is the useful part of the finding.
  //
  // The drag renders, then — it has to, or there is no band to see — but only
  // through `live` below, which changes no item's version.

  // Parsed here rather than inside the renderer, because the item list is what
  // CodeView takes and the cache key prefix has to be stable per revision: it
  // is what lets a re-render of the same patch reuse work instead of
  // re-highlighting every file.
  //
  // No `useMemo`, and it used to have one. The viewed marks are read during
  // render — they are derived from the workspace and revision on screen, the
  // same shape the folds use — and a state write during render is something
  // React Compiler cannot see past, so it could no longer prove the memo held
  // and said so:
  //
  //   react(preserve-manual-memoization): Existing memoization could not be
  //   preserved
  //
  // That is the compiler doing its job. It memoizes this on its own, which is
  // what `_c(n)` in the served module is, and react-doctor was already asking
  // for the manual one to go. Two tools agreeing is enough.
  const parsed = ((): ReadonlyArray<{
    readonly id: string;
    readonly type: "diff";
    readonly content: number;
    readonly fileDiff: ReturnType<typeof parsePatchFiles>[number]["files"][number];
  }> => {
    if (patch === undefined || patch === "") {
      return [];
    }
    return parsePatchFiles(patch, at ?? WORKING_COPY).flatMap((one) =>
      one.files.map((fileDiff, index) => {
        // ── the cache key has to move when the file does ────────────────────
        //
        // The renderer decides whether two diffs are the same thing by their
        // `cacheKey` — `areDiffTargetsEqual` is `a === b || a.cacheKey ===
        // b.cacheKey` — and `parsePatchFiles` builds that key from position
        // alone: `<prefix>-<patch index>-<file index>`. The prefix here was the
        // revision, which is correct for a *committed* revision, because that
        // is immutable, and wrong for the working copy, which is not.
        //
        // So a file that changed on disk came back under the key it already
        // had. The worker's cached token stream for the previous content was
        // handed to `processDiffResult` alongside hunks parsed from the new
        // one, and the line arrays were then indexed past their ends:
        //
        //   deletionLines[deletionLine.lineIndex]   undefined
        //   additionLines[additionLine.lineIndex]   undefined
        //   → "deletionLine and additionLine are null, something is wrong"
        //
        // which is thrown, not logged, so the panel went out through its
        // boundary. It needed a *second* patch to happen at all, which is why
        // it never appeared on opening the tab and why it only started once the
        // daemon began pushing patches on its own instead of waiting for a
        // refresh button.
        //
        // Keyed per file rather than per patch on purpose: a change to one file
        // leaves the other nine keys alone, so their highlighting is still
        // reused. A hash of the whole patch would be correct and would
        // re-tokenize every file on every keystroke an agent makes.
        fileDiff.cacheKey = `${at ?? WORKING_COPY}|${index}|${contentOf(fileDiff)}`;

        return {
          // The path and its position, because CodeView keys its items by id
          // and a patch is allowed to carry the same path twice — a file split
          // across two diff entries by a mode change is the ordinary way that
          // happens. The path alone would silently drop the second.
          id: `${fileDiff.name}-${index}`,
          type: "diff" as const,
          // The file's content, because neither the id nor the cache key above
          // reaches the item's DOM cache — `version` is what does. Without it a
          // changed file keeps the rows it already drew.
          content: contentOf(fileDiff),
          fileDiff,
        };
      }),
    );
  })();

  const revision = at ?? WORKING_COPY;
  const here = review.comments.filter((one) => one.revision === revision);

  // `collapsed`, the annotations and `version` folded in together.
  //
  // No `useMemo` around this, unlike `parsed` above, and the difference is real
  // rather than an inconsistency. `parsed` wraps `parsePatchFiles`, which walks
  // the whole patch text; this is one `map` over a list of ten. React Compiler
  // memoizes it either way, and react-doctor refuses manual memoization in code
  // it manages.
  //
  // A Set, not `shutIds.includes`, for the usual reason: one lookup per file
  // against a list is a scan per file.
  // `NO_FOLDS` and not a fresh `[]`: a new array every render is a memo that
  // never hits.
  //
  // ── a fresh patch starts folded where it was already viewed ─────────────
  //
  // The marks survive a reload and the folds do not — folds are this visit,
  // marks are the review. Left alone that produced the one state nobody wants:
  // a file ticked "viewed" sitting open with eight hundred lines under it,
  // which is exactly what ticking it was meant to get rid of.
  //
  // So the fold list is *seeded* from the marks rather than being a second
  // record of them. Once anything is folded or unfolded by hand this render's
  // list takes over, which is what makes unfolding a viewed file possible at
  // all — a rule that forced viewed files shut would have no way back.
  const shutIds =
    folds.at === at
      ? folds.ids
      : parsed.filter((one) => seen.has(one.fileDiff.name)).map((one) => one.id);

  const shut = new Set(shutIds);
  const items: ReadonlyArray<CodeViewItem<Note>> = parsed.map((item) => {
    const off = shut.has(item.id);
    // Annotated, not inferred. Without it the array's element type is fixed by
    // the first `map` — a comment — and pushing the composer onto it is an
    // error about a string literal rather than about what is going on.
    const annotations: Array<DiffLineAnnotation<Note>> = here
      .filter((one) => one.path === item.fileDiff.name)
      .map((comment) => ({
        side: comment.side,
        // Drawn under the LAST line of the range, not the first. An annotation
        // is a block inserted into the flow, so anchoring it at the start would
        // push the rest of what the comment is about below it — the reader
        // would have the remark and then have to scroll to reach what it names.
        lineNumber: comment.endLine,
        metadata: { kind: "comment" as const, comment },
      }));

    // The composer is an annotation like any other, anchored at the end of the
    // selection. `endSide` first because a selection dragged upward reports its
    // two ends in the order they were touched, not in line order.
    if (selection?.id === item.id) {
      const span = spanOf(selection.range);
      annotations.push({
        side: span.side,
        lineNumber: span.endLine,
        metadata: { kind: "draft" as const, line: span.line, endLine: span.endLine },
      });
    }

    return {
      ...item,
      collapsed: off,
      annotations,
      version: versionOf(
        // The viewed mark is in here as well as the fold, even though marking
        // viewed also folds. The cache is keyed on this number, so anything the
        // header draws has to be in the string — and relying on the fold to
        // carry it would make the checkbox go stale the day the two stop moving
        // together, in a way that looks like a lost click rather than a cache.
        `${item.content}|${off ? "shut" : "open"}|${seen.has(item.fileDiff.name) ? "seen" : "new"}|${annotations
          .map((one) =>
            one.metadata.kind === "draft"
              ? `draft:${one.side}:${one.metadata.line}-${one.metadata.endLine}`
              : `${one.metadata.comment.id}:${one.metadata.comment.sentAt === undefined ? "d" : "s"}`,
          )
          .join(",")}`,
      ),
    };
  });

  // Functional, and it re-derives the stale check itself. For the same reason
  // the header reads `item`: this runs from a closure CodeView is holding, so
  // anything it reads from the render that created it may be old.
  const toggle = (id: string) => {
    setFolds((prev) => {
      // `shutIds` and not `NO_FOLDS`, so the first fold of a visit does not
      // silently unfold everything the marks had folded.
      const ids = prev.at === at ? prev.ids : shutIds;
      return { at, ids: ids.includes(id) ? ids.filter((one) => one !== id) : [...ids, id] };
    });
  };

  /** Fold every file, or none. The bar's two buttons. */
  const foldAll = (shutThem: boolean) => {
    setFolds({ at, ids: shutThem ? parsed.map((one) => one.id) : NO_FOLDS });
  };

  const save = () => {
    const body = writing.trim();
    if (selection === null || body === "") {
      return;
    }
    const file = parsed.find((one) => one.id === selection.id);
    if (file === undefined) {
      return;
    }
    review.add({ revision, path: file.fileDiff.name, ...spanOf(selection.range), body });
    setWriting("");
    setSelection(null);
    setLive(null);
  };

  const stat = patch === undefined ? undefined : statOf(patch);

  if (dir === undefined) {
    return <div {...stylex.props(styles.said)}>no workspace open</div>;
  }

  return (
    <div ref={panel} {...stylex.props(styles.panel)}>
      {/* Collapsed is one row tall, not gone. `display: none` was what this
          replaced, and what it lost is the answer to "which revision am I
          looking at" — the one thing the list is still for when nobody is
          browsing it. The row left showing is the selected one; see the effect
          that scrolls to it. */}
      <div
        ref={list}
        {...stylex.props(
          styles.revisions(folded ? rowHeight : split),
          folded && styles.shutOverflow,
        )}
      >
        {revisions.map((one) => {
          // The working copy addresses itself as absent — see the note at the
          // top of this file. Every other row is its change id.
          const value = one.workingCopy ? undefined : one.changeId;
          const selected = value === at;
          return (
            <button
              key={one.changeId}
              type="button"
              // Its address in the list, so the collapse can scroll to it —
              // see the effect above. The working copy addresses itself as
              // absent everywhere else in this panel, and an empty attribute
              // is not selectable, so it gets a name here and only here.
              data-revision={value ?? WORKING_COPY_ROW}
              // What ctrl+j and ctrl+k step through in this column — see
              // navigation.ts. The revision list is the one thing here that is
              // a list; the panel's buttons are a toolbar.
              data-nav-item
              {...stylex.props(styles.revision, selected && styles.on)}
              onClick={() => setAt(value)}
            >
              {one.workingCopy ? (
                <>
                  <span aria-hidden {...stylex.props(styles.mark)}>
                    ●
                  </span>
                  <span {...stylex.props(styles.subject)}>working copy</span>
                </>
              ) : (
                <>
                  <span {...stylex.props(styles.id)}>{one.changeId.slice(0, 8)}</span>
                  <span {...stylex.props(styles.subject)}>{subjectOf(one.description)}</span>
                </>
              )}

              {/* Local bookmarks only, which the daemon has already filtered —
                  a commit's own `json(bookmarks)` carries a *remote* row
                  whenever the remote disagrees, wearing the same name on a
                  different commit. See `BookmarkRefJson.remote`.

                  Drawn after the subject rather than before the id, so the
                  rows still line up on the change id when most of them carry
                  no bookmark at all — which is the ordinary case. */}
              {one.bookmarks.map((name) => (
                <span key={name} title={name} {...stylex.props(styles.bookmark)}>
                  {name}
                </span>
              ))}
            </button>
          );
        })}
      </div>

      {/* The boundary. A separator with a value, because a layout worth an
          assertion is worth announcing — and the accessible name is then also
          what a probe reads to check the drag did what it looked like. */}
      <div
        role="separator"
        aria-label="revision list height"
        aria-orientation="horizontal"
        aria-valuenow={split}
        {...stylex.props(styles.splitter, dragging && styles.held)}
        onPointerDown={(event) => {
          event.currentTarget.setPointerCapture(event.pointerId);
          setDragging(true);
        }}
        onPointerMove={(event) => {
          if (!dragging) {
            return;
          }
          const top = panel.current?.getBoundingClientRect().top ?? 0;
          resize(event.clientY - top);
        }}
        onPointerUp={(event) => {
          event.currentTarget.releasePointerCapture(event.pointerId);
          setDragging(false);
        }}
      />

      {/* ── one row that always says something ────────────────────────────

          The stat, the state sentences and the list's collapse control were
          three separate things in three places, and two of them were only
          sometimes on screen. "reading the diff…" and "no changes" were rows
          of their own that appeared and disappeared, which moved everything
          under them; the caret lived inside the drag bar, where it had to
          fight the bar's own gesture for the same pixels.

          So: one row, always present, always the same height. It carries the
          stat when there is one and the sentence when there is not — they are
          the same slot because they answer the same question, which is what
          this patch is. The controls on the right are for acting on a patch,
          so they are absent when there is no patch to act on. */}
      <div {...stylex.props(styles.head)}>
        <button
          type="button"
          aria-expanded={!folded}
          aria-label={folded ? "show the revision list" : "collapse the revision list"}
          title={folded ? "show the revision list" : "collapse to the selected revision"}
          {...stylex.props(styles.peg)}
          onClick={() => resize(folded ? DEFAULT_SPLIT : 0)}
        >
          {folded ? <CaretDownIcon size={12} /> : <CaretUpIcon size={12} />}
        </button>

        {stat !== undefined && stat.files > 0 ? (
          <span {...stylex.props(styles.stat)}>
            <span {...stylex.props(styles.statPart)}>
              {stat.files === 1 ? "1 file" : `${stat.files} files`}
            </span>
            <span {...stylex.props(styles.statPart, styles.added)}>+{stat.added}</span>
            {/* A real minus sign, U+2212. A hyphen beside a `+` at this size
                is a dash of a different weight and reads as a typo. */}
            <span {...stylex.props(styles.statPart, styles.removed)}>−{stat.removed}</span>
          </span>
        ) : (
          // Every one of these looks like a blank panel, and they are not the
          // same state — which is why each is said out loud rather than left
          // to the absence of a patch. The top of a jj stack is empty most of
          // the day, so "no changes" is the ordinary reading, not a fault.
          <span {...stylex.props(styles.headSaid, failure !== undefined && styles.warn)}>
            {failure ?? (patch === undefined ? "reading the diff…" : "no changes")}
          </span>
        )}

        {review.unsent > 0 && (
          <button
            type="button"
            disabled={review.sending}
            {...stylex.props(styles.button, styles.send, review.sending && styles.busy)}
            onClick={review.send}
          >
            {review.sending
              ? "sending…"
              : `send ${review.unsent} ${review.unsent === 1 ? "comment" : "comments"}`}
          </button>
        )}

        {/* Fold and unfold everything. Two buttons rather than one that
            toggles, because a single control has to decide what "the opposite
            of a patch with four of ten files folded" is — and either answer is
            wrong half the time. Two buttons each state what they do. */}
        {items.length > 1 && (
          <>
            <button
              type="button"
              aria-label="collapse every file"
              title="fold all"
              {...stylex.props(styles.button, styles.icon)}
              onClick={() => foldAll(true)}
            >
              <ArrowsInLineVerticalIcon size={13} weight="bold" />
            </button>
            <button
              type="button"
              aria-label="expand every file"
              title="unfold all"
              {...stylex.props(styles.button, styles.icon)}
              onClick={() => foldAll(false)}
            >
              <ArrowsOutLineVerticalIcon size={13} weight="bold" />
            </button>
          </>
        )}
      </div>

      {review.failure !== undefined && (
        <div {...stylex.props(styles.said, styles.warn)}>{review.failure}</div>
      )}

      <div {...stylex.props(styles.patch)}>
        {items.length > 0 && (
          <CodeView<Note>
            items={items}
            selectedLines={live}
            // Every range the gesture passes through, including the ones it is
            // only passing through. This is what draws the band, and it
            // deliberately does not touch `selection` — see above.
            //
            // It also arrives from somewhere that is not a gesture: CodeView
            // clears the selection itself when the item holding it stops
            // existing. A composer left open over a file no longer in the patch
            // would save a comment onto a line nobody can see, so that case
            // settles immediately.
            onSelectedLinesChange={(next) => {
              setLive(next);
              if (next === null) {
                setSelection(null);
                setWriting("");
              }
            }}
            // The header is the library's; the control in front of it is ours.
            // See `fold` — CodeView folds a file when the item says so and has
            // no click of its own to say it.
            //
            // Read off `item`, never off state closed over here. This closure is
            // handed to CodeView and called back with the *current* item, so the
            // item is the value guaranteed to be fresh.
            //
            // Measured after the change: folding Sidebar.tsx-0 took its button
            // from `collapse`/expanded to `expand`/collapsed, and a second
            // file's header rose into view as the content shrank from 19984px
            // to 13304px — which is also what made the first attempt to measure
            // this read wrong. `locator.first()` re-resolves, so after the fold
            // it was reporting a *different* file's button and looked like a
            // stale label.
            renderHeaderPrefix={(item) => (
              <button
                type="button"
                aria-expanded={item.collapsed !== true}
                aria-label={`${item.collapsed === true ? "expand" : "collapse"} ${item.id}`}
                onClick={() => toggle(item.id)}
                {...stylex.props(styles.fold)}
              >
                {item.collapsed === true ? (
                  <CaretRightIcon size={14} />
                ) : (
                  <CaretDownIcon size={14} />
                )}
              </button>
            )}
            // The right-hand end of the file header, past the +/- counts.
            // `renderHeaderMetadata` rather than a second prefix, because the
            // fold caret is already the prefix and these two controls belong at
            // opposite ends: one says "show me less of this", the other says
            // "I am done with this".
            renderHeaderMetadata={(item) => {
              const path = item.type === "diff" ? item.fileDiff.name : item.id;
              const on = seen.has(path);
              return (
                <label {...stylex.props(styles.viewed)} title="viewed">
                  <input
                    type="checkbox"
                    checked={on}
                    aria-label="viewed"
                    {...stylex.props(styles.box)}
                    onChange={(event) => markViewed(path, event.target.checked)}
                  />
                </label>
              );
            }}
            renderAnnotation={(annotation) => {
              // Lifted out before it is asked about. `annotation` is itself a
              // union of the file and diff shapes, so `annotation.metadata.kind`
              // is not a reference TypeScript will narrow through — the local is.
              const note = annotation.metadata;
              return note.kind === "draft" ? (
                <div {...stylex.props(styles.note, styles.draft)}>
                  <textarea
                    // The one place in this panel where focus has to be moved
                    // rather than offered. The selection was made with a
                    // pointer or with the keyboard, and either way the next
                    // thing anybody wants is to type — an autofocus that has to
                    // be reached for is a box that looks ready and is not.
                    autoFocus
                    value={writing}
                    placeholder="what about this line?"
                    aria-label="comment on the selected line"
                    {...stylex.props(styles.write)}
                    onChange={(event) => setWriting(event.target.value)}
                    onKeyDown={(event) => {
                      // Escape abandons, cmd/ctrl+enter keeps. A bare enter is
                      // a newline, because a comment about code is a comment
                      // that quotes code.
                      if (event.key === "Escape") {
                        event.stopPropagation();
                        setSelection(null);
                        setLive(null);
                        setWriting("");
                      }
                      if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                        event.preventDefault();
                        save();
                      }
                    }}
                  />
                  <div {...stylex.props(styles.noteRow)}>
                    <span {...stylex.props(styles.hint, styles.noteBody)}>
                      {/* Which lines, said out loud. The selection is
                          highlighted in the gutter, but a composer that has
                          scrolled a few lines away from a six-line block leaves
                          nothing on screen saying what is being commented on. */}
                      {note.endLine > note.line
                        ? `lines ${note.line}–${note.endLine} · ⌘↵ save · esc discard`
                        : `line ${note.line} · ⌘↵ save · esc discard`}
                    </span>
                    <button type="button" {...stylex.props(styles.button)} onClick={save}>
                      save
                    </button>
                  </div>
                </div>
              ) : (
                <div
                  {...stylex.props(styles.note, note.comment.sentAt === undefined && styles.draft)}
                >
                  {/* The meta on its own row, above the words. Sharing a row
                      with the body is what squeezed a comment into a column
                      four characters wide in this column's narrow case: the
                      range, the state and the delete button are all
                      `flex-shrink: 0`, so everything they need comes out of the
                      one item that can give — the text. */}
                  <div {...stylex.props(styles.noteRow)}>
                    {note.comment.endLine > note.comment.line && (
                      // Only for a block. A single-line comment sits under the
                      // line it is about and saying so is a label that repeats
                      // what the position already says.
                      <span {...stylex.props(styles.noteWhere)}>
                        {note.comment.line}–{note.comment.endLine}
                      </span>
                    )}
                    <span {...stylex.props(styles.noteWhen, styles.noteBody)}>
                      {note.comment.sentAt === undefined ? "draft" : "sent"}
                    </span>
                    <button
                      type="button"
                      aria-label="delete this comment"
                      {...stylex.props(styles.button)}
                      onClick={() => review.remove(note.comment.id)}
                    >
                      ×
                    </button>
                  </div>
                  <div {...stylex.props(styles.noteBody)}>{note.comment.body}</div>
                </div>
              );
            }}
            {...stylex.props(styles.view)}
            options={{
              // Unified, because this column is two hundred pixels wide at its
              // floor and split would give each side a hundred of them.
              diffStyle: "unified",
              // Wrapped for the same reason. A horizontal scrollbar per file
              // in a narrow column is a diff nobody reads the right-hand half
              // of.
              overflow: "wrap",
              // The same object the worker pool was built with — see
              // highlighting.tsx. Disagreeing here makes the pool re-resolve
              // the theme and re-broadcast it to every worker before it can
              // answer the first request.
              theme: THEME,
              // The scheme the window resolved, not "system". The appearance
              // toggle is the window's own and the media query knows nothing
              // about it — see theme.ts.
              themeType: scheme,
              stickyHeaders: true,
              // What makes a line clickable at all. Without it there is no
              // selection, and with no selection there is nowhere to anchor a
              // comment. It is also what gives a *drag* meaning, which is the
              // whole of multi-line support — the library reports a range and
              // `spanOf` decides what that range means.
              enableLineSelection: true,
              // ── the gutter is the handle, and it has to look like one ────
              //
              // A drag can only *start* on the line number. That is the
              // library's decision and it is the right one — dragging over the
              // code is how text gets selected and copied, and stealing it
              // would break copying a snippet out of a diff. GitHub draws the
              // same line. `startLineSelectionFromPointerDown` passes
              // `requireNumberColumn: true` and takes no option to change it.
              //
              // What that leaves is a discoverability problem, and it is a real
              // one: the natural gesture is to drag across the code, and doing
              // that produced *nothing at all* — no selection, no cursor
              // change, no hint that the numbers to the left were the grip.
              //
              // Measured, each gesture on its own page so nothing inherited the
              // last selection:
              //
              //   drag the line numbers    lines 4–9   ✓
              //   click, then shift-click  lines 4–9   ✓
              //   drag over the code       nothing     ← what a person does
              //
              // So the number column is lit on hover and given a pointer
              // cursor. Both are about the same sentence: this part is grabbable
              // and the part beside it is text.
              lineHoverHighlight: "number",
              unsafeCSS: GUTTER_CSS,
              // The hover control: a `+` beside the line under the pointer.
              // Off by default, and without it the only way to start a comment
              // is to already know that a line number is clickable — which is a
              // feature nobody finds.
              enableGutterUtility: true,
              // The library hands back the range under the pointer — one line
              // when nothing is dragged, the whole run when something is. So
              // the `+` and a drag are the same gesture and reach the composer
              // by one path rather than two.
              //
              // In `options` and not as a prop, unlike the render callbacks
              // beside it. The React wrapper lifts the `render*` names to props
              // and leaves the `on*Click` ones here; the second argument is the
              // context, which is where the item being clicked is named.
              //
              // **`renderGutterUtility` cannot be used with this**, and the
              // library says so by throwing: "Use only one gutter utility API."
              // A custom node was the first attempt and is the worse half of
              // the choice — its callback is handed `getHoveredLine()`, which
              // is one line, while this one is handed the whole `range`. So
              // pressing `+` after dragging over six lines comments on six
              // lines here, and on one line there. A nicer-looking button is
              // not worth the feature.
              onGutterUtilityClick: (range, context) => {
                setSelection({ id: context.item.id, range });
                setLive({ id: context.item.id, range });
                setWriting("");
              },
              // The end of the gesture, and the only place a selection is
              // taken from. Both ways of starting one arrive here — the library
              // brackets its `selecting` and `gutterSelecting` sessions alike —
              // so there is one rule and not one per gesture. A plain click is
              // a gesture too: press and release, with no move in between.
              onLineSelectionEnd: (range, context) => {
                const settled = range === null ? null : { id: context.item.id, range };
                setSelection(settled);
                setLive(settled);
                setWriting("");
              },
            }}
          />
        )}
      </div>
    </div>
  );
}
