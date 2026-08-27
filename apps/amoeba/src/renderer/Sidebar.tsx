import type { SessionInfo, Thread, WorkspaceFacts, WorkspaceStatus } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { MoveToThread } from "./MoveToThread";
import { type Facts, factsKey } from "./useFacts";
import { rememberLooseOpen, rememberedLooseOpen } from "./remembered";
import { colors, space, text } from "./tokens.stylex";
import {
  PRIMARY,
  type ThreadGroup,
  type Workspace,
  groupByThread,
  groupByWorkspace,
  openable,
} from "./workspaces";

/**
 * What is known about a workspace, if anything is.
 *
 * A workspace with no session carrying an identity has no key to look up —
 * which is every session someone else started, and is why this can answer
 * undefined rather than taking the two halves as arguments.
 */
const factsFor = (facts: Facts, workspace: Workspace): WorkspaceFacts | undefined => {
  const id = workspace.sessions[0]?.identity;
  return id === undefined ? undefined : facts.get(factsKey(id.project, id.workspace));
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
//   ● effect-ts-tiered-discount-timemachine
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
  // No padding at the bottom, and that is the sticky footing's doing rather
  // than a spacing choice: a sticky child is offset from its container's
  // *padding* box, so a gutter here would leave that much of the column below
  // the button — with rows visibly scrolling through the gap. The footing
  // carries the space instead, where it is opaque.
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
    padding: `${space.row} 0 0`,
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
    padding: `${space.row} ${space.gutter}`,
    marginBottom: "0.3rem",
  },
  // The one level of indent. Enough that the eye finds the thread's left edge,
  // not so much that the name loses its column.
  nested: { paddingInlineStart: "1.5rem" },

  group: { marginBottom: "0.5rem" },
  heading: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.4rem",
    padding: `0.15rem ${space.gutter}`,
  },
  threadName: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
    fontSize: text.small,
  },
  loose: { color: colors.muted },
  headingButton: {
    width: "100%",
    borderStyle: "none",
    backgroundColor: "transparent",
    font: "inherit",
    textAlign: "left",
    cursor: "pointer",
  },
  caret: { flexShrink: 0, width: "0.7rem", fontSize: text.tiny, color: colors.muted },
  // How much is behind the fold. A disclosure that will not say how much it is
  // hiding is one nobody opens.
  count: { flexShrink: 0, fontSize: text.tiny, color: colors.muted },
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
  // ── the new-thread button, and why it is sticky ─────────────────────────
  //
  // It has now been in three places, and the two it left are both instructive.
  //
  //   inside the list, after the groups   at y=1056 in a 760-tall window: the
  //                                       entry point to the whole feature,
  //                                       reachable only by scrolling past
  //                                       everything it exists to create.
  //   above the list, fixed               always there, and always the first
  //                                       thing above a column whose point is
  //                                       the list. It pushed the work down to
  //                                       make room for the way to make more.
  //
  // Sticky is both at once: it sits at the *end* of the list, where a thing
  // that appends belongs, and it never leaves the window. A short list shows it
  // just under the last thread; a long one shows it pinned to the bottom edge
  // with the rows passing behind.
  //
  // `bottom: 0` needs the scroll container to be the ancestor, which is why it
  // is inside `list` rather than a sibling of it — and why it carries the base
  // colour. Without an opaque background the rows scroll *through* the button
  // rather than behind it, which is the one way sticky fails that looks like a
  // paint bug.
  //
  // ── sticky has to *look* sticky, and that is not free ────────────────────
  // The first version carried a rule above it always, and read as a fixed
  // footer even though it was not one: measured at the bottom of the list it
  // sat 8px under the last row, exactly where it belongs — and nobody could
  // tell, because a permanent border and an opaque band are what chrome looks
  // like. A sticky element that never changes is indistinguishable from a
  // fixed one.
  //
  // So the rule appears only while it is actually stuck, which CSS cannot ask.
  // The `stuck` style below is driven by a one-pixel sentinel *after* the
  // footing: while that is on screen the list is scrolled to its end and the
  // button is in the flow; the moment it leaves, the button is floating over
  // rows and says so.
  footing: {
    position: "sticky",
    insetBlockEnd: 0,
    marginTop: "0.35rem",
    padding: `0.35rem ${space.gutter} 0.5rem`,
    backgroundColor: colors.base,
    borderTopWidth: 1,
    borderTopStyle: "solid",
    // Transparent rather than absent, so that gaining the rule does not also
    // move the button by a pixel.
    borderTopColor: "transparent",
    transitionProperty: "border-top-color",
    transitionDuration: "120ms",
  },
  stuck: { borderTopColor: colors.border },
  /** One pixel after the footing. See `footing`. */
  sentinel: { height: 1 },
  newThread: {
    width: "100%",
    padding: "0.25rem 0.45rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    textAlign: "left",
    cursor: "pointer",
  },
  rowOn: { backgroundColor: colors.border },

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
    textAlign: "left",
    cursor: "pointer",
  },
  titleShut: { color: colors.muted, cursor: "default", opacity: 0.55 },
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
  dot: { width: "0.85rem", flexShrink: 0, fontSize: 10, color: colors.muted },
  // One per state, and named for the state rather than the colour so a theme
  // can move them. `exited` deliberately has none: a session that ended is what
  // the muted default already says, and giving it a hue would put a colour on
  // the strip for the one thing nobody needs to look at.
  dotWorking: { color: colors.live },
  dotWaiting: { color: colors.waiting },
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
    fontSize: text.tiny,
    color: colors.muted,
    lineHeight: 1.5,
    overflow: "hidden",
  },
  ident: { flexShrink: 0 },
  // The pull request number, in the accent. It is the one thing on line two
  // that points somewhere outside this window, and the hue is what says so.
  pr: { flexShrink: 0, color: colors.accent },
  // Where the work is in the configured dev loop. Never truncated — `impl…`
  // says nothing that `implement` does not, and the whole word is nine
  // characters.
  phase: { flexShrink: 0, whiteSpace: "nowrap" },
  // The one thing on line two allowed to truncate. A project name is short and
  // a kind is shorter; a slug is the long one, and it is also the one whose
  // beginning carries the information.
  slug: { minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
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
    fontSize: text.tiny,
    cursor: "inherit",
    whiteSpace: "nowrap",
  },
  kindOn: { color: colors.text },
  kindPick: { cursor: "pointer" },
  reason: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
});

