// What a kind of job knows how to do — and how to take it back.
//
// A job is a list of named steps rather than one function, and the list is the
// whole design. Two properties fall out of it that a single function cannot
// have:
//
//   - **Resume.** The names of completed steps are on the record, so a second
//     attempt starts at the first step that is not there. Work already done is
//     not done twice.
//   - **Compensation.** A step may say how to undo itself. When a job finally
//     fails, the runner walks the completed steps backwards and undoes them,
//     so the failure leaves the world as it found it.
//
// Those two pull in opposite directions and the runner reconciles them, which
// is the interesting part — see `runner.ts`. What matters here is the promise a
// step author is making, and it is a strong one:
//
//   **`run` must be safe to call twice.**
//
// A retry re-runs the step that failed, and that step may have got halfway.
// `mkdir -p`, not `mkdir`. `create bookmark if absent`, not `create bookmark`.
// If a step cannot be written that way it is two steps with the durable part
// recorded between them.

import { Data, type Duration, Effect, Schema } from "effect";
import type { JobId } from "./job";

/**
 * A step said no.
 *
 * `retryable` is the field the runner branches on, and it is the difference
 * between "the network was down" and "that repository does not exist". Retrying
 * the second one wastes every attempt the job had and then reports the same
 * sentence it could have reported immediately.
 */
export class JobError extends Data.TaggedError("JobError")<{
  readonly reason: string;
  readonly retryable: boolean;
  readonly cause?: unknown;
}> {}

/** The world was not ready. Worth another attempt. */
export const transient = (reason: string, cause?: unknown): JobError =>
  new JobError({ reason, retryable: true, cause });

/** The request itself is wrong. No number of attempts will change that. */
export const permanent = (reason: string, cause?: unknown): JobError =>
  new JobError({ reason, retryable: false, cause });

/** What a step is told about the job it belongs to. */
export interface JobContext {
  readonly id: JobId;
  /** 1 on the first run. A step may use it to widen a timeout, or to shout. */
  readonly attempt: number;
  /** A line for the job's log — the only thing a person has to read later. */
  readonly log: (line: string) => Effect.Effect<void>;
}

/**
 * `Input` is invariant, and was contravariant until `run` could return a patch.
 *
 * The `in` annotation said a step only ever *consumes* its input, which stopped
 * being true the moment a step could also describe a change to it. Removing it
 * is the honest spelling; adding `out` as well would be the same thing said at
 * greater length.
 */
export interface JobStep<Input> {
  /**
   * Stable, and stable across releases.
   *
   * This string is what gets written to the record as "done", so renaming a
   * step in a running system makes every job in flight redo it. That is
   * recoverable precisely because `run` is safe twice — but it is still a
   * rename with consequences, which is why the name is declared rather than
   * taken from the function.
   */
  readonly name: string;

  /**
   * Do the step, and optionally record what it learned.
   *
   * Most steps answer `Effect.void`: they act on the world and the input
   * already says everything they needed. A step that *discovers* something the
   * later steps depend on returns a patch instead, and the runner merges it
   * into the stored input before marking the step done.
   *
   * ── why a step can write to the input at all ──────────────────────────────
   * A step cannot hand a value to the next one — there is nowhere to put it. A
   * job resumed by a restarted daemon has only its record, so anything not on
   * the record did not happen. The first real need for this was naming a
   * workspace: a model turns a sentence into a name, it takes ten seconds, and
   * four of the five steps need the answer.
   *
   * Resolving it *before* enqueuing was the first design, and it worked. What
   * it cost was the ten seconds, spent in front of a person watching a form
   * that would not close, for work that has a progress panel of its own. The
   * job has to exist immediately, so the naming has to happen inside it, so the
   * step that names has to be able to write down what it found.
   *
   * The patch is merged and **saved with the same write that marks the step
   * done**, so a resumed job reads the name rather than asking the model again
   * — which would otherwise be a second answer, and possibly a different one.
   *
   * Two things this is not. It is not a channel between steps: the patch goes
   * into the input, which is durable and schema-checked, rather than into
   * memory. And it is not an escape from `run` being safe twice — a step whose
   * patch is already present should notice and do nothing, exactly as every
   * other step checks the world before changing it.
   */
  readonly run: (
    input: Input,
    context: JobContext,
  ) => Effect.Effect<void | Partial<Input>, JobError>;

