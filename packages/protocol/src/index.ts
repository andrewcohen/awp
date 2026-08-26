// The RPC contract between an awp client and the daemon.
//
// One file, and it is the whole agreement. The daemon implements what is
// declared here and the renderer calls it; both derive their types from these
// same values, so a change to a payload is a compile error on both sides rather
// than a runtime surprise on one. That property is the reason this package
// exists as its own thing rather than living in the daemon.
//
// ── why effect/unstable/rpc ────────────────────────────────────────────────
// v4 folds rpc into core, so the import is `effect/unstable/rpc` and there is
// no `@effect/rpc` in the tree — that package is the v3 line and peers on
// `effect ^3.22.1`. Depending on both would put two Effect runtimes in one
// workspace, which does not fail like a version problem: two runtimes means two
// sets of Context tags, so a service provided through one is simply not found
// by the other. `test/deps.test.ts` guards it.
//
// `unstable/` is upstream's own label for the surface. Absorbing its churn is
// this package's job: a rename upstream touches this file rather than every
// call site in the daemon and the renderer.

import { Job } from "@awp-kit/jobs";
import { Schema } from "effect";
import { Rpc, RpcGroup } from "effect/unstable/rpc";

/** Bumped when a change here is not backwards compatible. */
export const protocolVersion = 0;

// ── what a session looks like on the wire ──────────────────────────────────
//
// Deliberately not the daemon's own `Session` type. That one is what zmx
// reports; this is what a client is promised, and the two are allowed to drift.
// The daemon maps between them, which is the moment a field that stopped
// existing upstream becomes visible.

/**
 * Which workspace a session belongs to, and what it is doing there.
 *
 * On the wire rather than derived by the client, and that is the whole reason
 * this exists. A name is `awp.<project>.<workspace>.<kind>`, so splitting it on
 * dots looks like it answers the question — and it does not. Shortening rewrites
 * the stem when a name would exceed what zmx accepts, and a dot inside a real
 * project or workspace name comes back as an underscore. The daemon holds the
 * labels awp wrote when it created the session, which are the unshortened truth.
 *
 * Absent for a session awp did not create: `zmx ls` lists every session on the
 * machine, and someone else's is not a workspace.
 */
export const SessionIdentity = Schema.Struct({
  project: Schema.String,
  workspace: Schema.String,
  /** `agent`, `editor`, a user action — what this session is *for*. */
  kind: Schema.String,
});

export type SessionIdentity = (typeof SessionIdentity)["Type"];

export const SessionInfo = Schema.Struct({
  name: Schema.String,
  pid: Schema.Int,
  /**
   * How many clients are looking at it. Each one imposes its size, which is
   * why this is on the wire at all — a client deciding whether to attach needs
   * to know it is about to reflow someone else's terminal.
   */
  clients: Schema.Int,
  startDir: Schema.String,
  /**
   * True once the command has exited. The session is still listed.
   *
   * The field worth reading twice: zmx keeps a session listed after its command
   * exits so the output can still be read, so **listed and running are
   * different questions**. A client that treats presence in the list as life
   * will attach to a dead program's last screen.
   */
  ended: Schema.Boolean,
  exitCode: Schema.Int,
  /** When zmx started it, or absent if zmx did not say. */
  created: Schema.UndefinedOr(Schema.Date),
  cmd: Schema.String,
  /** Everything `zmx ls` printed that was not a known field. */
  labels: Schema.Record(Schema.String, Schema.String),
  /** See {@link SessionIdentity}. Absent if this is not one of awp's. */
  identity: Schema.UndefinedOr(SessionIdentity),
  /**
   * Why this session cannot be attached to, or absent if it can.
   *
   * The daemon's judgement, on the wire, rather than a rule the client
   * re-derives. Two reasons for that. One of them a client could not work out
   * at all — whether this is the session awp itself is running in, which only
   * the daemon knows. And a re-derived copy of the rest is a second
   * implementation of a rule, and the copy that drifts is always the one nobody
   * is testing.
   *
   * A sentence and not a flag, because a disabled row that will not say why is
   * worse than no row. This string is the entire explanation the user gets.
   */
  refusal: Schema.UndefinedOr(Schema.String),
});

export type SessionInfo = (typeof SessionInfo)["Type"];

// ── threads ────────────────────────────────────────────────────────────────
//
// A thread is the piece of work. A workspace is a checkout, and one piece of
// work often needs two of them — a change to thicket's frontend and the api
// behind it is one thread, two projects, two jj workspaces, six sessions. The
// sidebar used to show that as two unrelated rows and leave the connection to
// be held in someone's head.
//
//   thread  "tiered discounts"
//     ├── thicket/tiered-discounts   agent · editor · action
//     └── api/tiered-discounts       agent · editor · action
//
// **A thread holds pairs, not sessions.** Sessions come and go — a workspace
// with nothing running is still part of the work — so what is written down is
// `(project, workspace)`, which is exactly the identity a session already
// reports. That is also why no new label was needed: the sidebar nests a
// session by looking up the pair its identity already carries.
//
// **A workspace belongs to at most one thread**, enforced on attach rather than
// checked on read. Two threads claiming one workspace has no rendering: the
// sidebar would have to draw it twice, and a person would have to decide which
// of the two was lying.

