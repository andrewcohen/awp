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
  /**
   * What a person called this work, or absent.
   *
   * The one field here that is not part of the address: `workspace` is a slug
   * because it has to be a directory and half a bookmark, and this is the
   * sentence it was made from. Absent for every session created before awp
   * wrote it, which is most of them — a reader falls back to the slug.
   *
   * `Schema.UndefinedOr`, like every other absent-able field on this wire —
   * `created`, `identity` and `refusal` are all spelled that way and all
   * survive a round trip today, which is the evidence that settles it. The
   * cost is that the key is required, so the two places that build an identity
   * say `label: undefined` rather than leaving it out.
   */
  label: Schema.UndefinedOr(Schema.String),
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
// work often needs two of them — a change to rowan's frontend and the api
// behind it is one thread, two projects, two jj workspaces, six sessions. The
// sidebar used to show that as two unrelated rows and leave the connection to
// be held in someone's head.
//
//   thread  "tiered discounts"
//     ├── rowan/tiered-discounts   agent · editor · action
//     └── beta/tiered-discounts       agent · editor · action
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
  /**
   * The thread this one branched from, if it branched from one.
   *
   * Recorded rather than re-derived, and that is the point of the field. The
   * relationship *could* be recovered later by asking jj which revision each
   * workspace descends from — but that answers a question about commits, and
   * this is a claim about work: someone said "this follows from that" at the
   * moment they started it. jj's answer also changes as branches are rebased,
   * merged and deleted, and the intent does not.
   *
   * It decides the base revision at creation and is kept afterwards, so a
   * thread can say where it came from once the bookmark it started on is gone.
   */
  parentId: Schema.UndefinedOr(Schema.String),
  members: Schema.Array(ThreadMember),
});

export type Thread = (typeof Thread)["Type"];

export class ThreadNotFound extends Schema.TaggedError<ThreadNotFound>()("ThreadNotFound", {
  thread: Schema.String,
}) {}

/**
 * A thread could not be started — the model was unreachable, or answered with
 * something unusable.
 *
 * Its own failure rather than a defect: it happens, it is the user's to see,
 * and the sentence is the one the model or the CLI produced.
 */
export class ThreadStartFailed extends Schema.TaggedError<ThreadStartFailed>()(
  "ThreadStartFailed",
  { reason: Schema.String },
) {}

/**
 * How hard the agent is asked to think.
 *
 * A union rather than a string, because these five are `claude --effort`'s
 * whole vocabulary — the CLI rejects anything else, and a typo that reached the
 * daemon would become a session that dies on its first line rather than a
 * message anyone can read. Absent means whatever the configured agent command
 * already says, which is the case that must stay expressible.
 */
export const Effort = Schema.Literals(["low", "medium", "high", "xhigh", "max"]);

export type Effort = (typeof Effort)["Type"];

/**
 * Which model the agent runs.
 *
 * A string and *not* a union, unlike {@link Effort}, and the asymmetry is
 * deliberate: `--model` takes an alias (`opus`) or a full id (`claude-opus-5`),
 * and both sets move faster than this file does. A union here would refuse a
 * model that exists.
 */
export const Model = Schema.String;

/**
 * What making a workspace needs to know.
 *
 * The one input a person actually supplies is `workspace` — the rest is the
 * project it belongs to, the thread it is for, and where its code comes from.
 *
 * `agent` is on the payload rather than read from a config the daemon holds,
 * and deliberately for now: there is no config service, and a command baked
 * into the job is a command nobody can see. It moves the day settings exist.
 */
