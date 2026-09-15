import type { Task } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { listBoard, onReconnect, sendTask, watchTasks } from "./daemon";
import { Markdown } from "./Markdown";
import { type Listed, merge } from "./tasklist";
import { typeset } from "./typeset";
import { colors, space, text } from "./tokens.stylex";

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
  list: { flex: 1, minHeight: 0, overflowY: "auto", overflowX: "hidden", padding: "0.3rem 0" },
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
});

interface RowProps {
  readonly task: Listed;
  /** Absent for a session awp did not make: there is no agent to address. */
  readonly onSend: (() => void) | undefined;
  readonly state: "idle" | "sending" | "sent" | "failed";
}

function Row({ task, onSend, state }: RowProps) {
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
      <span aria-hidden {...stylex.props(styles.dot, doing ? styles.doing : styles.todo)}>
        {doing ? "●" : "○"}
      </span>

      <div {...stylex.props(styles.body)}>
        <button
          type="button"
          data-nav-item
          aria-expanded={open}
          title={open ? "hide what the task says" : "show what the task says"}
          onClick={() => setOpen((was) => !was)}
          {...stylex.props(typeset.control, styles.subject)}
        >
          <span {...stylex.props(styles.id)}>{task.label}</span>
          {task.subject}
        </button>

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
}

export function Tasks({ dir, project, workspace }: TasksProps) {
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
  const held = useRef<ReadonlyArray<Task>>([]);

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
          {rows.length === 0 ? "nothing to do" : `${rows.length} to do`}
          {done === 0 ? "" : ` · ${done} done`}
        </span>
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

      <div {...stylex.props(styles.list)}>
        {rows.length > 0 ? (
          rows.map((task) => (
            <Row
              key={task.key}
              task={task}
              onSend={send(task)}
              state={states[task.key] ?? "idle"}
            />
          ))
        ) : asked ? (
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
      </div>
    </div>
  );
}
