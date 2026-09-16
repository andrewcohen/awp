import type { Task } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { AnimatePresence, motion } from "motion/react";
import { useArriving, useSpring } from "../design/springs";
import { useEffect, useRef, useState } from "react";
import { ArrowsSplit } from "@phosphor-icons/react/ArrowsSplit";
import { useAtomSet } from "@effect/atom-react";
import { newThreadAtom } from "../data/atoms";
import {
  addTask,
  listBoard,
  onReconnect,
  sendTask,
  setTaskStatus,
  watchTasks,
} from "../data/daemon";
import { Markdown } from "./Markdown";
import { type Found, type Listed, filter, merge } from "./tasklist";
import { type Span, pieces } from "../search/fuzzy";
import { typeset } from "../design/typeset";
import { colors, space, text } from "../design/tokens.stylex";

// What the agent in this workspace has written down for itself.
//
// The panel exists because that list was already there and was unreachable:
// an agent keeps one as it works, and the only way to see it was to ask the
// agent in prose and read the answer out of a scrollback. It is a queue, and
// a queue somebody can look at is a different thing from a queue somebody has
// to interview for.
//
// ── read-only, and a send ──────────────────────────────────────────────────
//
// Nothing here writes to the list; see `agent-tasks.ts`. The one thing this
// panel does is hand a task *back* to the agent as a prompt — which is not a
// write, it is the same gesture as sending a review or a page note, and it
// goes down the same wire.
//
// So there are exactly two verbs on a row: read it, and ask for it next.
//
// **Next, not now.** The prompt asks the agent to finish what it is doing
// first — see `taskPrompt`. Send is for adding to the queue, and an agent that
// abandons a half-made change to start something else has cost more than the
// task was worth.
//
// ── a row is one line until it is asked to be more ─────────────────────────
//
// The description was clamped to two lines to begin with, which was still far
// too much: a description here is a paragraph or several, and two lines of
// every one of twenty-four tasks is a column of prose nobody can scan. What a
// list is for is finding the row you want, and a subject is the whole of what
// that takes.
//
// So the description is out of the layout entirely until the subject is
// clicked. That makes the panel a list of titles, which is what it should have
// been, and the reading of one task a deliberate act.
//
// ── one read, and a stream instead of a poll ───────────────────────────────
//
// This panel used to make two reads on a four second timer: the session's own
// list by directory, and the board. Both halves are gone.
//
//   the second read   the daemon ingests Claude Code's lists now, so an
//                     agent's queue IS a board row — read twice, it drew twice
//   the timer         `TaskChanges`. The sources are files nothing here
//                     writes, so the sweep belongs where one timer can serve
//                     every client rather than one per open panel
//
// Subscribing is also what makes the daemon sweep at all, which is why the
// subscription is the panel's own rather than the window's: a hidden tab is
// unmounted, and a panel nobody is looking at should not be paying for a disk
// read of every project. The opposite of `usePages`, and for the opposite
// reason — nothing arrives here that somebody is not already looking at.
//
// The board needs no directory, which changes what an empty panel means: a
// workspace with nothing running still has tasks written down about it, so
// this panel is no longer blank when no session is open.
//
// ── the filter is a row, and it is always there ────────────────────────────
//
// The list is twenty-four outstanding in this workspace before the finished
// ones are shown, and scrolling is the wrong gesture for finding a row whose
// word somebody already remembers.
//
// It does not share the head. There is room for three things up there and the
// count, the add button and the scope control are already those three — a
// field squeezed in beside them would be two characters wide in a 280px
// column. The row it costs is spent every time the panel is open, which is the
// argument the add line loses and this one wins: writing a task down here is
// the rarest thing done in this panel, and finding one is the commonest.
//
// The head still answers, though: while a filter is on, the count says how
// many of how many, because a list that has quietly become three rows should
// say what it is hiding. See `search/fuzzy.ts` for why a subject and a
// description are not matched the same way.
//
// ── done tasks are a count, not rows ───────────────────────────────────────
//
// A finished task is worth knowing the number of and almost never worth
// reading. This workspace's own list is eighty-odd completed against a
// handful outstanding, and showing them all would bury the four that matter
// under everything that no longer does. The header says how many there are,
// which is the whole of what a completed task is still good for.

