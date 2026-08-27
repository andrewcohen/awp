import type { Job } from "@awp-kit/jobs";
import {
  AwpClient,
  type AwpClientShape,
  layerClient,
  layerConnection,
} from "@awp-kit/protocol/client";
import type {
  CommentSide,
  Effort,
  PageNote,
  Patch,
  Project,
  ReviewComment,
  ReviewSent,
  Revision,
  SessionInfo,
  Thread,
  ThreadBase,
  ThreadMember,
  ThreadStarted,
  WorkspaceFacts,
} from "@awp-kit/protocol";
import { Effect, Fiber, Schedule, Stream } from "effect";
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

// ── is the daemon there ────────────────────────────────────────────────────
//
// A real signal, rather than the proxy this used to be: whether the last
// session listing happened to fail. That answers "did a request work", which
// gets read as "is the daemon there" — the same thing only until a call
// succeeds against a connection that dies a second later.
//
// Two listeners want it and they want different halves. The bar says "no
// daemon" while it is down; `useSessions` and `useThreads` have to *re-ask*
// when it comes back, because a list is an answer and not a feed — nothing
// will arrive to tell them what changed while they were not listening.

let live = false;
const watchers = new Set<(connected: boolean) => void>();

const announce = (connected: boolean): void => {
  if (live === connected) {
    return;
  }
  live = connected;
  // Iterated directly. A Set tolerates a delete during iteration, which is the
  // case that actually happens here — a watcher unsubscribing itself from
  // inside its own callback.
  for (const watcher of watchers) {
    watcher(connected);
  }
};

/**
 * Hear about the connection until the returned function is called.
 *
 * The current state is delivered immediately, so a component mounting while the
 * daemon is down does not have to wait for it to come back to find out.
 */
export const watchConnection = (onChange: (connected: boolean) => void): (() => void) => {
  watchers.add(onChange);
  onChange(live);
  return () => {
    watchers.delete(onChange);
  };
};

/**
 * Run `again` each time the daemon comes back, but not for the state it is in
 * now.
 *
 * The distinction is the whole of it. `watchConnection` reports the current
 * value the moment it is called, which is what a status bar wants and is
 * exactly wrong for a reload: a component that has just asked would ask a
 * second time for the same answer. What a list wants is the *transition*.
 */
export const onReconnect = (again: () => void): (() => void) => {
  let first = true;
  return watchConnection((connected) => {
    if (first) {
      first = false;
      return;
    }
    if (connected) {
      again();
    }
  });
};

const runtime = ManagedRuntime.make(
  layerClient(
    undefined,
    layerConnection({ opened: () => announce(true), lost: () => announce(false) }),
  ),
);

// How long to wait before subscribing again after the connection took a feed
// out.
//
// The same shape as the socket's own policy and deliberately not the same
// object: this backs off a *resubscribe*, which is cheap and wants to be quick
// once the socket is up, where the socket's backs off connecting to a daemon
// that may not be running at all. Capped, so a window left open overnight
// against a daemon that is not coming back is not asking every half second
// until morning.
const RESUBSCRIBE = Schedule.min([Schedule.exponential(500, 1.5), Schedule.spaced(5000)]);

/**
 * Run a feed for as long as the caller wants it, across reconnections.
 *
 * ── why every feed needs this and not just the socket ──────────────────────
 *
 * `layerClient` reconnects the socket, and that is necessary and not
 * sufficient: an rpc stream is a *request*, so its fiber dies with the
 * connection it was made on. Without a retry here the socket comes back and the
 * window goes on showing whatever it last heard — the failure the reconnect was
 * added to fix, moved one layer up and made harder to see, because now the
 * status bar says the daemon is fine.
 *
 * `run` is handed the client and returns the whole effect rather than only a
 * stream, so a caller can answer its own failures before the retry sees them.
 * That is how a refusal stops the loop: `attach` catches `AttachRefused` into a
 * sentence for a person, and what reaches the retry is only what is worth
 * trying again.
 *
 * Interruption is how unsubscribing works and is not retried — it arrives as a
 * cause rather than a failure, so it passes through untouched.
 */
