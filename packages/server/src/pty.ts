// Spawning a process in a pty.
//
// ── why this is a tag and not a function ───────────────────────────────────
// So a caller can be handed a fake one. A real pty means a real process, and
// the thing that needs a pty here is `zmx attach`, which cannot be run from
// inside a zmx session at all — see zmx-session.ts. Without a fake, everything
// built on top of this would be untestable in the environment it is developed
// in.
//
// ── what Scope is for ─────────────────────────────────────────────────────
// `spawn` returns `Effect<PtyHandle, PtyError, Scope>`. That third slot is the
// promise that the process gets killed: whoever runs it must supply a scope,
// and when the scope closes the pty dies. Not a `finally` somebody remembers to
// write — a requirement in the type.
//
// The probe that came before this hand-rolled the same thing: a `finish()` with
// a try/catch around kill, a spawnSync for cleanup, and a setTimeout racing it,
// with a path where cleanup ran twice. Scope replaces all of it.

import { Context, Data, Effect, Queue, type Scope, Stream } from "effect";

/** A pty that could not be opened, or that failed while open. */
export class PtyError extends Data.TaggedError("PtyError")<{
  readonly op: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

export interface PtySize {
  readonly cols: number;
  readonly rows: number;
}

export interface PtyCommand {
  readonly command: string;
  readonly args: ReadonlyArray<string>;
  readonly size: PtySize;
  /**
   * The child's environment, complete. Not merged with this process's — a
   * caller spawning zmx has to be able to *remove* a variable, and a merge
   * cannot express that. See zmxChildEnv.
   */
  readonly env: Readonly<Record<string, string>>;
  readonly term?: string;
}

/**
 * A live pty.
 *
 * Text rather than bytes, throughout. The binding decodes with a single
 * streaming decoder, so a multi-byte character split across two reads arrives
 * whole — which is the hazard that mattered. What it cannot carry is a byte
 * sequence that is not valid UTF-8, and nothing a terminal pane shows needs
 * that: escapes and mouse reports are ASCII, and the inline-image protocols
 * base64 their payloads. See the spec.
 */
export interface PtyHandle {
  readonly pid: number;

  /**
   * Everything the process writes, in order.
   *
   * Unbounded by default, deliberately. Dropping output corrupts a terminal
   * permanently — an escape sequence half-delivered leaves the emulator in a
   * state nothing later corrects — so a slow consumer costs memory rather than
   * correctness.
   */
  readonly output: Stream.Stream<string, PtyError>;

  /** Send input, as a person typing would. */
  readonly write: (data: string) => Effect.Effect<void, PtyError>;

  /**
   * Tell the process it has a new size.
   *
   * Worth remembering what this means through zmx: a session takes its size
   * from the client looking at it, so resizing here reflows whatever is running
   * in that session, for everyone.
   */
  readonly resize: (size: PtySize) => Effect.Effect<void, PtyError>;

  /** Completes when the process exits. */
  readonly exit: Effect.Effect<PtyExit, PtyError>;
}

export interface PtyExit {
  readonly code: number;
  readonly signal: string | number | undefined;
}

/**
 * Opens ptys.
 *
 * Shaped after `effect/unstable/process`'s `ChildProcessSpawner` so it reads as
 * its sibling: that one spawns with pipes and has no notion of a size or a
 * resize, which is exactly the gap this fills.
 */
export class PtySpawner extends Context.Service<
  PtySpawner,
  {
    spawn(command: PtyCommand): Effect.Effect<PtyHandle, PtyError, Scope.Scope>;
  }
>()("@awp-kit/server/PtySpawner") {}

/**
 * The shape a fake implementation fills in.
 *
 * Exported because the fake lives in a test file, and the scripted-chunk idea
 * is worth stating once here rather than reinventing per test.
 */
export interface ScriptedPty {
  /** Chunks the fake emits, in order, as soon as anything reads. */
  readonly chunks: ReadonlyArray<string>;
  /** What the fake exits with once its chunks are exhausted. */
  readonly exit?: PtyExit;
}

/**
 * Turn a queue-driven callback source into a stream.
 *
 * Factored out because both the real and the fake spawner need it, and because
 * getting the completion signal wrong is the difference between a pane that
 * ends and one that hangs: `Queue.endUnsafe` is what closes the stream, and a
 * source that only stops calling back leaves the consumer waiting forever.
 */
export const streamFromCallback = (
  register: (emit: (chunk: string) => void, done: () => void) => Effect.Effect<void>,
): Stream.Stream<string, PtyError> =>
  Stream.callback<string, PtyError>((queue) =>
    register(
      (chunk) => {
        Queue.offerUnsafe(queue, chunk);
      },
      () => {
        Queue.endUnsafe(queue);
      },
    ),
  );
