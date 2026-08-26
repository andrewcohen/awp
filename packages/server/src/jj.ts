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

/** One commit, as much of it as a list of them needs. */
export interface JjRevision {
  /** The stable handle. What a caller passes back as a revision. */
  readonly changeId: string;
  readonly commitId: string;
  /** The whole message, newlines and all. Trimming is the reader's business. */
  readonly description: string;
  readonly author: string;
  /** Absent when jj did not say, or said something unreadable as a date. */
  readonly authored: Date | undefined;
  /** Changes nothing. Worth saying: the top of a stack usually is one. */
  readonly empty: boolean;
  /** This is the working copy of the workspace that was asked. */
  readonly workingCopy: boolean;
  readonly bookmarks: ReadonlyArray<string>;
}

/**
 * Which commits to list, and where from.
 *
 * `dir` is a directory in a **workspace**, not a repository, and that is the
 * one thing on this interface that breaks the `repo` rule stated above. It has
 * to: `@` means the working copy of the workspace jj was pointed at, so asking
 * the repository would answer about the default workspace every time — which
 * is the one nobody is looking at.
 */
export interface RevisionsIn {
  readonly dir: string;
  /** A revset. The caller decides what a stack is; this only runs it. */
  readonly revset: string;
  /** How many, newest first. A stack against a stale trunk can be hundreds. */
  readonly limit: number;
}

/**
 * Which revision to diff, in which workspace, and whether to look at the disk.
 *
 * See {@link RevisionsIn} on why this takes a directory rather than a repo.
 */
export interface DiffOf {
  readonly dir: string;
  readonly revision: string;
  /**
   * Snapshot the working copy first, so the answer includes what is on disk.
   *
   * **The one read here that is allowed to write**, and it is not an oversight.
   * Every other read passes `--ignore-working-copy` because a question should
   * not change its subject; a diff of the working copy is the case where the
   * snapshot *is* the question. Without it, a workspace where an agent has
   * edited six files and run no jj command diffs as empty — which is exactly
   * what this view exists to show.
   *
   * False for any named revision, because history does not move and a snapshot
   * there would be a write for nothing.
   */
  readonly snapshot: boolean;
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
    /**
     * The **workspace** root containing `dir`.
     *
     * Named for what it is, because `jj root` is a shortcut for
     * `jj workspace root` and inside a secondary workspace it answers with
     * that workspace's own path. This repository is a secondary workspace, so
     * the wrong reading of this is the one you get while developing here.
     */
    readonly workspaceRoot: (dir: string) => Effect.Effect<string, JjError>;

    /**
     * The root of the repository a workspace belongs to.
     *
     * The one every method above wants as `repo`. A secondary workspace's
     * `.jj/repo` is a *file* holding a path to the real repository's `.jj/repo`
     * rather than a directory, so this reads it and walks back up:
     *
     *   ~/.awp/workspaces/alpha/work/.jj/repo
     *     → ../../../../../src/alpha/.jj/repo
     *       → /src/alpha
     *
     * For a primary workspace `.jj/repo` is a directory and the answer is the
     * workspace root itself, unchanged.
     *
     * Inherited from the Go implementation and **re-proven** 2026-08-26 against
     * this checkout, which is a secondary workspace: `jj root` gave the
     * workspace and the pointer gave the repository. jj's own help is better
     * evidence than the comment was — it says `root` is a shortcut for
     * `workspace root` outright.
     */
    readonly sourceRoot: (dir: string) => Effect.Effect<string, JjError>;

    readonly workspaces: (repo: string) => Effect.Effect<ReadonlyArray<JjWorkspace>, JjError>;

    /** The commits a revset selects, newest first. See {@link RevisionsIn}. */
    readonly revisions: (options: RevisionsIn) => Effect.Effect<ReadonlyArray<JjRevision>, JjError>;

    /**
     * One revision as a git-format patch, or an empty string if it changed
     * nothing.
     *
     * git format rather than jj's own, because what reads it is a diff
     * renderer that speaks git — and because the format is the one thing here
     * that is not allowed to be a jj-shaped invention. See {@link DiffOf} for
     * the snapshot rule, which is the interesting half.
     */
    readonly diff: (options: DiffOf) => Effect.Effect<string, JjError>;

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