export const CreateWorkspace = Schema.Struct({
  /** The thread that claims it. See {@link Thread}. */
  thread: Schema.String,
  project: Schema.String,
  /**
   * What a person typed, in their own words.
   *
   * The one field here nothing can derive, and the seed for three that are
   * derived from it: `workspace`, `label` and `prompt`.
   */
  description: Schema.String,
  /**
   * The workspace's name: a directory, and jj's name for it.
   *
   * **Optional on the way in, present from the first step onward.** A model
   * turns the description into it, and that takes about ten seconds — which
   * used to happen before the job was enqueued, in front of a person watching
   * a form that would not close. The `name` step does it now and records the
   * answer here, so the job exists the instant it is asked for and the waiting
   * happens where there is a progress panel to show it.
   *
   * Recorded rather than recomputed, so a resumed job uses the same name
   * instead of asking again and possibly getting a different one. See
   * `JobStep.run` in @awp-kit/jobs for why a step may write here at all.
   */
  workspace: Schema.optional(Schema.String),
  /** What the sidebar shows for the thread. Resolved by the same step. */
  label: Schema.optional(Schema.String),
  /**
   * What to type into the new agent session once it exists, or absent for
   * nothing. A workspace with no instruction is a workspace, not an error.
   */
  prompt: Schema.optional(Schema.String),
  /**
   * The repository the workspace comes from — the *source* repo, not a
   * workspace of it. `jj root` answers with a workspace and is the wrong thing
   * to put here; the daemon resolves it with `Jj.sourceRoot`.
   */
  repo: Schema.String,
  /**
   * The revision the new workspace starts from. Absent means jj's default,
   * which is the parents of whichever workspace the command ran in — not a
   * useful answer for a daemon, so callers should pass one.
   */
  base: Schema.optional(Schema.String),
  /**
   * A bookmark to point at the new workspace, or absent for none.
   *
   * Composed by the `name` step from the configured prefix and the name it
   * just resolved, because neither is known before then. Absent means no
   * bookmark at all rather than an unprefixed one: a bare workspace name in a
   * shared repository's bookmark list is a name nobody can attribute.
   */
  bookmark: Schema.optional(Schema.String),
  /** What the agent session runs. */
  agent: Schema.Array(Schema.String),
});

export type CreateWorkspace = (typeof CreateWorkspace)["Type"];

/**
 * Somewhere a new workspace could start from.
 *
 * Composed by the daemon rather than by a client, and that is the point of the
 * type existing at all. A base is a jj revset; the things a person recognises
 * are branch names and the work they are looking at. Turning the second into
 * the first needs the bookmark prefix from the daemon's config and the local
 * bookmark list from jj, and a client has neither.
 *
 * This replaced a list of *threads*, which was the first attempt and was wrong
 * in a way that only showed up in use: most workspaces on a real machine
 * predate threads and belong to none, so the picker was empty exactly when
 * someone stood in a workspace and wanted to branch off it.
 */
/**
 * A side of a diff line. The library's word for it, not a new one.
 *
 * `@pierre/diffs` calls the two halves of a unified diff `deletions` and
 * `additions`, and a comment has to name the same thing the renderer does or
 * the anchor cannot be drawn. Inventing "before"/"after" here would mean a
 * translation at every boundary and one place that forgets it.
 */
export const CommentSide = Schema.Literals(["deletions", "additions"]);

export type CommentSide = (typeof CommentSide)["Type"];

/**
 * Something a person said about one line of one revision.
 *
 * ── the anchor is five fields, and all five are needed ─────────────────────
 * `revision` because the same line of the same file says different things in
 * two commits; `path` for the obvious reason; `line` and `endLine` because a
 * remark is usually about a block rather than a line; and `side`, because a
 * unified diff shows a changed line twice and a comment on the old one is not a
 * comment on the new one.
 *
 * ── `sentAt` is the state, and there are only two ──────────────────────────
 * Absent is a draft — editable, deletable, counted in "3 unsent". Present means
 * the agent has been told, and when. One nullable field rather than a status
 * word, because two states do not need a vocabulary and the interesting
 * question is *when*.
 *
 * Sent comments are kept. A review is a record of what was asked for, and
 * deleting each one as it was delivered would leave the panel looking like
 * nobody had said anything.
 */
