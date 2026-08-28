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

// ── what is known about a workspace ────────────────────────────────────────
//
// A session says a workspace exists. This says what is happening in it.
//
// ── the source is not on the wire, deliberately ────────────────────────────
//
// Today these come from `~/.awp/workspace-state.json`, written by the Go
// implementation: Claude Code hooks shell out to `awp internal report-status`
// on every tool call and every stop, and that writes the file. That is a
// stopgap for half these fields — ACP replaces it with a live notification from
// the agent itself — so the *field* is on the wire and the source is a ranked
// list inside the daemon:
//
//   1  ACP session updates       when it lands
//   2  the Go state file         today
//   3  zmx output recency        crude, but never absent
//
// A client that knew which one answered would be a client that has to be
// changed when the answer improves.
//
// ── every field is optional, and that is not defensiveness ─────────────────
//
// A workspace nobody has run an agent in has no status, a branch with no pull
// request has no number, and a workspace made before display names existed has
// no name but its slug. Absent is the ordinary case for most of these, so a
// client renders what is there rather than filling a fixed shape.

/** How a workspace's agent is doing. */
export const WorkspaceStatus = Schema.Literals(["working", "waiting", "idle", "exited", "error"]);

export type WorkspaceStatus = (typeof WorkspaceStatus)["Type"];

export const WorkspaceFacts = Schema.Struct({
  project: Schema.String,
  workspace: Schema.String,
  /**
   * What a person called this work, as opposed to what the directory is called.
   *
   * The slug has to be a slug — it is a directory, a jj workspace and half a
   * bookmark — so `effect-ts-tiered-discount-timemachine` is what the filesystem
   * gets and "tiered discount timemachine" is what was meant.
   */
  displayName: Schema.UndefinedOr(Schema.String),
  status: Schema.UndefinedOr(WorkspaceStatus),
  /**
   * The agent said something that has not been looked at.
   *
   * A boolean and not a count: what it drives is a dot, and "how many things
   * you have not read" is not a question anybody asks of a workspace.
   */
  unread: Schema.Boolean,
  /** The pull request this workspace's branch is on, if it is on one. */
  pr: Schema.UndefinedOr(Schema.Int),
  bookmark: Schema.UndefinedOr(Schema.String),
  /** The last thing asked of the agent, for a row that has room to say it. */
  prompt: Schema.UndefinedOr(Schema.String),
  /** Where the work is in the configured dev loop — explore, implement, verify. */
  phase: Schema.UndefinedOr(Schema.String),
  /** What the agent is working on, in the words of the task it claimed. */
  task: Schema.UndefinedOr(Schema.String),
  done: Schema.UndefinedOr(Schema.Int),
  total: Schema.UndefinedOr(Schema.Int),
  lastActiveAt: Schema.UndefinedOr(Schema.Date),
});

export type WorkspaceFacts = (typeof WorkspaceFacts)["Type"];

// ── projects ───────────────────────────────────────────────────────────────
//
// A project is a repository awp knows about. Until now that was not a record
// at all — the window derived the list from whichever sessions happened to be
// running, so a repository awp had never opened a session in could not be
// picked, and the *first* thread on a machine could not be started from this
// window in any repository whatsoever.
//
//   derived from sessions   a project exists because something is running in
//                           it — which is backwards, since the reason to name
//                           a project is usually that nothing is yet
//   imported                a person said "this one", and it stays said
//
// Written down for the same reason a thread is: it is a claim somebody made,
// not a fact recoverable from the machine. The Go implementation recorded it
// by writing a `default` workspace entry into its state file; here it is a
// table, because the alternative is that an import is forgotten by the next
// daemon restart and the list quietly goes back to being derived.

/** A repository awp has been told about. */
export const Project = Schema.Struct({
  /**
   * The repository directory's basename, and the project's whole identity.
   *
   * A name and not a path, because a name is what everything downstream is
   * built on: `sessionName` composes `awp.<project>.<workspace>.<kind>`, the
   * sidebar groups on it, and the address in the URL carries it. Two
   * repositories with the same basename are therefore a refusal rather than a
   * disambiguation — there is nowhere to put the second one.
   */
  name: Schema.String,
  /** The repository root, absolute and tilde-expanded. */
  root: Schema.String,
  /**
   * When it was imported, or absent for one that was merely *found*.
   *
   * The distinction the window needs: a project recovered from a running
   * session is real and usable and can still be forgotten by a restart, so
   * offering to forget it would be offering a button that does nothing.
   */
  importedAt: Schema.UndefinedOr(Schema.Date),
});

export type Project = (typeof Project)["Type"];

/**
 * A path could not be imported, said in a sentence.
 *
 * Every reason is a thing about the path a person can look at and fix — it is
 * not there, it is not a repository, its name is taken — so there is one error
 * with one sentence rather than a tag per case. A tagged variant would exist to
 * be branched on, and nothing branches on it: the window shows the sentence.
 */
export class ProjectImportFailed extends Schema.TaggedError<ProjectImportFailed>()(
  "ProjectImportFailed",
  { path: Schema.String, reason: Schema.String },
) {}

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

/**
 * A pull request a thread is about.
 *
 * ── why this is recorded rather than read off a directory name ─────────────
 *
 * It *was* readable: a review workspace is called `pr-<number>`, so the number
 * could be parsed back out of the member. That works until any of the ordinary
 * things happen — somebody renames a workspace, opens a PR for work that
 * already had a thread, or reviews a PR in a checkout they made by hand — and
 * every one of those is a thread whose pull request awp then cannot name.
 *
 * It is the same argument as `parentId`: a name is an address and this is a
 * claim about the work. The address is still used, for the workspaces that
 * predate this field and for the Go implementation's `pr-<n>-<branch>`.
 *
 * ── and why a thread may have several ──────────────────────────────────────
 *
 * Because a thread already holds several workspaces, and each is in a different
 * repository with its own pull request — a change to a frontend and the api
 * behind it is one piece of work and two PRs. A stack in one repository is the
 * other case: two open PRs, one thread, and the second is not a different job.
 *
 * The reverse is *not* many-to-many: a pull request belongs to at most one
 * thread, enforced the way a workspace's single claim is. Two threads about one
 * PR has no rendering — the inbox row would have to pick which of them to point
 * at, and a person would have to work out which was lying.
 */
