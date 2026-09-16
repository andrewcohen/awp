import type { Job } from "@awp-kit/jobs";

// When the window should go and look again.
//
// A job is what creates a session, a workspace and a thread claim, so a job is
// the one thing that makes something new to read. This is the key the effect
// in App watches.
//
// ── it used to wait for the job to STOP, and that is the bug ───────────────
//
// The key was the ids of the jobs that had *finished*, on the premise that a
// finished job is when there is something new. The premise is wrong, and a
// chat-face thread is what made it obvious:
//
//   1 workspace   jj workspace add
//   2 bookmark    jj bookmark set
//   3 session     zmx run -d          ← the sidebar could draw a row from here
//   4 claim       the thread takes it ← and the row belongs under its thread
//   5 brief       Chat.brief — sends, then WAITS for the turn to end, up to 20
//                 minutes. The job is `running` for the whole of the agent's
//                 first answer
//
// So on a chat-face create the two things the sidebar needs land at steps 3
// and 4, and the job does not go terminal until the agent has finished
// answering — or, if it stopped to ask a permission nobody can see, not at
// all. What that looked like: a thread on the strip reading **"nothing yet"**
// with a workspace on disk, a session running in it and a briefed agent
// halfway through a turn. Reported as "i cant connect to the chat", which is
// the honest description — the row is how you get into it.
//
// The terminal face hid it, because `zmx send` returns immediately and the job
// was terminal a second after the claim.
//
// ── so the key moves on progress, not on completion ───────────────────────
//
// `done.length` is a step boundary, and every step boundary is a moment the
// record was saved and the feed pushed — see `JobChanges`. The status is in
// the key as well, so the last transition into a terminal state still fires.
//
// The cost is a re-read of the sessions and the threads per step of every job,
// which is four extra reads for a create — two socket round trips each,
// against a daemon holding both answers in memory.

/**
 * A signature of where every job has got to: which ones, what state, how far.
 *
 * Three reasons it is this and not something smaller:
 *
 *   the ids       a count only moves when a job finishes *and* nothing else
 *                 has left the list. Clearing the panel deletes terminal rows,
 *                 so a count falls and the next completion returns it to a
 *                 number it has already been — no change, therefore no
 *                 refresh, for exactly the job somebody is waiting on
 *   the status    queued → running → succeeded, each worth a look
 *   done.length   the step boundaries, which is where the session and the
 *                 claim actually appear
 *
 * Sorted, because the listing and the change feed do not agree on order and an
 * order-dependent key would re-read on nothing at all.
 */
export const progressKey = (jobs: ReadonlyArray<Job>): string =>
  jobs
    .map((job) => `${job.id}:${job.status}:${String(job.done?.length ?? 0)}`)
    .toSorted()
    .join(",");
