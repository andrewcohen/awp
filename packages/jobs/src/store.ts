// Where jobs are kept, and the in-memory one.
//
// A tag, because the runner's behaviour is the interesting thing and it should
// be testable without a file on disk — every test in this package runs against
// the memory store below, and the sqlite store is checked separately against
// this same contract.
//
// The interface is deliberately not a general-purpose table. `put` writes a
// whole record rather than a patch: a job is small, the runner always has the
// current one in hand, and a partial update is where two writers quietly
// disagree about a field neither of them mentioned.

import { Context, Data, Effect, Layer, Ref } from "effect";
import type { Job, JobId } from "./job";

/** The store could not answer. Always a defect from the caller's side. */
export class StoreError extends Data.TaggedError("StoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

export class JobStore extends Context.Service<
  JobStore,
  {
    /** Insert or replace, whole. */
    readonly put: (job: Job) => Effect.Effect<void, StoreError>;
    readonly get: (id: JobId) => Effect.Effect<Job | undefined, StoreError>;
    /**
     * The job holding this idempotency key, if any.
     *
     * The lookup that makes enqueuing idempotent. Separate from `get` because
     * the key is the caller's word for the job and the id is the store's, and
     * only one of them exists before the job does.
     */
    readonly byKey: (key: string) => Effect.Effect<Job | undefined, StoreError>;
    /** Newest first. */
    readonly list: () => Effect.Effect<ReadonlyArray<Job>, StoreError>;
    /** Add lines to a job's log. */
    readonly append: (id: JobId, lines: ReadonlyArray<string>) => Effect.Effect<void, StoreError>;
    readonly log: (id: JobId) => Effect.Effect<ReadonlyArray<string>, StoreError>;
  }
>()("awp/JobStore") {}

/**
 * The order a listing comes back in, and the reason it is stated once.
 *
 * Newest first, ties broken by id — which is date-prefixed, so the tiebreak is
 * still chronological within a day and is at least stable when two jobs were
 * created in the same millisecond. Both stores sort with this, so a test
 * written against one is a test of the other.
 */
export const newestFirst = (a: Job, b: Job): number =>
  b.createdAt.getTime() - a.createdAt.getTime() || b.id.localeCompare(a.id);

interface Held {
  readonly job: Job;
  readonly log: ReadonlyArray<string>;
}

/**
 * How much of a job's log is kept.
 *
 * A cap rather than a rotation, because the log here is for a person reading a
 * failure and the useful part of it is the end. The Go implementation kept the
 * whole thing in the record and grew a 4MB json file watching CI.
 */
export const LOG_LINES = 500;

const keep = (lines: ReadonlyArray<string>): ReadonlyArray<string> =>
  lines.length <= LOG_LINES ? lines : lines.slice(lines.length - LOG_LINES);

export const makeMemory = Effect.gen(function* () {
  const held = yield* Ref.make(new Map<JobId, Held>());

  const update = (id: JobId, change: (held: Held) => Held): Effect.Effect<void, StoreError> =>
    Ref.update(held, (all) => {
      const current = all.get(id);
      if (current === undefined) {
        return all;
      }
      return new Map([...all, [id, change(current)] as const]);
    });

  return {
    put: (job: Job) =>
      Ref.update(held, (all) => {
        return new Map([...all, [job.id, { log: all.get(job.id)?.log ?? [], job }] as const]);
      }),

    get: (id: JobId) => Ref.get(held).pipe(Effect.map((all) => all.get(id)?.job)),

    byKey: (key: string) =>
      Ref.get(held).pipe(
        Effect.map((all) =>
          [...all.values()].map((entry) => entry.job).find((job) => job.key === key),
        ),
      ),

    list: () =>
      Ref.get(held).pipe(
        Effect.map((all) => [...all.values()].map((entry) => entry.job).toSorted(newestFirst)),
      ),

    append: (id: JobId, lines: ReadonlyArray<string>) =>
      update(id, (entry) => ({ ...entry, log: keep([...entry.log, ...lines]) })),

    log: (id: JobId) => Ref.get(held).pipe(Effect.map((all) => all.get(id)?.log ?? [])),
  };
});

/** Kept for the life of the process, and no longer. */
export const layerMemory = Layer.effect(JobStore)(makeMemory);