/**
 * The glyph for a state, and the state's own name for a screen reader.
 *
 * Two facts on one mark: the *hue* is the state, and the *shape* is whether it
 * has been read. That pairing is deliberate — a colour alone excludes anyone
 * who cannot see the difference between the amber and the green, and a shape
 * alone would need five of them, which is a legend nobody has.
 *
 *   ● working · idle · exited      seen
 *   ◉ waiting · error, unread      not seen, and drawn as a ring so it is
 *                                  distinguishable without the hue
 */
const GLYPH = { seen: "●", unseen: "◉" } as const;

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

  return (
    <span
      // The name, not the glyph. A screen reader reading "●" says "black
      // circle", which is a description of the ink rather than of the row.
      role="img"
      aria-label={unread ? `${say}, unread` : say}
      {...stylex.props(styles.dot, style, known === undefined && unread && styles.dotUnknownUnread)}
    >
      {unread ? GLYPH.unseen : GLYPH.seen}
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
  selected,
  onSelect,
  threads,
  thread,
  onThreadsChanged,
}: {
  readonly workspace: Workspace;
  /** What is known about this workspace, or nothing has reported on it. */
  readonly facts: WorkspaceFacts | undefined;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly threads: ReadonlyArray<Thread>;
  /** The thread holding this workspace, if any. */
  readonly thread: Thread | undefined;
  readonly onThreadsChanged: () => void;
}) {
  // Hover is tracked here rather than done in CSS, because the control lives in
  // a child component and `:hover` on a parent cannot reach across one. Focus
  // is left to CSS — see `trigger` in MoveToThread.
  const [hovered, setHovered] = useState(false);
  const active = workspace.sessions.some((session) => session.name === selected);
  const live = workspace.sessions.some((session) => !session.ended);
  const primary = openable(workspace);
  const several = workspace.sessions.length > 1;

  // Whichever half of project/workspace the name did not use. A `default`
  // workspace is the repository's, so the project is the name and `default`
  // goes below; anything else names itself and the project goes below.
  const other = workspace.foreign ? "elsewhere" : (workspace.otherIdent ?? "");

  // ── the label takes line one, and the slug moves down ────────────────────
  //
  // `effect-ts-tiered-discount-timemachine` is a slug because it has to be a
  // directory, a jj workspace and half a bookmark. What the person typed was a
  // sentence, and line one is the line they read.
  //
  //   before   ● effect-ts-tiered-discount-timemachine
  //              thicket
  //
  //   after    ● Tiered discount time machine
  //              thicket · effect-ts-tiered-discount-timemachine
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
  //   workspace.label     `awp_label`, which amoeba writes for what it makes
  //   workspace.name      the slug, which every workspace has
  //
  // The Go one first and not last, which is the opposite of the obvious
  // ranking. amoeba's own label is the better long-term home — it travels with
  // the session and needs no second file — but a workspace with both got them
  // from the same sentence, and a workspace with only one of them is the
  // ordinary case either way. First is where the data actually is.
  const shown = facts?.displayName ?? workspace.label ?? workspace.name;
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

  return (
    <div
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      {...stylex.props(styles.row, active && styles.rowOn)}
    >
      <div {...stylex.props(styles.titleRow)}>
        <button
          type="button"
          disabled={primary === undefined}
          // What ctrl+j and ctrl+k step through in this column. The row's
          // title, and not the chips or the hover controls beside it — a list
          // that moved through those is a list nobody can predict. See
          // navigation.ts.
          data-nav-item
          // The reason is the tooltip as well as line two. A row that will not
          // say why it is disabled is worse than no row at all.
          title={refusal ?? workspace.address}
          onClick={() => primary !== undefined && onSelect(primary)}
          {...stylex.props(styles.title, primary === undefined && styles.titleShut)}
        >
          <Dot live={live} status={facts?.status} unread={facts?.unread === true} />
          <span {...stylex.props(styles.label)}>{shown}</span>
        </button>
        <MoveToThread
          workspace={workspace}
          threads={threads}
          current={thread}
          shown={hovered}
          onChanged={onThreadsChanged}
        />
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
            {facts?.pr !== undefined && (
              <span {...stylex.props(styles.pr)} title={`pull request #${facts.pr}`}>
                {`#${facts.pr}`}
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
    </div>
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
  onSelect,
  folded,
  onFold,
  threads,
  onThreadsChanged,
}: {
  readonly group: ThreadGroup;
  readonly facts: Facts;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  readonly threads: ReadonlyArray<Thread>;
  readonly onThreadsChanged: () => void;
  /** Only the loose group folds; a thread is small and is the point. */
  readonly folded: boolean;
  readonly onFold: (() => void) | undefined;
}) {
  const heading =
    onFold === undefined ? (
      <div {...stylex.props(styles.heading)}>
        <span {...stylex.props(styles.threadName)}>{group.title}</span>
      </div>
    ) : (
      // A button, because it does something — unlike a thread heading, which
      // is a heading precisely because a thread has nothing to open.
      <button
        type="button"
        aria-expanded={!folded}
        onClick={onFold}
        {...stylex.props(styles.heading, styles.headingButton)}
      >
        <span aria-hidden {...stylex.props(styles.caret)}>
          {folded ? "▸" : "▾"}
        </span>
        <span {...stylex.props(styles.threadName, styles.loose)}>{group.title}</span>
        <span {...stylex.props(styles.count)}>{group.workspaces.length}</span>
      </button>
    );

  return (
    <div {...stylex.props(styles.group)}>
      {heading}
      {folded ? null : (
        <>
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
                selected={selected}
                onSelect={onSelect}
                threads={threads}
                thread={group.thread}
                onThreadsChanged={onThreadsChanged}
              />
            </div>
          ))}
        </>
      )}
    </div>
  );
}