/**
 * How wide the question is.
 *
 * Three, and the middle one is the default for the reason it always was: a
 * column beside a checkout is usually asked about that checkout's project.
 * `checkout` is new and is what the session's own list used to be — the agent
 * in *this* directory and nothing else — which had no name while it was a
 * separate read.
 */
type Scope = "checkout" | "project" | "everywhere";

const NEXT: Record<Scope, Scope> = {
  checkout: "project",
  project: "everywhere",
  everywhere: "checkout",
};

/** Whether two listings differ in anything that would change a pixel. */
const same = (a: ReadonlyArray<Task>, b: ReadonlyArray<Task>): boolean =>
  a.length === b.length &&
  a.every((one, at) => {
    const other = b[at];
    return (
      other !== undefined &&
      one.id === other.id &&
      one.status === other.status &&
      one.subject === other.subject &&
      one.description === other.description
    );
  });

const styles = stylex.create({
  panel: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  // The same band as the diff's revision row and the web panel's address bar:
  // one line of chrome under the tabs, saying what the panel is showing.
  head: {
    display: "flex",
    alignItems: "center",
    gap: "0.5rem",
    flexShrink: 0,
    minHeight: space.titlebar,
    padding: "0.4rem 0.6rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  count: { flex: 1, minWidth: 0, color: colors.muted, fontSize: text.small },
  button: {
    padding: "0.15rem 0.45rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
    transitionProperty: "color, border-color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text, borderColor: colors.muted },
  },
  // The finished count, which is a control. Drawn as the sentence it was
  // rather than as a button: it sits inside the count's own line, and a
  // bordered box there would read as a second thing in the head instead of as
  // half of what is already written.
  done: {
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    cursor: "pointer",
    ":hover": { color: colors.text },
  },
  showing: { color: colors.text },
  list: { flex: 1, minHeight: 0, overflowY: "auto", overflowX: "hidden", padding: "0.3rem 0" },
  // Quieter than the outstanding rows, and separated by a rule rather than by
  // a heading: what is above is the list, and this is an appendix to it.
  finished: {
    marginTop: "0.3rem",
    paddingTop: "0.3rem",
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: colors.border,
    opacity: 0.6,
  },
  note: { padding: "0.6rem", color: colors.muted, fontSize: text.small },

  row: {
    display: "flex",
    // Baseline, not flex-start. Three things of three different heights sit on
    // this row — a bullet, a line of text and a bordered button — and aligning
    // their *boxes* aligns nothing a reader can see, because none of the three
    // fills its box the same way. What the eye lines up on is the text, so
    // that is what the layout lines up on. The same rule the sidebar's rows
    // already follow.
    alignItems: "baseline",
    gap: "0.45rem",
    padding: "0.35rem 0.6rem",
    // The row is the hover target for the send button, which is otherwise
    // invisible. Nothing else about it responds.
    ":hover": { backgroundColor: colors.surface },
  },
  // Sized against the subject beside it rather than against the type floor —
  // a bullet is not a word. See the note on the floor in AGENTS.md.
  //
  // A fixed width rather than an intrinsic one, so `●` and `○` — which are not
  // the same width — do not move the subject a pixel as a task starts. Copied
  // from the sidebar's row, where the same two glyphs alternate.
  //
  // The `marginTop` this replaced was a nudge under flex-start, and it was
  // wrong at every size but the one it was tuned at.
  dot: { width: "0.85rem", flexShrink: 0, fontSize: 10, color: colors.muted },
  doing: { color: colors.live },
  todo: { color: colors.muted },

  body: { flex: 1, minWidth: 0 },
  // The whole subject line is the disclosure control, so the target is the
  // width of the row rather than a chevron somebody has to aim at.
  subject: {
    display: "block",
    width: "100%",
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.text,
    font: "inherit",
    textAlign: "start",
    cursor: "pointer",
  },
  /** The agent's own id. Monospace, because it is an address into its list. */
  id: { marginRight: "0.4rem", color: colors.muted, fontFamily: text.mono },
  detail: {
    margin: "0.2rem 0 0",
    color: colors.muted,
    fontSize: text.small,
    // Preserved, because a description is written with paragraphs in it and
    // reflowing it into one block loses the shape the author gave it.
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
  },
  // Hidden by opacity and *not* by display, or it would leave the layout and
  // stop being reachable from the keyboard — which is the mandate in
  // AGENTS.md, and the reason `MoveToThread` is shaped the same way.
  send: {
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
    opacity: 0,
    transitionProperty: "opacity, color, border-color",
    transitionDuration: "100ms",
    ":focus-visible": { opacity: 1 },
    ":hover": { color: colors.text, borderColor: colors.muted },
  },
  // The fan-out, beside the send and hidden by the same rule. A glyph rather
  // than a second word: the row is 280px wide, `send` is the thing done here
  // most often, and two labelled buttons make the rarer one look like half of
  // a pair of equals.
  fan: {
    flexShrink: 0,
    display: "flex",
    alignItems: "center",
    padding: "0.15rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    cursor: "pointer",
    opacity: 0,
    transitionProperty: "opacity, color",
    transitionDuration: "100ms",
    ":focus-visible": { opacity: 1 },
    ":hover": { color: colors.text },
  },
  shown: { opacity: 1 },
  // The scope control. A button that reads as a label until it is hovered —
  // the panel's own noun, and pressing it widens the question.
  scope: {
    marginInlineStart: "auto",
    borderStyle: "none",
    backgroundColor: "transparent",
    color: colors.muted,
    cursor: "pointer",
    padding: "0.1rem 0.25rem",
    borderRadius: "0.2rem",
    ":hover": { color: colors.text, backgroundColor: colors.raised },
  },
  said: { color: colors.live },
  failed: { color: colors.warn },

  // The add line. A box that is not there until it is asked for, so it opens
  // by height — `overflow: hidden` is what makes that animatable at all, and
  // the padding is inline-only for the same reason the chat's ledge is: block
  // padding on a box animating to nothing leaves a gap that never closes.
  opening: { overflow: "hidden", flexShrink: 0 },
  writing: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    padding: "0.35rem 0.6rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  field: {
    flex: 1,
    minWidth: 0,
    padding: "0.15rem 0.3rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    // No `font: inherit` and no size: `typeset.label` is applied beside this
    // and holds both. A form control does not inherit the family on its own,
    // which is the whole reason the role has to be on it.
    ":focus-visible": { borderColor: colors.accent, outlineStyle: "none" },
  },
  // The filter's own band, under the head. The add line's shape without its
  // animation: this one never leaves, so there is nothing to open.
  filtering: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    flexShrink: 0,
    padding: "0.35rem 0.6rem",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  // What matched, and what did not.
  //
  // Deliberately NOT the accent. An accent is spent, not applied — it marks a
  // deviation from the rows around it — and a highlight draws on every row a
  // filter keeps, which is the arithmetic that put thirty accents in one
  // column of the review queue. So the mark is made of what the row already
  // has: the hit keeps the text colour and takes the weight, and everything
  // around it drops to muted. The matched characters are the ones at full
  // strength rather than the ones painted a new colour.
  mark: { color: colors.text, fontWeight: text.strong },
  dim: { color: colors.muted },
  // The line of body a description hit is evidenced by. One line, clamped
  // rather than wrapped: it is a fragment cut mid-sentence, and a fragment
  // that took three lines would be the wall of prose the excerpt exists to
  // avoid.
  excerpt: {
    margin: "0.15rem 0 0",
    color: colors.muted,
    fontSize: text.small,
    whiteSpace: "nowrap",
    overflow: "hidden",
    textOverflow: "ellipsis",
  },
  // A dot that is a control, for the rows this window owns. Everything about
  // it matches the span it replaces — see `dot` — so the list does not shift
  // by a pixel between a task awp wrote and one it copied.
  toggle: {
    width: "0.85rem",
    flexShrink: 0,
    padding: 0,
    backgroundColor: "transparent",
    borderStyle: "none",
    fontSize: 10,
    lineHeight: 1,
    cursor: "pointer",
    color: colors.muted,
  },
});

