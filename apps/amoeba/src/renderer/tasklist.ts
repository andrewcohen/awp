import type { AgentTask, Task } from "@awp-kit/protocol";

// Two lists of tasks, drawn as one.
//
// ── why one list and not two sections ──────────────────────────────────────
//
//   the session's   what the agent in this checkout wrote down for itself,
//                   read off disk. Dies with the session
//   the board       what awp holds — a project's TODO.md today, tagged, and
//                   durable
//
// They overlap, and a person scanning this column is asking "what should
// happen next", not "which file did this come from". Two headed sections make
// the provenance the primary axis, which is the one nobody is scanning by. So
// the source is a mark on a row and the order is the queue.
//
// Nothing is deduplicated, deliberately. The same work being a TODO.md entry
// *and* a session task is common and the two entries are not the same object —
// different ids, different statuses, and the agent's copy is the one it is
// actually working from. Merging them would have to pick a status, and picking
// wrong is worse than a row appearing twice with two honest states.

/** A row, whichever list it came from. */
export interface Listed {
  /** Unique across both sources — a react key, and nothing else. */
  readonly key: string;
  /** What to show in front of the subject. Short: it sits in a 280px column. */
  readonly label: string;
  readonly subject: string;
  readonly description: string;
  readonly status: string;
  readonly source: "session" | "board";
  /**
   * What `TaskSend` takes.
   *
   * A board task is handed over in the same shape, which is what makes the
   * send work for both without a second call — see that rpc's own note on why
   * the task travels by value rather than by id.
   */
  readonly task: AgentTask;
}

/** Reading order: what is underway, then the rest. */
const RANK: Record<string, number> = { in_progress: 0, blocked: 1 };

const rank = (status: string): number => RANK[status] ?? 2;

const DONE = new Set(["completed", "done", "cancelled"]);

/** Whether a task is finished, and therefore a count rather than a row. */
export const finished = (status: string): boolean => DONE.has(status);

const fromSession = (task: AgentTask): Listed => ({
  key: `session:${task.id}`,
  label: task.id,
  subject: task.subject,
  description: task.description,
  status: task.status,
  source: "session",
  task,
});

const fromBoard = (task: Task): Listed => ({
  key: task.id,
  // `#91`, not `todo:awp#91`. The full id is what `awp_task` wants and what a
  // person would never read — the source is already said by the mark, and the
  // project by the panel's own scope.
  label: task.seq === undefined ? task.id : `#${task.seq}`,
  subject: task.subject,
  description: task.description,
  status: task.status,
  source: "board",
  task: {
    id: task.id,
    subject: task.subject,
    description: task.description,
    status: task.status,
  },
});

/**
 * The two lists as one, outstanding rows only, and how many are done.
 *
 * The session's tasks come first before the sort, so within one status the
 * agent's own queue is above what is merely written down — it is the list that
 * describes what is happening right now.
 *
 * `toSorted` on a mapped array, so the order is stable: two rows of equal rank
 * keep the order they were concatenated in rather than whatever the engine's
 * comparator happens to do with them.
 */
export const merge = (
  session: ReadonlyArray<AgentTask>,
  board: ReadonlyArray<Task>,
): { readonly rows: ReadonlyArray<Listed>; readonly done: number } => {
  const all = [...session.map(fromSession), ...board.map(fromBoard)];
  const rows = all
    .filter((row) => !finished(row.status))
    .toSorted((a, b) => rank(a.status) - rank(b.status));
  return { rows, done: all.length - rows.length };
};
