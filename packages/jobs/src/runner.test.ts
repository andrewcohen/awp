import { Context, Deferred, Effect, Exit, Layer, Result, Schema } from "effect";
import { beforeEach, describe, expect, test } from "vitest";
import { type Job, isTerminal } from "./job";
import { type ErasedKind, type JobKind, erase, permanent, transient } from "./kind";
import { Jobs, layer } from "./runner";
import { JobStore, layerMemory, makeMemory } from "./store";

// The runner's behaviour, against the memory store.
//
// Everything here is written as "what did the steps actually do", not as "what
// does the record say" — a record claiming success is exactly what a broken
// resume would produce. `trace` is the evidence; the record is the corroboration.

const Input = Schema.Struct({ name: Schema.String });
type Input = (typeof Input)["Type"];

let trace: Array<string> = [];
/** Names of steps rigged to fail, and how many times each still should. */
let breaking: Map<string, { readonly times: number; readonly retryable: boolean }> = new Map();
let broken = new Map<string, number>();

beforeEach(() => {
  trace = [];
  breaking = new Map();
  broken = new Map();
});

const step = (name: string, undoable = true) => ({
  name,
  run: (input: Input) =>
    Effect.suspend(() => {
      const rig = breaking.get(name);
      const already = broken.get(name) ?? 0;
      if (rig !== undefined && already < rig.times) {
        broken.set(name, already + 1);
        trace.push(`${name}!`);
        return Effect.fail(
          rig.retryable ? transient(`${name} not ready`) : permanent(`${name} refused`),
        );
      }
      trace.push(`${name}(${input.name})`);
      return Effect.void;
    }),
  ...(undoable
    ? {
        undo: (input: Input) =>
          Effect.suspend(() => {
            if (breaking.get(`undo:${name}`) !== undefined) {
              trace.push(`undo:${name}!`);
              return Effect.fail(permanent(`cannot undo ${name}`));
            }
            trace.push(`undo:${name}(${input.name})`);
            return Effect.void;
          }),
      }
    : {}),
});

const threeSteps: JobKind<Input> = {
  name: "three",
  input: Input,
  title: (input) => `three for ${input.name}`,
  steps: [step("one"), step("two"), step("three")],
  attempts: 3,
  // Zero, so a test does not wait out a real backoff. The delay is the kind's
  // to choose precisely so this is possible.
  backoff: () => 0,
};

const unregistered: JobKind<Input> = { ...threeSteps, name: "nowhere" };

// ── harness ────────────────────────────────────────────────────────────────

type JobsService = Context.Service.Shape<typeof Jobs>;

const run = <A>(
  program: (jobs: JobsService) => Effect.Effect<A, unknown>,
  kinds: ReadonlyArray<ErasedKind> = [erase(threeSteps)],
  store: Layer.Layer<JobStore> = layerMemory,
): Promise<A> =>
  Effect.gen(function* () {
    const jobs = yield* Jobs;
    return yield* program(jobs);
  }).pipe(
    Effect.provide(layer(kinds).pipe(Layer.provide(store))),
    Effect.scoped,
    Effect.runPromise,
  ) as Promise<A>;

/**
 * Wait for a job to stop moving.
 *
 * Polling rather than the change stream, deliberately: the stream is one of the
 * things under test, and a test that waits on the mechanism it is checking
 * cannot fail in the interesting direction.
 */
const settle = (jobs: JobsService, id: string): Effect.Effect<Job, unknown> =>
  Effect.gen(function* () {
    for (let tick = 0; tick < 4000; tick += 1) {
      const job = yield* jobs.get(id);
      if (job !== undefined && isTerminal(job.status)) {
        return job;
      }
      yield* Effect.sleep("1 millis");
    }
    return yield* Effect.die(new Error(`job ${id} never settled: ${trace.join(" ")}`));
  });

// ── the happy path ─────────────────────────────────────────────────────────

