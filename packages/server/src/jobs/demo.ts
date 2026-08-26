// A job that does nothing, so the panel that shows jobs can be looked at.
//
// Nothing in awp enqueues real work yet: there is no workspace to create, no
// review to run and no CI to watch. Without this the jobs list is an empty box
// and every claim about it is untested by eye — which, for a panel whose entire
// purpose is *visibility*, is the same as not having built it.
//
// One payload reaches every state a person needs to recognise:
//
//   plain                    queued → running → succeeded
//   failAt, retryable        a step fails, the record goes back to queued, the
//                            backoff elapses, and it resumes at that step
//   failAt, not retryable    one attempt, then the completed steps are undone
//                            backwards and the job is failed + clean
//   undoFails                the rollback stops partway and the job is dirty,
//                            which is the only state that asks for a human
//
// It goes when the first real kind lands, together with the `JobDemo` call in
// the contract.

import { type JobKind, type JobStep, permanent, transient } from "@awp-kit/jobs";
import { type DemoJob, DemoJob as DemoJobSchema } from "@awp-kit/protocol";
import { Effect } from "effect";

/**
 * How many steps a demo job has, and why it is a constant.
 *
 * A kind's step list is fixed, and it has to be: the runner reads `done` back
 * from the store and resumes against the same list, so a list that varied per
 * payload would be a list a restarted daemon could not reproduce. Making the
 * count a payload field would have looked harmless and quietly broken exactly
 * the property this package exists for.
 */
export const DEMO_STEPS = 4;

const label = (index: number): string => `step ${index + 1}`;

const step = (index: number): JobStep<DemoJob> => ({
  name: label(index),
  run: (input, context) =>
    Effect.gen(function* () {
      yield* context.log(`${label(index)}: working`);
      yield* Effect.sleep(`${input.pace} millis`);
      // A retryable failure gives way after the first attempt, so the button
      // that says "retry then pass" does. A demo that failed every time would
      // only ever show the exhausted case, which is the one the *other* button
      // is for.
      const failing = input.failAt === index + 1 && (!input.retryable || context.attempt === 1);
      if (failing) {
        return yield* Effect.fail(
          input.retryable
            ? transient(`${label(index)} was not ready`)
            : permanent(`${label(index)} refused`),
        );
      }
      yield* context.log(`${label(index)}: done`);
    }),
  undo: (input, context) =>
    Effect.gen(function* () {
      yield* Effect.sleep(`${Math.min(input.pace, 200)} millis`);
      if (input.undoFails) {
        return yield* Effect.fail(permanent(`${label(index)} cannot be undone`));
      }
      yield* context.log(`${label(index)}: undone`);
    }),
});

// A whole second between attempts, and deliberately not less. The point of
// watching this is seeing the record sit in `queued` with an error on it, which
// is what a real job waiting out a backoff looks like — and is invisible if the
// retry is instant.
const BACKOFF = "1 second" as const;

export const demo: JobKind<DemoJob> = {
  name: "demo",
  input: DemoJobSchema,
  // The same sentence the button said, so a row can be matched to the click
  // that made it. `4 steps, failing at 3` described the payload, which meant
  // three of the four buttons produced rows nobody could tell apart.
  title: (input) => {
    if (input.failAt === undefined) {
      return "a demo that works";
    }
    if (input.retryable) {
      return "a demo that fails once, then works";
    }
    return input.undoFails
      ? "a demo that gives up and cannot undo itself"
      : "a demo that gives up and undoes itself";
  },
  steps: Array.from({ length: DEMO_STEPS }, (_, index) => step(index)),
  attempts: 3,
  backoff: () => BACKOFF,
};