export const ReviewComment = Schema.Struct({
  id: Schema.String,
  project: Schema.String,
  workspace: Schema.String,
  /** The change id the comment was made against, or `@` for the working copy. */
  revision: Schema.String,
  path: Schema.String,
  side: CommentSide,
  /** One-based, as the diff renders it. The first line of the range. */
  line: Schema.Int,
  /**
   * The last line of the range, on the same side. Equal to `line` for one line.
   *
   * A range and not a line, because a remark is often about a block — three
   * lines of a condition, a whole added function — and a comment pinned to the
   * first of them makes the reader work out where it stops.
   *
   * **Both numbers are read on `side`.** A selection dragged across the
   * boundary of a unified diff has its two ends numbered on different sides,
   * and those numbers cannot be compared: line 12 of the deletions is not
   * before or after line 40 of the additions. The client collapses that case to
   * the end alone rather than storing a span that means nothing — see
   * `spanOf` in Diff.tsx.
   */
  endLine: Schema.Int,
  body: Schema.String,
  createdAt: Schema.Date,
  /** When the agent was told, or absent while it is a draft. */
  sentAt: Schema.UndefinedOr(Schema.Date),
});

export type ReviewComment = (typeof ReviewComment)["Type"];

/**
 * What {@link Rpc ReviewSend} hands back.
 *
 * The comments it marked *and* what it typed at the agent. The second is not
 * for display — it is what makes the call testable and what a person can be
 * shown when they ask what was actually said, which is otherwise knowable only
 * by scrolling the agent's own terminal back.
 */
export const ReviewSent = Schema.Struct({
  sent: Schema.Array(ReviewComment),
  prompt: Schema.String,
});

export type ReviewSent = (typeof ReviewSent)["Type"];

/**
 * A remark about one element of one page.
 *
 * ── it is not a {@link ReviewComment}, and forcing it to be one would lie ──
 * A review comment is anchored by `revision`, `path`, `side` and two line
 * numbers, and a page has none of those. What it has instead is a URL and a
 * selector, which name a thing in a document somebody else is serving — an
 * anchor that can stop resolving between one press of reload and the next.
 * Storing that in the same table would make five columns meaningless for half
 * the rows, and `path:12` — the form the whole review prompt is built on —
 * would have nothing to put in it.
 *
 * ── `label` and `text` are both here, and they answer different questions ──
 * `selector` is for a machine and is often unreadable; `label` is the short
 * name a person recognises (`button.primary`); `text` is what the element said,
 * capped, which is what makes the note findable when the selector has rotted.
 * An agent handed only a selector has to fetch the page to know what was meant.
 */
export const PageNote = Schema.Struct({
  /** Where the page was. Sent as loaded, not as typed. */
  url: Schema.String,
  /** A CSS selector for the element, best-effort — see `annotate.ts`. */
  selector: Schema.String,
  /** A short readable name for the element: tag, id and one class. */
  label: Schema.String,
  /** What the element said, trimmed and capped. May be empty. */
  text: Schema.String,
  /** What the person said about it. */
  body: Schema.String,
});

export type PageNote = (typeof PageNote)["Type"];

/** No session to tell. A workspace whose agent has ended, or never started. */
export class NoAgent extends Schema.TaggedError<NoAgent>()("NoAgent", {
  project: Schema.String,
  workspace: Schema.String,
}) {}

export const ThreadBase = Schema.Struct({
  /** What to hand jj: `trunk()`, or a bookmark name. */
  revset: Schema.String,
  /** What to show. Never a revset if a person would not recognise one. */
  label: Schema.String,
  /**
   * The awp workspace this base belongs to, when it belongs to one.
   *
   * Recovered from the naming convention — a bookmark is `<prefix>/<name>` —
   * and used for two things: preselecting the base a person is standing in,
   * and recording which thread the new one followed from.
   */
  workspace: Schema.UndefinedOr(Schema.String),
});

export type ThreadBase = (typeof ThreadBase)["Type"];

/** What {@link Rpc ThreadStart} hands back: the thread, and the job building it. */
export const ThreadStarted = Schema.Struct({ thread: Thread, job: Job });

export type ThreadStarted = (typeof ThreadStarted)["Type"];

