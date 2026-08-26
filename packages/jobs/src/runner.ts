// The runner: what actually happens to a job between enqueue and a status.
//
// Everything interesting in this package is the reconciliation of two things
// that want opposite behaviour on failure.
//
//   resume        a retry should not redo work that succeeded
//   compensate    a failure should not leave half a thing behind
//
// Doing both at once is incoherent — you cannot undo step two and also skip it
// next time — so they are separated by *when*:
//
//   ┌─ attempt fails, attempts remain ─────────────────────────────────────┐
//   │  status → queued, sleep the backoff, run again from the first step   │
//   │  not in `done`. Nothing is undone. This is why a step's `run` must   │
//   │  be safe to call twice: the step that failed is re-entered, possibly │
//   │  having got halfway.                                                 │
//   └──────────────────────────────────────────────────────────────────────┘
//   ┌─ attempts exhausted, or cancelled ───────────────────────────────────┐
//   │  walk `done` backwards, run each step's `undo`, emptying `done` as   │
//   │  each one succeeds. Then status → failed or cancelled.               │
//   │  `done` is emptied because the world was put back: resuming into it  │
//   │  later would be resuming into a world that no longer matches.        │
//   └──────────────────────────────────────────────────────────────────────┘
//
// Compensation stops at the first `undo` that fails, rather than pressing on.
// The undos are ordered because each assumes the ones after it already ran;
// once one has not, the rest are being asked to undo a state that never
// existed. The job is marked `cleanup: "dirty"`, and that is a thing for a
// person — which is most of why a jobs list exists at all.
//
// ── interruption is two different events ──────────────────────────────────
// A cancelled job and a daemon shutting down both arrive as an interrupted
// fiber, and they must not be treated the same: one should compensate, the
// other should leave the record alone so the next start picks it up. Nothing
// about the interrupt itself distinguishes them, so `cancel` records the
// intent before it interrupts and the exit handler reads it back.
//
// ── every store failure here is a defect ──────────────────────────────────
// The public calls declare StoreError, because a client asking for a job it
// cannot have is a real answer. Inside a running job it is not: the store is
// the daemon's own, a retry does not fix it, and a job that reported "failed"
// because its *bookkeeping* broke would be a lie about the work. So the run
// path dies instead, loudly, and the record stays where it was.

import {
  Clock,
  Context,
  Effect,
  Exit,
  FiberMap,
  Layer,
  PubSub,
  Ref,
  Result,
  Schema,
  Semaphore,
  Stream,
} from "effect";
import { type Cleanup, type Job, type JobId, isTerminal, jobId } from "./job";
import type { ErasedKind, JobContext, JobError, JobKind } from "./kind";
import { JobStore, type StoreError } from "./store";

/** Asked to enqueue a kind the runner was not built with. */
export class UnknownKind extends Schema.TaggedError<UnknownKind>()("UnknownKind", {
  kind: Schema.String,
}) {}

/**
 * A kind whose input does not survive being stored.
 *
 * The store is JSON, and JSON has no `undefined`: a field written as
 * `Schema.UndefinedOr(…)` and given `undefined` is simply *absent* when the
 * record is read back, and `UndefinedOr` requires the key to be there. The kind
 * then fails on its first step with "stored input does not match", three
 * seconds after the mistake, in a message about the wrong thing. Use
 * `Schema.optional`, which accepts both absent and undefined.
 *
 * Checked at enqueue by encoding, putting it through JSON, and reading it
 * straight back — so the refusal happens where the mistake is.
 */
export class InputNotPortable extends Schema.TaggedError<InputNotPortable>()("InputNotPortable", {
  kind: Schema.String,
  reason: Schema.String,
}) {}

export interface EnqueueOptions {
  /**
   * The caller's word for "this particular piece of work".
   *
   * Enqueuing twice with the same key returns the first job. A double-clicked
   * button, a retried rpc and a client that reconnected and replayed its queue
   * all become one job, which is the outer half of a job being idempotent. The
   * inner half is that each step is safe to run twice.
   */
  readonly key?: string;
}