export const ThreadPr = Schema.Struct({
  project: Schema.String,
  number: Schema.Int,
});

export type ThreadPr = (typeof ThreadPr)["Type"];

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
  /** The pull requests this work is about. See {@link ThreadPr}. */
  prs: Schema.Array(ThreadPr),
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
 * The pull request a workspace is being made to review.
 *
 * On the payload rather than looked up by the job, and that is the rule the
 * jobs package states in general: **a resumed job has only its record.** A
 * daemon restarted mid-create has no answer from GitHub in memory and no
 * reason to spend another round trip on one, so what the fetch needs travels
 * with the job.
 *
 * `headRef` is a branch name and not a revision on purpose — it does not exist
 * locally until the fetch step has run, which is why that step is the one that
 * resolves `base`. See `create-workspace.ts`.
 */
export const ReviewTarget = Schema.Struct({
  number: Schema.Int,
  /** The PR's own branch, as GitHub names it. */
  headRef: Schema.String,
  /**
   * The head repository when it is a fork, `owner/name`.
   *
   * A fork's head branch is not on `origin`, so `jj git fetch` does not bring
   * it down and the base would name a branch nothing has heard of. Absent means
   * the head is on the repository itself, which is the ordinary case and needs
   * no second fetch.
   */
  fork: Schema.optional(Schema.Struct({ owner: Schema.String, repo: Schema.String })),
});

export type ReviewTarget = (typeof ReviewTarget)["Type"];

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
/**
 * What archiving a thread needs to know.
 *
 * ── archive is a label; reclaim is an act ──────────────────────────────────
 *
 * `ThreadArchive` sets a flag and can be undone by clearing it. This is the
 * other half, and it is not reversible: a removed checkout does not come back,
 * so an unarchive afterwards restores the row and not the work. Putting both
 * behind one word is what makes the word ambiguous, which is why the
 * destructive one is a *job* — it has a progress panel, a log, and steps that
 * can be looked at afterwards.
 *
 * Measured before this existed: twenty of twenty-nine threads in the store had
 * `archived_at` set, and `ThreadList` returned all twenty-nine. Archiving was
 * written and never read.
 */
export const ArchiveThread = Schema.Struct({
  thread: Schema.String,
  /**
   * What the thread is called, for the job's own title.
   *
   * On the input rather than looked up, because a job record has to be
   * readable before its first step has run — and because a title is a caption
   * on what somebody asked for at the moment they asked. A thread renamed
   * afterwards does not retitle the job that archived it.
   */
  title: Schema.String,
  /**
   * Whether to delete each workspace's bookmark as well.
   *
   * **Off by default, and that is the safety.** A bookmark is not part of a
   * workspace — it is a name for a commit, stored in the repository, so it
   * outlives the checkout being removed. Keeping it is what keeps the *work*
   * addressable; deleting it can leave commits with nothing pointing at them,
   * and jj collects those eventually.
   *
   * Everywhere else here, forgetting takes nothing with it. This is the one
   * place a person can ask for the opposite, and they have to ask.
   */
  deleteBookmarks: Schema.Boolean,
  /**
   * Which workspaces are being reclaimed, and where each one's repository is.
   *
   * **Absent on the way in, present from the first step onward.** The `plan`
   * step reads the thread's members once and records them here, so a resumed
   * job reclaims what the thread held when the button was pressed rather than
   * whatever it holds now.
   *
   * `Schema.optional` and not `UndefinedOr`: the store is JSON, which has no
   * `undefined`, so a required key left unset comes back *absent* and the kind
   * dies on its first step in a message about the wrong thing.
   */
  plan: Schema.optional(
    Schema.Array(
      Schema.Struct({
        project: Schema.String,
        workspace: Schema.String,
        /** The repository's path. `jj -R` takes one, and a member has a name. */
        repo: Schema.String,
      }),
    ),
  ),
});

export type ArchiveThread = (typeof ArchiveThread)["Type"];