// ── the diff of a workspace ────────────────────────────────────────────────
//
// What the accessory column's diff panel needs, and nothing beyond it. Two
// calls: the commits worth looking at, and the patch for one of them.
//
// **The patch crosses the wire as text.** Not as parsed files and hunks, and
// that is the decision worth defending. A diff renderer already parses unified
// diffs — it has to, for the highlighting and the expansion — so a daemon-side
// parse would be a second implementation of the same grammar, shipped so that
// the first one could be handed something it then flattens back into lines.
// The format is also not awp's to invent: `--git` is what jj emits and what
// every renderer reads, and keeping it end to end means the thing on screen is
// the thing jj said.

/** One commit, as much of it as a picker needs to draw a row. */
export const Revision = Schema.Struct({
  /**
   * The stable handle, and what {@link AwpRpcs Diff} takes back.
   *
   * The change id rather than the commit id, because a commit id changes every
   * time the commit is amended and a panel holding one would be pointing at a
   * revision that no longer exists the moment the agent edits a file. The
   * change id is the same commit through every rewrite, which is the whole
   * reason jj has it.
   */
  changeId: Schema.String,
  commitId: Schema.String,
  /** The whole message. A row shows the first line; a header shows the rest. */
  description: Schema.String,
  author: Schema.String,
  authored: Schema.UndefinedOr(Schema.Date),
  /** Changes nothing. The top of a working stack usually is one. */
  empty: Schema.Boolean,
  /**
   * The working copy of the workspace that was asked about.
   *
   * On the wire rather than derived, because a client cannot derive it: `@` is
   * resolved per workspace and the client passed a directory, not a workspace
   * name. It is also the row that must ask for its diff *without* naming a
   * revision — see {@link AwpRpcs Diff}.
   */
  workingCopy: Schema.Boolean,
  bookmarks: Schema.Array(Schema.String),
});

export type Revision = (typeof Revision)["Type"];

/** A git-format patch, and which revision was read to get it. */
export const Patch = Schema.Struct({
  /**
   * What was actually diffed — `@` when the working copy was asked for.
   *
   * Echoed back so a client can drop a reply it no longer wants. Two of these
   * are in flight whenever someone clicks a second commit before the first has
   * answered, and they do not necessarily come back in order.
   */
  revision: Schema.String,
  /** Empty when the revision changed nothing, which is not a failure. */
  patch: Schema.String,
});

export type Patch = (typeof Patch)["Type"];

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
/**
 * jj could not answer — the directory is not in a repository, or the revset
 * names nothing.
 *
 * A declared failure rather than a defect, because every one of these is
 * ordinary: a session started in a directory nobody put under version control
 * is a normal thing to have open, and the panel's job is to say so rather than
 * to go blank.
 */
