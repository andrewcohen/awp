import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { Effect } from "effect";

// The task list the agent in a workspace is keeping, read off disk.
//
// ── why disk, and not the agent ────────────────────────────────────────────
//
// There is no channel to ask an agent what it is planning. amoeba's only wire
// into a session is a pty carrying bytes a person typed, and a question typed
// into a terminal comes back as prose in a scrollback. Claude Code writes its
// task list to a file as it goes, so the file is the answer — and it is a far
// better one than asking would be, because it is current whether or not the
// agent is at a prompt.
//
// This is somebody else's format, and nothing here writes to it. A task read
// out of it is shown and quoted; it is never marked done from this side. That
// is deliberate rather than unfinished — the agent owns its own list, and two
// writers of one file is the shape `claude-trust.ts` needed a lock for.
//
// ── how a directory finds its tasks ────────────────────────────────────────
//
//   /Users/…/workspaces/thicket/lantern       the workspace
//     └─ ~/.claude/projects/<slug>/           slug: every non-alphanumeric → -
//          └─ <session>.jsonl                 one per session run in there
//               └─ ~/.claude/tasks/<session>/ that session's list, if it kept one
//                    └─ 1.json 2.json …
//
// **The slug rule was measured, not guessed.** Three candidate rules agreed on
// every path in this tree, because none of them contains an underscore — and
// `sanitize` can produce one, so the difference is reachable. A transcript
// elsewhere on this machine recorded a `cwd` that settled it:
//
//   /Users/…/workspaces/ses_ABC123  →  -Users-…-workspaces-ses-ABC123
//                            └─ an underscore, and it became a dash
//
// ── which session, when a directory has had several ────────────────────────
//
// A workspace accumulates transcripts: a session per launch, plus one per
// subagent, all in the same project directory. The newest transcript is
// therefore *not* the agent in the pane — a subagent that ran a minute ago
// beats the session that spawned it.
//
// So the pick is by the **task directory's** modification time, over the
// sessions that have one at all. That asks the question actually being put —
// whose list is live — and skips every session that never kept one, which on
// this machine is most of them.

/** One task, as the panel needs it. Fields this does not use are dropped. */
export interface AgentTask {
  readonly id: string;
  readonly subject: string;
  readonly description: string;
  /** `pending`, `in_progress`, `completed`, or whatever else it gains. */
  readonly status: string;
}

/**
 * The directory name Claude Code files a working directory's sessions under.
 *
 * Every non-alphanumeric byte becomes a dash, the leading slash included — so
 * an absolute path always yields a leading dash. Resolved first, because a
 * relative path would produce a slug that matches nothing and read as "no
 * tasks" rather than as a mistake.
 */
export const projectSlug = (dir: string): string => resolve(dir).replaceAll(/[^A-Za-z0-9]/gu, "-");

/** Ordered so the list reads as a queue: what is being done, then what is next. */
const RANK: Record<string, number> = { in_progress: 0, pending: 1 };

/**
 * Sort key. Within a status, by the numeric id the agent gave it.
 *
 * Numeric, because the ids are counted rather than named and `10.json` sorts
 * before `2.json` as text — which puts a task list in an order nobody wrote.
 */
const order = (a: AgentTask, b: AgentTask): number =>
  (RANK[a.status] ?? 9) - (RANK[b.status] ?? 9) ||
  (Number(a.id) || 0) - (Number(b.id) || 0) ||
  a.id.localeCompare(b.id);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

/** One `<n>.json`, if it is one. Anything unrecognisable is skipped. */
const toTask = (raw: unknown, fallbackId: string): AgentTask | undefined => {
  if (!isRecord(raw)) {
    return undefined;
  }
  const subject = typeof raw["subject"] === "string" ? raw["subject"].trim() : "";
  if (subject === "") {
    return undefined;
  }
  return {
    id: typeof raw["id"] === "string" ? raw["id"] : fallbackId,
    subject,
    description: typeof raw["description"] === "string" ? raw["description"] : "",
    status: typeof raw["status"] === "string" ? raw["status"] : "pending",
  };
};

/**
 * Every task the newest live list in `dir` holds, most urgent first.
 *
 * Empty for a directory no agent has run in, for one whose agent kept no list,
 * and for a machine where Claude Code is not the agent at all. None of those
 * is an error: the panel says "nothing here" and that is the truth in all
 * three cases. A permission problem is the one that would be worth
 * distinguishing, and it is not distinguishable from absence through this
 * API — so it reads as empty too, which is the honest failure to have.
 */
export const readTasks = (
  dir: string,
  home: string = homedir(),
): Effect.Effect<ReadonlyArray<AgentTask>> =>
  Effect.promise(async () => {
    const fs = await import("node:fs/promises");

    const sessions = await fs
      .readdir(join(home, ".claude", "projects", projectSlug(dir)))
      .catch(() => [] as Array<string>);

    // The newest non-empty list among them. `stat` on the directory rather
    // than on its newest file: a list whose last change was a *deletion* has
    // no file carrying that time, and the directory does.
    let best: { at: number; path: string; files: Array<string> } | undefined;
    for (const file of sessions) {
      if (!file.endsWith(".jsonl")) {
        continue;
      }
      const path = join(home, ".claude", "tasks", file.slice(0, -".jsonl".length));
      const files = await fs.readdir(path).catch(() => [] as Array<string>);
      if (files.length === 0) {
        continue;
      }
      const at = await fs
        .stat(path)
        .then((info) => info.mtimeMs)
        .catch(() => 0);
      if (best === undefined || at > best.at) {
        best = { at, path, files };
      }
    }
    if (best === undefined) {
      return [];
    }

    const held = best;
    const read = await Promise.all(
      held.files
        .filter((file) => file.endsWith(".json"))
        .map(async (file) => {
          const raw = await fs.readFile(join(held.path, file), "utf8").catch(() => "");
          try {
            return toTask(JSON.parse(raw), file.slice(0, -".json".length));
          } catch {
            // Half-written. The agent appends to this directory while the
            // panel reads it, so a torn file is an ordinary event rather than
            // a fault — and one task missing for a second is much better than
            // the whole list failing. Falling out of the catch answers
            // `undefined`, which the filter below drops.
          }
        }),
    );

    return read.filter((task): task is AgentTask => task !== undefined).toSorted(order);
  });

/**
 * One task, as something to say to an agent.
 *
 * Shaped like `notePrompt`: address first, instruction last. The id is in it
 * because the agent's own list is keyed by it — a task named only by its
 * subject is one the agent has to find again by reading.
 *
 * The description is quoted whole rather than capped. A page note caps the
 * element's text because pointing at `<body>` would paste a page into a
 * terminal; a task description is something a person wrote on purpose, and
 * cutting it would drop the half that says what "done" means.
 *
 * **"next", not "now".** A prompt typed into a running agent arrives in the
 * middle of whatever it was doing, and "now" reads as an instruction to drop
 * it. That is almost never what pressing send means — the button is for adding
 * to the queue, not for interrupting — and an agent that abandons a half-made
 * change to start something else has cost more than the task was worth.
 */
export const taskPrompt = (task: AgentTask): string =>
  [
    "— a task from this workspace's list",
    `task ${task.id}: ${task.subject}`,
    ...(task.description.trim() === "" ? [] : ["", task.description.trim()]),
    "",
    "Please pick this up next — finish what you are doing first.",
  ].join("\n");