/**
 * What a press on the dot does next.
 *
 * Three states and not four: `completed` leaves the list — it is counted in the
 * header rather than drawn — so the cycle is how a task awp owns is finished,
 * and pressing the dot of a finished one is not a gesture that exists.
 */
const NEXT_STATUS: Record<string, string> = {
  pending: "in_progress",
  in_progress: "completed",
  blocked: "in_progress",
  // Back to the beginning, which is what makes the dot on a finished row a
  // control rather than a decoration. The completed section is reachable now,
  // and the one act wanted from a finished task is putting it back — a dot
  // that did nothing there would be exactly the lie this panel refuses to
  // tell for a copied row.
  completed: "pending",
};

/**
 * The brief a task makes, which is the task.
 *
 * Subject and body, separated by a blank line, because the form's one field
 * is markdown a person is about to read back — and a description that ran on
 * from its own title would be a paragraph nobody wrote. The body is often
 * empty (a `TODO.md` heading with nothing under it), and then the subject is
 * the whole of it rather than a subject with a trailing gap.
 *
 * The thread's *name* is not set here and cannot be: the daemon writes it,
 * one step into the job, out of this sentence. That is the `name` step's
 * whole job and a client composing one would be guessing at a prompt it
 * cannot see.
 */
const briefFor = (task: Listed): string => {
  const body = task.description.trim();
  return body === "" ? task.subject : `${task.subject}\n\n${body}`;
};

