import { type Job, isTerminal } from "@awp-kit/jobs";

// When the window should go and look again.
//
// A job is what creates a session, a workspace and a thread claim, so a job
// stopping is the one moment there is something new to read. This is the key
// that effect watches.

/**
 * A signature of the jobs that have stopped — **which** ones, not how many.
 *
 * It was `.length`, and a count only moves when a job finishes *and* nothing
 * else has left the list. Clearing the panel deletes terminal jobs, so the
 * count falls — and the next job to finish brings it back to a number it has
 * already been, which is no change at all and therefore no refresh.
 *
 * The ids answer both: a completion adds one, a clear removes some, and either
 * changes the string. Sorted, because the listing and the change feed do not
 * agree on order and an order-dependent key would refresh on nothing.
 */
export const finishedKey = (jobs: ReadonlyArray<Job>): string =>
  jobs
    .filter((job) => isTerminal(job.status))
    .map((job) => job.id)
    .toSorted()
    .join(",");