/** One workspace a thread has claimed. */
export const ThreadMember = Schema.Struct({
  project: Schema.String,
  workspace: Schema.String,
});

export type ThreadMember = (typeof ThreadMember)["Type"];

export const Thread = Schema.Struct({
  id: Schema.String,
  /**
   * What the work is called, in a person's words.
   *
   * The one field awp does not derive from anything. A workspace is named after
   * a branch and a session after a workspace, so every name in the system so
   * far has been an address; this is the first one that is a description, and
   * it is the whole reason the thread exists as a record rather than as a
   * grouping rule.
   */
  title: Schema.String,
  createdAt: Schema.Date,
  /**
   * Set when the work is finished. Archived rather than deleted: a thread is
   * the only record that a set of workspaces were once one job, and that is
   * worth more after the fact than during.
   */
  archivedAt: Schema.UndefinedOr(Schema.Date),
  members: Schema.Array(ThreadMember),
});

export type Thread = (typeof Thread)["Type"];

export class ThreadNotFound extends Schema.TaggedError<ThreadNotFound>()("ThreadNotFound", {
  thread: Schema.String,
}) {}

// ── failures a client is expected to handle ────────────────────────────────
//
// Schema-backed so they survive the wire as themselves rather than as a string.
// A client selects one by tag — `Effect.catchTag("AttachRefused", …)`, or
// `Match.tags` for a total handler — and reads its fields directly.
//
// Anything not declared here arrives as a defect instead, which is the correct
// shape for "the daemon broke" as opposed to "the thing you asked for cannot be
// done". Only the second kind belongs in a schema.

export class SessionNotFound extends Schema.TaggedError<SessionNotFound>()("SessionNotFound", {
  session: Schema.String,
}) {}

/**
 * The daemon declined to attach, and the reason is for a human.
 *
 * Refusing is a normal outcome, not an error condition. A session that has
 * ended is still listed; the daemon's own session must never be attached to,
 * because a session takes its size from the client looking at it and attaching
 * would reflow the terminal doing the attaching.
 */
export class AttachRefused extends Schema.TaggedError<AttachRefused>()("AttachRefused", {
  session: Schema.String,
  reason: Schema.String,
}) {}

/** Asked about a job the daemon has no record of. */
export class JobNotFound extends Schema.TaggedError<JobNotFound>()("JobNotFound", {
  job: Schema.String,
}) {}

// ── jobs on the wire ───────────────────────────────────────────────────────
//
// `Job` comes from @awp-kit/jobs rather than being restated here, which is the
// opposite of what SessionInfo does — and for the opposite reason. SessionInfo
// is a translation of what zmx reports, so the two are allowed to drift and the
// mapping is where a change upstream becomes visible. A job record has no
// upstream: awp writes it, stores it and shows it, so a second definition here
// would only be a copy waiting to fall behind.
//
// It is a Schema on both sides already, because the store needed it decoded
// from a row. Sending it is the same decode with a different transport.

/**
 * A demonstration job, and it is here to be deleted.
 *
 * Nothing in awp enqueues real work yet — there is no workspace to create and
 * no CI to watch — so without this the jobs panel is an empty list that cannot
 * be shown to be working. Every state a person needs to see is reachable from
 * one payload: it succeeds, it retries, it exhausts its attempts and rolls
 * back, or its rollback fails and leaves the job dirty.
 *
 * When the first real kind lands, this and the `demo` kind behind it go.
 */
export const DemoJob = Schema.Struct({
  /** Milliseconds each step takes, so the panel has something to show. */
  pace: Schema.Int,
  /**
   * Fail on this step, one-based, or never if absent. See `DEMO_STEPS`.
   *
   * `optional` and not `UndefinedOr`: this crosses JSON on its way into the
   * job store, and JSON has no `undefined` — so a field spelled `UndefinedOr`
   * and left unset comes back *absent*, which `UndefinedOr` rejects. The
   * runner refuses such a kind at enqueue rather than letting it fail on its
   * first step; see `InputNotPortable`.
   */
  failAt: Schema.optional(Schema.Int),
  /** Whether that failure is worth retrying. */
  retryable: Schema.Boolean,
  /** Whether the rollback itself fails — the one outcome a person must act on. */
  undoFails: Schema.Boolean,
});

export type DemoJob = (typeof DemoJob)["Type"];

// ── the calls ──────────────────────────────────────────────────────────────

