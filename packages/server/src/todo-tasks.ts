import { Effect } from "effect";
import { readFile, readdir, stat } from "node:fs/promises";
import { join } from "node:path";

// A project's `TODO.md`, read as tasks.
//
// ── why a file, and why this one ────────────────────────────────────────────
//
// This repository keeps its own list in `TODO.md` and says why in its first
// line: "Every task that is not finished, written out so that losing the task
// list does not lose the reasoning in it." Which makes it exactly the thing a
// task store wants — durable, versioned, and already the place the arguments
// live — and it is the only source of tasks on this machine that outlives a
// session.
//
// Nothing here writes back. The file is a person's, and this reads it the way
// `agent-tasks.ts` reads Claude Code's: a task is shown and quoted, never
// marked done from this side.
//
// ── the parse rule is the heading, and it has to be total ───────────────────
//
//   ## 113. Dragging a divider near the top moves the window     ← a task
//   ## The spike, 2026-08-28                                     ← NOT one
//   ## Landed                                                    ← NOT one
//
// An unnumbered `##` is a *sub-heading of the task above it* — #91 has three —
// so a rule that took every heading would have reported the spike and its two
// sections as three tasks with no bodies. A number is what makes a heading a
// task, which is also what gives every task a stable key.
//
// One heading on this machine was a real task without a number and was given
// one rather than special-cased here: a parser with an exception list is a
// parser that gets it wrong for the next file.

/** One task, read out of a `TODO.md`. */
export interface TodoTask {
  /** The number in the heading. The stable half of its key. */
  readonly number: number;
  readonly subject: string;
  /** Everything under the heading, verbatim, sub-headings included. */
  readonly description: string;
  /** `pending`, or `in_progress` when the heading says so. */
  readonly status: string;
}

/**
 * A trailing marker on a heading, as `#91` carries today.
 *
 * ```
 *   ## 91. Run the agent under ACP, not only in a terminal · in progress
 * ```
 *
 * Read rather than assumed absent, because a task the file says is underway
 * appearing as pending is the one difference a reader would act on. Anything
 * after the separator that is not recognised is left on the subject — losing a
 * word off a title is worse than an unread marker.
 */
const MARKERS: Record<string, string> = {
  "in progress": "in_progress",
  wip: "in_progress",
  blocked: "blocked",
};

const HEADING = /^## (\d+)\.\s+(.+)$/u;

/** Split a heading's text into its subject and whatever status it declares. */
export const subjectOf = (text: string): { readonly subject: string; readonly status: string } => {
  const at = text.lastIndexOf("·");
  if (at === -1) {
    return { subject: text.trim(), status: "pending" };
  }
  const marker =
    MARKERS[
      text
        .slice(at + 1)
        .trim()
        .toLowerCase()
    ];
  return marker === undefined
    ? { subject: text.trim(), status: "pending" }
    : { subject: text.slice(0, at).trim(), status: marker };
};

/**
 * Every task in a `TODO.md`, in the order the file has them.
 *
 * The preamble above the first numbered heading is dropped: it is the file's
 * note to its own reader — how many are open, what was hand-edited — and not a
 * task. A file with no numbered headings therefore yields nothing rather than
 * one task made of the whole document.
 */
export const parseTodo = (markdown: string): ReadonlyArray<TodoTask> => {
  const tasks: TodoTask[] = [];
  let open:
    | { readonly number: number; readonly subject: string; readonly status: string }
    | undefined;
  let body: string[] = [];

  const close = () => {
    if (open !== undefined) {
      tasks.push({ ...open, description: body.join("\n").trim() });
    }
  };

  for (const line of markdown.split("\n")) {
    const found = HEADING.exec(line);
    if (found === null) {
      body.push(line);
      continue;
    }
    close();
    open = { number: Number(found[1]), ...subjectOf(found[2] ?? "") };
    body = [];
  }
  close();
  return tasks;
};

/**
 * Where a project's `TODO.md` might be.
 *
 * The root is the obvious answer and is not enough on its own: a project's
 * root is its *default* jj workspace, and `TODO.md` is a working-copy file —
 * so reading the root reads whatever revision that one checkout is parked on.
 * Measured on this machine, and it is this repository that showed it:
 *
 * ```
 *   project awp, root ~/go/src/…/awp     the default workspace, on an old commit
 *   TODO.md there                        absent
 *   TODO.md in the workspace being        46 tasks
 *   worked in
 * ```
 *
 * So every awp workspace of the project is a candidate too. They are found by
 * the directory convention rather than by asking jj: `workspacePath`'s shape
 * is already the one thing this repo relies on to recover a session's identity
 * when it carries no labels, and a `jj workspace list` per project per sweep
 * is a subprocess for an answer a `readdir` already has.
 */
export const candidates = (root: string, project: string, home: string): ReadonlyArray<string> => [
  root,
  join(home, ".awp", "workspaces", project),
];

/**
 * The newest `TODO.md` among a project's checkouts, and what it says.
 *
 * **Newest by modification time**, which is the same rule `agent-tasks.ts`
 * already applies to pick among a directory's sessions — and for the same
 * reason: several candidates legitimately have one, they disagree because they
 * are on different revisions, and the most recently written is the one
 * somebody is keeping. Taking the root unconditionally reads a stale branch;
 * taking all of them would put one project's task list in the store several
 * times over, at several revisions, with nothing able to say which row was
 * true.
 *
 * A project with none is not a failure and says nothing. Most repositories on
 * a real machine keep no such file, and the inbox's rule applies: a sentence
 * that is true and unactionable for every project trains a person to stop
 * reading them.
 */
export const readTodo = (options: {
  readonly root: string;
  readonly project: string;
  readonly home: string;
}): Effect.Effect<ReadonlyArray<TodoTask>> =>
  Effect.promise(async () => {
    const found: { path: string; at: number }[] = [];
    for (const dir of candidates(options.root, options.project, options.home)) {
      // A candidate is either a checkout or a directory holding several, and
      // the second is what `~/.awp/workspaces/<project>` is. Both are asked
      // the same way rather than branched on, because a project root could
      // itself be either.
      for (const path of [join(dir, "TODO.md"), ...(await inside(dir))]) {
        const when = await mtime(path);
        if (when !== undefined) {
          found.push({ path, at: when });
        }
      }
    }
    const newest = found.toSorted((a, b) => b.at - a.at)[0];
    if (newest === undefined) {
      return [];
    }
    return parseTodo(await readFile(newest.path, "utf8"));
  }).pipe(Effect.catchCause(() => Effect.succeed([])));

/** Every `<dir>/*\/TODO.md`. Empty when `dir` is not a directory. */
const inside = async (dir: string): Promise<ReadonlyArray<string>> => {
  try {
    const entries = await readdir(dir, { withFileTypes: true });
    return entries.filter((one) => one.isDirectory()).map((one) => join(dir, one.name, "TODO.md"));
  } catch {
    return [];
  }
};

const mtime = async (path: string): Promise<number | undefined> => {
  try {
    return (await stat(path)).mtimeMs;
  } catch {
    return undefined;
  }
};