export class Jobs extends Context.Service<
  Jobs,
  {
    readonly enqueue: <Input, Encoded>(
      kind: JobKind<Input, Encoded>,
      input: Input,
      options?: EnqueueOptions,
    ) => Effect.Effect<Job, UnknownKind | InputNotPortable | StoreError>;

    /**
     * Run a finished job again, from whatever survived its compensation.
     *
     * Only for a job that has stopped; a running one is already trying, and
     * returns unchanged.
     */
    readonly retry: (id: JobId) => Effect.Effect<Job | undefined, StoreError>;

    /** Stop it, undo what it did, and say it was cancelled. */
    readonly cancel: (id: JobId) => Effect.Effect<void, StoreError>;

    readonly get: (id: JobId) => Effect.Effect<Job | undefined, StoreError>;
    readonly list: () => Effect.Effect<ReadonlyArray<Job>, StoreError>;
    readonly log: (id: JobId) => Effect.Effect<ReadonlyArray<string>, StoreError>;

    /**
     * Every record as it changes.
     *
     * Sliding rather than unbounded: a client that stopped reading must not be
     * able to stall the runner, and a jobs list only ever wanted the latest
     * state of each job anyway.
     */
    readonly changes: Stream.Stream<Job>;
  }
>()("awp/Jobs") {}

/** How many jobs run at once. */
export const CONCURRENCY = 4;

/** How many records the change feed holds for a slow reader. */
const FEED = 256;