/**
 * Text with the matched characters standing out of it.
 *
 * With no spans it renders the string and nothing else — the unfiltered case
 * is every render of this panel, and it must not pay for a filter nobody
 * typed, nor draw a row differently for having been asked about.
 *
 * Keyed by offset rather than by index: the pieces are a partition of one
 * string, so where a piece starts is already unique, and an index key would
 * re-use a node for a different fragment as somebody types.
 */
function Marks({
  text: said,
  spans,
}: {
  readonly text: string;
  readonly spans: ReadonlyArray<Span>;
}) {
  if (spans.length === 0) {
    return said;
  }
  return pieces(said, spans).map((piece) => (
    <span key={piece.at} {...stylex.props(piece.hit ? styles.mark : styles.dim)}>
      {piece.text}
    </span>
  ));
}

interface RowProps {
  readonly found: Found;
  /** Absent for a session awp did not make: there is no agent to address. */
  readonly onSend: (() => void) | undefined;
  /**
   * Move it, for the rows awp wrote.
   *
   * Absent for every copied row, which is the honest shape: ingest writes the
   * source's status back over anything set here, so a `TODO.md` task marked
   * done in this panel would be pending again within ten seconds. The dot for
   * one of those stays a mark rather than becoming a control that lies.
   */
  readonly onMove: (() => void) | undefined;
  /**
   * Start a thread of its own for it, rather than telling the agent here.
   *
   * The opposite act to `onSend`, and the reason the panel is a queue rather
   * than a list: read the pending work, and either hand one to the agent in
   * front of you or fan one out beside it. Absent when no project is on
   * screen — the form has to open on one, and the panel does not know which.
   */
  readonly onFanOut: (() => void) | undefined;
  readonly state: "idle" | "sending" | "sent" | "failed";
}

