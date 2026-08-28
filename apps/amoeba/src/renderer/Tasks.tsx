import type { AgentTask } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { listTasks, sendTask } from "./daemon";
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
// ── done tasks are a count, not rows ───────────────────────────────────────
//
// A finished task is worth knowing the number of and almost never worth
// reading. This workspace's own list is eighty-odd completed against a
// handful outstanding, and showing them all would bury the four that matter
// under everything that no longer does. The header says how many there are,
// which is the whole of what a completed task is still good for.

/** How often the list is taken again while the panel is open. */
const POLL_MS = 4000;

/** Whether two listings differ in anything that would change a pixel. */
const same = (a: ReadonlyArray<AgentTask>, b: ReadonlyArray<AgentTask>): boolean =>
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
    fontSize: text.small,
    fontWeight: text.medium,
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
  said: { color: colors.live },
  failed: { color: colors.warn },
});

interface RowProps {
  readonly task: AgentTask;
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
          {...stylex.props(styles.subject)}
        >
          <span {...stylex.props(styles.id)}>{task.id}</span>
          {task.subject}
        </button>

        {open && task.description.trim() !== "" ? (
          <p {...stylex.props(styles.detail)}>{task.description.trim()}</p>
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
  const [tasks, setTasks] = useState<ReadonlyArray<AgentTask>>([]);
  const [asked, setAsked] = useState(false);
  const [states, setStates] = useState<Record<string, RowProps["state"]>>({});
  const held = useRef<ReadonlyArray<AgentTask>>([]);

  // Read on mount, then again on a timer.
  //
  // A poll, unlike the diff panel next door, and the difference is what the
  // thing being watched is. A diff changes when the *workspace* changes, which
  // the daemon already watches and pushes. A task list changes because the
  // agent decided something — there is nothing on disk to watch that is not
  // this same directory, and no event to subscribe to. So it is asked again.
  //
  // Only while the panel is mounted, which Base UI makes cheap: a hidden tab
  // is unmounted, so a panel nobody is looking at is not polling.
  useEffect(() => {
    let live = true;
    const take = () => {
      if (dir === undefined) {
        setTasks([]);
        setAsked(true);
        return;
      }
      listTasks(dir)
        .then((got) => {
          if (!live) {
            return;
          }
          setAsked(true);
          // Compared before it is stored, or every tick replaces the array and
          // re-renders a list that has not changed — which on an open row also
          // costs the description's layout.
          if (!same(held.current, got)) {
            held.current = got;
            setTasks(got);
          }
        })
        .catch(() => {
          // The daemon is gone. The bar already says so, and the last good
          // list is better than an error in its place.
          if (live) {
            setAsked(true);
          }
        });
    };
    take();
    const timer = setInterval(take, POLL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [dir]);

  const open = tasks.filter((task) => task.status !== "completed");
  const done = tasks.length - open.length;

  const send = (task: AgentTask) => {
    if (project === undefined || workspace === undefined) {
      return;
    }
    return () => {
      setStates((was) => ({ ...was, [task.id]: "sending" }));
      sendTask(project, workspace, task)
        .then(() => setStates((was) => ({ ...was, [task.id]: "sent" })))
        .catch(() => setStates((was) => ({ ...was, [task.id]: "failed" })));
    };
  };

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.head)}>
        <span {...stylex.props(styles.count)}>
          {open.length === 0 ? "nothing to do" : `${open.length} to do`}
          {done === 0 ? "" : ` · ${done} done`}
        </span>
      </div>

      <div {...stylex.props(styles.list)}>
        {dir === undefined ? (
          <p {...stylex.props(styles.note)}>Open a session to see what its agent is planning.</p>
        ) : open.length > 0 ? (
          open.map((task) => (
            <Row key={task.id} task={task} onSend={send(task)} state={states[task.id] ?? "idle"} />
          ))
        ) : asked ? (
          // Three different situations, one sentence, and that is honest: an
          // agent that has finished its list, one that never kept a list, and
          // a workspace whose agent is not Claude Code are indistinguishable
          // from here. See `readTasks`.
          <p {...stylex.props(styles.note)}>
            No outstanding tasks. This is the agent&rsquo;s own list, so it is empty until the agent
            writes one.
          </p>
        ) : undefined}
      </div>
    </div>
  );
}