export class AwpRpcs extends RpcGroup.make(
  /** Every session the multiplexer knows about, awp's or not. */
  Rpc.make("SessionList", {
    success: Schema.Array(SessionInfo),
  }),

  /**
   * Attach to a session and receive its output until the client goes away.
   *
   * `stream: true` is why rpc was worth using rather than a hand-rolled socket:
   * the stream's lifetime is the request's lifetime, so a client that drops
   * interrupts the handler, which releases the pty's Scope, which kills the
   * process. Nothing has to notice the disconnection and clean up by hand.
   *
   * Chunks are `String`, not bytes. There is no byte stage anywhere on this
   * path — the pty hands out strings and `term.write` takes them — so encoding
   * to bytes here would exist only to be undone at the other end.
   *
   * The size is part of attaching, not a call that follows it. A client knows
   * its own geometry before it asks, and opening at some default and resizing
   * afterwards would reflow the real session twice — visibly, since the first
   * reflow is at the wrong size and whatever is running redraws for it.
   */
  Rpc.make("Attach", {
    payload: { session: Schema.String, cols: Schema.Int, rows: Schema.Int },
    success: Schema.String,
    error: AttachRefused,
    stream: true,
  }),

  /**
   * Keystrokes, going the other way.
   *
   * Separate from Attach because rpc streams one way: the server may stream to
   * the client, not the reverse. Keyed by session name rather than by some
   * handle returned from Attach, so that a client which reconnects can resume
   * typing without first re-establishing an identity.
   *
   * Do not await this on the keystroke path. The reply is an acknowledgement,
   * and the echo a typist actually waits for comes back through the Attach
   * stream — awaiting the ack before sending the next key would put a full
   * round trip between keystrokes for no benefit.
   */
  Rpc.make("Write", {
    payload: { session: Schema.String, data: Schema.String },
    error: SessionNotFound,
  }),

  /** Tell the pty its new size. The session takes its size from its client. */
  Rpc.make("Resize", {
    payload: { session: Schema.String, cols: Schema.Int, rows: Schema.Int },
    error: SessionNotFound,
  }),

  /** Every job the daemon has a record of, newest first. */
  Rpc.make("JobList", {
    success: Schema.Array(Job),
  }),

  /**
   * Each record as it changes, for as long as the client is listening.
   *
   * A stream rather than a poll because the interesting moments are short: a
   * step starting, an attempt failing, a rollback finishing. A list refreshed
   * on a timer shows the state between them and nothing else, which is how a
   * job that took two seconds looks like a job that never ran.
   *
   * Carries whole records, not patches. A client that joins late, or misses a
   * message because the feed slid, is still correct — it has the newest state
   * of every job it has heard about, which is all a list renders.
   */
  Rpc.make("JobChanges", {
    success: Job,
    stream: true,
  }),

  /** What a job wrote about itself. The end of it — see `LOG_LINES`. */
  Rpc.make("JobLog", {
    payload: { job: Schema.String },
    success: Schema.Array(Schema.String),
    error: JobNotFound,
  }),

  /**
   * Run a finished job again.
   *
   * Not an error when the job is still running: it is already trying, and
   * returning the record unchanged is a truer answer than a failure.
   */
  Rpc.make("JobRetry", {
    payload: { job: Schema.String },
    success: Job,
    error: JobNotFound,
  }),

  /** Stop a job, undo what it did, and mark it cancelled. */
  Rpc.make("JobCancel", {
    payload: { job: Schema.String },
    error: JobNotFound,
  }),

  /** See {@link DemoJob}. Goes when the first real kind arrives. */
  Rpc.make("JobDemo", {
    payload: DemoJob,
    success: Job,
  }),

  /**
   * Every thread, newest first, archived ones included.
   *
   * No stream beside it, unlike jobs, and the difference is the point. A job
   * changes on its own — that is what a job is — so a client that only asks is
   * a client that misses everything interesting. A thread changes when a person
   * changes it, in this window, so the reply to the change is the update.
   */
  Rpc.make("ThreadList", {
    success: Schema.Array(Thread),
  }),

  Rpc.make("ThreadCreate", {
    payload: { title: Schema.String },
    success: Thread,
  }),

  Rpc.make("ThreadRename", {
    payload: { thread: Schema.String, title: Schema.String },
    success: Thread,
    error: ThreadNotFound,
  }),

  /** Archive, or bring one back — `archived: false` undoes it. */
  Rpc.make("ThreadArchive", {
    payload: { thread: Schema.String, archived: Schema.Boolean },
    success: Thread,
    error: ThreadNotFound,
  }),

  /**
   * Claim a workspace for this thread, releasing it from whichever thread held
   * it before. See {@link Thread} for why the second claim simply wins.
   */
  Rpc.make("ThreadAttach", {
    payload: { thread: Schema.String, member: ThreadMember },
    success: Thread,
    error: ThreadNotFound,
  }),

  Rpc.make("ThreadDetach", {
    payload: { thread: Schema.String, member: ThreadMember },
    success: Thread,
    error: ThreadNotFound,
  }),
) {}
