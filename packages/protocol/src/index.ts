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
   * bookmark — so `effect-ts-tabular-export-timemachine` is what the filesystem
   * gets and "tabular export timemachine" is what was meant.
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
/**
 * One task awp holds.
 *
 * Ingested rather than authored, for now: a project's `TODO.md` and — next —
 * Claude Code's own per-session lists are copied into awp's store, which is
 * what makes a task outlive the session that wrote it. `source` says who may
 * change it, and nothing in this window writes back to a source it did not
 * create.
 */
export const Task = Schema.Struct({
  id: Schema.String,
  subject: Schema.String,
  /** Everything under the heading, verbatim. Markdown, and rendered as such. */
  description: Schema.String,
  /**
   * Free text, deliberately not a literal union.
   *
   * Claude Code's own set can grow — `agent-tasks.ts` says so — and a schema
   * that refused an unknown value would make an upstream addition arrive as a
   * decode failure rather than as a task.
   */
  status: Schema.String,
  /** `todo` · `claude` · `awp`. A string for the same reason `status` is. */
  source: Schema.String,
  /**
   * What the task is about, as labels rather than a scope field.
   *
   * `project:thicket`, `thread:<id>`, or anything a person applies. A field
   * with three values would force every task to pick one and make the third
   * awkward; a tag gives the cross-cutting view for free.
   *
   * A `thread:` tag is deliberately not a reference: it is a label applied at
   * a moment, and it outlives the thread being archived — the same argument as
   * a thread's `parentId` being recorded rather than re-derived.
   */
  tags: Schema.Array(Schema.String),
  /** The source's own ordering number, where it has one. */
  seq: Schema.UndefinedOr(Schema.Number),
  /**
   * When this row last said something different, in epoch milliseconds.
   *
   * On the wire for one reader: the panel's completed section, which is read
   * most-recent-first — "what did the agent get through while I was away" is a
   * question about order of finishing, and `seq` answers order of writing.
   * Ingest only touches it when a field actually changed, so for a finished
   * task it is as close to "when it finished" as the sources can say.
   *
   * A number rather than a Date because this crosses JSON, which has neither.
   */
  updatedAt: Schema.Number,
});
export type Task = (typeof Task)["Type"];

/**
 * What a sweep of the task sources changed.
 *
 * Counts rather than rows — see {@link Rpc TaskChanges} for why the listing is
 * asked for rather than pushed. `at` is on it for the same reason `Page.at` is:
 * two sweeps that changed the same number of rows are two events, and a client
 * that could not tell them apart would treat a second one as no news.
 */
export const TaskChange = Schema.Struct({
  added: Schema.Number,
  changed: Schema.Number,
  removed: Schema.Number,
  at: Schema.Number,
});
export type TaskChange = (typeof TaskChange)["Type"];

/**
 * A write aimed at a task awp only copied.
 *
 * Nothing is broken, so it is a refusal rather than a failure, and the sentence
 * names where that task is actually written. Ingest's upsert writes a source's
 * status back over anything set here — so a `TODO.md` entry marked done in this
 * window would be pending again within ten seconds, with nothing on screen to
 * say why. The file is where that one finishes.
 *
 * A tag is the exception and takes no refusal: it is the one thing that can be
 * said about somebody else's row without contradicting it.
 */
export class TaskRefused extends Schema.TaggedError<TaskRefused>()("TaskRefused", {
  reason: Schema.String,
}) {}

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
//   thread  "tabular exports"
//     ├── rowan/tabular-exports   agent · editor · action
//     └── beta/tabular-exports       agent · editor · action
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
 * PR has no rendering — the reviewQueue row would have to pick which of them to point
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

/**
 * Which of the agent column's two faces a workspace is worked in.
 *
 * ── it was a renderer preference, and that was the bug ────────────────────
 *
 * The new-thread form has offered this choice for as long as there have been
 * two faces, and it only ever reached `localStorage`: it decided which panel
 * the *window drew* and was never sent anywhere. The job started a `claude` in
 * a pty and typed the prompt into it whichever face was chosen, so a thread
 * started in chat mode ran in the terminal.
 *
 * Reported as "i started it in chat mode yet it is running in terminal mode",
 * and the half that makes it worth a wire field rather than a wider default is
 * the other order: had the window opened on the chat face, it would have shown
 * an empty conversation saying `nothing said yet` while the work happened in a
 * terminal nobody was looking at. Two agents, one briefed, one visible.
 *
 * So the choice crosses to the daemon, and the `brief` step delivers to it.
 */
export const Face = Schema.Literals(["terminal", "chat"]);

export type Face = (typeof Face)["Type"];

export class ThreadNotFound extends Schema.TaggedError<ThreadNotFound>()("ThreadNotFound", {
  thread: Schema.String,
}) {}

/**
 * A directory is not a checkout awp made.
 *
 * The one thing that can go wrong with every **directory-scoped** call, and it
 * is one condition rather than one per call: `~/.awp/workspaces/<project>/<ws>`
 * either holds the path or it does not.
 *
 * A refusal and not a guess, and the Go implementation is why: an agent that
 * ran the filing command in the *source* repository filed seven findings into
 * that repository's own review, and both sides reported success. A sentence
 * naming the directory is the only thing that makes that visible from the
 * agent's end — which is also what makes this an error with prose in it rather
 * than a tag to branch on. What reads it is an agent, so the sentence is the
 * interface.
 */
export class NotAWorkspace extends Schema.TaggedError<NotAWorkspace>()("NotAWorkspace", {
  reason: Schema.String,
}) {}

/**
 * One checkout of a thread, as a caller standing in another one needs it.
 *
 * `dir` is the field that makes this worth sending. An agent told that its
 * work also lives in `beta/tabular-exports` cannot do anything with the pair —
 * it needs the path, and the path is `~/.awp/workspaces/<project>/<workspace>`,
 * which is a rule the daemon owns. Same argument as {@link SessionIdentity}
 * being on the wire: a caller re-deriving it is a second implementation.
 *
 * `running` is not "healthy". It is whether a session is live in that checkout,
 * which is what separates "somebody is working here, coordinate" from "this is
 * a checkout waiting to be picked up".
 */
export const ThreadCheckout = Schema.Struct({
  project: Schema.String,
  workspace: Schema.String,
  /** Where the checkout is. The reason a pair alone would not do. */
  dir: Schema.String,
  /** Whether a session is running in it. */
  running: Schema.Boolean,
});

export type ThreadCheckout = (typeof ThreadCheckout)["Type"];

/**
 * The work a checkout is part of, answered from the checkout.
 *
 * ── every wire pointed one way ─────────────────────────────────────────────
 *
 * The window could type at an agent — a review, a page note — and an agent
 * could say nothing back except by printing into a terminal the window only
 * draws. This is the read that turns that round, and it is the one a thread
 * across several repositories cannot do without: an agent in one checkout has
 * no way to learn that the api half of its own change is in another, or where.
 *
 * **Scoped by directory, not by argument.** There is no pair to pass, so there
 * is no call that can reach the wrong workspace — the structural form of the
 * `-R` rule on every jj call. It is also what makes this safe to hand an agent
 * over MCP: the binding is the absence of a parameter.
 *
 * Two different negatives, and only one is a failure:
 *
 *   not a workspace at all   {@link NotAWorkspace} — the agent is somewhere it
 *                            did not expect to be, and should be told by name
 *   a workspace, no thread   `thread: undefined` — an answer. Most checkouts
 *                            on a real machine predate threads entirely
 */
export const ThreadHere = Schema.Struct({
  /** The checkout the caller is standing in. */
  project: Schema.String,
  workspace: Schema.String,
  dir: Schema.String,
  /** The work it is part of, or absent because no live thread claims it. */
  thread: Schema.UndefinedOr(
    Schema.Struct({
      id: Schema.String,
      title: Schema.String,
      /**
       * What this work follows on from, by title.
       *
       * The title and not the id, because the id is for joining and this
       * answer is read. An id would send the agent looking for a second call
       * that does not exist.
       */
      parent: Schema.UndefinedOr(Schema.String),
      /** The pull requests this work is about. */
      prs: Schema.Array(ThreadPr),
      /**
       * Every checkout the thread holds, the caller's own included.
       *
       * Included rather than filtered out, so a count means what it says and
       * the caller can tell which row is itself from the pair above. Filtering
       * would make "one member" and "no siblings" the same reading.
       */
      checkouts: Schema.Array(ThreadCheckout),
    }),
  ),
});

export type ThreadHere = (typeof ThreadHere)["Type"];

