import { Context, Data, Effect } from "effect";

// jj, as a service.
//
// The tag and the vocabulary; `jj-cli.ts` is the only file that knows there is
// a binary behind it. Same arrangement as Multiplexer and zmx, and for the same
// reason: a caller tested against a fake is a caller that can be tested at all.
//
// ── every call names its repository ────────────────────────────────────────
// There is no implicit "the current repo" anywhere on this interface. jj
// resolves a repository by walking up from the working directory, which is
// exactly the behaviour that makes a daemon dangerous: the daemon's cwd is
// wherever it was launched, and that is a real repository — this one. A command
// meant for a workspace under ~/.awp/workspaces would land here instead.
//
// So `repo` is a required argument on every method and becomes `-R`. It is the
// structural version of the zmx rule: never act on something the caller did not
// name.
//
// ── reads do not write, and that needed saying ─────────────────────────────
// `jj workspace list` snapshots the working copy before answering, because
// nearly every jj command does. A question that mutates the thing it is asking
// about is not a question. Every read here passes `--ignore-working-copy`.
//
// ── everything is safe to run twice ────────────────────────────────────────
// A job step re-runs from the first step not yet done, so a step that succeeded
// and then failed later gets called again. `addWorkspace` on a workspace that
// exists succeeds; `forgetWorkspace` on one that does not succeeds. That is not
// politeness, it is the contract the runner is built on — see the jobs package.

/** Anything jj could not do. */
export class JjError extends Data.TaggedError("JjError")<{
  /** What was being attempted, for a message a person can act on. */
  readonly op: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

export interface JjWorkspace {
  readonly name: string;
  /** The working-copy commit of that workspace. */
  readonly commitId: string;
  readonly changeId: string;
}

export interface JjBookmark {
  readonly name: string;
  /**
   * The remote this row is for, or absent for the local bookmark.
   *
   * `jj bookmark list` prints a row per local bookmark *and* per remote that
   * disagrees with it, so a name can appear more than once. A caller asking
   * "does this bookmark exist locally" has to filter on this rather than on the
   * name alone — which is a mistake this field exists to make visible.
   */
  readonly remote: string | undefined;
  /** Absent for a deleted bookmark that is still listed. */
  readonly target: string | undefined;
}

export interface AddWorkspace {
  readonly repo: string;
  /** The workspace's name in the repo, which is how it is forgotten later. */
  readonly name: string;
  /** Where it goes on disk. */
  readonly destination: string;
  /**
   * What its working copy starts from. Omitted means jj's default — the same
   * parents as the working copy of whichever workspace the command ran in,
   * which for a daemon is not a useful answer. Callers should pass one.
   */
  readonly revision?: string | undefined;
}

export class Jj extends Context.Service<
  Jj,
  {
    /** The repository root containing `dir`, for turning a path into a repo. */
    readonly root: (dir: string) => Effect.Effect<string, JjError>;

    readonly workspaces: (repo: string) => Effect.Effect<ReadonlyArray<JjWorkspace>, JjError>;

    readonly bookmarks: (repo: string) => Effect.Effect<ReadonlyArray<JjBookmark>, JjError>;

    /** Add a workspace, or do nothing if one of that name is already there. */
    readonly addWorkspace: (options: AddWorkspace) => Effect.Effect<void, JjError>;

    /**
     * Stop tracking a workspace. Does nothing if it is already untracked.
     *
     * The name is required and is checked before the command runs, because
     * `jj workspace forget` with no argument forgets **the workspace it is
     * standing in**. For a daemon that is this repository. There is no
     * arrangement of arguments in which calling it without a name is correct
     * here, so it is refused rather than guarded against.
     *
     * Forgetting does not touch the directory — that is a separate act, and the
     * job that undoes a workspace creation has to do both.
     */
    readonly forgetWorkspace: (repo: string, name: string) => Effect.Effect<void, JjError>;

    /** Create a bookmark, or move an existing one. Already idempotent in jj. */
    readonly setBookmark: (
      repo: string,
      name: string,
      revision: string,
    ) => Effect.Effect<void, JjError>;

    /** Delete a bookmark, or do nothing if it is not there. */
    readonly deleteBookmark: (repo: string, name: string) => Effect.Effect<void, JjError>;
  }
>()("awp/Jj") {}