const subscribe = <E>(run: (rpc: AwpClientShape) => Effect.Effect<void, E>): (() => void) => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, run).pipe(
      Effect.retry(RESUBSCRIBE),
      Effect.catchCause(() => Effect.void),
    ),
  );

  return () => {
    runtime.runFork(Fiber.interrupt(fiber));
  };
};

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
  // Reattaches when the daemon comes back, and what arrives then is a redraw
  // rather than the scrollback a second time: `zmx attach` paints the session's
  // current screen, which is what reattaching in a multiplexer has always done.
  // The size goes with it, so the session is sized to this window again on the
  // way back in.
  const detach = subscribe((rpc) =>
    Stream.runForEach(rpc.Attach({ session, cols, rows }), (chunk) =>
      Effect.sync(() => handlers.onChunk(chunk)),
    ).pipe(
      // A refusal is a sentence written for a person — the session ended, or it
      // is the daemon's own — and the pane shows it instead of going blank.
      //
      // Caught *inside*, so the retry never sees it. "The session ended" is an
      // answer rather than an outage, and asking again every half second would
      // replace a sentence somebody can read with a loop.
      Effect.catchTag("AttachRefused", (error) =>
        Effect.sync(() => handlers.onRefused(error.reason)),
      ),
    ),
  );

  return { detach };
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

// ── projects ───────────────────────────────────────────────────────────────
//
// The list is the daemon's answer and not this window's, because it is the
// union of two things only the daemon holds: what has been imported, and what
// the running sessions imply. It used to be derived here from the session
// listing alone, which meant a repository nothing was running in could not be
// picked — so the first thread in any repository could not be started at all.

export const listProjects = (): Promise<ReadonlyArray<Project>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ProjectList()));

/**
 * Repositories under the configured roots that are not imported yet.
 *
 * Asked for when a picker opens rather than kept with the list, because it
 * costs a walk of somebody's filesystem and the list does not.
 */
export const projectCandidates = (): Promise<ReadonlyArray<Project>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ProjectCandidates()));

/**
 * Import a path. Rejects with the daemon's sentence about why not.
 *
 * A path *inside* the project is enough — the daemon resolves the repository
 * root, which this window cannot: `jj root` inside a secondary workspace
 * answers with the workspace.
 */
export const importProject = (path: string): Promise<Project> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ProjectImport({ path })));

/** Forget one. Takes no workspace, session or thread with it. */
export const forgetProject = (name: string): Promise<boolean> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.ProjectForget({ name })));

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
export const watchJobs = (onJob: (job: Job) => void): (() => void) =>
  subscribe((rpc) => Stream.runForEach(rpc.JobChanges(), (job) => Effect.sync(() => onJob(job))));

/**
 * Watch what is known about every workspace until the returned function is
 * called.
 *
 * The whole table arrives each time, so a listener that joins late or misses a
 * push is still correct — which is what lets this replace its state outright
 * rather than merging, and is why nothing here has to know what changed.
 *
 * A stream and not a call, unlike threads: an agent goes from working to
 * waiting on its own, and a window that only asked would miss the transition it
 * was watching for.
 */
export const watchFacts = (onFacts: (facts: ReadonlyArray<WorkspaceFacts>) => void): (() => void) =>
  subscribe((rpc) =>
    Stream.runForEach(rpc.WorkspaceFactsChanges(), (facts) => Effect.sync(() => onFacts(facts))),
  );

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
export const watchWorkspace = (from: string, onChange: () => void): (() => void) =>
  subscribe((rpc) =>
    Stream.runForEach(rpc.WorkspaceChanges({ from }), () => Effect.sync(() => onChange())),
  );

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

/**
 * Tell the agent about one element of one page, now.
 *
 * Unbatched, unlike `sendReview`, and there is nothing to mark afterwards: a
 * page note has no draft state to be in. See `NoteSend` in the contract for
 * why the two are shaped differently.
 *
 * Rejects with `NoAgent` when the workspace has no agent to type into. Resolves
 * with the prompt that was typed, which is what the panel shows when someone
 * asks what was actually said.
 */
export const sendNote = (project: string, workspace: string, note: PageNote): Promise<string> =>
  runtime.runPromise(
    Effect.flatMap(AwpClient, (rpc) => rpc.NoteSend({ project, workspace, note })),
  );