/**
 * One thing an agent said to another agent.
 *
 * ── the body never arrives as a prompt ─────────────────────────────────────
 *
 * A message is *fetched*, not injected. What the daemon pushes into the
 * recipient's chat is a nudge saying how many are waiting; the bodies come back
 * through `awp_messages`, as tool output.
 *
 * That is not a convenience. ACP has one `user` role, so a body delivered as a
 * turn wears the operator's face — and an agent cannot then tell "run the
 * migration" from a peer apart from the same words from the person running the
 * window. Tool output is already the channel every model treats as data from a
 * system rather than as an instruction, and it costs no protocol change to say
 * so.
 *
 * ── addressed to a workspace, not to an agent ──────────────────────────────
 *
 * The workspace is the durable row; the agent inside it is a context window
 * that dies with its session. The same argument that keeps a thread's
 * understanding in sqlite rather than in a model.
 */
export const Message = Schema.Struct({
  id: Schema.String,
  /** The work both ends are part of. A message never leaves its thread. */
  thread: Schema.String,
  from: ThreadMember,
  to: ThreadMember,
  body: Schema.String,
  sentAt: Schema.Number,
  /**
   * When the recipient was told it had mail, or absent because it has not been
   * told yet.
   *
   * Separate from {@link readAt} because they are two different acts by two
   * different parties: the daemon nudges, the agent fetches. Folding them into
   * one column would make a nudge that was never answered indistinguishable
   * from a message nobody has been told about, and the deliverer would either
   * re-nudge forever or go quiet after one try.
   */
  notifiedAt: Schema.UndefinedOr(Schema.Number),
  /** When the recipient fetched it. The fetch *is* the read. */
  readAt: Schema.UndefinedOr(Schema.Number),
});

export type Message = (typeof Message)["Type"];

/**
 * A message was not accepted, and the sentence says why.
 *
 * Its own failure rather than a defect, because every case is a thing an agent
 * can do something about: name a sibling that is in the thread, wait for the
 * hour to turn over, or stop trying to talk to a checkout that belongs to
 * nobody's work.
 */
