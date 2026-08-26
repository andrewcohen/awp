import type { Job } from "@awp-kit/jobs";
import { useEffect, useRef, useState } from "react";
import { listJobs, watchJobs } from "./daemon";

// The jobs the window knows about, kept current.
//
// A listing once, then a stream — not a poll. The interesting moments in a job
// are short: a step starting, an attempt failing, a rollback finishing. A list
// refreshed on a timer shows the state between them and nothing else, which is
// how a job that took two seconds ends up looking like a job that never ran.
//
// Held as a Map keyed by id and merged, rather than replaced wholesale, because
// the stream carries one record at a time and the listing carries all of them.
// Merging means the two cannot disagree: whichever arrives last for a given id
// is the newest state of it, and everything else is left alone.

export interface JobsView {
  readonly jobs: ReadonlyArray<Job>;
  /** Absent while the first listing is in flight or has succeeded. */
  readonly failure: string | undefined;
  /**
   * Take the listing again, **replacing** what is held rather than merging.
   *
   * The one operation merging cannot express. Every other change arrives on the
   * stream as a record, and a record is something to merge in; a job that has
   * been *deleted* arrives as nothing at all, and there is no record whose
   * absence a merge could notice. So clearing has to re-read the whole list and
   * throw away what is no longer in it.
   */
  readonly refresh: () => void;
}

const newestFirst = (a: Job, b: Job): number =>
  b.createdAt.getTime() - a.createdAt.getTime() || b.id.localeCompare(a.id);

export function useJobs(): JobsView {
  const [held, setHeld] = useState<ReadonlyMap<string, Job>>(new Map());
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  const replace = () => {
    listJobs()
      .then((listed) => {
        if (alive.current) {
          setHeld(new Map(listed.map((job) => [job.id, job] as const)));
        }
      })
      .catch(() => {
        // Deliberately silent. The list on screen is still the last good one,
        // and a failed refresh after a successful clear is not worth replacing
        // it with an error.
      });
  };

  useEffect(() => {
    let live = true;
    alive.current = true;
    const merge = (arriving: ReadonlyArray<Job>) => {
      if (!live) {
        return;
      }
      setHeld((all) => new Map([...all, ...arriving.map((job) => [job.id, job] as const)]));
    };

    // Subscribed before the listing, deliberately. The other order has a window
    // between the list being taken and the stream being joined, and a job that
    // changed inside it would sit at a stale state until it changed again — for
    // a job that finished in that window, forever.
    const stop = watchJobs((job) => merge([job]));

    listJobs()
      .then((listed) => {
        merge(listed);
        if (live) {
          setFailure(undefined);
        }
      })
      .catch((error: unknown) => {
        if (live) {
          setFailure(error instanceof Error ? error.message : String(error));
        }
      });

    return () => {
      live = false;
      alive.current = false;
      stop();
    };
  }, []);

  return { jobs: [...held.values()].toSorted(newestFirst), failure, refresh: replace };
}

/** How many jobs are in each state worth saying out loud. */
export const tally = (
  jobs: ReadonlyArray<Job>,
): { readonly running: number; readonly failed: number; readonly dirty: number } => ({
  running: jobs.filter((job) => job.status === "running" || job.status === "queued").length,
  failed: jobs.filter((job) => job.status === "failed").length,
  // Counted separately from `failed` because it is a different request of the
  // reader: a failed job wants a retry, a dirty one wants a person to go and
  // look at what its rollback could not undo.
  dirty: jobs.filter((job) => job.cleanup === "dirty").length,
});