describe("running", () => {
  test("runs every step in order, once", async () => {
    const job = await run((jobs) =>
      jobs
        .enqueue(threeSteps, { name: "a" })
        .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
    );

    expect(trace).toEqual(["one(a)", "two(a)", "three(a)"]);
    expect(job.status).toBe("succeeded");
    expect(job.done).toEqual(["one", "two", "three"]);
    // The kind's whole step list, written once at enqueue. It is what gives a
    // client a denominator to show progress against.
    expect(job.steps).toEqual(["one", "two", "three"]);
    expect(job.attempt).toBe(1);
    expect(job.title).toBe("three for a");
  });

  // A step that throws rather than failing. Until this was handled, the defect
  // sailed past the `Effect.result` wrapping an attempt, killed the fiber, and
  // left the record saying `running` with nothing behind it — a job that never
  // finished and never failed, which is worse than either.
  //
  // It was found by a fake missing a method, which is exactly how a real
  // service gains one.
  test("a step that throws fails the job instead of hanging it", async () => {
    const throws: JobKind<Input> = {
      name: "throws",
      input: Input,
      title: () => "one that throws",
      steps: [
        step("one"),
        {
          name: "two",
          run: () =>
            Effect.sync(() => {
              throw new TypeError("threads.rename is not a function");
            }),
        },
        step("three"),
      ],
      attempts: 1,
    };

    const job = await run(
      (jobs) =>
        jobs
          .enqueue(throws, { name: "a" })
          .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
      [erase(throws)],
    );

    expect(job.status).toBe("failed");
    // Named, so the log says which step and what it threw rather than leaving a
    // person to guess from a stopped progress bar.
    expect(job.error).toContain("two");
    expect(job.error).toContain("threads.rename is not a function");
    // And it is a failure like any other, so the completed steps are undone.
    expect(trace).toEqual(["one(a)", "undo:one(a)"]);
    expect(job.done).toEqual([]);
  });

  test("an input that cannot survive the store is refused at enqueue", async () => {
    // `UndefinedOr` plus an unset value: JSON drops the key, and `UndefinedOr`
    // wants it present. Without the check at enqueue this becomes a job that
    // fails on its first step with a message about a schema, one backoff after
    // the mistake was made.
    const Fragile = Schema.Struct({ name: Schema.String, note: Schema.UndefinedOr(Schema.String) });
    const fragile: JobKind<(typeof Fragile)["Type"]> = {
      ...threeSteps,
      name: "fragile",
      input: Fragile,
      title: () => "fragile",
      steps: [],
    };

    const outcome = await run(
      (jobs) => Effect.result(jobs.enqueue(fragile, { name: "a", note: undefined })),
      [erase(fragile)],
    );

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect(outcome.failure).toMatchObject({ kind: "fragile" });
    }
    // And nothing was written, so the list is not carrying a job that cannot run.
    expect(trace).toEqual([]);
  });

  test("the same field spelled `optional` is accepted", async () => {
    const Sturdy = Schema.Struct({ name: Schema.String, note: Schema.optional(Schema.String) });
    const sturdy: JobKind<(typeof Sturdy)["Type"]> = {
      ...threeSteps,
      name: "sturdy",
      input: Sturdy,
      title: () => "sturdy",
      steps: [step("one")],
    };

    const job = await run(
      (jobs) =>
        jobs
          .enqueue(sturdy, { name: "a", note: undefined })
          .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
      [erase(sturdy)],
    );

    expect(job.status).toBe("succeeded");
    expect(trace).toEqual(["one(a)"]);
  });

  test("a kind the runner was not built with is refused, not queued", async () => {
    const outcome = await run((jobs) => Effect.result(jobs.enqueue(unregistered, { name: "a" })));

    expect(Result.isFailure(outcome)).toBe(true);
  });
});

// ── idempotence ────────────────────────────────────────────────────────────

describe("idempotence", () => {
  test("one key is one job, however many times it is asked for", async () => {
    const [first, second] = await run((jobs) =>
      Effect.gen(function* () {
        const a = yield* jobs.enqueue(threeSteps, { name: "a" }, { key: "make-a" });
        yield* settle(jobs, a.id);
        const b = yield* jobs.enqueue(threeSteps, { name: "a" }, { key: "make-a" });
        return [a, b] as const;
      }),
    );

    expect(second.id).toBe(first.id);
    // And the work happened once, which is the half a returned id cannot show.
    expect(trace).toEqual(["one(a)", "two(a)", "three(a)"]);
  });

  test("different keys are different jobs", async () => {
    const [first, second] = await run((jobs) =>
      Effect.gen(function* () {
        const a = yield* jobs.enqueue(threeSteps, { name: "a" }, { key: "a" });
        const b = yield* jobs.enqueue(threeSteps, { name: "b" }, { key: "b" });
        yield* settle(jobs, a.id);
        yield* settle(jobs, b.id);
        return [a, b] as const;
      }),
    );

    expect(second.id).not.toBe(first.id);
  });
});

// ── retry resumes ──────────────────────────────────────────────────────────

describe("retrying", () => {
  test("a second attempt starts at the step that failed, not at the first", async () => {
    breaking.set("two", { times: 1, retryable: true });

    const job = await run((jobs) =>
      jobs
        .enqueue(threeSteps, { name: "a" })
        .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
    );

    // `one` appears once. That single fact is the whole point of `done`.
    expect(trace).toEqual(["one(a)", "two!", "two(a)", "three(a)"]);
    expect(job.status).toBe("succeeded");
    expect(job.attempt).toBe(2);
  });

  test("a permanent failure spends one attempt, not all of them", async () => {
    breaking.set("two", { times: 99, retryable: false });

    const job = await run((jobs) =>
      jobs
        .enqueue(threeSteps, { name: "a" })
        .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
    );

    expect(job.status).toBe("failed");
    expect(job.attempt).toBe(1);
    expect(job.error).toBe("two refused");
  });
});