  /**
   * Put back what `run` did, or absent if there is nothing to put back.
   *
   * Runs only when the job has finally failed or been cancelled, in reverse
   * order over the steps that completed. Like `run` it must be safe twice, and
   * unlike `run` it must also be safe when `run` only got halfway — it is
   * undoing a step whose own failure is the reason it is being called.
   *
   * A step with no `undo` is a claim that it left nothing behind worth
   * removing: a check, a read, a write that the next attempt overwrites anyway.
   */
  readonly undo?: (input: Input, context: JobContext) => Effect.Effect<void, JobError>;
}

/** How long to wait before attempt `n + 1`. */
export type Backoff = (attempt: number) => Duration.Input;

/**
 * The addressing half of a kind: what it is called, and how its input is
 * encoded.
 *
 * All `enqueue` actually needs. The steps that run come from the registry the
 * runner was built with, looked up by this name — so a caller that only wants
 * to *start* a job does not have to construct one, which for a kind whose steps
 * close over services would mean constructing those too.
 *
 * `JobKind` extends it, so passing a whole kind still works and the name and
 * schema cannot drift between the two.
 */
export interface JobRef<Input, Encoded = Input> {
  /** Unique, and persisted on every job of this kind. */
  readonly name: string;
  /**
   * How the input is stored and read back.
   *
   * A job outlives the process that enqueued it, so its input arrives at the
   * runner as whatever JSON came out of the store — not as `Input`. This is
   * what turns one into the other, and a decode failure is permanent by
   * construction: an input that no longer matches its schema will not start
   * matching on the fourth attempt.
   */
  readonly input: Schema.Codec<Input, Encoded, never, never>;
  /** One line for a list. Computed once, when the job is enqueued. */
  readonly title: (input: Input) => string;
}

export interface JobKind<Input, Encoded = Input> extends JobRef<Input, Encoded> {
  readonly steps: ReadonlyArray<JobStep<Input>>;
  /** Total attempts allowed, including the first. Default 3. */
  readonly attempts?: number;
  /** Default: two seconds, doubling. */
  readonly backoff?: Backoff;
}

// ── erasure ────────────────────────────────────────────────────────────────
//
// The registry holds many kinds with different Input types, and the runner does
// not care which is which — it has an id, a name and some stored JSON. So a
// kind is erased once, here, at the boundary where its schema is still in hand.
// Nothing downstream needs a cast, because the decode happens inside.

export interface ErasedStep {
  readonly name: string;
  readonly run: (input: unknown, context: JobContext) => Effect.Effect<void, JobError>;
  readonly undo?:
    | ((input: unknown, context: JobContext) => Effect.Effect<void, JobError>)
    | undefined;
}

export interface ErasedKind {
  readonly name: string;
  readonly attempts: number;
  readonly backoff: Backoff;
  readonly steps: ReadonlyArray<ErasedStep>;
}

export const DEFAULT_ATTEMPTS = 3;

const defaultBackoff: Backoff = (attempt) => `${2 ** attempt} seconds`;

/**
 * Hide a kind's Input behind its schema.
 *
 * Every call decodes, rather than decoding once per job and threading the
 * result through: a step is one call, and paying a schema decode per step is
 * not measurable beside the work a step is doing.
 */
export const erase = <Input, Encoded>(kind: JobKind<Input, Encoded>): ErasedKind => {
  const decode = Schema.decodeUnknownEffect(kind.input);

  const wrap =
    (run: (input: Input, context: JobContext) => Effect.Effect<void, JobError>) =>
    (raw: unknown, context: JobContext): Effect.Effect<void, JobError> =>
      decode(raw).pipe(
        Effect.mapError((error) => permanent(`stored input does not match ${kind.name}`, error)),
        Effect.flatMap((input) => run(input, context)),
      );

  return {
    name: kind.name,
    attempts: kind.attempts ?? DEFAULT_ATTEMPTS,
    backoff: kind.backoff ?? defaultBackoff,
    steps: kind.steps.map((step) => ({
      name: step.name,
      run: wrap(step.run),
      undo: step.undo === undefined ? undefined : wrap(step.undo),
    })),
  };
};
