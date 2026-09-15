import type { AgentTask, Task } from "@awp-kit/protocol";

// The board, as rows.
//
// ── one read, and it used to be two ────────────────────────────────────────
//
// The panel drew two lists: the session's own — `agent-tasks.ts`'s, read off
// disk by directory — and the board, which held a project's `TODO.md`. That
// was right while awp could not see an agent's list, and it stopped being
// right when the daemon started ingesting one:
//
//   before   listTasks(dir) + listBoard(tags)   a claude task read twice, and
//                                               drawn twice, once per reader
//   after    listBoard(tags)                    one reader, and `source` says
//                                               which file it came from
//
// Reading one source twice is not the same thing as two sources agreeing to
// disagree — which is the case the note below is about, and which is still not
// deduplicated.
//
// ── why one list and not two sections ──────────────────────────────────────
//
//   claude   what an agent in a checkout wrote down for itself. Dies with the
//            session that wrote it, and is copied here before it does
//   todo     a project's TODO.md, in whichever checkout wrote it last
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

/** A row, whichever source it came from. */
export interface Listed {
  /** Unique across the list — a react key, and nothing else. */
  readonly key: string;
  /** What to show in front of the subject. Short: it sits in a 280px column. */
  readonly label: string;
  readonly subject: string;
  readonly description: string;
  readonly status: string;
  /** `todo` · `claude` · `awp`, as the store has it. */
  readonly source: string;
  /**
   * What `TaskSend` takes.
   *
   * Every row is handed over in the same shape, which is what makes the send
   * work for all of them without a second call — see that rpc's own note on
   * why the task travels by value rather than by id.
   */
  readonly task: AgentTask;
}

/** Reading order: what is underway, then the rest. */
const RANK: Record<string, number> = { in_progress: 0, blocked: 1 };

const rank = (status: string): number => RANK[status] ?? 2;

/**
 * Within a status, an agent's live queue above what is merely written down.
 *
 * The same order the two lists had when they were two reads, and for the same
 * reason: one of them describes what is happening right now.
 */
const BY_SOURCE: Record<string, number> = { claude: 0, awp: 1, todo: 2 };

const from = (source: string): number => BY_SOURCE[source] ?? 3;

const DONE = new Set(["completed", "done", "cancelled"]);

/** Whether a task is finished, and therefore a count rather than a row. */
export const finished = (status: string): boolean => DONE.has(status);

const listed = (task: Task): Listed => ({
  key: task.id,
  // `#91`, not `todo:awp#91`. The full id is what `awp_task` wants and what a
  // person would never read — the source is already said by the mark, and the
  // project by the panel's own scope.
  label: task.seq === undefined ? task.id : `#${task.seq}`,
  subject: task.subject,
  description: task.description,
  status: task.status,
  source: task.source,
  task: {
    id: task.id,
    subject: task.subject,
    description: task.description,
    status: task.status,
  },
});

/**
 * The board as rows, outstanding only, and how many are done.
 *
 * `toSorted` on a mapped array, so the order is stable: two rows of equal rank
 * keep the order the store answered in rather than whatever the engine's
 * comparator happens to do with them.
 */
export const merge = (
  board: ReadonlyArray<Task>,
): { readonly rows: ReadonlyArray<Listed>; readonly done: number } => {
  const all = board.map(listed);
  const rows = all
    .filter((row) => !finished(row.status))
    .toSorted((a, b) => rank(a.status) - rank(b.status) || from(a.source) - from(b.source));
  return { rows, done: all.length - rows.length };
};