/**
 * Whether a bottom-sticky element is currently floating rather than in flow.
 *
 * Answered by watching a sentinel placed *after* it: while that pixel is on
 * screen the list is scrolled to its end and the element is exactly where it
 * would be without `position: sticky`; the moment it leaves, the element is
 * over content. CSS has no selector for this, and the alternative — comparing
 * scrollTop to scrollHeight on a scroll handler — asks the same question once
 * per frame instead of once per change.
 *
 * False until the observer has fired. The first paint of a short list is the
 * common case, and a rule that flashes on during mount is worse than one that
 * arrives a frame late.
 */
const useStuck = (sentinel: { readonly current: HTMLElement | null }): boolean => {
  const [stuck, setStuck] = useState(false);
  useEffect(() => {
    const element = sentinel.current;
    if (element === null) {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.at(-1);
        if (entry !== undefined) {
          setStuck(!entry.isIntersecting);
        }
      },
      // The scroll container, not the viewport. A null root measures against
      // the window, which this list is not the size of.
      { root: element.parentElement },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [sentinel]);
  return stuck;
};

export function Sidebar({
  sessions,
  facts,
  threads,
  selected,
  onSelect,
  onNew,
  onThreadsChanged,
  failure,
}: {
  readonly sessions: ReadonlyArray<SessionInfo>;
  /** What each workspace's agent is doing. See useFacts. */
  readonly facts: Facts;
  readonly threads: ReadonlyArray<Thread>;
  readonly selected: string | undefined;
  readonly onSelect: (session: SessionInfo) => void;
  /** Open the new-thread modal. The window owns it — see App.tsx. */
  readonly onNew: () => void;
  /** A workspace changed threads, so the list App holds is out of date. */
  readonly onThreadsChanged: () => void;
  readonly failure: string | undefined;
}) {
  const groups = groupByThread(
    threads,
    groupByWorkspace(sessions),
    (workspace) => factsFor(facts, workspace)?.status,
  );
  // What the menu may offer. An archived thread is not somewhere to put work,
  // and `groupByThread` drops them from the strip for the same reason.
  const live = threads.filter((thread) => thread.archivedAt === undefined);
  const sentinel = useRef<HTMLDivElement | null>(null);
  const stuck = useStuck(sentinel);
  const [looseOpen, setLooseOpen] = useState(rememberedLooseOpen);

  // The two states of the column, chosen before the markup rather than inside
  // it. A daemon that is not running is the ordinary case during development,
  // so it gets a sentence and the command, not an empty list.
  const body =
    failure === undefined ? (
      <>
        {groups.length === 0 && <div {...stylex.props(styles.empty)}>no workspaces</div>}
        {groups.map((group) => {
          const isLoose = group.thread === undefined;
          return (
            <Group
              key={group.key}
              group={group}
              facts={facts}
              selected={selected}
              onSelect={onSelect}
              threads={live}
              onThreadsChanged={onThreadsChanged}
              folded={isLoose && !looseOpen}
              onFold={
                isLoose
                  ? () =>
                      setLooseOpen((open) => {
                        rememberLooseOpen(!open);
                        return !open;
                      })
                  : undefined
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
      <div {...stylex.props(styles.list)}>
        {body}

        {/* Last in the list and pinned to its bottom — see `footing`.

            Never disabled. It used to be, because the project came from the
            selected row and nothing selected meant nothing to create into; the
            modal picks a project instead, so the button always has an answer. */}
        {failure === undefined && (
          <>
            <div {...stylex.props(styles.footing, stuck && styles.stuck)}>
              <button
                type="button"
                title="new thread (⌘N)"
                onClick={onNew}
                {...stylex.props(styles.newThread)}
              >
                + thread
              </button>
            </div>
            <div ref={sentinel} aria-hidden {...stylex.props(styles.sentinel)} />
          </>
        )}
      </div>
    </div>
  );
}