export const CreateWorkspace = Schema.Struct({
  /** The thread that claims it. See {@link Thread}. */
  thread: Schema.String,
  /**
   * The thread this one follows from, or absent for none.
   *
   * Carried so the job can *rebuild* the thread, not so it can create one. The
   * handler makes the thread before enqueuing and the job's rollback removes
   * it again when it was left empty — which means a retry after a rollback
   * arrives at a job naming a thread that is no longer there. Everything
   * needed to put it back has to be on the record, because a resumed job has
   * nothing else.
   */
  threadParent: Schema.optional(Schema.String),
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
  /**
   * The pull request this workspace is for, or absent for ordinary work.
   *
   * Present is what turns the `fetch` step from a no-op into a fetch, and it is
   * the only difference between the two jobs. Everything else a review needs —
   * a pre-set `workspace`, so the naming step does not spend ten seconds on a
   * name that is already decided, and no `bookmark`, because `pr-123` is not a
   * branch anybody should push — falls out of fields that already existed.
   */
  review: Schema.optional(ReviewTarget),
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

/** Who filed a remark. See {@link ReviewComment.author}. */
export const CommentAuthor = Schema.Literals(["human", "agent"]);

export type CommentAuthor = (typeof CommentAuthor)["Type"];

/** What kind of remark it is. See {@link ReviewComment.kind}. */
export const CommentKind = Schema.Literals(["comment", "suggestion", "question", "praise"]);

export type CommentKind = (typeof CommentKind)["Type"];

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
  /**
   * Who wrote it.
   *
   * The field that makes a *review* out of a list of comments. A person reading
   * their own remarks knows which are theirs; the moment an agent files
   * findings into the same store, a panel that drew them alike would be asking
   * somebody to remember which of thirty lines they wrote.
   *
   * It also decides what may be done with one: a person's comment is a draft
   * until it is sent, and an agent's finding is already delivered to the person
   * — the direction is reversed, and the panel has to be able to tell.
   */
  author: CommentAuthor,
  /**
   * What kind of remark it is.
   *
   * Four, from the archive, and the set is closed on purpose: a reviewer given
   * a free-text label uses ten and the reader learns none of them. Each one
   * says something different about what to do next —
   *
   *   comment     an observation. Nothing is being asked for
   *   suggestion  a change worth making
   *   question    an answer is wanted before anything changes
   *   praise      worth keeping, and worth saying so
   *
   * `praise` is the one that looks like a nicety and is not: a review of only
   * problems reads as a verdict on the work, and an agent with no way to say
   * "this part is right" restates every problem harder.
   */
  kind: CommentKind,
  /**
   * The line's text as the filer saw it, when they said.
   *
   * Recorded for two reasons and used for one of them today. The one: filing
   * verifies it against the file, so a finding aimed at a line number that has
   * since moved is refused where the mistake is rather than becoming a remark
   * about the wrong line. The other: it is what a later relocation pass would
   * need to find the line again after the code moves — the archive anchored to
   * content for exactly that, and nothing here does it yet.
   *
   * Absent for a comment written in the window, where the panel already knows
   * which line it is on and the anchor cannot be stale.
   */
  text: Schema.optional(Schema.String),
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
/**
 * A finding could not be filed, said in a sentence.
 *
 * One error with one sentence rather than a tag per cause, the same shape as
 * {@link ProjectImportFailed}: every reason is a thing about the directory, the
 * path or the line that the caller can look at and fix, and nothing branches on
 * which. What reads it is an agent, in a terminal, so the sentence *is* the
 * interface.
 */
export class ReviewFileFailed extends Schema.TaggedError<ReviewFileFailed>()("ReviewFileFailed", {
  reason: Schema.String,
}) {}

/**
 * A filed finding, and where it went.
 *
 * The `where` is not decoration. An agent filing from the wrong directory is
 * the failure this whole call is shaped around, and the only thing that makes
 * it visible is a reply naming the review — which the caller is told to read.
 */
export const ReviewFiled = Schema.Struct({
  comment: ReviewComment,
  /** `added a suggestion to thicket/pr-2418 on src/router.ts:42`. */
  where: Schema.String,
});

export type ReviewFiled = (typeof ReviewFiled)["Type"];

/** Which review a directory is in, and what is already filed against it. */
export const ReviewFound = Schema.Struct({
  project: Schema.String,
  workspace: Schema.String,
  comments: Schema.Array(ReviewComment),
});

export type ReviewFound = (typeof ReviewFound)["Type"];

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
  /**
   * The React components it sits inside, outermost first. Empty when the page
   * is not a React app, or is a production build whose names are minified.
   *
   * Optional in the schema and not on the wire from older clients — a note is
   * still a note without it.
   */
  react: Schema.optional(Schema.String),
  /** `file:line` from StyleX's `data-style-src`, when the page carries one. */
  source: Schema.optional(Schema.String),
  /** What the person said about it. */
  body: Schema.String,
});

export type PageNote = (typeof PageNote)["Type"];

/** No session to tell. A workspace whose agent has ended, or never started. */
export class NoAgent extends Schema.TaggedError<NoAgent>()("NoAgent", {
  project: Schema.String,
  workspace: Schema.String,
}) {}

// ── the agent's own task list ──────────────────────────────────────────────
//
// Not the daemon's. `Job` is work amoeba is doing and owns; an `AgentTask` is
// work the *agent* wrote down for itself, read off Claude Code's files. The
// two are deliberately different types with different verbs — a job can be
// retried and cancelled, and a task can only be read and quoted back.
//
// Nothing here writes to that list. See `agent-tasks.ts` for why: the agent
// owns it, and a second writer would need the lock `claude-trust.ts` needed.

export const AgentTask = Schema.Struct({
  /** The agent's own id for it, which is what the agent will recognise. */
  id: Schema.String,
  subject: Schema.String,
  /** May be empty. Sent whole — see `taskPrompt` for why it is not capped. */
  description: Schema.String,
  /**
   * `pending`, `in_progress`, `completed` — and whatever else it grows.
   *
   * A plain string rather than a literal union on purpose: this field is
   * somebody else's, and a union would turn a new status upstream into a
   * decode failure that loses the whole list rather than one unfamiliar word.
   */
  status: Schema.String,
});

export type AgentTask = (typeof AgentTask)["Type"];

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

// ── the inbox ──────────────────────────────────────────────────────────────
//
// Every open pull request awp can see, sectioned by what the next move is.
// Ported from the deck's inbox scope, and one thing about it is inverted.
//
//   deck    the rows were WORKSPACES, and a PR with no local checkout had to
//           be synthesized as a "virtual" row — three passes of it (review
//           requested, mine, and a fourth to fill the holes a partly-shown
//           stack left), each deduping against the ones before
//   here    the rows are PULL REQUESTS, and a local workspace is an
//           annotation on one
//
// Nothing was cleverer about the second; it starts from the list GitHub
// actually returns. All three synthesis passes and their dedup tables exist
// only because the first one started from the wrong set.
//
// **The daemon classifies, sections and orders.** A client receives rows it can
// render top to bottom, for the same reason `SessionIdentity` is on the wire: a
// client re-deriving a rule is a second implementation of it, and the copy that
// drifts is the one nobody tests. The rule here is `PRInboxBucket`'s
// precedence, which is subtle enough that the archive locked it with tests.

