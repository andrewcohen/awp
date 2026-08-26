// What a job is, as a record.
//
// A job is work that outlives the thing that asked for it. Creating a
// workspace, watching CI, running a review: none of them should end because a
// window closed, and all of them are worth seeing afterwards whether they
// worked or not. That second half is most of the reason this package exists —
// an action that leaves no trace cannot be retried and cannot be explained.
//
// Written as a Schema rather than as an interface because this record has two
// consumers that both need it decoded from text: the store, which keeps it as a
// row, and the protocol, which sends it to a client. A hand-written type would
// have needed a parser beside it, and the parser is the part that drifts.

import { Schema } from "effect";

/** `<yyyymmdd>-<four random characters>`, so a listing sorts by day. */
export type JobId = string;

/**
 * Where a job is in its life.
 *
 * Deliberately five values and not six. "Rolled back" is not a status: it is a
 * failure whose compensation happened to succeed, and a user asking about a
 * failed job wants to know what broke first and what was left behind second.
 * That second question is {@link Job.cleanup}.
 */
export const JobStatus = Schema.Literals([
  /** Accepted, not yet picked up. */
  "queued",
  /** A fiber is executing steps right now. */
  "running",
  /** Every step completed. */
  "succeeded",
  /** A step failed and no attempts remain. */
  "failed",
  /** Someone asked it to stop. */
  "cancelled",
]);

export type JobStatus = (typeof JobStatus)["Type"];

const TERMINAL: ReadonlySet<JobStatus> = new Set<JobStatus>(["succeeded", "failed", "cancelled"]);

/** True once the runner will not move this job again without being asked. */
export const isTerminal = (status: JobStatus): boolean => TERMINAL.has(status);

/**
 * What the world looks like after a job that did not finish.
 *
 * Only set on a job that ended without succeeding, and it is the field that
 * decides whether a human has to go and look. `clean` says every completed step
 * was undone, so nothing is half-built and a retry starts from nothing.
 * `dirty` says an undo itself failed — which is the one outcome this package
 * cannot recover from on its own, and therefore the one it must say out loud.
 */
export const Cleanup = Schema.Literals(["clean", "dirty"]);

export type Cleanup = (typeof Cleanup)["Type"];

export const Job = Schema.Struct({
  id: Schema.String,
  /** Which {@link JobKind} knows how to run this. */
  kind: Schema.String,
  /** One line, for a list. Computed from the input when the job is enqueued. */
  title: Schema.String,
  /**
   * The caller's idempotency key, if it gave one.
   *
   * Enqueuing twice with the same key returns the first job rather than making
   * a second. This is the outer half of "jobs are idempotent" — the half that
   * stops a double-click from creating two workspaces. The inner half is that
   * each step is safe to run twice, which is a promise the step makes.
   */
  key: Schema.UndefinedOr(Schema.String),
  /** Whatever the kind needs to do the work, as it was given. */
  input: Schema.Unknown,

  status: JobStatus,
  /** Attempts started so far. The first run is attempt 1. */
  attempt: Schema.Int,
  /** How many are allowed before failure becomes terminal. */
  attempts: Schema.Int,

  /**
   * Steps completed and not since undone, in the order they completed.
   *
   * This is the resume point. A retry runs the first step *not* in here, so a
   * job that died at step four does not redo the first three — and because
   * compensation empties this list, a job that was rolled back starts clean
   * instead of resuming into a world that no longer matches.
   */
  done: Schema.Array(Schema.String),
  /** The step being run right now, or the one that failed. */
  step: Schema.UndefinedOr(Schema.String),

  /** Why it failed, in a sentence a person can act on. */
  error: Schema.UndefinedOr(Schema.String),
  /** See {@link Cleanup}. Absent unless the job ended without succeeding. */
  cleanup: Schema.UndefinedOr(Cleanup),

  createdAt: Schema.Date,
  startedAt: Schema.UndefinedOr(Schema.Date),
  endedAt: Schema.UndefinedOr(Schema.Date),
});

export type Job = (typeof Job)["Type"];

/**
 * A new id.
 *
 * Date-prefixed so `ls` and a plain lexical sort both put today's jobs at the
 * bottom, which is the property the Go implementation's ids had and the only
 * one worth keeping. The suffix is four characters of base36 — enough that two
 * jobs enqueued in the same millisecond do not collide, and short enough to be
 * read aloud.
 */
export const jobId = (now: Date, random: number): JobId => {
  const day = [
    now.getFullYear().toString().padStart(4, "0"),
    (now.getMonth() + 1).toString().padStart(2, "0"),
    now.getDate().toString().padStart(2, "0"),
  ].join("");
  const suffix = Math.floor(random * 36 ** 4)
    .toString(36)
    .padStart(4, "0");
  return `${day}-${suffix}`;
};
