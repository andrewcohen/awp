import type { Job } from "@awp-kit/jobs";
import { AwpClient, layerClient } from "@awp-kit/protocol/client";
import type { DemoJob, Effort, SessionInfo, Thread, ThreadStarted } from "@awp-kit/protocol";
import { Effect, Fiber, Stream } from "effect";
import { ManagedRuntime } from "effect";

// The renderer's one connection to the daemon.
//
// A ManagedRuntime and not a runtime per call, because building a client opens
// the socket — a client per call would be a connection per keystroke.
//
// This is the seam between Effect and React, and it is deliberately narrow.
// Everything above it is plain promises and callbacks, which is what React
// components are good at; everything below it keeps its Scopes, error channels
// and interruption, which is what makes the pty get killed when a pane closes.
// Widening this file is how those two worlds start leaking into each other.

const runtime = ManagedRuntime.make(layerClient());

/** Every session the multiplexer knows about. */
export const listSessions = (): Promise<ReadonlyArray<SessionInfo>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.SessionList()));

/** Keystrokes. Fire and forget — see below. */
export const write = (session: string, data: string): void => {
  runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) => rpc.Write({ session, data })).pipe(
      // Deliberately not awaited and deliberately not surfaced. The reply is an
      // acknowledgement; the echo a typist waits for arrives through the attach
      // stream instead, so awaiting this would put a round trip between
      // keystrokes for nothing. A write that fails means the session went away,
      // which the stream ending says more clearly than a toast would.
      Effect.catchCause(() => Effect.void),
    ),
  );
};

export const resize = (session: string, cols: number, rows: number): void => {
  runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) => rpc.Resize({ session, cols, rows })).pipe(
      Effect.catchCause(() => Effect.void),
    ),
  );
};

export interface Attachment {
  /** Stops the stream, which releases the pty if this was the last client. */
  readonly detach: () => void;
}

/**
 * Attach to a session and receive its output until `detach`.
 *
 * The returned function is the whole lifecycle, and interrupting the fiber is
 * not merely tidy: the stream's lifetime is the request's lifetime, so this
 * interrupt travels down the socket, cancels the handler, closes the pty's
 * Scope and kills the process. Forgetting to call it leaves a zmx client
 * attached, which means a session sized to a window that no longer exists.
 */
export const attach = (
  session: string,
  cols: number,
  rows: number,
  handlers: {
    readonly onChunk: (chunk: string) => void;
    readonly onRefused: (reason: string) => void;
  },
): Attachment => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) =>
      Stream.runForEach(rpc.Attach({ session, cols, rows }), (chunk) =>
        Effect.sync(() => handlers.onChunk(chunk)),
      ),
    ).pipe(
      // A refusal is a sentence written for a person — the session ended, or it
      // is the daemon's own — and the pane shows it instead of going blank.
      Effect.catchTag("AttachRefused", (error) =>
        Effect.sync(() => handlers.onRefused(error.reason)),
      ),
      // Interruption is how detach works, so it is not a failure to report.
      Effect.catchCause(() => Effect.void),
    ),
  );

  return {
    detach: () => {
      runtime.runFork(Fiber.interrupt(fiber));
    },
  };
};

// ── jobs ───────────────────────────────────────────────────────────────────

/** Every job the daemon has a record of, newest first. */
export const listJobs = (): Promise<ReadonlyArray<Job>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobList()));

export const jobLog = (job: string): Promise<ReadonlyArray<string>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobLog({ job })));

export const retryJob = (job: string): Promise<Job> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobRetry({ job })));

export const cancelJob = (job: string): Promise<void> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobCancel({ job })));

/** See `DemoJob` in the contract. Goes when the first real kind arrives. */
export const enqueueDemo = (payload: DemoJob): Promise<Job> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobDemo(payload)));

// ── threads and workspaces ─────────────────────────────────────────────────
//
// No watcher beside these, unlike jobs, and the asymmetry is the point. A job
// changes on its own; a thread changes when a person changes it, in this
// window, so the reply to the change *is* the update.

export const listThreads = (): Promise<ReadonlyArray<Thread>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadList()));

/**
 * Start a thread from a sentence, and get back the thread and the job.
 *
 * The slow call — a model turns what was typed into a name, a title and an
 * instruction, and that takes about ten seconds. It happens in the daemon
 * rather than here, and the job it returns is where the rest of the work shows
 * up: see `watchJobs`.
 */
export const startThread = (payload: {
  readonly description: string;
  readonly project: string;
  /** A directory in the project — a session's `startDir` will do. */
  readonly from: string;
  readonly base?: string | undefined;
  /**
   * Overrides for the configured agent command, or absent to leave it alone.
   *
   * Absent is not the same as passing what the config already says: the daemon
   * replaces the flag in place, so a value here wins and no value means the
   * config does. See `agentWith` in the server's settings.ts.
   */
  readonly model?: string | undefined;
  readonly effort?: Effort | undefined;
}): Promise<ThreadStarted> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadStart(payload)));

/**
 * Watch every job change until the returned function is called.
 *
 * The same shape as `attach`, and for the same reason: the stream's lifetime is
 * the request's, so interrupting the fiber is what unsubscribes the daemon's
 * end. A component that forgets to call it leaves a subscriber on the feed for
 * as long as the window lives.
 *
 * Records arrive whole, so a listener that joins late or misses one is still
 * correct — it holds the newest state of every job it has heard about.
 */
export const watchJobs = (onJob: (job: Job) => void): (() => void) => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) =>
      Stream.runForEach(rpc.JobChanges(), (job) => Effect.sync(() => onJob(job))),
    ).pipe(Effect.catchCause(() => Effect.void)),
  );

  return () => {
    runtime.runFork(Fiber.interrupt(fiber));
  };
};