/** How CI rolls up. `none` is a PR with no checks, which is not a failure. */
export const CIState = Schema.Literals(["none", "pending", "passing", "failing"]);

export type CIState = (typeof CIState)["Type"];

/**
 * GitHub's branch-protection verdict, or `none` when nobody has reviewed.
 *
 * Not the same question as "did anyone leave feedback" — see
 * {@link InboxItem.hasReviewComments}, which is the only signal that catches a
 * reviewer who commented without formally requesting changes.
 */
export const ReviewDecision = Schema.Literals([
  "none",
  "approved",
  "changes-requested",
  "review-required",
]);

export type ReviewDecision = (typeof ReviewDecision)["Type"];

/**
 * Whether the PR would merge as it stands.
 *
 * `behind` only ever appears when the repository requires up-to-date branches;
 * without that rule GitHub reports an out-of-date PR as `clean`, so a client
 * must not read the absence of `behind` as "up to date".
 */
export const MergeState = Schema.Literals([
  "unknown",
  "clean",
  "dirty",
  "behind",
  "blocked",
  "draft",
  "unstable",
  "has-hooks",
]);

export type MergeState = (typeof MergeState)["Type"];

/**
 * Which section of the inbox a row belongs to, and the order the sections are
 * drawn in: most-your-problem first.
 *
 * Sections rather than the attention scope's flat list of reasons, because the
 * question the inbox answers is "what is my next move", and the five answers
 * are stable enough to be headings. The archive's precedence, kept:
 *
 *   needs-your-review   somebody asked you — wins over everything, including
 *                       the PR's own state, because it names you
 *   needs-action        yours, and something is wrong with it
 *   ready-to-merge      yours, approved and green
 *   other-open          neither yours nor waiting on you
 *   mine                yours, and the ball is elsewhere — or still a draft
 */
export const InboxBucket = Schema.Literals([
  "needs-your-review",
  "needs-action",
  "ready-to-merge",
  "other-open",
  "mine",
]);

export type InboxBucket = (typeof InboxBucket)["Type"];

/** The heading for a bucket. One place, so two surfaces cannot disagree. */
export const bucketLabel = (bucket: InboxBucket): string => {
  switch (bucket) {
    case "needs-your-review":
      return "Needs your review";
    case "needs-action":
      return "Needs action";
    case "ready-to-merge":
      return "Ready to merge";
    case "other-open":
      return "Other open PRs";
    case "mine":
      return "Mine";
  }
};

/** The order the sections are drawn in. */
export const inboxBuckets: ReadonlyArray<InboxBucket> = [
  "needs-your-review",
  "needs-action",
  "ready-to-merge",
  "other-open",
  "mine",
];

/**
 * One open pull request, as a row.
 *
 * The viewer-relative fields — `mine`, `reviewRequested`, `reviewRerequested` —
 * are reduced to booleans by the daemon against the authenticated `gh` login,
 * so nothing downstream has to know whose inbox it is rendering. With no login
 * they are all false, which is why {@link Inbox.viewer} is on the answer: every
 * bucket that names the viewer is empty in that case, and an inbox that is
 * empty because nobody is signed in must not look like an inbox with nothing in
 * it.
 */