export class MessageRefused extends Schema.TaggedError<MessageRefused>()("MessageRefused", {
  reason: Schema.String,
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
  /**
   * One checkout instead of the whole thread, or absent for all of them.
   *
   * ── the same job, at a smaller scope ──────────────────────────────────
   *
   * Reclaiming one member of a thread is every step this kind already has,
   * over a one-entry plan: kill its sessions, forget the workspace, remove the
   * directory, maybe delete the bookmark. A second kind would be the same four
   * step bodies written twice, and the one that drifts is the copy nobody
   * runs.
   *
   * What differs is only the last step. With no `only` the thread is archived;
   * with one it is *detached* — the thread stays, having lost a checkout, and
   * a thread whose other repositories are still being worked in is exactly the
   * case this exists for.
   *
   * `Schema.optional` and not `UndefinedOr`, like every field on a job record:
   * the store is JSON, JSON has no `undefined`, and a required key left unset
   * comes back absent.
   */
  only: Schema.optional(ThreadMember),
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
  /**
   * Where the `brief` step delivers the prompt. See {@link Face}.
   *
   * `Schema.optional` and not `UndefinedOr`, which is the rule for everything
   * on this record: the input is stored as JSON, JSON has no `undefined`, and
   * `UndefinedOr` requires the key — so a job enqueued without a face would
   * die on its first step with "stored input does not match", one backoff
   * after the mistake and in a message about the wrong thing.
   *
   * Absent means the terminal, which is what every job written before this
   * field existed did. The `claim` step records it on the workspace, which is
   * what everything afterwards reads — see `faces.ts`.
   */
  face: Schema.optional(Face),
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

// ── the page beside the agent, as something either side can move ───────────
//
// The web panel's address was the window's alone: typed into a box, remembered
// per thread in localStorage, and reachable by nothing else. What that ruled
// out is the ordinary request — "open the failing build" — being answered by
// the agent that is looking at it, which is the direction `mcp.ts` exists to
// open.
//
// So a page is on the contract. Two calls, and the split is the one this file
// already draws twice:
//
//   PageOpen      an act. Somebody — a person or an agent — says where to go
//   PageChanges   a stream, because the OTHER side has to hear about it. An
//                 agent navigating is exactly the change a window that only
//                 asked would miss
//
// Keyed by **thread** and not by workspace, because that is what the panel is
// keyed by: a thread is a piece of work and the page beside it is part of that
// work — the ticket, the preview, the failing build — so two checkouts of one
// thread share a page and two threads do not.

/** A page the panel should be showing, and which thread it belongs to. */
export const Page = Schema.Struct({
  /**
   * The thread the page belongs to, or nothing when no thread claims the
   * workspace it was set from.
   *
   * Absent rather than a placeholder id, and the window's own bucket for
   * unclaimed workspaces is the same absence — see `Web.tsx`. Most checkouts
   * on a real machine predate threads, so refusing to carry a page for them
   * would refuse the ordinary case.
   */
  thread: Schema.optional(Schema.String),
  /** Where to go. Absolute, http or https — see {@link PageRefused}. */
  url: Schema.String,
  /**
   * When it was set, as epoch milliseconds.
   *
   * On the wire because the window needs to tell one navigation from the next:
   * an agent that asks for the page it is already on has said something, and a
   * value-equal event would be indistinguishable from no event at all.
   */
  at: Schema.Number,
});

export type Page = (typeof Page)["Type"];

/**
 * The url was not one this panel can be sent to.
 *
 * A refusal rather than a guess. The window's own address bar guesses — a bare
 * host gets a scheme, prose becomes a search — because a person is watching
 * what happens and can retype it. An agent is not watching, and a call that
 * quietly turned a mistyped path into a search would report success for a
 * navigation to a search engine.
 */
export class PageRefused extends Schema.TaggedError<PageRefused>()("PageRefused", {
  reason: Schema.String,
}) {}

// ── a gadget: a page the agent writes and a person reads ───────────────────
//
// The web panel points at somebody else's page. A gadget is the other kind of
// thing to put in that column: a small document an agent authors — a table
// with a toggle in it, a chart, a comparison it wants looked at — served from
// the daemon and drawn by this window.
//
// It is MDX and not HTML because a gadget is mostly prose, and prose in HTML
// spends a tag on every paragraph. Markdown is what a model writes most
// cheaply; MDX is markdown that can define a component in the minority of
// places where something has to move.
//
// ── a panel of its own, and an address that is not navigable ──────────────
//
// Gadgets briefly rode the page feed, one claim per thread about what the
// column was showing, and the flaw was in the arithmetic rather than in the
// plumbing: a thread accumulates gadgets and a page feed holds one address, so
// every gadget an agent wrote destroyed the last one. A person's reason for
// looking at the third is usually the first.
//
// So they are a panel beside the web one, with a strip of their own — the
// thread's gadgets by {@link GadgetHead.title}, newest first. Which makes the
// feed a different shape too: {@link GadgetChanges} says *one more exists*,
// where a page feed says *look here now*.
//
// `gadget://<thread>/<name>` is still the address, and is still never handed
// to a webview. `app://` was the other candidate, since it is already a
// registered standard scheme with a real origin; the reason against it is that
// `app://renderer/index.html` is this application, and a call able to name it
// points a browser view with a preload in it at the window's own origin. A
// `gadget:` address names a document *this renderer* draws, and the navigation
// guard in `pages.ts` never hears of it: two schemes to browse, and gadgets
// are not browsing.

/** The scheme, as `URL.protocol` spells it. */
export const gadgetScheme = "gadget:";

/**
 * A name a person could say out loud, because it is in the address bar.
 *
 * Narrow on purpose: it is a url segment and a map key, and the agent chooses
 * it, so every character that would need escaping somewhere is refused where
 * the mistake is rather than encoded and handed back looking different.
 */
export const gadgetName = /^[a-z0-9][a-z0-9-]{0,47}$/u;

/**
 * The bucket for a workspace no thread claims.
 *
 * The same answer the page feed gives — see {@link Page} — and it cannot be
 * mistaken for a thread, whose id is always a date and four characters.
 * Two unclaimed workspaces share it, which is honest: there is nothing to tell
 * them apart by.
 */
const loose = "loose";

/** Where a gadget is, given whose it is. */
export const gadgetAddress = (thread: string | undefined, name: string): string =>
  `${gadgetScheme}//${thread ?? loose}/${name}`;

/**
 * Read one back, or nothing when the url is not a gadget address.
 *
 * In the contract rather than in either process, because both ends ask the
 * same question of the same string — the daemon to find the document, the
 * window to know it must draw one rather than navigate — and a second parser
 * is the copy that drifts.
 */
export const readGadgetAddress = (
  url: string,
): { readonly thread?: string | undefined; readonly name: string } | undefined => {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return undefined;
  }
  if (parsed.protocol !== gadgetScheme) {
    return undefined;
  }
  const name = parsed.pathname.replace(/^\//u, "");
  const host = parsed.hostname;
  if (!gadgetName.test(name) || host === "") {
    return undefined;
  }
  return { ...(host === loose ? {} : { thread: host }), name };
};

/**
 * What a gadget's document has in scope, by name.
 *
 * ── there is no registry, and this is the whole of what there is instead ──
 *
 * A gadget defines its own components inline — MDX lets a document declare one
 * and use it — so what awp publishes is not a set of components to choose
 * from. It is this: the handful of names an inline component can reach for,
 * and it is deliberately the smallest list that can work.
 *
 *   React                   hooks, or nothing can move
 *   colors · text · space   the window's tokens, so a gadget does not look
 *                           foreign in the column it is drawn in
 *
 * What is missing is the app-native half — open this diff, this thread's
 * status, a live value from the daemon — and it is missing on purpose. Nothing
 * in it is known yet, and a list guessed at now is a registry designed for
 * gadgets nobody has written. It grows out of what the first real ones reach
 * for and cannot have.
 *
 * In the contract because two places must agree on it and neither can import
 * the other: the renderer binds these names when it runs a document, and the
 * MCP tool's description tells the agent what it may use. A name in one and
 * not the other is either a document that throws or a capability nobody knows
 * about.
 */
export const gadgetScope: ReadonlyArray<string> = ["React", "colors", "text", "space"];

/**
 * A gadget without its document: what a strip of them is drawn from.
 *
 * Separate from {@link Gadget} because a thread's gadgets are listed every
 * time the panel opens and the document is the large half — the first real one
 * compiled to 4.7KB, against a head of about a hundred bytes. Listing heads and
 * reading the one being looked at is the same shape the review queue uses for
 * the same reason.
 */
export const GadgetHead = Schema.Struct({
  /** {@link gadgetAddress} — what the list carries and the window reads by. */
  address: Schema.String,
  thread: Schema.optional(Schema.String),
  name: Schema.String,
  /**
   * What the tab says, taken from the document's first heading.
   *
   * Derived and not asked for. A third argument to `awp_gadget` is a third
   * thing to get wrong, and the failure is silent in the worst way: a tab
   * saying one thing above a document saying another. The heading is what the
   * author already wrote, in the author's own words, and it cannot disagree
   * with the document because it *is* the document.
   *
   * A document with no heading falls back to {@link GadgetHead.name}, which is
   * never empty — so nothing here is optional and no reader needs a fallback.
   */
  title: Schema.String,
  at: Schema.Number,
});

export type GadgetHead = (typeof GadgetHead)["Type"];

/** A gadget, compiled and ready to run. */
export const Gadget = Schema.Struct({
  ...GadgetHead.fields,
  /**
   * The document as JavaScript: MDX compiled to a function body.
   *
   * Compiled by the daemon and not by the window, and that is the one decision
   * in here worth arguing. A compile in the renderer is a compiler in the
   * bundle and a syntax error nobody can act on — it appears as a blank panel,
   * minutes after the agent that wrote it has moved on. Compiled at the moment
   * it is written, a broken document is a refusal returned to the agent's own
   * tool call, in the compiler's own words, while it still has the source in
   * hand. That is the same argument every refusal sentence in `mcp.ts` makes:
   * what reads it is a model, and it can fix its own output.
   */
  code: Schema.String,
});

export type Gadget = (typeof Gadget)["Type"];

/**
 * The document was not one that can be drawn.
 *
 * Carries the compiler's sentence, or the rule that was broken. A refusal and
 * not a gadget that renders an error, because the caller is the author.
 */
export class GadgetRefused extends Schema.TaggedError<GadgetRefused>()("GadgetRefused", {
  reason: Schema.String,
}) {}

// ── what a conversation was handed ─────────────────────────────────────────
//
// `/mcp` in the chat asks what tools the agent on the other end has, and the
// honest answer is narrower than the question. The daemon hands every
// conversation an MCP server on `session/new`, `session/load` and
// `session/fork` — see `chat.ts` — so what it knows for certain is *what it
// handed over*. Whether the agent's own MCP client accepted the handshake and
// listed the tools is not something ACP reports back, and this does not
// pretend otherwise: the window says which half it is showing.

export const McpTool = Schema.Struct({
  name: Schema.String,
  /** The first sentence of it. A model reads the whole one; a person does not. */
  description: Schema.String,
});

export type McpTool = (typeof McpTool)["Type"];

/**
 * The server this workspace's conversation is given, as it was given.
 *
 * Every field is what was actually passed — `process.execPath` rather than
 * "bun", the workspace directory rather than the repository, and the daemon url
 * the spawned process is told to talk to. That last one is the field worth
 * having on screen: a second instance's agents must reach the second instance,
 * and the way that goes wrong is silently.
 */
export const McpStatus = Schema.Struct({
  name: Schema.String,
  command: Schema.String,
  args: Schema.Array(Schema.String),
  /**
   * The directory the server is bound to, which is the whole of its scope.
   *
   * No tool takes a workspace argument, so this path is the reason a
   * conversation cannot reach another checkout. Shown for that reason: it is
   * the sentence "what can this agent touch", as a value.
   */
  cwd: Schema.String,
  url: Schema.String,
  tools: Schema.Array(McpTool),
});

export type McpStatus = (typeof McpStatus)["Type"];

/** No session to tell. A workspace whose agent has ended, or never started. */
export class NoAgent extends Schema.TaggedError<NoAgent>()("NoAgent", {
  project: Schema.String,
  workspace: Schema.String,
  /**
   * Why, when there is anything to add beyond "there is nobody there".
   *
   * The terminal face needs none: a missing or ended zmx session is the whole
   * of what went wrong, and the two names above say which workspace. The chat
   * face refuses for reasons that are sentences — no adapter installed, no
   * `claude` on the PATH — and `ChatError` already carries them, so this is
   * where they arrive rather than being flattened to one word.
   *
   * `said` in the window's daemon.ts reads exactly this field. A refusal with
   * nothing in it renders as the tag, which is what five call sites were doing
   * before that helper existed.
   */
  reason: Schema.optional(Schema.String),
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

// ── the reviewQueue ──────────────────────────────────────────────────────────────
//
// Every open pull request awp can see, sectioned by what the next move is.
// Ported from the deck's reviewQueue scope, and one thing about it is inverted.
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
// drifts is the one nobody tests. The rule here is `PRReviewQueueBucket`'s
// precedence, which is subtle enough that the archive locked it with tests.

/** How CI rolls up. `none` is a PR with no checks, which is not a failure. */
export const CIState = Schema.Literals(["none", "pending", "passing", "failing"]);

export type CIState = (typeof CIState)["Type"];

/**
 * GitHub's branch-protection verdict, or `none` when nobody has reviewed.
 *
 * Not the same question as "did anyone leave feedback" — see
 * {@link ReviewQueueItem.hasReviewComments}, which is the only signal that catches a
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
 * Which section of the reviewQueue a row belongs to, and the order the sections are
 * drawn in: most-your-problem first.
 *
 * Sections rather than the attention scope's flat list of reasons, because the
 * question the reviewQueue answers is "what is my next move", and the five answers
 * are stable enough to be headings. The archive's precedence, kept:
 *
 *   needs-your-review   somebody asked you — wins over everything, including
 *                       the PR's own state, because it names you
 *   needs-action        yours, and something is wrong with it
 *   ready-to-merge      yours, approved and green
 *   other-open          neither yours nor waiting on you
 *   mine                yours, and the ball is elsewhere — or still a draft
 */
export const ReviewQueueBucket = Schema.Literals([
  "needs-your-review",
  "needs-action",
  "ready-to-merge",
  "other-open",
  "mine",
]);

export type ReviewQueueBucket = (typeof ReviewQueueBucket)["Type"];

/** The heading for a bucket. One place, so two surfaces cannot disagree. */
export const bucketLabel = (bucket: ReviewQueueBucket): string => {
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
export const reviewQueueBuckets: ReadonlyArray<ReviewQueueBucket> = [
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
 * so nothing downstream has to know whose reviewQueue it is rendering. With no login
 * they are all false, which is why {@link ReviewQueue.viewer} is on the answer: every
 * bucket that names the viewer is empty in that case, and an reviewQueue that is
 * empty because nobody is signed in must not look like an reviewQueue with nothing in
 * it.
 */
export const ReviewQueueItem = Schema.Struct({
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

  bucket: ReviewQueueBucket,
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

export type ReviewQueueItem = (typeof ReviewQueueItem)["Type"];

/**
 * Where one project's rows came from, and what went wrong if they did not.
 *
 * **Per project, because one repository's failure must not lose the others.**
 * `gh` is missing, or a repository's remote is not GitHub, or a token expired —
 * and the honest answer is the other projects' pull requests plus a sentence
 * about the one that could not be read. A single error for the whole call would
 * turn one unauthenticated repository into an empty reviewQueue.
 */
export const ReviewQueueSource = Schema.Struct({
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
   * silently, which would leave a person reading a clean-looking reviewQueue for a
   * repository where nothing can say a PR is in conflict.
   */
  degraded: Schema.optional(Schema.String),
});

export type ReviewQueueSource = (typeof ReviewQueueSource)["Type"];

export const ReviewQueue = Schema.Struct({
  /** Every row, already sectioned and ordered. See {@link ReviewQueueItem.bucket}. */
  items: Schema.Array(ReviewQueueItem),
  sources: Schema.Array(ReviewQueueSource),
  /**
   * The authenticated `gh` login, or absent when there is none.
   *
   * On the answer rather than left implicit because it is the difference
   * between "nothing is waiting on you" and "nobody knows who you are". Every
   * viewer-relative bucket is empty without it.
   */
  viewer: Schema.optional(Schema.String),
});

export type ReviewQueue = (typeof ReviewQueue)["Type"];

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
 * Deliberately a different shape from {@link ReviewQueueItem}, which is a *row*: this
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
   * Here as well as on {@link ReviewQueueItem} because the panel is opened *from* a
   * workspace and has to be able to offer the repair below without the reviewQueue
   * having been read at all.
   */
  workspace: Schema.optional(Schema.String),
  /** That workspace does not contain this head. See {@link ReviewQueueItem.moved}. */
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

// ── the chat ─────────────────────────────────────────────────────────────────
//
// A conversation with an agent, as records rather than as a picture of one.
// See `chat.ts` in the daemon for what these are made of and why the session
// is asked for rather than composed from a path.

/** Who said it. `thought` is the model reasoning aloud, and is drawn quietly. */
export const ChatRole = Schema.Literals(["user", "agent", "thought"]);

export type ChatRole = (typeof ChatRole)["Type"];

/**
 * One of the agent's own slash commands, as it advertises them.
 *
 * A skill is one of these. Claude Code discovers commands from the project,
 * from `~/.claude` and dynamically as it works, and `available_commands_update`
 * is how it says so — a list this daemon used to drop.
 *
 * **Not the window's commands.** `/new` and `/mcp` are things amoeba does and
 * are intercepted before anything is sent; one of these is delivered as
 * ordinary prompt text, because that is exactly what it is — the adapter's
 * `promptToClaude` passes `/bro` through to the CLI, which resolves it.
 */
export const ChatCommand = Schema.Struct({
  /** With the slash, because that is what somebody types. */
  name: Schema.String,
  description: Schema.String,
  /** What its arguments are, when it takes any: `[file]`, `<message>`. */
  hint: Schema.optional(Schema.String),
});

export type ChatCommand = (typeof ChatCommand)["Type"];

/**
 * A file a tool changed, as a patch anybody can render.
 *
 * The adapter reports an edit as `{type: "diff", path, oldText, newText}` on
 * the tool call's content — two whole texts, not a patch — and it reports one
 * per hunk, so a `MultiEdit` is several of these about one file. Neither half
 * is drawable: a window would have to diff the two texts itself, and a
 * *terminal* would have to do it a second time.
 *
 * So the daemon does it once. What crosses the wire is a unified patch,
 * which is the shape both faces already render: amoeba parses it with the
 * same `parsePatchFiles` a fenced ```diff in a message goes through, and the
 * TUI hands it to the same tree-sitter its fences use, under `diff`. Same
 * argument as `SessionIdentity` — a client re-deriving a daemon's rule is a
 * second implementation, and the copy that drifts is the one nobody tests.
 */
export const ChatDiff = Schema.Struct({
  /** As the adapter named it: absolute. A client shortens it to taste. */
  path: Schema.String,
  /** One file, unified, with three lines of context. */
  patch: Schema.String,
});

export type ChatDiff = (typeof ChatDiff)["Type"];

/** One of the buttons on a permission request. */
export const ChatPermissionOption = Schema.Struct({
  id: Schema.String,
  name: Schema.String,
  /** `allow_once`, `allow_always`, `reject_once` — the agent's own vocabulary. */
  kind: Schema.String,
});

export type ChatPermissionOption = (typeof ChatPermissionOption)["Type"];

/**
 * Something that happened in a conversation.
 *
 * One struct with a `kind` rather than a union of tagged structs, which is the
 * shape the rest of this contract already uses — and it keeps the renderer off
 * `_tag`, which the lint rule here deliberately refuses.
 *
 * **A tool arrives as several of these sharing one `id`**, and they are
 * patches: pending with a generic title, then the command, then the output,
 * then completed. Measured against a real turn — one `tool_call` and four
 * `tool_call_update` for a single `cat`. So every field but `kind` and `id` is
 * optional, and the window merges by id rather than appending.
 */
export const ChatUpdate = Schema.Struct({
  kind: Schema.Literals(["message", "tool", "permission", "turn", "usage", "commands", "compact"]),

  /** message: who, and what they said. Chunks, so they are appended. */
  role: Schema.optional(ChatRole),
  text: Schema.optional(Schema.String),
  /**
   * Which row this update is about: a tool call, a permission — or a whole
   * message with a name.
   *
   * The third is the daemon's echo of what somebody typed, and only that
   * carries one — see `ChatSend.key`. It reuses `id` rather than taking a
   * field of its own because it is asking for the same thing the other two
   * are: the name of the row, and therefore what makes applying an update
   * twice a no-op.
   *
   * ── and a `turn` carries the key of the message that caused it ──────────
   *
   * Which is the same question again: *which row is this about*. A turn is
   * the daemon's own bracket around one `session/prompt`, so the message
   * that prompted it is the row it belongs to.
   *
   * It is on the wire because nothing else can put it there. A window can see
   * that turns started and ended; it cannot see **whose**, and with two
   * messages behind one slow answer that is the whole question — the first
   * end belongs to the turn they are both waiting behind, and a client
   * without the key has to guess that it released both. See `queued` in
   * `conversation.ts`, which used to guess exactly that.
   */
  id: Schema.optional(Schema.String),
  title: Schema.optional(Schema.String),
  /** `execute`, `read`, `edit` — what sort of thing the tool is. */
  toolKind: Schema.optional(Schema.String),
  /**
   * The tool's own name: `Bash`, `Read`, `Edit`, `mcp__awp__awp_thread`.
   *
   * ── the kind is too coarse to label a row with ──────────────────────────
   *
   * `toolKind` is ACP's enum, and the adapter maps every Claude Code tool
   * onto ten values: `Bash` is `execute`, and so is nothing else — while
   * `Skill`, `AskUserQuestion` and every MCP tool are all `other`, and
   * `TodoWrite`, `Task` and the task tools are all `think`. A column of rows
   * reading `execute` and `other` says less than the titles beside it.
   *
   * The name is on every `tool_call` notification as
   * `_meta.claudeCode.toolName` and was simply not taken. It is a name from
   * somebody else's vocabulary and is passed through unchanged: a client
   * shortens or renames it to taste, and a tool this daemon has never heard
   * of still arrives with a label rather than as `other`.
   */
  toolName: Schema.optional(Schema.String),
  /**
   * What the call is *for*, in the agent's own words.
   *
   * Bash's schema requires a description — "Clear, concise description of
   * what this command does in active voice" — and the adapter forwards it as
   * `_meta.claudeCode.title`. It is the difference between a row reading
   *
   *   bash  python3 - <<'PY' … forty lines of heredoc …
   *   bash  Show where the adapter reads a tool's description field
   *
   * and it is the only field on a tool call that says intent rather than
   * mechanism. A `Task` carries the same thing as its title, which is why
   * this is not read from there: that one is already the title.
   *
   * Absent for every other tool — nothing else in the set has a purpose
   * field — so the command, the path or the pattern stands as the row.
   */
  purpose: Schema.optional(Schema.String),
  status: Schema.optional(Schema.String),
  output: Schema.optional(Schema.String),

  /**
   * tool: what it changed on disk, when it changed anything.
   *
   * Only an edit carries these, and they **replace** rather than merge: the
   * adapter sends its guess at the change when the call is made and the real
   * one — out of the SDK's `structuredPatch` — when it has run, and the second
   * is the truth about the same file. A merge would draw both.
   */
  diffs: Schema.optional(Schema.Array(ChatDiff)),

  /** permission: what a person may answer. */
  options: Schema.optional(Schema.Array(ChatPermissionOption)),
  /**
   * permission: which option was chosen, once one has been.
   *
   * The other half of the two-client problem. **Nothing in ACP says a
   * permission was answered** — the adapter's reply *is* the answer, so the
   * only process that knows is the daemon, and a second client went on
   * offering buttons for a question that had been settled minutes ago. So the
   * daemon says so itself, with `status: "answered"` and the option id.
   *
   * The id and not the name: every client already holds the options for that
   * request, so it can say `Always Allow` in its own words, and a name on the
   * wire would be a second copy of something already sent.
   */
  chose: Schema.optional(Schema.String),
  /**
   * permission: the tool call this is asking about.
   *
   * The adapter emits the tool call *before* it asks — `ensureToolCallEmitted`
   * in its own source, and it is why the id is worth carrying: without it the
   * question is a second row saying the same command as the row above it,
   * which is what the panel drew. With it the buttons sit on the call they
   * are about.
   */
  about: Schema.optional(Schema.String),

  // ── tool: a delegated call ──────────────────────────────────────────────
  //
  // A subagent is not a kind of update. Measured in the adapter's own source
  // 2026-09-08: there is no `subagent` anywhere in ACP, no nesting and no
  // second stream — an agent that spawns one produces an ordinary `tool_call`
  // that sits at `in_progress` for minutes. What the subagent is rides in
  // `_meta.claudeCode.toolResponse` on the progress updates, and was being
  // thrown away.
  //
  // So there is no tree to draw here, only a call to label honestly.

  /** Which kind of subagent a `Task` call spawned. */
  subagent: Schema.optional(Schema.String),
  /** How long the call has been running, in seconds, as the adapter reports it. */
  elapsed: Schema.optional(Schema.Number),
  /**
   * Why a spawn looks stalled.
   *
   * The adapter forwards the SDK's retry counters verbatim and says why in its
   * own comment: "when the subagent is waiting out an API rate-limit retry …
   * so clients can show why a spawn looks stalled". A subagent behind a rate
   * limit and a subagent doing slow work are the same picture without this,
   * and only one of them is worth waiting for.
   */
  retry: Schema.optional(
    Schema.Struct({
      attempt: Schema.Number,
      of: Schema.optional(Schema.Number),
      inMs: Schema.optional(Schema.Number),
    }),
  ),

  /**
   * turn: `started` or `ended`, and why it ended.
   *
   * There is no turn boundary in the updates the adapter sends — a turn is a
   * request and a reply, and the reply is not an update. So the daemon says so
   * itself, on either side of the prompt it made. Without it the panel cannot
   * tell "the agent is thinking" from "the agent had nothing to say", and
   * those look identical: an empty space.
   *
   * On the wire rather than kept in the window because a second window on the
   * same workspace has to know too, and because a window opening in the middle
   * of a turn should say so rather than looking idle.
   */
  stopReason: Schema.optional(Schema.String),

  // ── usage ───────────────────────────────────────────────────────────────
  //
  // Arrives on its own, several times a turn, and was being dropped as
  // something nobody reads. It is the only place the context figure exists.
  //
  // **`size` changes mid-turn.** Measured: 200000 on the first update of a
  // turn and 1000000 on the last, because the model in use has a larger window
  // than the default and the adapter learns that as it goes. So the newest
  // pair is the answer and an earlier one must not be kept beside it.

  /** Tokens used so far in this session. */
  used: Schema.optional(Schema.Number),
  /** The context window, in tokens. */
  size: Schema.optional(Schema.Number),
  /** What the session has cost so far, in whatever currency the agent reports. */
  cost: Schema.optional(Schema.Number),

  // ── commands ────────────────────────────────────────────────────────────
  //
  // The agent's own slash commands, skills included. An update rather than a
  // call, and for a reason particular to this list: the adapter *pushes* it
  // when the set changes — its own comment says "skills discovered dynamically
  // as the agent works in a subdirectory" — so a client that asked once would
  // be right until the agent learned something. It goes through the daemon's
  // transcript like every other update, so a window that opens later is told
  // by the replay rather than by a second call.

  /** Every command the agent advertises. Replaces the set, never merges. */
  commands: Schema.optional(Schema.Array(ChatCommand)),

  // ── compact ─────────────────────────────────────────────────────────────
  //
  // `/compact` throws most of a conversation away and keeps a summary, which
  // is a thing that HAPPENED to the transcript rather than a thing anybody
  // said. The adapter reports it as three ordinary agent messages — measured
  // in its own source, `acp-agent.js`:
  //
  //   status "compacting"          →  a chunk reading `Compacting...`
  //   compact_result "success"     →  a chunk reading `Compacting completed.`
  //   compact_result "failed"      →  a chunk, `Compacting failed: <error>`
  //   compact_boundary             →  a usage_update carrying the real
  //                                   post-compaction figure
  //
  // Left as prose, a compaction is three paragraphs in the middle of a
  // conversation — reported as "it just said compacting compacting
  // compacting" — and nothing on screen says the transcript above is no
  // longer what the agent can see. So the daemon recognises the three and
  // says so as a state on one row, which both faces draw as a boundary.
  //
  // The token counts are deliberately absent: only the usage figure knows
  // them, it is already under the composer, and it corrects itself from
  // `compact_boundary` seconds later. A second copy here would be the one
  // that drifts.
  //
  // `status` is `running`, `done` or `failed`, and `text` carries the
  // adapter's own sentence when it failed. `id` is one per compaction, so
  // the outcome patches the row that announced it rather than adding a
  // second — assigned by the daemon, because the three messages are prose
  // and carry no id of their own.
});

export type ChatUpdate = (typeof ChatUpdate)["Type"];

/**
 * One thing about a session a person may change.
 *
 * Everything the adapter lets a client set arrives in this one shape — the
 * permission mode, the model, the effort, fast mode — each a select with its
 * current value and the values it accepts. Measured 2026-08-28: four of them,
 * all `type: "select"`, all through `session/set_config_option`.
 *
 * That is why there is no `ChatModel` and no `ChatMode` call. Three bespoke
 * controls would be three things to keep in step with an adapter that already
 * answers the question generically, and a fifth option appearing upstream
 * would be a fifth thing to add here rather than a row that simply shows up.
 */
/**
 * How a message reached the agent.
 *
 * Worth a value on the wire rather than being inferred, because the two are a
 * different thing to a person watching: a steer is being read *now*, inside
 * the turn already running, and a prompt is a turn of its own. The window
 * cannot tell them apart on its own — whether a steer is possible depends on
 * whether a turn was in flight at the moment the adapter looked, which is a
 * question only the adapter can answer without a race.
 *
 * `queued` is the third, and it is about one state only: a **compaction**.
 * Compacting is a turn like any other, so a steer aimed at it is injected into
 * the very turn that is rewriting the context — which the adapter reports as
 * `compacting failed`, losing the compaction and leaving the message
 * delivered into a turn that is going nowhere. So the daemon holds it and
 * sends it once the compaction is over, and says so, because from the
 * window's side "held for a few seconds" and "steered" are the same silence.
 */
export const ChatDelivery = Schema.Literals(["steer", "prompt", "queued"]);

export type ChatDelivery = (typeof ChatDelivery)["Type"];

export const ChatConfigOption = Schema.Struct({
  /** `mode`, `model`, `effort`, `fast` — the adapter's own ids. */
  id: Schema.String,
  name: Schema.String,
  description: Schema.optional(Schema.String),
  currentValue: Schema.String,
  values: Schema.Array(
    Schema.Struct({
      value: Schema.String,
      name: Schema.String,
      description: Schema.optional(Schema.String),
    }),
  ),
});

export type ChatConfigOption = (typeof ChatConfigOption)["Type"];

/**
 * A session could not be started again.
 *
 * A sentence rather than a set of cases, for the same reason
 * {@link ProjectImportFailed} carries one: every cause is a thing about the
 * machine — no such project, a workspace directory that has been removed, zmx
 * refusing — and the only useful rendering of any of them is what was said.
 */
export class SessionStartFailed extends Schema.TaggedError<SessionStartFailed>()(
  "SessionStartFailed",
  { reason: Schema.String },
) {}

/** The conversation could not be had. */
export class ChatUnavailable extends Schema.TaggedError<ChatUnavailable>()("ChatUnavailable", {
  reason: Schema.String,
}) {}

export class AwpRpcs extends RpcGroup.make(
  /** Every session the multiplexer knows about, awp's or not. */
  Rpc.make("SessionList", {
    success: Schema.Array(SessionInfo),
  }),

  /**
   * Where a workspace's checkout is.
   *
   * ── why this is asked rather than composed ────────────────────────────────
   * `~/.awp/workspaces/<project>/<workspace>` is the convention, and the
   * renderer could write that string itself — except that it could not: the
   * home directory is not something a browser knows, and the renderer may not
   * import a node builtin at all (`import/no-nodejs-modules`, which is on for
   * exactly this reason).
   *
   * It is the same argument as {@link SessionIdentity} being on the wire. A
   * client re-deriving a rule the daemon owns is a second implementation of
   * it, and the copy that drifts is the one nobody tests.
   *
   * Asked only for a workspace with no session, because a session already
   * carries `startDir`. Answers the path whether or not anything is there —
   * the callers are questions about a checkout, and "no such directory" is
   * theirs to report in their own words.
   */
  Rpc.make("WorkspaceDir", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.String,
  }),

  /**
   * Start a workspace's agent session again.
   *
   * The one act the sidebar could not offer, and the reason it had to exist is
   * in the address rather than in the session: a workspace whose session has
   * exited is still a workspace — its directory, its bookmark, its thread and
   * its conversation are all still there — and until this there was no way
   * back to a terminal in it short of a shell.
   *
   * **The agent kind only.** An editor or a user action is configured per
   * project and started on purpose; the agent is the one every workspace has
   * and the one whose absence is what makes a row look dead.
   *
   * Idempotent, because `Multiplexer.start` is: a name that is already there
   * is left exactly as it was, which is also what stops this ever touching a
   * session it did not create. Answers with the session's name so the caller
   * can go straight to it rather than waiting for the next listing.
   */
  Rpc.make("SessionStart", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.String,
    error: SessionStartFailed,
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

  // ── the chat ─────────────────────────────────────────────────────────────
  //
  // The same split as Attach and Write, for the same reason: rpc streams one
  // way, so reading the conversation and adding to it are two calls. Keyed by
  // the workspace rather than by a handle, so a window that reconnects can
  // carry on talking without first re-establishing anything.

  /**
   * Open a conversation on a workspace and receive it: the history first, then
   * whatever happens next, in the same shape.
   *
   * One adapter process per workspace, shared by everyone looking at it and
   * released when the last one goes — the arrangement Attach uses for a pty,
   * and it holds here for a different reason. Two adapters loaded on one
   * session would be two writers on one transcript, and nothing in the
   * protocol stops them.
   */
  Rpc.make("ChatOpen", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: ChatUpdate,
    error: ChatUnavailable,
    stream: true,
  }),

  /**
   * Say something.
   *
   * Returns when the turn has *started*. The answer comes back down ChatOpen,
   * because a turn takes as long as the work does — awaiting it here would make
   * sending a message a call that returns when the agent has finished thinking.
   */
  Rpc.make("ChatSend", {
    /**
     * `key` names the message, and the client mints it.
     *
     * ── two clients on one conversation ────────────────────────────────────
     *
     * The daemon echoes what was said back down the update stream, because
     * nothing else does: no adapter sends a user chunk on a live turn, so a
     * TUI and a window sharing a conversation each saw only what *they* had
     * typed. The echo is what makes them honest with each other.
     *
     * That leaves the sender with two copies of its own message — the local
     * one it painted on the keypress and the echo — so the two need one name.
     * The client mints it rather than the reply carrying it, and that is not
     * arbitrary: the copy is drawn *before* the reply exists, so a key that
     * arrived with the reply would name a row already on screen. A uuid, so
     * two clients cannot collide.
     */
    payload: {
      project: Schema.String,
      workspace: Schema.String,
      text: Schema.String,
      key: Schema.String,
      /**
       * Stop what the agent is doing and read this instead.
       *
       * ── waiting is the default, and that is a correction ─────────────────
       *
       * Every message used to be an interrupt. The daemon tried
       * `_session/steering` on every send, which the adapter delivers at
       * priority `now` — and its own comment is unambiguous about what that
       * means: *"Pre-empting means ABORTING: the interrupted cycle emits a
       * `result` of its own and the steered message runs as a second one."*
       * The answer on screen stopped mid-sentence and a different one began.
       *
       * Reported as steering too aggressively, and the giveaway is what the
       * CLI itself does with a person's message. Its input queue is ranked
       * `{ now: 0, next: 1, later: 2 }`, an ordinary user message is built at
       * `next`, and an absent priority reads as `next` — so waiting for the
       * boundary is not a fallback, it is the default everywhere except here.
       *
       * So the two are a choice now, and the destructive one is the one you
       * have to ask for:
       *
       *   absent   the agent finishes, then reads this        session/prompt
       *   true     the agent stops where it is                _session/steering
       *
       * Optional rather than a required boolean, because absent has to mean
       * the safe one: an older client, a replayed call, or a caller that has
       * not thought about it must not interrupt anybody.
       */
      interrupt: Schema.optional(Schema.Boolean),
    },
    success: ChatDelivery,
    error: ChatUnavailable,
  }),

  /**
   * Open the terminal's own conversation in the chat, by forking it.
   *
   * A fork and not a load. Loading would make the daemon a second writer on a
   * transcript an interactive `claude` is still appending to, with neither
   * process aware of the other — which is why `ChatOpen` never joins the
   * newest session in a directory. A fork reads it, copies it under a new id
   * and leaves the original alone, so this is the one shape of "look at what
   * the terminal is doing" that is safe to offer.
   *
   * Answers the new session id. The window then re-opens its stream, which
   * replays the forked history as ordinary updates.
   */
  Rpc.make("ChatFork", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.String,
    error: ChatUnavailable,
  }),

  /**
   * Start this workspace's chat again from nothing, answering the new session.
   *
   * ── it is not a fork, and it is not a reload ────────────────────────────
   *
   * `ChatFork` copies the conversation the *terminal* is having. This forgets
   * the one the chat is having: the stored session id goes, the adapter holding
   * it is thrown away, and the next open is a `session/new`. What survives is
   * the transcript on disk — nothing is deleted — so the old conversation is
   * still there to be found, it is simply no longer this workspace's.
   *
   * The reply is the new session id, for the same reason the fork's is: the
   * panel re-subscribes on it, and a refusal lands on the keypress rather than
   * silently on the next subscribe.
   */
  Rpc.make("ChatFresh", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.String,
    error: ChatUnavailable,
  }),

  /**
   * The MCP server this workspace's conversation is handed.
   *
   * No error channel, and that is deliberate: it is a description of what the
   * daemon passes on every open, composed from the same functions that pass it,
   * so there is nothing to fail. A workspace with no conversation open still
   * has an answer — which is the right one, because the question a person asks
   * with `/mcp` is "what will this agent be able to do".
   */
  Rpc.make("McpStatus", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: McpStatus,
  }),

  /**
   * Answer a permission request, by the id its update carried.
   *
   * This exists because the alternative is the agent's default: a model
   * classifier approving tool calls with nobody in this window asked. Measured
   * — see chat.ts.
   */
  Rpc.make("ChatAnswer", {
    payload: {
      project: Schema.String,
      workspace: Schema.String,
      request: Schema.String,
      option: Schema.String,
    },
    error: ChatUnavailable,
  }),

  /**
   * Stop the turn the agent is in the middle of.
   *
   * ── silent when there is nothing to stop ──────────────────────────────
   *
   * The adapter answers `session/cancel` on an idle session by doing nothing
   * — its own handler returns early — so this is a no-op rather than a
   * refusal, and a client does not have to hold "is a turn running" to know
   * whether pressing the key was allowed. Which matters, because the two
   * clients would each be holding their own copy of that.
   *
   * There is no update saying "cancelled" either, and none is wanted: the
   * turn's own `session/prompt` returns with a stop reason, and the daemon
   * already turns that into the `turn ended` every other ending produces. One
   * edge, whatever stopped it.
   */
  Rpc.make("ChatCancel", {
    payload: { project: Schema.String, workspace: Schema.String },
    error: ChatUnavailable,
  }),

  /**
   * What this session is running as, and what it could be running as instead.
   *
   * A call rather than a field on the stream, because it is asked once when a
   * panel opens and again after a change — where an update on the stream would
   * put a list of every model on the wire several times a turn.
   */
  Rpc.make("ChatConfig", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Schema.Array(ChatConfigOption),
    error: ChatUnavailable,
  }),

  /**
   * Change one of them, and get the whole set back as it now stands.
   *
   * The reply carries the list rather than an acknowledgement, so a client
   * never has to ask again to find out what it just did — and so a setting the
   * agent refused or adjusted comes back as what actually happened rather than
   * as what was requested.
   */
  Rpc.make("ChatSet", {
    payload: {
      project: Schema.String,
      workspace: Schema.String,
      option: Schema.String,
      value: Schema.String,
    },
    success: Schema.Array(ChatConfigOption),
    error: ChatUnavailable,
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
   * Every task awp holds, filtered.
   *
   * Named `TaskBoard` rather than `TaskList`, which is taken by the reader for
   * a *session's* own list — `agent-tasks.ts`'s, keyed by a directory. The two
   * are deliberately different calls: that one asks what the agent in one
   * checkout is doing, this one asks what is written down anywhere.
   *
   * Read-only, and that is the whole surface for now. There is no composer, so
   * nothing in this window is a writer — the store is filled by ingest from
   * sources that already existed, which keeps the promise `agent-tasks.ts`
   * makes about not being a second writer of somebody else's list.
   *
   * The reply comes from the store and a re-read of the sources is started
   * *behind* it, the same shape as the pull request cache: a panel remounted
   * on every tab switch must not cost a disk sweep per glance, and a question
   * that writes is what `--ignore-working-copy` exists to prevent.
   */
  Rpc.make("TaskBoard", {
    payload: {
      /** Every tag named has to be present — an AND. Absent means everything. */
      tags: Schema.optional(Schema.Array(Schema.String)),
      /** Absent means every status, finished ones included. */
      statuses: Schema.optional(Schema.Array(Schema.String)),
    },
    success: Schema.Array(Task),
  }),

  /**
   * A sweep found the sources saying something different.
   *
   * A nudge and not a listing, deliberately. The panel narrows by tag and by
   * status and the daemon does not know which filter a given client is holding,
   * so a push carrying rows would be a push most clients would have to correct.
   * What is carried instead is what changed and how much, which is enough to
   * decide whether to re-ask — and the counts make a silent stream tellable
   * from a stream nothing is listening to.
   *
   * Nothing is replayed. This is an event, the same argument
   * {@link Rpc PageChanges} makes: what a client that has just connected wants
   * is the listing, which it asks for. And the rule that goes with it — a
   * stream carries changes from *now* — means a client has to re-ask on
   * reconnect as well, or it is up to date on everything except what it missed.
   *
   * **Subscribing is also what makes the sweep run.** The sources are files
   * nothing here writes: a `TODO.md` an agent edited in some checkout, and
   * Claude Code's own per-session lists. There is no event to hang this on, so
   * the daemon sweeps on a slow timer for as long as anybody is watching, and
   * once more whenever a turn ends — which is the moment the files an agent was
   * editing have settled.
   */
  Rpc.make("TaskChanges", {
    success: TaskChange,
    stream: true,
  }),

  // ── and this window is a writer now ──────────────────────────────────────
  //
  // The board was read-only for as long as it had nothing of its own in it:
  // every row was a copy of a file somebody else wrote, and a panel that could
  // change one would have been a second writer of somebody else's list — which
  // `agent-tasks.ts` says in its own comment it will not be.
  //
  // What changes that is a source awp owns. An `awp` task is swept by nothing,
  // so there is no later reading of a file that could decide it had been
  // finished, and these four calls are the whole of what may be done to one.
  //
  //   TaskAdd      write one here
  //   TaskStatus   move one awp owns · refused for a copy
  //   TaskTag      any task, whatever its source — see TaskRefused
  //   TaskForget   drop one awp owns · refused for a copy

  Rpc.make("TaskAdd", {
    payload: {
      subject: Schema.String,
      /** Markdown, and rendered as such. Empty is ordinary. */
      description: Schema.String,
      /**
       * Where it starts.
       *
       * Given rather than defaulted, because the two openings are different
       * acts: writing something down for later, and starting it.
       */
      status: Schema.String,
      tags: Schema.Array(Schema.String),
    },
    success: Task,
    error: TaskRefused,
  }),

  Rpc.make("TaskStatus", {
    payload: { id: Schema.String, status: Schema.String },
    success: Task,
    error: TaskRefused,
  }),

  /**
   * Apply or remove a tag.
   *
   * On any task, whatever wrote it, and it outlives every sweep — which is what
   * makes `thread:<id>` on a `TODO.md` entry the thing it is: a claim about
   * what the work belongs to, not a contradiction of what the file says.
   */
  Rpc.make("TaskTag", {
    payload: { id: Schema.String, tag: Schema.String, on: Schema.Boolean },
    success: Task,
    error: TaskRefused,
  }),

  Rpc.make("TaskForget", {
    payload: { id: Schema.String },
    success: Schema.Void,
    error: TaskRefused,
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
   * Every thread, newest first. Archived ones are left out.
   *
   * The store keeps them and this call does not return them — archiving is how
   * a thread leaves the sidebar, and a reply that carried them would make that
   * act invisible. Everything that has to see a put-away thread (the archive
   * job, a restore) reads the store rather than this.
   */
  Rpc.make("ThreadList", {
    success: Schema.Array(Thread),
  }),

  /**
   * The same list again, each time any thread changes.
   *
   * This was deliberately absent, on the argument that a thread changes when a
   * person changes it in this window, so the reply to the change is the update.
   * That premise is false three ways over, and each one was patched separately
   * before it was replaced:
   *
   * ```
   *   a create job    claims the workspace at its second-to-last step, minutes
   *                   after the reply the window already acted on
   *   a review        links the pull request from inside the job
   *   the reviewQueue join  adopts a pull request by its head commit, on a read
   *                   nobody made from this window
   *   another daemon  a second instance on the same store — see the note in
   *                   CLAUDE.md. Its writer is not this window at all
   * ```
   *
   * The whole list rather than one record, for the reason `WorkspaceFactsChanges`
   * sends the whole table: it is a few kilobytes, and it is the only shape that
   * can say a thread is gone.
   *
   * Nothing is replayed. A subscription answers what changes and a question
   * answers what is, so a client asks {@link Rpc ThreadList} at mount and again
   * on every reconnect, exactly as it does for jobs.
   */
  Rpc.make("ThreadChanges", {
    success: Schema.Array(Thread),
    stream: true,
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
   * Reclaim one checkout, leaving the thread and its other members alone.
   *
   * The same job as {@link Rpc ThreadArchiveStart} with `only` set — see
   * {@link ArchiveThread} for why that is one kind rather than two. The last
   * step detaches the member instead of archiving the thread.
   *
   * A job and not a call, for the reason every destructive multi-step thing
   * here is one: it kills sessions, forgets a workspace and removes a
   * directory, and a failure part way through has to be visible and resumable
   * rather than a rejected promise.
   *
   * {@link Rpc ThreadDetach} is the *other* thing and stays: that releases the
   * claim and takes nothing with it, which is what moving a workspace between
   * threads means. This takes the checkout away.
   */
  Rpc.make("ThreadReclaimStart", {
    payload: {
      thread: Schema.String,
      member: ThreadMember,
      deleteBookmarks: Schema.Boolean,
    },
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
  /**
   * The thread a directory's checkout belongs to. See {@link ThreadHere}.
   *
   * The read half of the agent-facing surface, and the mirror of `ReviewAt`:
   * both take a directory and neither takes a pair.
   */
  Rpc.make("ThreadAt", {
    payload: { from: Schema.String },
    success: ThreadHere,
    error: NotAWorkspace,
  }),

  /**
   * Say something to a sibling checkout in the same thread.
   *
   * `from` is a directory and the recipient is a workspace *name*, which is the
   * whole addressing rule: the sender is resolved from where it is standing and
   * the target is looked up among the members of the thread that holds it. So
   * there is no pair to pass and no call that reaches another piece of work —
   * the same binding {@link Rpc ThreadAt} has, applied to a write.
   *
   * The workspace name alone is enough because a thread holds at most one
   * checkout per project and the names within one thread are its members' —
   * `beta/tabular-exports` and `rowan/tabular-exports` are one name to a
   * sender. Ambiguity is refused rather than guessed at.
   */
  Rpc.make("MessageSend", {
    payload: { from: Schema.String, to: Schema.String, body: Schema.String },
    success: Message,
    error: Schema.Union([NotAWorkspace, MessageRefused]),
  }),

  /**
   * Every message waiting for this checkout, and reading it marks it read.
   *
   * The two are one call on purpose. A separate `markRead` is a second thing
   * an agent has to remember to do, and the one that forgets re-reads its
   * inbox forever — so the fetch is the acknowledgement, and an agent that
   * crashes between the two loses a message it had already been handed rather
   * than looping on one it had not.
   */
  Rpc.make("MessageInbox", {
    payload: { from: Schema.String },
    success: Schema.Array(Message),
    error: NotAWorkspace,
  }),

  /**
   * Everything anyone has said, newest first — what the viewer draws.
   *
   * Read and unread alike: this is the record, not a queue. The whole list
   * rather than a page, for the reason `ThreadList` sends the whole list, and
   * with the same consequence — it is the only shape that can say a message is
   * no longer there.
   */
  Rpc.make("MessageList", {
    success: Schema.Array(Message),
  }),

  /**
   * The same list again, each time anything about a message changes.
   *
   * Sent, nudged and read are three writes by three different parties — an
   * agent's tool call, the daemon's deliverer, the recipient's fetch — and only
   * the first of them is ever something this window did. "The reply is the
   * update" was never true here for two of the three.
   */
  Rpc.make("MessageChanges", {
    success: Schema.Array(Message),
    stream: true,
  }),

  /**
   * Point the web panel somewhere, from the workspace this was asked in.
   *
   * `from` is a directory and there is no thread parameter, which is the same
   * binding every other agent-facing call has: the thread is resolved from the
   * checkout, so a conversation cannot move a page beside somebody else's
   * work. The reply is the page as recorded, so the caller is told which
   * thread it landed on rather than having to guess.
   *
   * Idempotent in the sense that matters — sending the url already showing is
   * a fresh `at`, and the window reloads. That is what "open it again" means.
   */
  Rpc.make("PageOpen", {
    payload: {
      /** A directory inside the workspace asking. A session's `startDir` will do. */
      from: Schema.String,
      url: Schema.String,
    },
    success: Page,
    error: Schema.Union([NotAWorkspace, PageRefused]),
  }),

  /**
   * Every page anybody sets, from now.
   *
   * A stream for the reason `WorkspaceFactsChanges` is one and `ThreadList` is
   * not: this changes without the window doing anything, so a client that only
   * asked would miss the one event it cares about. Nothing is replayed — a
   * navigation is an event and not a state, and the window already remembers
   * the page it last had.
   */
  Rpc.make("PageChanges", {
    success: Page,
    stream: true,
  }),

  /**
   * Write a gadget into this thread's strip.
   *
   * `source` is MDX. It is compiled here, and a document that does not compile
   * comes back as {@link GadgetRefused} carrying the compiler's own sentence.
   *
   * The reply is the {@link GadgetHead}, which tells the caller two things it
   * cannot work out for itself: which thread the gadget was filed under — the
   * panel belongs to the piece of work and not to this checkout — and what
   * title was read out of the document, which is the one part of a gadget the
   * author wrote without meaning to.
   */
  Rpc.make("GadgetShow", {
    payload: {
      /** A directory inside the workspace asking. A session's `startDir` will do. */
      from: Schema.String,
      /** {@link gadgetName}. Writing a name twice replaces what was there. */
      name: Schema.String,
      source: Schema.String,
    },
    success: GadgetHead,
    error: Schema.Union([NotAWorkspace, GadgetRefused]),
  }),

  /**
   * A thread's gadgets, newest first, without their documents.
   *
   * By thread and not by directory, because the caller is the window and the
   * window already knows which thread it is showing — the resolution from a
   * directory exists for agents, which have nothing else to name work by.
   * A thread nothing claims passes `undefined` and gets the loose bucket, the
   * same answer `gadgetAddress` gives it.
   */
  Rpc.make("GadgetList", {
    payload: { thread: Schema.optional(Schema.String) },
    success: Schema.Array(GadgetHead),
  }),

  /**
   * Every gadget written, as it is written.
   *
   * Not replayed, and it does not need to be: unlike a navigation this feed
   * says only *one more exists*, and the list a panel opens with is
   * {@link GadgetList}. A window that reconnects has missed nothing it cannot
   * ask for.
   *
   * Carries every thread's, and the window keeps the ones that are its own —
   * the same shape as {@link PageChanges}, for the same reason: a per-thread
   * subscription is a subscription that has to be torn down and rebuilt every
   * time somebody clicks a different row.
   */
  Rpc.make("GadgetChanges", {
    success: GadgetHead,
    stream: true,
  }),

  /**
   * The document at an address, for whoever has to draw it.
   *
   * Asked rather than pushed, because {@link GadgetList} is what a strip is
   * drawn from and only one of those documents is being looked at. A gadget
   * the daemon has forgotten — it holds them in memory, so a restart is
   * exactly that — is a refusal with a sentence in it, which the panel prints.
   * The alternative is an empty column that reads as a gadget that drew
   * nothing.
   */
  Rpc.make("GadgetRead", {
    payload: { address: Schema.String },
    success: Gadget,
    error: GadgetRefused,
  }),

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
       * A thread to add this workspace to, rather than a new one.
       *
       * ── one thread, several repositories ─────────────────────────────────
       *
       * A thread holds `(project, workspace)` pairs and a piece of work often
       * needs two of them — a change and the api behind it. That has always
       * been the shape of `thread_members`, and `create-workspace` has always
       * taken a thread id; what did not exist was any way to reach it. So the
       * *second* repository in a piece of work could not be created from the
       * window at all: `ThreadStart` always minted a new thread, and
       * `ThreadAttach` only moves a workspace that already exists.
       *
       * Absent is the ordinary case and means what it always did. Given, it
       * changes four things — see the handler, which is where each is
       * argued — and the shortest version is: the thread is not created, the
       * base is this project's trunk rather than a thread's bookmark, the
       * workspace takes its *sibling's* name so a thread reads as one piece
       * of work across repositories, and a lost race does not archive a
       * thread that already holds somebody's work.
       */
      thread: Schema.optional(Schema.String),
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
      /**
       * Which agent this workspace's work should live in. See {@link Face}.
       *
       * The one moment a face is chosen without a swap, and the choice does
       * two things: it decides where the `brief` step delivers, and the
       * `claim` step records it on the workspace so everything afterwards —
       * every send, and the agent column itself — follows it without asking
       * the window what it thinks.
       */
      face: Schema.optional(Face),
    },
    success: ThreadStarted,
    error: ThreadStartFailed,
  }),

  /**
   * Which of a workspace's two agents holds its work.
   *
   * ── a call and not a field on a stream ──────────────────────────────────
   *
   * Nothing changes a face on its own. It is set once when a thread is made
   * and afterwards only by {@link Rpc WorkspaceSwap}, which is a person
   * pressing a menu item in this window — so the reply to the swap is the
   * update, exactly as it is for threads. A stream would carry one event ever,
   * and a client would still have to ask for the state it started in.
   *
   * Never fails. A workspace with nothing recorded is the terminal, which is
   * what every workspace made before this existed actually is — see
   * `faces.ts`, where that default is argued rather than assumed.
   */
  Rpc.make("WorkspaceFace", {
    payload: { project: Schema.String, workspace: Schema.String },
    success: Face,
  }),

  /**
   * Move a workspace's work to the other agent, and say where it ended up.
   *
   * ── a hard swap, which is the whole point of it being a call ─────────────
   *
   * The window used to hold this as a two-state toggle in the top bar, and the
   * toggle changed which panel was *drawn* and nothing else. What that could
   * not express is the thing anybody actually wants when they press it: the
   * conversation should come with them.
   *
   *   to chat, chat empty   forks the terminal's conversation into it, so the
   *                         agent that answers knows what was already said
   *   to chat, chat has one there is already work here. Just point at it —
   *                         forking would overwrite somebody's conversation
   *   to terminal           nothing to move. The pty has been running the
   *                         whole time; there is no way to replay a
   *                         conversation into an interactive claude, and
   *                         pretending otherwise is worse than saying so
   *
   * Answers with the face it settled on rather than with nothing, so a window
   * updates from the reply. It is the same face it was asked for in every case
   * that succeeds — stated as a reply anyway, because a call whose success is
   * silence is one a client has to assume things about.
   */
  Rpc.make("WorkspaceSwap", {
    payload: { project: Schema.String, workspace: Schema.String, face: Face },
    success: Face,
    error: ChatUnavailable,
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
   * {@link ReviewQueueSource} — because one repository whose `gh` is unauthenticated
   * must not cost the others their rows.
   *
   * `refresh` asks GitHub again rather than answering from what was last read.
   * The default is the cache, because this is called every time a panel is
   * opened and `gh pr list` against a busy repository is a couple of seconds.
   */
  Rpc.make("ReviewQueueList", {
    payload: { refresh: Schema.optional(Schema.Boolean) },
    success: ReviewQueue,
  }),

  /**
   * One pull request, by project and number.
   *
   * Its own call rather than a fatter {@link Rpc ReviewQueueList}, because the two
   * are asked at different times for different reasons: the listing fills a
   * panel that is open all day, and this answers "what is this pull request"
   * for the one a workspace is about. It also answers for a merged one, which
   * the reviewQueue by definition does not.
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
       * The panel needs it for the same reason the reviewQueue does, and slightly
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
    payload: {
      project: Schema.String,
      number: Schema.Int,
      /**
       * Which agent this review should live in. See {@link Face}.
       *
       * The same field {@link Rpc ThreadStart} carries, and here for the same
       * reason: the `brief` step delivers by it and the `claim` step records
       * it, so a review started without one was briefed into a pty while the
       * chat beside it said `nothing said yet`.
       *
       * Optional, so absent still means the terminal — `faces.ts` argues that
       * default — and an older client is no worse than it was.
       */
      face: Schema.optional(Face),
    },
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