export class DiffUnavailable extends Schema.TaggedError<DiffUnavailable>()("DiffUnavailable", {
  reason: Schema.String,
}) {}

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

  // ── review comments ──────────────────────────────────────────────────────
  //
  // Batched, and that decision is the reason these exist as records at all
  // rather than as text typed straight at an agent. An agent interrupted once
  // per comment loses the thread it is holding; six things about one change is
  // one prompt it can act on. Batching means a comment exists for a while with
  // nobody having seen it, and that has to survive a reload.

  Rpc.make("ReviewList", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.Array(ReviewComment),
  }),

  /**
   * Write one down. Always a draft — nothing here can create a sent comment.
   *
   * The id and the timestamp are the daemon's, not the client's. Two windows
   * would otherwise mint ids from two clocks, and the ordering the panel reads
   * is `created_at`.
   */
  Rpc.make("ReviewAdd", {
    payload: {
      project: Schema.String,
      workspace: Schema.String,
      revision: Schema.String,
      path: Schema.String,
      side: CommentSide,
      line: Schema.Int,
      endLine: Schema.Int,
      body: Schema.String,
    },
    success: ReviewComment,
  }),

  /** Delete one, sent or not. Not an error when it has already gone. */
  Rpc.make("ReviewRemove", {
    payload: { comment: Schema.String },
    success: Schema.Boolean,
  }),

  /**
   * Tell the agent everything unsent about this workspace, and mark it sent.
   *
   * **Which comments** is decided by the daemon, not passed in. A client
   * sending a list of ids would have read that list a moment earlier, and a
   * comment written in between would be marked sent without being in the
   * prompt — or worse, sent twice. Here the read and the mark are one
   * synchronous block over one connection.
   *
   * Fails with {@link NoAgent} when there is no session to type into, and
   * marks nothing in that case: a comment that could not be delivered is still
   * a draft.
   */
  Rpc.make("ReviewSend", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: ReviewSent,
    error: NoAgent,
  }),

  /**
   * Tell the agent about one element of one page, now.
   *
   * **Not batched, and that asymmetry is deliberate.** A review comment is
   * written while reading a diff — six of them arrive in a minute, and
   * interrupting the agent once per comment loses whatever it was holding. A
   * page note is a whole gesture on its own: arm the picker, point at a thing,
   * say what is wrong with it, press send. There is no second one on the way,
   * so a draft that waits for a batch is a draft nobody remembers to deliver.
   *
   * The reply is the prompt that was typed, for the same reason
   * {@link ReviewSent} carries one: it is what makes the call testable, and
   * what a person can be shown when they ask what was actually said.
   */
  Rpc.make("NoteSend", {
    payload: { project: Schema.String, workspace: Schema.String, note: PageNote },
    success: Schema.String,
    error: NoAgent,
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

  /**
   * Forget every job that is over, and say how many.
   *
   * Not "clear the list". A queued or running job keeps its record — the
   * runner still holds a fiber for it — and so does one whose compensation
   * left something behind, which is the single outcome a person has to act on.
   * The daemon decides that, not the client: a rule about which records may be
   * destroyed is not one to have two copies of.
   */
  Rpc.make("JobClear", { success: Schema.Int }),

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

  /**
   * Make a workspace, in the background.
   *
   * Returns the job rather than the workspace, because the work outlives the
   * request: a jj workspace, a bookmark, a session and a thread claim is four
   * things that can each fail, and the answer to "did it work" is the record
   * rather than this reply. Watch it on {@link Rpc JobChanges}.
   */
  Rpc.make("WorkspaceCreate", {
    payload: CreateWorkspace,
    success: Job,
  }),

  /**
   * Start a thread from a sentence.
   *
   * The one call the new-thread box makes: it asks a model to turn what a
   * person typed into a workspace name, a title and an instruction for the
   * agent, makes the thread, and enqueues the job that builds the rest.
   *
   * **Returns as soon as the record exists**, which is the whole point. Naming
   * the workspace takes a model about ten seconds, and that used to happen
   * here — so a person watched a window that would not close while work with a
   * progress panel of its own went unrepresented. It is the job's first step
   * now, and this call does only what has to be true before a job can exist:
   * resolve the repository, resolve the base, make the thread, enqueue.
   *
   * The thread comes back titled with what was typed. The job renames it once
   * the model answers, which is a title that improves ten seconds later rather
   * than a wait.
   */
  /**
   * Everywhere a new workspace in this project could start from.
   *
   * The project's main line, then every local bookmark. Local only: a name that
   * exists solely on a remote is not something jj can branch from here without
   * fetching first, and offering it would be offering a failure.
   */
  Rpc.make("ThreadBases", {
    payload: { from: Schema.String },
    success: Schema.Array(ThreadBase),
    error: ThreadStartFailed,
  }),

  Rpc.make("ThreadStart", {
    payload: {
      /** What the person typed, in their own words. */
      description: Schema.String,
      project: Schema.String,
      /**
       * A directory inside the project — a session's `startDir` will do.
       *
       * The daemon turns it into the repository root. The client passes what
       * it has rather than computing a repo path itself, because `jj root`
       * answers with a *workspace* inside a secondary workspace and the client
       * has no way to know the difference. See `Jj.sourceRoot`.
       */
      from: Schema.String,
      /**
       * A thread to branch from, or absent for the project's main line.
       *
       * A thread and not a revision, because a client cannot compute the
       * revision: the workspace's bookmark is `<prefix>/<name>` and the prefix
       * is in the daemon's config. So the client names the *work* and the
       * daemon resolves it — see `baseOfThread` in handlers.ts, which prefers
       * the bookmark and falls back to the working copy when there is none.
       */
      parent: Schema.optional(Schema.String),
      /**
       * An explicit revision, which wins over `parent` when both are given.
       *
       * Nothing in the window sends this yet. It stays on the payload because
       * the probe uses it and because "start from this exact revset" is a real
       * thing to want; it is simply not something a chip can express.
       */
      base: Schema.optional(Schema.String),
      /**
       * What the agent runs with, or absent for what the config says.
       *
       * These are *overrides*, not the whole command. The agent argv lives in
       * the config — `claude --permission-mode auto --model opus` — and a
       * chosen model has to replace the `--model` already in it rather than
       * follow it, because two of a flag is a thing the CLI resolves by a rule
       * nobody here should be relying on. See `agentWith` in settings.ts.
       */
      model: Schema.optional(Model),
      effort: Schema.optional(Effort),
    },
    success: ThreadStarted,
    error: ThreadStartFailed,
  }),

  /**
   * The commits worth looking at in a workspace, newest first.
   *
   * The daemon decides what "worth looking at" means — the working copy and
   * everything between it and the project's main line — for the same reason
   * {@link ThreadBases} resolves its own revsets: the rule involves `trunk()`,
   * which a client cannot evaluate, and a rule with two implementations has
   * one that drifts.
   *
   * `limit` is the client's to set because the client is what has to draw
   * them. A stack measured against a trunk nobody has fetched in a month is
   * hundreds of commits, and the panel is a column two hundred pixels wide.
   */
  Rpc.make("Revisions", {
    payload: {
      /** A directory in the workspace — a session's `startDir` will do. */
      from: Schema.String,
      limit: Schema.optional(Schema.Int),
    },
    success: Schema.Array(Revision),
    error: DiffUnavailable,
  }),

  /**
   * One revision as a git-format patch.
   *
   * **Leave `revision` out to mean the working copy**, and that is not the
   * same as passing the change id the listing gave for it. Absent is the only
   * form that snapshots the files on disk first, so it is the only form that
   * includes what an agent has written and not yet committed — which, for a
   * panel watching an agent work, is the entire point. Naming a revision reads
   * history, changes nothing, and is what every other row wants.
   */
  Rpc.make("Diff", {
    payload: {
      /** A directory in the workspace — a session's `startDir` will do. */
      from: Schema.String,
      /** A change id. Absent means the working copy, freshly snapshotted. */
      revision: Schema.optional(Schema.String),
    },
    success: Patch,
    error: DiffUnavailable,
  }),

  /**
   * A tick each time the files in a workspace change.
   *
   * Not the patch. The daemon says *that something happened*, and the client
   * asks for what it wants — which is not always the working copy: the panel
   * may be showing a commit, and a commit does not change because a file was
   * written. Pushing a patch would mean the daemon deciding which revision the
   * client is looking at, which is the client's business and would be a second
   * copy of that decision.
   *
   * `at` is the daemon's clock, and exists so that two ticks in a row are two
   * values. A stream of identical messages is one a client cannot tell apart
   * from a stalled one.
   *
   * ── what is deliberately not watched ─────────────────────────────────────
   *
   * `.jj` and `.git`. Asking for the working copy snapshots it, which writes
   * to `.jj` — so watching it means every answer causes the next question, for
   * ever. That is not a tuning problem; it is a loop, and the ignore list is
   * what makes this feature possible at all.
   */
  Rpc.make("WorkspaceChanges", {
    payload: {
      /** A directory in the workspace — a session's `startDir` will do. */
      from: Schema.String,
    },
    success: Schema.Struct({ at: Schema.Number }),
    stream: true,
    error: DiffUnavailable,
  }),
) {}