export const InboxItem = Schema.Struct({
  /** The project this PR's repository is, by awp's name for it. */
  project: Schema.String,
  /** That repository's root, so an action does not have to resolve it again. */
  repo: Schema.String,

  number: Schema.Int,
  title: Schema.String,
  /** The author's login, whoever they are. */
  author: Schema.String,
  url: Schema.String,
  /** The PR's own branch, and the branch it merges into. */
  headRef: Schema.String,
  baseRef: Schema.String,

  draft: Schema.Boolean,
  ci: CIState,
  review: ReviewDecision,
  mergeState: MergeState,
  labels: Schema.Array(Schema.String),

  mine: Schema.Boolean,
  reviewRequested: Schema.Boolean,
  /** You reviewed it once and the author has asked again. */
  reviewRerequested: Schema.Boolean,
  /**
   * A reviewer left COMMENTED or CHANGES_REQUESTED feedback.
   *
   * Distinct from `review`, and the distinction is the whole reason it is here:
   * a plain review comment never moves GitHub's verdict off `review-required`,
   * so this is the only signal that catches "somebody gave you notes".
   */
  hasReviewComments: Schema.Boolean,

  bucket: InboxBucket,
  /**
   * How deep in its stack: 0 for a PR based on the trunk, 1+ for one based on
   * another open PR's branch. Drives the row's indent.
   *
   * Derived from the base/head graph over the repository's open PRs, which the
   * daemon has in hand anyway — the deck needed a whole extra synthesis pass
   * here only because its rows were workspaces and a stack's middle link is
   * frequently somebody else's PR.
   */
  depth: Schema.Int,
  /**
   * Which stack this row belongs to — the head branch of its root — or absent
   * when the pull request stands alone.
   *
   * **It was removed from here once**, as "an implementation of contiguity": the
   * daemon sorts the rows, so a client had no use for it. Drawing the tree gave
   * it one. A guide character depends on what comes *after* a row within the
   * same stack, and a client inferring stack membership from runs of `depth`
   * would be re-deriving the grouping the daemon already did — which is the
   * thing this whole record exists to avoid.
   *
   * Absent for a lone pull request rather than set to its own branch, because
   * that is what decides whether any guide is drawn at all: a tree of one is
   * not a tree, and a `└─` in front of every unstacked row is noise.
   */
  stack: Schema.optional(Schema.String),
  /** An open ancestor that is not ready to merge. It cannot land yet. */
  blocked: Schema.Boolean,

  /**
   * The awp workspace reviewing this PR, when it exists.
   *
   * What makes the row's action idempotent *visibly* — a row with a workspace
   * offers to open it rather than to make a second one.
   *
   * **Set as soon as the workspace can be opened, not when it is finished.**
   * Two sources, and the second was added because the first was too late: a
   * thread claiming it, and a *session* whose identity names it. The claim is
   * the create job's second-to-last step, so a row built on it alone said
   * nothing for the thirty seconds between the session appearing and the job
   * ending — which is precisely the window a person is watching.
   */
  workspace: Schema.optional(Schema.String),
  /** The thread holding that workspace, once one has claimed it. */
  thread: Schema.optional(Schema.String),
  /**
   * The workspace does not contain what the pull request now is.
   *
   * ── the one signal a review cannot do without ─────────────────────────────
   *
   * A review workspace is a checkout of the head at the moment it was made, and
   * a pull request moves: the author pushes a fix, or force-pushes a rewrite.
   * From then on the diff being read, the comments being written and the agent's
   * findings are all about code the pull request no longer has — and *nothing on
   * screen says so*. That is worse than being out of date, because a review
   * delivered against an old head reads as a review of the current one.
   *
   * Asked as "is the head an ancestor of the working copy", not "are they equal":
   * a person who has committed something of their own on top is still reviewing
   * the right code. Absent evidence counts as moved — a head that was
   * force-pushed away is not in the repository at all, and "we do not have what
   * the pull request is" and "we have something older" call for the same act.
   *
   * False for a row with no workspace, where there is nothing to be stale.
   */
  moved: Schema.Boolean,
  /**
   * The job that built this review, or is building it now.
   *
   * The **id** and not the record, deliberately. A job changes on its own and
   * the client already has a live feed of every one of them, so sending the
   * record here would put a second, staler copy on a list that is a snapshot —
   * and the two would disagree exactly while a person watched a row progress.
   * The id is the join; `JobChanges` is the truth.
   *
   * Present whatever became of it, including a failure: a review whose job
   * failed is a row that has to be able to say so, rather than one that looks
   * untouched and starts a second job on the next press.
   */
  job: Schema.optional(Schema.String),
});

export type InboxItem = (typeof InboxItem)["Type"];

/**
 * Where one project's rows came from, and what went wrong if they did not.
 *
 * **Per project, because one repository's failure must not lose the others.**
 * `gh` is missing, or a repository's remote is not GitHub, or a token expired —
 * and the honest answer is the other projects' pull requests plus a sentence
 * about the one that could not be read. A single error for the whole call would
 * turn one unauthenticated repository into an empty inbox.
 */
export const InboxSource = Schema.Struct({
  project: Schema.String,
  root: Schema.String,
  /** When these rows were read from GitHub. Absent when they never were. */
  fetchedAt: Schema.optional(Schema.Date),
  /** `gh`'s own sentence about why not. */
  failure: Schema.optional(Schema.String),
  /**
   * What had to be given up to read this project's rows, if anything.
   *
   * Distinct from `failure`, which means there are no rows. This means there
   * *are* rows and one signal is missing from them — GitHub refuses to compute
   * mergeability for a hundred pull requests on a busy repository, so conflicts
   * and behind-base are unknown there. Said out loud rather than degrading
   * silently, which would leave a person reading a clean-looking inbox for a
   * repository where nothing can say a PR is in conflict.
   */
  degraded: Schema.optional(Schema.String),
});

export type InboxSource = (typeof InboxSource)["Type"];

export const Inbox = Schema.Struct({
  /** Every row, already sectioned and ordered. See {@link InboxItem.bucket}. */
  items: Schema.Array(InboxItem),
  sources: Schema.Array(InboxSource),
  /**
   * The authenticated `gh` login, or absent when there is none.
   *
   * On the answer rather than left implicit because it is the difference
   * between "nothing is waiting on you" and "nobody knows who you are". Every
   * viewer-relative bucket is empty without it.
   */
  viewer: Schema.optional(Schema.String),
});

export type Inbox = (typeof Inbox)["Type"];

/**
 * ── `Schema.optional`, not `Schema.UndefinedOr`, for anything absent-able ────
 *
 * Measured, after a pull request panel refused to decode with
 * `Missing key at ["value"]["remarks"][0]["verdict"]`:
 *
 *   ENCODED     {"author":…,"body":…}   keys: author, body, verdict, at
 *   AFTER JSON  {"author":…,"body":…}   ← stringify drops an undefined value
 *   DECODED     Missing key at ["verdict"]
 *
 * The serialization is ndjson, which is `JSON.stringify`, and JSON has no
 * `undefined` — so a field spelled `UndefinedOr` and *given* undefined arrives
 * as an absent key, and `UndefinedOr` requires the key to be there. It is the
 * same rule the jobs store already documents for its own JSON column; what is
 * new is that it applies to the wire, which is the same JSON.
 *
 * `Schema.optional` accepts both spellings — absent, and present-but-undefined —
 * and its TypeScript type is `x?: T | undefined`, so a caller may still pass
 * `undefined` explicitly under `exactOptionalPropertyTypes`.
 *
 * The older fields here are deliberately left as they are: they are what a
 * caller has always written, they are covered by the round-trip tests in
 * index.test.ts, and changing them all at once would be a large edit whose
 * failures would be indistinguishable from this one's. New ones use `optional`.
 */