function Row({ found, onSend, onMove, onFanOut, state }: RowProps) {
  const task = found.task;
  const [open, setOpen] = useState(false);
  // Hover is React state rather than a CSS descendant selector, because StyleX
  // writes atomic rules for one element and has no way to say "while my parent
  // is hovered". Same shape as `MoveToThread`, which is the worked example.
  const [hovered, setHovered] = useState(false);
  const doing = task.status === "in_progress";
  const label =
    state === "sending"
      ? "sending"
      : state === "sent"
        ? "sent"
        : state === "failed"
          ? "no agent"
          : "send";

  return (
    <div
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      {...stylex.props(styles.row)}
    >
      {onMove === undefined ? (
        <span aria-hidden {...stylex.props(styles.dot, doing ? styles.doing : styles.todo)}>
          {doing ? "●" : "○"}
        </span>
      ) : (
        <button
          type="button"
          data-nav-item
          title={doing ? "mark it done" : `start it — this one is awp's own, so it moves from here`}
          onClick={onMove}
          {...stylex.props(styles.toggle, doing ? styles.doing : styles.todo)}
        >
          {doing ? "●" : "○"}
        </button>
      )}

      <div {...stylex.props(styles.body)}>
        <button
          type="button"
          data-nav-item
          aria-expanded={open}
          title={open ? "hide what the task says" : "show what the task says"}
          onClick={() => setOpen((was) => !was)}
          {...stylex.props(typeset.control, styles.subject)}
        >
          <span {...stylex.props(styles.id)}>
            <Marks text={task.label} spans={found.label} />
          </span>
          <Marks text={task.subject} spans={found.subject} />
        </button>

        {found.excerpt === undefined ? undefined : (
          // Where it was found, for a row whose title visibly does not contain
          // what was typed. Without this the filter reads as a bug: a list of
          // titles, none of which say the word.
          <p {...stylex.props(styles.excerpt)}>
            <Marks text={found.excerpt.text} spans={found.excerpt.spans} />
          </p>
        )}

        {open && task.description.trim() !== "" ? (
          // Markdown, because a board task's body IS markdown — it is a
          // section of somebody's TODO.md, headings and code blocks and all,
          // and #123's runs to 6722 characters. Rendered as preformatted text
          // it showed `## Summary` and `- [ ]` as literal characters, which is
          // the same finding the PR panel already recorded.
          //
          // A session task's description is plain prose and renders as prose
          // through the same path, so there is no branch here.
          <div {...stylex.props(styles.detail)}>
            <Markdown>{task.description.trim()}</Markdown>
          </div>
        ) : undefined}
      </div>

      {onFanOut === undefined ? undefined : (
        <button
          type="button"
          data-nav-item
          title={`start a thread of its own for "${task.subject}"`}
          onClick={onFanOut}
          {...stylex.props(styles.fan, (hovered || state !== "idle") && styles.shown)}
        >
          <ArrowsSplit size={14} aria-hidden />
        </button>
      )}

      <button
        type="button"
        data-nav-item
        disabled={onSend === undefined || state === "sending"}
        title={
          onSend === undefined
            ? "this session is not one of ours, so there is no agent to tell"
            : `ask the agent to pick up "${task.subject}" next`
        }
        onClick={onSend}
        {...stylex.props(
          styles.send,
          (hovered || state !== "idle") && styles.shown,
          state === "sent" && styles.said,
          state === "failed" && styles.failed,
        )}
      >
        {label}
      </button>
    </div>
  );
}

export interface TasksProps {
  /** A directory in the open session's workspace, or nothing is open. */
  readonly dir: string | undefined;
  readonly project: string | undefined;
  readonly workspace: string | undefined;
  /**
   * The thread the open workspace belongs to, or nothing claims it.
   *
   * What a task written here is tagged with, beside its project — which is what
   * makes the store able to answer "this piece of work" later. Most checkouts
   * on a real machine predate threads, so its absence is ordinary and costs the
   * task nothing but that one tag.
   */
  readonly thread: string | undefined;
}