// ── compensation ───────────────────────────────────────────────────────────

describe("compensating", () => {
  test("a final failure undoes the completed steps, backwards", async () => {
    breaking.set("three", { times: 99, retryable: false });

    const job = await run((jobs) =>
      jobs
        .enqueue(threeSteps, { name: "a" })
        .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
    );

    expect(trace).toEqual(["one(a)", "two(a)", "three!", "undo:two(a)", "undo:one(a)"]);
    expect(job.status).toBe("failed");
    expect(job.cleanup).toBe("clean");
    // Emptied, because the world was put back. A retry starts from nothing.
    expect(job.done).toEqual([]);
  });

  test("an undo that fails stops the rest and says the job is dirty", async () => {
    breaking.set("three", { times: 99, retryable: false });
    breaking.set("undo:two", { times: 99, retryable: false });

    const job = await run((jobs) =>
      jobs
        .enqueue(threeSteps, { name: "a" })
        .pipe(Effect.flatMap((queued) => settle(jobs, queued.id))),
    );

    // `undo:one` is deliberately absent: it assumes `undo:two` already ran.
    expect(trace).toEqual(["one(a)", "two(a)", "three!", "undo:two!"]);
    expect(job.cleanup).toBe("dirty");
    // And the record still names what was left behind.
    expect(job.done).toEqual(["one", "two"]);
  });

  test("a step with no undo is dropped from done without ceremony", async () => {
    const noUndo: JobKind<Input> = {
      ...threeSteps,
      name: "no-undo",
      steps: [step("one", false), step("two")],
    };
    breaking.set("two", { times: 99, retryable: false });

    const job = await run(
      (jobs) => jobs.enqueue(noUndo, { name: "a" }).pipe(Effect.flatMap((q) => settle(jobs, q.id))),
      [erase(noUndo)],
    );

    expect(trace).toEqual(["one(a)", "two!"]);
    expect(job.cleanup).toBe("clean");
    expect(job.done).toEqual([]);
  });
});

// ── retry, by hand ─────────────────────────────────────────────────────────

describe("retry()", () => {
  test("runs a failed job again from a clean slate", async () => {
    breaking.set("three", { times: 99, retryable: false });

    const job = await run((jobs) =>
      Effect.gen(function* () {
        const queued = yield* jobs.enqueue(threeSteps, { name: "a" });
        yield* settle(jobs, queued.id);
        // Un-rig it, the way a person fixing the cause would.
        breaking.clear();
        broken.clear();
        trace = [];
        yield* jobs.retry(queued.id);
        return yield* settle(jobs, queued.id);
      }),
    );

    expect(trace).toEqual(["one(a)", "two(a)", "three(a)"]);
    expect(job.status).toBe("succeeded");
    expect(job.attempt).toBe(1);
  });

  test("leaves a running job alone", async () => {
    const job = await run((jobs) =>
      Effect.gen(function* () {
        const queued = yield* jobs.enqueue(threeSteps, { name: "a" });
        yield* jobs.retry(queued.id);
        return yield* settle(jobs, queued.id);
      }),
    );

    // Asking a job that is already trying to try again does not start a second
    // run of it, and does not reset the attempt it is in the middle of.
    expect(trace).toEqual(["one(a)", "two(a)", "three(a)"]);
    expect(job.attempt).toBe(1);
  });
});

// ── cancelling ─────────────────────────────────────────────────────────────

describe("cancel()", () => {
  test("stops a running job, winds it back, and says so", async () => {
    // A step that does not finish on its own, so there is something to cancel.
    // The latch is opened by the step itself, which is how the test knows the
    // job has actually reached it rather than guessing with a sleep.
    const reached = Deferred.makeUnsafe<void>();
    const held: JobKind<Input> = {
      ...threeSteps,
      name: "held",
      steps: [
        step("one"),
        {
          name: "hold",
          run: () =>
            Effect.gen(function* () {
              trace.push("hold");
              yield* Deferred.done(reached, Exit.void);
              yield* Effect.never;
            }),
          undo: () => Effect.sync(() => void trace.push("undo:hold")),
        },
      ],
    };

    const job = await run(
      (jobs) =>
        Effect.gen(function* () {
          const queued = yield* jobs.enqueue(held, { name: "a" });
          yield* Deferred.await(reached);
          yield* jobs.cancel(queued.id);
          return yield* settle(jobs, queued.id);
        }),
      [erase(held)],
    );

    expect(job.status).toBe("cancelled");
    expect(job.cleanup).toBe("clean");
    expect(job.done).toEqual([]);
    // `hold` never completed, so it is not in `done` and is not undone. `one`
    // did, and is.
    expect(trace).toEqual(["one(a)", "hold", "undo:one(a)"]);
  });
});