/** Something somebody said on a pull request — a comment, or a review's body. */
export const PullRequestRemark = Schema.Struct({
  author: Schema.String,
  body: Schema.String,
  /** `approved`, `changes requested`, `commented` — absent for a comment. */
  verdict: Schema.optional(Schema.String),
  at: Schema.optional(Schema.Date),
});

export type PullRequestRemark = (typeof PullRequestRemark)["Type"];

/**
 * One pull request, in the detail a panel shows and a briefing reads.
 *
 * Deliberately a different shape from {@link InboxItem}, which is a *row*: this
 * carries the description and the conversation, and the listing cannot afford
 * either — `gh pr list` asks for a hundred at once. The fields they share are
 * projected by the same functions in the daemon, so the state a row shows and
 * the state this shows cannot disagree.
 *
 * **Not restricted to open pull requests.** A panel is opened on one that merged
 * an hour ago, and answering "no such pull request" for it would be a lie about
 * a thing plainly on the screen — `state` says which it is.
 */
export const PullRequest = Schema.Struct({
  project: Schema.String,
  number: Schema.Int,
  title: Schema.String,
  /** Markdown, as the author wrote it. Empty is ordinary. */
  body: Schema.String,
  url: Schema.String,
  author: Schema.String,
  /** `open`, `merged`, `closed`. */
  state: Schema.String,
  draft: Schema.Boolean,
  baseRef: Schema.String,
  headRef: Schema.String,
  ci: CIState,
  review: ReviewDecision,
  mergeState: MergeState,
  labels: Schema.Array(Schema.String),
  /**
   * A reviewer left something to act on, whatever GitHub's verdict says.
   *
   * Not viewer-relative — unlike `mine` and `reviewRequested`, which are about
   * this machine's login and deliberately do not travel on this record. This one
   * is a fact about the pull request, and it is what decides whether the panel
   * offers a repair at all.
   */
  hasReviewComments: Schema.Boolean,
  remarks: Schema.Array(PullRequestRemark),
  /**
   * The workspace reviewing this pull request, if a thread names one.
   *
   * Here as well as on {@link InboxItem} because the panel is opened *from* a
   * workspace and has to be able to offer the repair below without the inbox
   * having been read at all.
   */
  workspace: Schema.optional(Schema.String),
  /** That workspace does not contain this head. See {@link InboxItem.moved}. */
  moved: Schema.Boolean,
  /** The size of the change, which is the first thing a reviewer wants. */
  additions: Schema.Int,
  deletions: Schema.Int,
  files: Schema.Int,
});

export type PullRequest = (typeof PullRequest)["Type"];

/**
 * What was said to the agent about what is wrong with a pull request.
 *
 * ── the prompt comes back because it was sent, not to be approved ──────────
 *
 * The deck handed this to a form first and let a person edit it before it went.
 * One press is better and the reason is what the button is for: somebody who
 * pressed *repair* has already decided. A box between the decision and the act
 * is a second decision to make about a sentence they did not write.
 *
 * So the prompt is on the answer for the reason {@link ReviewSent} carries one:
 * it is what the panel shows when asked what was actually said, which is
 * otherwise knowable only by scrolling the agent's own terminal back.
 *
 * **Empty means there was nothing to repair** — an open pull request with green
 * CI, no conflicts and nobody waiting — and then nothing was sent.
 */
export const Repaired = Schema.Struct({
  /** What was typed at the agent, or empty when there was nothing to say. */
  prompt: Schema.String,
  /**
   * Which tone it was written in: an owner is asked to fix, a reviewer to look.
   *
   * On the answer because it changes what the window should say about the button
   * — "fix and push" and "investigate and report" are different offers — and
   * because the rule that decides it is the daemon's (the bookmark prefix is in
   * its config).
   */
  mine: Schema.Boolean,
  /** The workspace whose agent heard it. Absent when nothing was sent. */
  workspace: Schema.optional(Schema.String),
});

export type Repaired = (typeof Repaired)["Type"];

/**
 * A review could not be started, said in a sentence.
 *
 * One error rather than a tag per case, the same shape as
 * {@link ProjectImportFailed} and for the same reason: every cause is a thing
 * about the PR or the repository a person can look at — it is closed, `gh`
 * cannot reach it, the project is not one awp knows — and nothing branches on
 * which. The window shows the sentence.
 */
export class ReviewStartFailed extends Schema.TaggedError<ReviewStartFailed>()(
  "ReviewStartFailed",
  { project: Schema.String, number: Schema.Int, reason: Schema.String },
) {}

/**
 * What {@link AwpRpcs ReviewStart} hands back.
 *
 * `created` is the field that makes the call safe to press twice. A second
 * click answers with the same thread and the same job and says it made
 * nothing, so the window can go to the workspace rather than reporting a
 * success that did not happen.
 *
 * `job` is absent when the work is already done and its record has since been
 * cleared — a thread holding the workspace is proof enough, and the panel does
 * not need a job to point at a finished workspace.
 */
export const ReviewStarted = Schema.Struct({
  thread: Thread,
  job: Schema.optional(Job),
  /** The workspace's name, which is `pr-<number>`. */
  workspace: Schema.String,
  created: Schema.Boolean,
});