export function Tasks({ dir, project, workspace, thread }: TasksProps) {
  // The new-thread modal is App's — a modal belongs to the window — and this
  // panel is three columns from it behind a Base UI tab, so the request is
  // written where both can reach it. See `newThreadAtom`.
  const openNewThread = useAtomSet(newThreadAtom);
  const [board, setBoard] = useState<ReadonlyArray<Task>>([]);
  const [asked, setAsked] = useState(false);
  const [states, setStates] = useState<Record<string, RowProps["state"]>>({});
  // ── how wide the question is ─────────────────────────────────────────────
  //
  // A control rather than a fixed choice, because all three are real questions
  // and the store answers them with one argument. `project` is the default; it
  // narrows to this checkout, which is what the agent's own list used to be,
  // and widens to everywhere, which is the reason the store exists at all.
  const [scope, setScope] = useState<Scope>("project");
  // The add line: absent until asked for, and Escape throws it away. A panel
  // that always showed a composer would spend a row of a 280px column on the
  // rarest thing done here.
  const [writing, setWriting] = useState(false);
  const [draft, setDraft] = useState("");
  // Whether the finished half is on screen. Deliberately not remembered: this
  // is a glance rather than a mode — "was that done already" — and a section
  // that came back open would make the panel's first screen a list of work
  // nobody has to think about. `rememberedPanels` is the worked example of the
  // other kind.
  const [showingDone, setShowingDone] = useState(false);
  // What is being looked for. Not remembered anywhere, for the same reason
  // `showingDone` is not: a filter left on is a list that lies about how much
  // work there is, and the panel is unmounted whenever its tab is hidden.
  const [query, setQuery] = useState("");
  const held = useRef<ReadonlyArray<Task>>([]);
  const list = useRef<HTMLDivElement>(null);
  const arriving = useArriving();
  const spring = useSpring();

  // Which tags that scope is, and the fallback when there is nothing to scope
  // by: a window with no session open has no project, and the honest answer
  // there is everything rather than nothing.
  const tags =
    scope === "everywhere" || project === undefined
      ? undefined
      : scope === "checkout" && workspace !== undefined
        ? [`workspace:${project}/${workspace}`]
        : [`project:${project}`];

  // Read on mount and on a nudge, never on a timer.
  //
  // `watchTasks` is a stream of "a sweep changed something", and holding it is
  // what makes the daemon sweep — so a panel nobody is looking at is a panel
  // that costs nothing, which a timer in here could not manage.
  //
  // And a stream carries changes from *now*, so the socket coming back has to
  // re-ask: everything that moved while it was down was carried nowhere at
  // all. The same rule the jobs hook learned the expensive way.
  useEffect(() => {
    let live = true;
    const take = () => {
      listBoard(tags)
        .then((got) => {
          if (!live) {
            return;
          }
          setAsked(true);
          // Compared before it is stored, or every nudge replaces the array
          // and re-renders a list that has not changed — which on an open row
          // also costs the description's layout.
          if (!same(held.current, got)) {
            held.current = got;
            setBoard(got);
          }
        })
        .catch(() => {
          // An older daemon has no `TaskBoard` at all, and the bar already
          // says when the daemon is gone. The last good list beats an error
          // drawn in its place.
          if (live) {
            setAsked(true);
          }
        });
    };
    take();
    const stop = watchTasks(() => take());
    const again = onReconnect(take);
    return () => {
      live = false;
      stop();
      again();
    };
    // `tags` is derived from the three below and rebuilt every render, so it
    // is deliberately not a dependency: listing it would resubscribe the
    // stream on every render of the panel.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, workspace, scope]);

  const { rows, done } = merge(board);
  const showing = filter(rows, query);
  const showingFinished = filter(done, query);
  const filtering = query.trim() !== "";

  /**
   * Down, out of the field and into the list.
   *
   * `navigation.ts` hands ctrl+j and ctrl+k over inside a real `<input>` on
   * purpose — on macOS they are kill-line and newline in any text field, and
   * the composer is a textarea. That decision is right and this is what it
   * costs: the field has to answer the chord itself, which it can, because the
   * window's capture-phase listener returns without preventing anything.
   *
   * ArrowDown does it too. A person typing into a filter reaches for the arrow
   * long before they reach for a chord, whatever the window's mandate is.
   */
  const intoTheList = () => {
    list.current?.querySelector<HTMLElement>("[data-nav-item]")?.focus();
  };

  /**
   * Write one down here.
   *
   * Tagged with the project and, when one claims this checkout, the thread —
   * which is what lets the store answer "this piece of work" later. The
   * description is deliberately not asked for: a one-line box is what makes
   * writing a task down cost nothing, and the body is what `awp_task_add` and
   * the file are for.
   */
  const write = () => {
    const subject = draft.trim();
    if (subject === "" || project === undefined) {
      return;
    }
    setDraft("");
    void addTask({
      subject,
      description: "",
      status: "pending",
      tags: [`project:${project}`, ...(thread === undefined ? [] : [`thread:${thread}`])],
    })
      // Re-read rather than splicing the answer in: the row has to land under
      // whatever scope is showing, and the store's order is the store's.
      .then((made) => {
        held.current = [...held.current, made];
        setBoard(held.current);
      })
      .catch(() => {
        // The bar says when the daemon is gone, and the draft is already
        // cleared — putting it back would fight somebody typing the next one.
      });
  };

  /** Move a task awp owns to the next status, or nothing for a copied row. */
  const move = (task: Listed) => {
    if (task.source !== "awp") {
      return undefined;
    }
    return () => {
      const next = NEXT_STATUS[task.status] ?? "completed";
      // Painted here and confirmed by the answer, because the sweep that
      // would otherwise carry it is a sweep of sources this row is not in.
      held.current = held.current.map((one) =>
        one.id === task.task.id ? { ...one, status: next } : one,
      );
      setBoard(held.current);
      void setTaskStatus(task.task.id, next).catch(() => {
        // Refused, which for an awp row means the daemon is gone or the task
        // has been removed elsewhere. The next nudge corrects the paint.
      });
    };
  };

  const fanOut = (task: Listed) => {
    if (project === undefined) {
      return undefined;
    }
    return () => {
      openNewThread({
        project,
        workspace,
        // Branch from the workspace on screen when there is one: a task read
        // out of this checkout usually follows on from it, which is what
        // cmd+shift+N means and what `baseOfThread` already resolves. With no
        // workspace there is nothing to branch from and the form stays on
        // trunk, which is the same answer cmd+N gives.
        fromWorkspace: workspace !== undefined,
        brief: briefFor(task),
      });
    };
  };

  const send = (task: Listed) => {
    if (project === undefined || workspace === undefined) {
      return;
    }
    return () => {
      setStates((was) => ({ ...was, [task.key]: "sending" }));
      sendTask(project, workspace, task.task)
        .then(() => setStates((was) => ({ ...was, [task.key]: "sent" })))
        .catch(() => setStates((was) => ({ ...was, [task.key]: "failed" })));
    };
  };

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.head)}>
        <span {...stylex.props(styles.count)}>
          {/*
            While a filter is on the count says how many of how many: the list
            has quietly stopped being all of them, and the head is where it
            says so.
          */}
          {rows.length === 0
            ? "nothing to do"
            : filtering
              ? `${showing.length} of ${rows.length}`
              : `${rows.length} to do`}
          {done.length === 0 ? undefined : (
            // The count was the whole sentence and it made the finished half
            // unreachable: a number saying a thing exists, with no way to it.
            // A button rather than a second control in the head — there is
            // room for three things up here and this is already one of them.
            <button
              type="button"
              data-nav-item
              aria-expanded={showingDone}
              title={showingDone ? "hide what is finished" : "show what is finished"}
              onClick={() => setShowingDone((was) => !was)}
              {...stylex.props(styles.done, showingDone && styles.showing)}
            >
              {filtering
                ? ` · ${showingFinished.length} of ${done.length} done`
                : ` · ${done.length} done`}
            </button>
          )}
        </span>
        {project === undefined ? undefined : (
          <button
            type="button"
            data-nav-item
            aria-expanded={writing}
            title={writing ? "put the box away" : "write a task down here"}
            onClick={() => setWriting((was) => !was)}
            {...stylex.props(typeset.control, styles.button)}
          >
            +
          </button>
        )}
        {project === undefined ? undefined : (
          <button
            type="button"
            data-nav-item
            title={
              scope === "checkout"
                ? `showing this checkout's own list — press for all of ${project}`
                : scope === "project"
                  ? `showing ${project} — press for every project`
                  : "showing every project — press for this checkout"
            }
            onClick={() =>
              setScope((was) =>
                // A checkout to scope to is not always there: with nothing
                // open there is no workspace, and a scope naming one would
                // answer nothing with no way to say why.
                NEXT[was] === "checkout" && workspace === undefined ? "project" : NEXT[was],
              )
            }
            {...stylex.props(typeset.address, styles.scope)}
          >
            {scope === "checkout"
              ? (workspace ?? project)
              : scope === "project"
                ? project
                : "everywhere"}
          </button>
        )}
      </div>

      <div {...stylex.props(styles.filtering)}>
        <input
          type="search"
          data-nav-item
          value={query}
          placeholder="find one"
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              // Clear rather than blur: the list coming back is the thing
              // being asked for, and the field is where the next query is
              // typed anyway.
              event.preventDefault();
              setQuery("");
              return;
            }
            if (event.key === "Enter" || event.key === "ArrowDown") {
              event.preventDefault();
              intoTheList();
              return;
            }
            if (event.ctrlKey && event.code === "KeyJ") {
              event.preventDefault();
              intoTheList();
            }
          }}
          {...stylex.props(typeset.label, styles.field)}
        />
      </div>

      <AnimatePresence initial={false}>
        {writing ? (
          <motion.div
            key="writing"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={spring}
            {...stylex.props(styles.opening)}
          >
            <div {...stylex.props(styles.writing)}>
              <input
                // Focused on the way in, because the press that opened this
                // row is the same gesture as starting to type in it.
                autoFocus
                data-nav-item
                value={draft}
                placeholder="what needs doing"
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    write();
                  }
                  if (event.key === "Escape") {
                    // Both faces throw a draft away on Escape, and silently.
                    event.preventDefault();
                    setDraft("");
                    setWriting(false);
                  }
                }}
                {...stylex.props(typeset.label, styles.field)}
              />
            </div>
          </motion.div>
        ) : undefined}
      </AnimatePresence>

      <div ref={list} {...stylex.props(styles.list)}>
        {showing.length > 0 ? (
          showing.map((one) => (
            <motion.div key={one.task.key} layout="position" {...arriving}>
              <Row
                found={one}
                onSend={send(one.task)}
                onFanOut={fanOut(one.task)}
                onMove={move(one.task)}
                state={states[one.task.key] ?? "idle"}
              />
            </motion.div>
          ))
        ) : filtering ? (
          // A filter that matches nothing says so about itself. The two
          // sentences below are about the board being empty, which is a
          // different fact and would be a lie here — and the finished half
          // may still have hits, which is the one thing worth pointing at.
          <p {...stylex.props(styles.note)}>
            {showingFinished.length === 0
              ? `Nothing matches “${query.trim()}”.`
              : `Nothing outstanding matches “${query.trim()}” — ${showingFinished.length} finished ${showingFinished.length === 1 ? "one does" : "ones do"}.`}
          </p>
        ) : asked && done.length === 0 ? (
          // Two situations now, and they want different sentences. An empty
          // board is a fact about what is written down; an empty session list
          // is the older, vaguer case — an agent that finished, one that never
          // kept a list, and an agent that is not Claude Code all look alike.
          <p {...stylex.props(styles.note)}>
            {dir === undefined
              ? "Nothing written down yet. A project's TODO.md is read into this list."
              : "No outstanding tasks — neither an agent's own list nor anything written down."}
          </p>
        ) : undefined}

        <AnimatePresence initial={false}>
          {showingDone && showingFinished.length > 0 ? (
            <motion.div
              key="finished"
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: "auto", opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={spring}
              {...stylex.props(styles.opening)}
            >
              <div {...stylex.props(styles.finished)}>
                {showingFinished.map((one) => (
                  <Row
                    key={one.task.key}
                    found={one}
                    onSend={send(one.task)}
                    onFanOut={fanOut(one.task)}
                    onMove={move(one.task)}
                    state={states[one.task.key] ?? "idle"}
                  />
                ))}
              </div>
            </motion.div>
          ) : undefined}
        </AnimatePresence>
      </div>
    </div>
  );
}