// ── shutdown is not cancellation ───────────────────────────────────────────
//
// The subtlest rule in the runner, and the one with no visible symptom: a
// cancelled job and a daemon being shut down both arrive as an interrupted
// fiber. Treating the second as the first would silently undo work that was
// meant to carry on after a restart — and the record would look tidy either
// way, which is why this is asserted on the trace and on a second runner.

describe("shutdown", () => {
  test("closing the runner leaves a job to be resumed, and does not undo it", async () => {
    // One store instance, shared by two runners, the way one sqlite file is
    // shared by two runs of the daemon.
    const shape = await Effect.runPromise(makeMemory);
    const shared = Layer.succeed(JobStore)(shape);

    const reached = Deferred.makeUnsafe<void>();
    const holding: JobKind<Input> = {
      ...threeSteps,
      name: "held",
      steps: [
        step("one"),
        {
          name: "hold",
          run: () =>
            Effect.gen(function* () {
              trace.push("hold");
              yield* Deferred.done(reached, Exit.void);
              yield* Effect.never;
            }),
          undo: () => Effect.sync(() => void trace.push("undo:hold")),
        },
      ],
    };

    const id = await Effect.runPromise(
      Effect.gen(function* () {
        const jobs = yield* Jobs;
        const queued = yield* jobs.enqueue(holding, { name: "a" });
        yield* Deferred.await(reached);
        return queued.id;
      }).pipe(
        Effect.provide(layer([erase(holding)]).pipe(Layer.provide(shared))),
        // Leaving the scope interrupts the job's fiber — this is the shutdown.
        Effect.scoped,
      ),
    );

    const stranded = await Effect.runPromise(shape.get(id));
    expect(stranded?.status).toBe("running");
    expect(stranded?.done).toEqual(["one"]);
    // Nothing was wound back. A daemon restarting is not a job being abandoned.
    expect(trace).toEqual(["one(a)", "hold"]);

    // The same kind, with the step that was hanging now able to finish.
    const released: JobKind<Input> = {
      ...holding,
      steps: [step("one"), step("hold")],
    };
    trace = [];

    const resumed = await run((jobs) => settle(jobs, id), [erase(released)], shared);

    expect(resumed.status).toBe("succeeded");
    // `one` is not re-run, and `hold` is.
    expect(trace).toEqual(["hold(a)"]);
  });
});

// ── surviving a restart ────────────────────────────────────────────────────

describe("recovery", () => {
  test("a job left running by a dead process resumes where it stopped", async () => {
    // A store that already holds a half-finished job, as a crashed daemon
    // would have left it: status `running`, two steps recorded as done.
    const seeded = Layer.effect(JobStore)(
      Effect.gen(function* () {
        const store = yield* makeMemory;
        yield* store.put({
          id: "20260101-aaaa",
          kind: "three",
          title: "three for a",
          key: undefined,
          input: { name: "a" },
          status: "running",
          attempt: 1,
          attempts: 3,
          steps: ["one", "two", "three"],
          done: ["one", "two"],
          step: "three",
          error: undefined,
          cleanup: undefined,
          createdAt: new Date(0),
          startedAt: new Date(0),
          endedAt: undefined,
        });
        return store;
      }),
    );

    const job = await run((jobs) => settle(jobs, "20260101-aaaa"), [erase(threeSteps)], seeded);

    // Only the step that had not finished. Recovering by re-running everything
    // would have been indistinguishable from working, and wrong.
    expect(trace).toEqual(["three(a)"]);
    expect(job.status).toBe("succeeded");
    expect(job.attempt).toBe(2);
  });

  test("stored input that no longer matches its schema fails once and stays failed", async () => {
    const seeded = Layer.effect(JobStore)(
      Effect.gen(function* () {
        const store = yield* makeMemory;
        yield* store.put({
          id: "20260101-bbbb",
          kind: "three",
          title: "three for ?",
          key: undefined,
          input: { nome: "a" },
          status: "queued",
          attempt: 0,
          attempts: 3,
          steps: ["one", "two", "three"],
          done: [],
          step: undefined,
          error: undefined,
          cleanup: undefined,
          createdAt: new Date(0),
          startedAt: undefined,
          endedAt: undefined,
        });
        return store;
      }),
    );

    const job = await run((jobs) => settle(jobs, "20260101-bbbb"), [erase(threeSteps)], seeded);

    expect(job.status).toBe("failed");
    // One attempt, because an input that does not decode will not start.
    expect(job.attempt).toBe(1);
    expect(job.error).toContain("stored input does not match three");
  });
});