export type ReviewStarted = (typeof ReviewStarted)["Type"];

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
      /** Absent means `comment` — an observation, which most remarks are. */
      kind: Schema.optional(CommentKind),
    },
    success: ReviewComment,
  }),

  /**
   * File a finding from inside a workspace, as an agent does.
   *
   * ── why this is not `ReviewAdd` with two more fields ──────────────────────
   *
   * The caller is different in the one way that matters: it has a **directory**
   * and not a `(project, workspace)` pair. An agent runs in a checkout and
   * knows where it is standing; it does not know awp's name for the thing it is
   * standing in, and asking it to work that out would be asking it to
   * reimplement `~/.awp/workspaces/<project>/<workspace>`.
   *
   * That resolution is also the failure worth catching. The Go implementation
   * lost seven findings on a real pull request to an agent running the command
   * from the *source repository* rather than the workspace — both sides
   * reported success, and the findings went into a different review. So the
   * reply says which review it wrote to, in words, and the refusals below are
   * all about the same question.
   *
   * `text` is the line as the agent read it, and is verified against the file
   * before anything is stored: a finding aimed at a line number that has since
   * moved is refused where the mistake is rather than becoming a remark about
   * the wrong line.
   */
  Rpc.make("ReviewFile", {
    payload: {
      /** A directory inside the workspace being reviewed. */
      from: Schema.String,
      /** Repository-relative, as the diff names it. */
      path: Schema.String,
      line: Schema.Int,
      /** Absent for a single line. */
      endLine: Schema.optional(Schema.Int),
      /** Absent means the new side, which is where all but a deletion lives. */
      side: Schema.optional(CommentSide),
      kind: Schema.optional(CommentKind),
      body: Schema.String,
      /** The line's exact text, checked against the file. */
      text: Schema.optional(Schema.String),
      /** Absent means `agent`: this call exists for one. */
      author: Schema.optional(CommentAuthor),
    },
    success: ReviewFiled,
    error: ReviewFileFailed,
  }),

  /**
   * The review a directory is in, and everything already filed against it.
   *
   * The read half of {@link Rpc ReviewFile}, and it takes the same handle for
   * the same reason. It answers with the pair it resolved as well as the
   * comments, because "which review am I about to write to" is the question an
   * agent has to be able to ask *before* filing — and the one that lost seven
   * findings when it could not.
   */
  Rpc.make("ReviewAt", {
    payload: { from: Schema.String },
    success: ReviewFound,
    error: ReviewFileFailed,
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
   * Every project awp knows about, imported ones first.
   *
   * The daemon's answer, not the client's, because it is the union of two
   * sources a client only holds one of: the imported table, and the projects
   * the running sessions imply. Merging them here is what stops the window
   * showing a repository twice under two spellings of the same root.
   */
  /**
   * What is known about every workspace, and again whenever it changes.
   *
   * A stream and not a list, which is the same split jobs and threads sit on
   * either side of: a thread changes when a person changes it in this window,
   * so the reply to the change is the update — but an agent goes from working
   * to waiting on its own, and a client that only asked would miss the
   * transition it was watching for.
   *
   * The whole table each time rather than a delta. It is a few kilobytes, and a
   * delta would be machinery in service of an economy nobody can measure.
   */
  Rpc.make("WorkspaceFactsChanges", {
    success: Schema.Array(WorkspaceFacts),
    stream: true,
  }),

  Rpc.make("ProjectList", {
    success: Schema.Array(Project),
  }),

  /**
   * Repositories found under `deck.project_roots` that are not imported yet.
   *
   * Separate from {@link Rpc ProjectList} because it costs a walk of somebody's
   * filesystem and the list does not. It is asked for when a picker opens,
   * which is the only moment anybody wants it.
   *
   * Empty is the ordinary answer for a machine with no roots configured, and is
   * not a failure — the path route below works with no config at all, and that
   * is why it is the one that had to exist first.
   */
  Rpc.make("ProjectCandidates", {
    success: Schema.Array(Project),
  }),

  /**
   * Take a path and write down the repository it is in.
   *
   * A path *inside* the project is enough — the daemon walks up to the nearest
   * `.jj` and then resolves that with `Jj.sourceRoot`. Both halves are needed
   * and neither is the other: `jj -R <dir> root` does not walk up, so the first
   * is what makes a subdirectory work at all; and `jj root` inside a *secondary
   * workspace* answers with the workspace, so the second is what stops a
   * checkout being recorded as though it were the project.
   */
  Rpc.make("ProjectImport", {
    payload: { path: Schema.String },
    success: Project,
    error: ProjectImportFailed,
  }),

  /**
   * Forget an imported project. Says whether there was one to forget.
   *
   * It takes nothing else with it — no workspace is removed, no session is
   * killed, no thread is touched. Forgetting is a statement about this list and
   * nothing else, which is what makes it safe to offer next to a name in a
   * picker. A project with sessions still running simply reappears, derived,
   * which is honest rather than a bug.
   */
  Rpc.make("ProjectForget", {
    payload: { name: Schema.String },
    success: Schema.Boolean,
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
   * Archive a thread *and* reclaim what it holds, as a job.
   *
   * A job rather than a call, for the reason every destructive multi-step
   * thing here is one: it kills sessions, forgets workspaces and removes
   * directories, and a failure part way through has to be visible and
   * resumable rather than a rejected promise. The reply is the job's id, and
   * the panel already streaming job changes is what shows the rest.
   *
   * {@link ThreadArchive} stays, and is what brings a thread back — a flag can
   * be cleared, and this cannot be undone.
   */
  Rpc.make("ThreadArchiveStart", {
    // Not `ArchiveThread` itself. The job's input carries a title and a plan
    // that the *daemon* fills in — a client sending either would be sending a
    // second copy of something the daemon has in hand, and the plan is not a
    // client's to decide at all.
    payload: { thread: Schema.String, deleteBookmarks: Schema.Boolean },
    success: Schema.Struct({ job: Schema.String }),
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
   * Record that this thread is about a pull request, taking it from whichever
   * thread held it before. See {@link ThreadPr} for why the second claim wins.
   */
  Rpc.make("ThreadLinkPr", {
    payload: { thread: Schema.String, pr: ThreadPr },
    success: Thread,
    error: ThreadNotFound,
  }),

  Rpc.make("ThreadUnlinkPr", {
    payload: { thread: Schema.String, pr: ThreadPr },
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
   * Every open pull request awp can see, sectioned and ordered.
   *
   * One call for every project rather than one per project, because the sections
   * cut across them: "needs your review" is a heading over three repositories,
   * and a client assembling that from three replies would be sorting a list the
   * daemon already knows how to sort.
   *
   * **No declared error.** A per-project failure is a field on the answer — see
   * {@link InboxSource} — because one repository whose `gh` is unauthenticated
   * must not cost the others their rows.
   *
   * `refresh` asks GitHub again rather than answering from what was last read.
   * The default is the cache, because this is called every time a panel is
   * opened and `gh pr list` against a busy repository is a couple of seconds.
   */
  Rpc.make("InboxList", {
    payload: { refresh: Schema.optional(Schema.Boolean) },
    success: Inbox,
  }),

  /**
   * One pull request, by project and number.
   *
   * Its own call rather than a fatter {@link Rpc InboxList}, because the two
   * are asked at different times for different reasons: the listing fills a
   * panel that is open all day, and this answers "what is this pull request"
   * for the one a workspace is about. It also answers for a merged one, which
   * the inbox by definition does not.
   *
   * `undefined` when `gh` has no such pull request in that project — a number
   * typed wrongly, or a project that is not on GitHub at all.
   */
  Rpc.make("PullRequestView", {
    payload: {
      project: Schema.String,
      number: Schema.Int,
      /**
       * Ask GitHub again rather than answering from what was last read.
       *
       * The panel needs it for the same reason the inbox does, and slightly
       * more: a description is edited while somebody reads it, and a comment
       * arrives on a pull request the whole time. The cache is what makes
       * switching tabs instant; this is the way to say "that is not what it
       * says any more".
       */
      refresh: Schema.optional(Schema.Boolean),
    },
    success: Schema.UndefinedOr(PullRequest),
    error: ReviewStartFailed,
  }),

  /**
   * What to tell an agent about what is wrong with a pull request.
   *
   * Composed by the daemon rather than the window, for the reason every rule on
   * this wire is: it is a hundred lines of wording decisions — which issues a
   * reviewer may be asked about, which are the author's chores, when the agent
   * must propose before acting — and a second copy would drift into an agent
   * being asked to do the wrong job. See `repair.ts`, which is the deck's own
   * version of it.
   *
   * **It composes and sends in one call**, and answers with what it said. One
   * press, because somebody who pressed repair has already decided — see
   * {@link Repaired}. Refuses with {@link NoAgent} when the pull request has no
   * workspace with a live agent to type into, which is a sentence rather than a
   * silent success.
   */
  Rpc.make("PullRequestRepair", {
    payload: { project: Schema.String, number: Schema.Int },
    success: Repaired,
    error: Schema.Union([ReviewStartFailed, NoAgent]),
  }),

  /**
   * Make a thread and a workspace for reviewing a pull request, once.
   *
   * **Idempotent, and by two mechanisms rather than one.** The job carries an
   * idempotency key — `review:<project>:<number>` — so a double-clicked button
   * is one job; and the thread holding `pr-<number>` is looked for first, so a
   * review whose job record has since been cleared is still not built twice.
   * `created` says which of those happened.
   *
   * It returns as soon as the records exist, like {@link Rpc ThreadStart}: the
   * fetch, the workspace, the session and the claim are the job's, and the job
   * is what has a progress panel.
   */
  Rpc.make("ReviewStart", {
    payload: { project: Schema.String, number: Schema.Int },
    success: ReviewStarted,
    error: ReviewStartFailed,
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
      /**
       * Everything since the main line, as one patch, instead of one revision.
       *
       * `jj diff --from trunk() --to @` — the *net effect* of the work, which
       * is what a person reviews before shipping and what an agent is asked
       * about when the question is "is this change right" rather than "what
       * did that commit do". A file touched in three commits appears once,
       * with its final shape; the ordering is deliberately not shown, because
       * that is what the revision list is for.
       *
       * Wins over `revision` when both arrive. The two are different questions
       * and a payload asking both has already decided which it wants by
       * setting this.
       */
      stack: Schema.optional(Schema.Boolean),
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
  /**
   * What the agent working in this directory has written down for itself.
   *
   * Keyed by a directory rather than by a workspace, like the diff calls and
   * for the same reason: it is a question about a checkout on disk, and a
   * session amoeba did not make still has one.
   *
   * Never fails. A directory with no agent history, an agent that kept no
   * list, and a machine whose agent is not Claude Code are all the empty
   * array, because "nothing to show" is the true answer to all three.
   */
  Rpc.make("TaskList", {
    payload: {
      /** A directory in the workspace — a session's `startDir` will do. */
      from: Schema.String,
    },
    success: Schema.Array(AgentTask),
  }),

  /**
   * Hand one task to the agent as a prompt, now.
   *
   * Unbatched, like {@link Rpc NoteSend} and unlike `ReviewSend`: clicking
   * send on a task is one whole gesture, and there is no second one on the
   * way. The reply is the prompt that was typed, which is what makes the call
   * testable and what a person can be shown when they ask what was said.
   *
   * The task is sent by value rather than by id. The daemon would otherwise
   * have to read the list again to find it, and the list it read would be the
   * one *after* whatever the agent did in between — so a person could press
   * send on one task and have another delivered.
   */
  Rpc.make("TaskSend", {
    payload: { project: Schema.String, workspace: Schema.String, task: AgentTask },
    success: Schema.String,
    error: NoAgent,
  }),

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
