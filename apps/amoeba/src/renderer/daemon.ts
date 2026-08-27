import type { Job } from "@awp-kit/jobs";
import { AwpClient, layerClient } from "@awp-kit/protocol/client";
import type {
  CommentSide,
  Effort,
  Patch,
  ReviewComment,
  ReviewSent,
  Revision,
  SessionInfo,
  Thread,
  ThreadBase,
  ThreadMember,
  ThreadStarted,
} from "@awp-kit/protocol";
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

/**
 * Forget every job that is over, and answer with how many.
 *
 * The daemon decides which those are — see `JobClear` in the contract. This is
 * why the reply is a count rather than nothing: a button that says it cleared
 * the list while three rows stay put needs to be able to explain itself.
 */
export const clearJobs = (): Promise<number> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.JobClear()));

// ── threads and workspaces ─────────────────────────────────────────────────
//
// No watcher beside these, unlike jobs, and the asymmetry is the point. A job
// changes on its own; a thread changes when a person changes it, in this
// window, so the reply to the change *is* the update.

export const listThreads = (): Promise<ReadonlyArray<Thread>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadList()));

/**
 * Take an existing workspace into a thread, or out of one.
 *
 * ── why this exists at all ─────────────────────────────────────────────────
 * A thread is normally made by *starting* one, which creates the workspace as
 * its first member. That covers everything from now on and nothing from
 * before: every workspace on a machine that has been used predates threads and
 * lands in the "not in a thread" group, with no way out of it. Twenty-three of
 * twenty-six here.
 *
 * All three calls were already on the wire and reachable by nothing — the
 * daemon has had `ThreadCreate`, `ThreadAttach` and `ThreadDetach` since
 * threads landed. This is the window catching up, not new capability.
 *
 * A workspace belongs to at most one thread, and that is a UNIQUE constraint
 * rather than a rule remembered here: `attach` is one `on conflict do update`,
 * so the release and the claim cannot half happen and a caller never has to
 * detach first. See `threads.ts`.
 */
export const createThread = (title: string): Promise<Thread> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadCreate({ title })));

export const attachThread = (thread: string, member: ThreadMember): Promise<Thread> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadAttach({ thread, member })));

export const detachThread = (thread: string, member: ThreadMember): Promise<Thread> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadDetach({ thread, member })));

/**
 * Everywhere a new workspace in this project could start from.
 *
 * Asked of the daemon rather than worked out here, because turning a branch
 * name into a revset needs the bookmark prefix from its config and the local
 * bookmark list from jj — see `ThreadBase` in the contract.
 */
export const threadBases = (from: string): Promise<ReadonlyArray<ThreadBase>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ThreadBases({ from })));

/**
 * Start a thread from a sentence, and get back the thread and the job.
 *
 * **Returns as soon as the record exists.** Naming the workspace takes a model
 * about ten seconds, and that is the job's first step now rather than something
 * this call waits for — so the jobs panel has something to show from the moment
 * the button is pressed. See `watchJobs`.
 */
export const startThread = (payload: {
  readonly description: string;
  readonly project: string;
  /** A directory in the project — a session's `startDir` will do. */
  readonly from: string;
  /** A thread to branch from. The probe uses it; the window sends `base`. */
  readonly parent?: string | undefined;
  /**
   * Where to start — a `ThreadBase.revset`, so `trunk()` or a bookmark name.
   *
   * The daemon works out which thread that base belongs to, if any, and records
   * it as the new thread's parent. So branching off a workspace no thread has
   * claimed works and simply records no lineage — the ordinary case on a
   * machine whose workspaces predate threads, and the reason the picker used to
   * come up empty.
   */
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

/**
 * Watch one workspace's files until the returned function is called.
 *
 * The same shape as {@link watchJobs}, and for the same reason — the stream's
 * lifetime is the request's, so interrupting the fiber is what unsubscribes
 * the daemon's end.
 *
 * What arrives is a tick, not a patch. The panel may be showing a commit,
 * which does not change because a file was written, so what to re-read is the
 * caller's decision. See `WorkspaceChanges` in the contract.
 */
export const watchWorkspace = (from: string, onChange: () => void): (() => void) => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) =>
      Stream.runForEach(rpc.WorkspaceChanges({ from }), () => Effect.sync(() => onChange())),
    ).pipe(Effect.catchCause(() => Effect.void)),
  );

  return () => {
    runtime.runFork(Fiber.interrupt(fiber));
  };
};

// ── the diff of a workspace ────────────────────────────────────────────────

/**
 * The commits worth looking at in the workspace containing `from`.
 *
 * `from` is a session's `startDir`, the same handle {@link threadBases} takes.
 * The window has a directory, never a workspace name, and `@` is resolved per
 * workspace — so a directory is the only thing that names one of these.
 */
export const listRevisions = (from: string, limit?: number): Promise<ReadonlyArray<Revision>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.Revisions({ from, limit })));

/**
 * The patch for one revision, or for the working copy when none is named.
 *
 * **Leaving `revision` out is not the same as passing the working copy's own
 * change id.** Only the absent form snapshots the files on disk first, so only
 * the absent form shows what an agent has written and not yet committed. See
 * `Diff` in the contract.
 */
export const readDiff = (from: string, revision?: string): Promise<Patch> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.Diff({ from, revision })));

// ── comments on a diff ─────────────────────────────────────────────────────
//
// Four calls, and the shape of them is the batching decision made concrete: a
// comment is *written* one at a time and *delivered* all at once. Nothing here
// types at an agent except `sendReview`.

/** Every comment about this workspace, draft and sent, oldest first. */
export const listComments = (
  project: string,
  workspace: string,
): Promise<ReadonlyArray<ReviewComment>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ReviewList({ project, workspace })));

/**
 * Write one down. Always a draft.
 *
 * The id and the timestamp come back from the daemon rather than being made
 * here — the panel's order is `createdAt`, and two windows minting ids from two
 * clocks would interleave wrongly.
 */
export const addComment = (comment: {
  readonly project: string;
  readonly workspace: string;
  /** A change id, or `@` for the working copy — the row the diff is showing. */
  readonly revision: string;
  readonly path: string;
  readonly side: CommentSide;
  /** The first line of the range, and the last. Equal for a single line. */
  readonly line: number;
  readonly endLine: number;
  readonly body: string;
}): Promise<ReviewComment> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ReviewAdd(comment)));

/** Delete one. Not an error when it has already gone. */
export const removeComment = (comment: string): Promise<boolean> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ReviewRemove({ comment })));

/**
 * Tell the agent every unsent comment about this workspace.
 *
 * **Which ones is the daemon's decision, not this call's.** Passing a list of
 * ids would mean sending what was on screen a moment ago, and a comment written
 * in between would be marked delivered without appearing in the prompt.
 *
 * Rejects with `NoAgent` when the workspace has no agent session to type into,
 * and marks nothing in that case — an undeliverable comment is still a draft.
 */
export const sendReview = (project: string, workspace: string): Promise<ReviewSent> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ReviewSend({ project, workspace })));