export const make = (kinds: ReadonlyArray<ErasedKind>) =>
  Effect.gen(function* () {
    const store = yield* JobStore;
    const fibers = yield* FiberMap.make<JobId>();
    const feed = yield* PubSub.sliding<Job>(FEED);
    const limit = Semaphore.makeUnsafe(CONCURRENCY);

    // Ids whose interruption means "cancelled" rather than "the process is
    // going away". See the header.
    const cancelling = yield* Ref.make(new Set<JobId>());

    const registry = new Map(kinds.map((kind) => [kind.name, kind]));

    const now = Clock.currentTimeMillis.pipe(Effect.map((millis) => new Date(millis)));

    // Every write goes through here, so nothing changes a job without the
    // stream saying so. A subscriber that misses an intermediate state is
    // fine; one that misses the terminal state is a spinner that never stops.
    const save = (job: Job): Effect.Effect<Job> =>
      store.put(job).pipe(
        Effect.orDie,
        Effect.tap(() => PubSub.publish(feed, job)),
        Effect.as(job),
      );

    const load = (id: JobId): Effect.Effect<Job | undefined> => store.get(id).pipe(Effect.orDie);

    const note = (id: JobId, line: string): Effect.Effect<void> =>
      store.append(id, [line]).pipe(Effect.ignore);

    const context = (job: Job): JobContext => ({
      id: job.id,
      attempt: job.attempt,
      log: (line) => note(job.id, line),
    });

    // ── one attempt ───────────────────────────────────────────────────────
    //
    // Threads the job through the steps rather than re-reading it, so the
    // caller writing a terminal status writes over the state the last step
    // left rather than over the record the attempt started with.
    const attemptSteps = (kind: ErasedKind, start: Job): Effect.Effect<Job, JobError> =>
      Effect.reduce(
        kind.steps,
        () => start,
        (job, step) =>
          job.done.includes(step.name)
            ? Effect.succeed(job)
            : save({ ...job, step: step.name }).pipe(
                Effect.tap((current) => step.run(current.input, context(current))),
                Effect.flatMap((current) =>
                  save({ ...current, done: [...current.done, step.name], step: undefined }),
                ),
              ),
      );

    // ── winding back ──────────────────────────────────────────────────────
    const compensate = (
      kind: ErasedKind,
      start: Job,
    ): Effect.Effect<{ readonly job: Job; readonly cleanup: Cleanup }> =>
      Effect.gen(function* () {
        let job = start;
        for (const name of job.done.toReversed()) {
          const undo = kind.steps.find((candidate) => candidate.name === name)?.undo;
          if (undo !== undefined) {
            const outcome = yield* Effect.result(undo(job.input, context(job)));
            if (Result.isFailure(outcome)) {
              // Deliberately not attempting the rest — each undo assumes the
              // ones after it already ran.
              yield* note(job.id, `undo ${name} failed: ${outcome.failure.reason}`);
              return { job, cleanup: "dirty" as const };
            }
            yield* note(job.id, `undid ${name}`);
          }
          job = yield* save({ ...job, done: job.done.filter((entry) => entry !== name) });
        }
        return { job, cleanup: "clean" as const };
      });

    const stop = (
      kind: ErasedKind,
      start: Job,
      status: "failed" | "cancelled",
      error: string | undefined,
    ): Effect.Effect<void> =>
      Effect.gen(function* () {
        const wound = yield* compensate(kind, start);
        const ended = yield* now;
        yield* save({
          ...wound.job,
          status,
          error,
          cleanup: wound.cleanup,
          endedAt: ended,
          step: undefined,
        });
      });

    // ── the whole life of one job ─────────────────────────────────────────
    //
    // A loop rather than a `Schedule`, because the delay between attempts is
    // not the only thing that has to happen between them: the record moves
    // back to `queued` first, so a process that dies during the backoff leaves
    // a job the next start picks up rather than one that looks alive.
    const execute = (id: JobId): Effect.Effect<void> =>
      Effect.gen(function* () {
        for (;;) {
          const stored = yield* load(id);
          if (stored === undefined || isTerminal(stored.status)) {
            return;
          }
          const kind = registry.get(stored.kind);
          if (kind === undefined) {
            const ended = yield* now;
            yield* save({
              ...stored,
              status: "failed",
              error: `no such job kind: ${stored.kind}`,
              cleanup: "clean",
              endedAt: ended,
            });
            return;
          }

          const startedAt = stored.startedAt ?? (yield* now);
          const running = yield* save({
            ...stored,
            status: "running",
            attempt: stored.attempt + 1,
            startedAt,
            error: undefined,
          });

          const outcome = yield* Effect.result(attemptSteps(kind, running));
          if (Result.isSuccess(outcome)) {
            const ended = yield* now;
            yield* save({
              ...outcome.success,
              status: "succeeded",
              step: undefined,
              endedAt: ended,
            });
            return;
          }

          const failure = outcome.failure;
          const current = (yield* load(id)) ?? running;
          if (failure.retryable && current.attempt < kind.attempts) {
            yield* note(id, `attempt ${current.attempt} failed: ${failure.reason}`);
            yield* save({ ...current, status: "queued", error: failure.reason });
            yield* Effect.sleep(kind.backoff(current.attempt));
            continue;
          }
          yield* note(id, `failed: ${failure.reason}`);
          yield* stop(kind, current, "failed", failure.reason);
          return;
        }
      });

    // Interruption arrives here, and this handler is the only place that knows
    // which of the two interruptions it was.
    const onInterrupted = (id: JobId): Effect.Effect<void> =>
      Effect.gen(function* () {
        const ids = yield* Ref.get(cancelling);
        if (!ids.has(id)) {
          // The process is going away. The record stays as it is — `running`,
          // with its `done` list — and the next start reads it back.
          return;
        }
        const stored = yield* load(id);
        const kind = stored === undefined ? undefined : registry.get(stored.kind);
        if (stored !== undefined && kind !== undefined) {
          yield* note(id, "cancelled");
          yield* stop(kind, stored, "cancelled", "cancelled");
        }
        yield* Ref.update(cancelling, (all) => {
          const next = new Set(all);
          next.delete(id);
          return next;
        });
      });

    const start = (id: JobId): Effect.Effect<void> =>
      execute(id).pipe(
        Effect.onExit((exit) => (Exit.isSuccess(exit) ? Effect.void : onInterrupted(id))),
        limit.withPermits(1),
        // A job already running is already running: enqueue-by-key and the
        // recovery pass below can both reach this for one id.
        FiberMap.run(fibers, id, { onlyIfMissing: true }),
        Effect.asVoid,
      );

    const enqueue = <Input, Encoded>(
      kind: JobKind<Input, Encoded>,
      input: Input,
      options?: EnqueueOptions,
    ): Effect.Effect<Job, UnknownKind | InputNotPortable | StoreError> =>
      Effect.gen(function* () {
        const erased = registry.get(kind.name);
        if (erased === undefined) {
          return yield* Effect.fail(new UnknownKind({ kind: kind.name }));
        }
        if (options?.key !== undefined) {
          const existing = yield* store.byKey(options.key);
          if (existing !== undefined) {
            return existing;
          }
        }
        const created = yield* now;
        // Encoded here rather than at the store, because this is the last
        // point at which the input's own schema is in hand — the runner and
        // the store both see it as whatever JSON came back.
        const encoded = yield* Schema.encodeEffect(kind.input)(input).pipe(Effect.orDie);
        // Through JSON here rather than at the store, so both stores hold the
        // same thing. The memory store would otherwise keep the encoded value
        // as it is, and every kind whose input loses something in JSON would
        // pass its tests and fail in the daemon.
        const stored: unknown = JSON.parse(JSON.stringify(encoded ?? null));
        // And straight back out, because a schema that cannot make that trip
        // should say so here rather than on the first step of every attempt.
        yield* Schema.decodeUnknownEffect(kind.input)(stored).pipe(
          Effect.mapError(
            (error) => new InputNotPortable({ kind: kind.name, reason: String(error) }),
          ),
        );
        const job: Job = {
          id: jobId(created, Math.random()),
          kind: kind.name,
          title: kind.title(input),
          key: options?.key,
          input: stored,
          status: "queued",
          attempt: 0,
          attempts: erased.attempts,
          steps: erased.steps.map((step) => step.name),
          done: [],
          step: undefined,
          error: undefined,
          cleanup: undefined,
          createdAt: created,
          startedAt: undefined,
          endedAt: undefined,
        };
        yield* save(job);
        yield* start(job.id);
        return job;
      });

    // ── recovery ──────────────────────────────────────────────────────────
    //
    // The reason the store is durable rather than a Map. A job left `running`
    // by a process that died is picked up here, and because its completed
    // steps are on the record it resumes rather than starting over.
    for (const job of yield* store.list().pipe(Effect.orDie)) {
      if (!isTerminal(job.status)) {
        yield* note(job.id, "resumed after a restart");
        yield* start(job.id);
      }
    }

    return {
      enqueue,

      retry: (id: JobId) =>
        Effect.gen(function* () {
          const job = yield* store.get(id);
          if (job === undefined || !isTerminal(job.status)) {
            return job;
          }
          const reset = yield* save({
            ...job,
            status: "queued",
            attempt: 0,
            error: undefined,
            cleanup: undefined,
            step: undefined,
            endedAt: undefined,
          });
          yield* start(id);
          return reset;
        }),

      cancel: (id: JobId) =>
        Effect.gen(function* () {
          const job = yield* store.get(id);
          if (job === undefined || isTerminal(job.status)) {
            return;
          }
          yield* Ref.update(cancelling, (all) => new Set(all).add(id));
          yield* FiberMap.remove(fibers, id);
        }),

      get: (id: JobId) => store.get(id),
      list: () => store.list(),
      log: (id: JobId) => store.log(id),
      changes: Stream.fromPubSub(feed),
    };
  });

/**
 * A runner over the kinds it is given, and nothing else can be enqueued.
 *
 * Erased kinds rather than typed ones, because a registry of many kinds with
 * different inputs has no honest element type — and erasing at the call site
 * keeps the `any` that would otherwise be needed here from existing at all.
 */
export const layer = (kinds: ReadonlyArray<ErasedKind>): Layer.Layer<Jobs, never, JobStore> =>
  Layer.effect(Jobs)(make(kinds));
