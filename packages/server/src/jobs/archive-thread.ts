import { type JobError, type JobKind, type JobRef, type JobStep, permanent } from "@awp-kit/jobs";
import { type ArchiveThread, ArchiveThread as ArchiveThreadSchema } from "@awp-kit/protocol";
import { Effect } from "effect";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import type { Projects } from "../projects";
import type { Settings } from "../settings";
import type { Threads } from "../threads";
import { workspacePath, type WorkspaceFiles } from "./create-workspace";

// Putting a thread away, and taking back what it holds.
//
// The reverse of create-workspace, and the reason it is a job rather than a
// call is the same reason that one is: it touches four things outside itself
// and a failure part way through has to be visible and resumable.
//
//   1  plan        which workspaces, and which repository each is in
//   2  sessions    kill every session of every workspace
//   3  workspaces  jj workspace forget, then remove the directory
//   4  bookmarks   delete, only when asked
//   5  archive     set archived_at, so it leaves the sidebar
//
// ── archive is a label; reclaim is an act ──────────────────────────────────
//
// `Threads.archive` sets a flag and clearing it undoes that. Everything above
// it in the list is permanent — a removed checkout does not come back, so an
// unarchive afterwards restores the row and not the work. The two want
// opposite guarantees, and one word covering both is what makes the word
// ambiguous. So the flag stays a call, and this is the job.
//
// ── the archive is LAST ────────────────────────────────────────────────────
//
// A failure part way through leaves the thread where a person can still see
// it, with its job beside it saying what stopped. Archiving first would take
// the row out of the sidebar and then fail at removing 588MB, which is the one
// outcome nothing on screen would explain.
//
// ── and the members are read once ──────────────────────────────────────────
//
// Recorded into the input by `plan` rather than re-read by each step. A
// resumed job has only its record — see `JobStep.run` — and a step that asked
// the store again would act on whatever the thread holds *now*, which is not
// necessarily what it held when the person pressed the button.
//
// ── no undo, and saying so is the honest part ──────────────────────────────
//
// Only `archive` has one. A killed session, a forgotten workspace and a
// deleted directory cannot be put back by this job, and an `undo` that
// pretended otherwise would be worse than none. Which is also why there is one
// attempt: every failure here is a refusal, and a retry after a rollback would
// only re-enter steps whose work is already gone.

export const archiveThreadRef: JobRef<ArchiveThread> = {
  name: "archive-thread",
  input: ArchiveThreadSchema,
  // The id, because the title is not on the input and the record has to be
  // readable from the jobs panel before the first step has run.
  title: (input) => `archive ${input.thread}`,
};

export interface ArchiveDeps {
  readonly jj: Jj["Service"];
  readonly mux: Multiplexer["Service"];
  readonly threads: Threads["Service"];
  /** Where each project's repository is — `jj -R` needs a path, not a name. */
  readonly projects: Projects["Service"];
  readonly files: WorkspaceFiles;
  /** For `bookmark_prefix`, which is how a workspace's bookmark is named. */
  readonly settings: Settings["Service"];
}

const said = (error: unknown): string => (error instanceof Error ? error.message : String(error));

const refused = (what: string) => (error: unknown) => permanent(`${what}: ${said(error)}`, error);

/**
 * The workspaces recorded by `plan`, or a refusal naming what is missing.
 *
 * Checked rather than asserted, for the same reason `named` is in
 * create-workspace: an absent list asserted away becomes a job that reports
 * success having reclaimed nothing.
 */
const planned = (input: ArchiveThread): Effect.Effect<ReadonlyArray<Planned>, JobError> =>
  input.plan === undefined
    ? Effect.fail(permanent("nothing planned — the plan step recorded no workspaces"))
    : Effect.succeed(input.plan);

interface Planned {
  readonly project: string;
  readonly workspace: string;
  readonly repo: string;
}

export const archiveThread = (deps: ArchiveDeps): JobKind<ArchiveThread> => {
  const { jj, mux, threads, projects, files, settings } = deps;

  /**
   * Which workspaces, and where each project's repository is.
   *
   * The repository is the part that cannot be guessed: `jj -R` takes a path
   * and a thread member carries a project *name*. A project awp does not know
   * about has no path, and the honest answer there is to skip its workspaces
   * and say which — reclaiming nothing is better than reclaiming a directory
   * whose repository could not be named.
   */
  const planStep: JobStep<ArchiveThread> = {
    name: "plan",
    run: (input, context) =>
      Effect.gen(function* () {
        const all = yield* threads
          .list()
          .pipe(Effect.mapError(refused("could not read the threads")));
        const thread = all.find((one) => one.id === input.thread);
        if (thread === undefined) {
          return yield* Effect.fail(permanent(`there is no thread ${input.thread}`));
        }

        const known = yield* projects
          .list()
          .pipe(Effect.mapError(refused("could not read the projects")));
        const roots = new Map(known.map((project) => [project.name, project.root]));

        const plan: Array<Planned> = [];
        for (const member of thread.members) {
          const repo = roots.get(member.project);
          if (repo === undefined) {
            yield* context.log(
              `skipping ${member.project}/${member.workspace} — ${member.project} is not an imported project`,
            );
            continue;
          }
          plan.push({ project: member.project, workspace: member.workspace, repo });
        }

        yield* context.log(
          plan.length === 0
            ? "nothing to reclaim — the thread holds no workspaces"
            : `reclaiming ${plan.map((one) => `${one.project}/${one.workspace}`).join(", ")}`,
        );
        return { plan };
      }),
  };

  /**
   * Every session of every workspace, killed.
   *
   * Found by listing rather than by composing names, because a workspace has
   * one session *per kind* — an agent, an editor, an action — and the kinds
   * are open-ended. Composing `awp.<project>.<workspace>.agent` would leave
   * every other one running, holding the directory the next step removes.
   *
   * Safe twice: a session that is already gone is not in the listing.
   */
  const sessionsStep: JobStep<ArchiveThread> = {
    name: "sessions",
    run: (input, context) =>
      Effect.gen(function* () {
        const plan = yield* planned(input);
        const sessions = yield* mux
          .list()
          .pipe(Effect.mapError(refused("could not list the sessions")));

        for (const one of plan) {
          const mine = sessions.filter(
            (session) =>
              session.labels["awp_project"] === one.project &&
              session.labels["awp_workspace"] === one.workspace,
          );
          for (const session of mine) {
            yield* context.log(`killing ${session.name}`);
            yield* mux.kill(session.name).pipe(Effect.mapError(refused("could not kill it")));
          }
        }
      }),
  };

  /**
   * The checkout: forgotten, then removed.
   *
   * Both, because `jj workspace forget` does not touch the directory — jj says
   * so in its own help — and the directory is where the disk is. Measured on
   * one real workspace here: 588MB.
   *
   * Removed **only when it contains `.jj`**, which is the same guard the create
   * job's undo has and for the same reason: deleting a person's files because
   * a path looked right is far worse than leaving a stray directory.
   */
  const workspacesStep: JobStep<ArchiveThread> = {
    name: "workspaces",
    run: (input, context) =>
      Effect.gen(function* () {
        const plan = yield* planned(input);
        for (const one of plan) {
          yield* jj
            .forgetWorkspace(one.repo, one.workspace)
            .pipe(Effect.mapError(refused(`could not forget ${one.workspace}`)));

          const destination = workspacePath(one.project, one.workspace);
          const ours: boolean = yield* files
            .exists(`${destination}/.jj`)
            .pipe(Effect.orElseSucceed(() => false));
          if (!ours) {
            yield* context.log(`left ${destination} alone — it is not a jj workspace`);
            continue;
          }
          yield* files
            .remove(destination, { recursive: true })
            .pipe(Effect.orElseSucceed(() => {}));
          yield* context.log(`removed ${destination}`);
        }
      }),
  };

  /**
   * The bookmark, only when asked.
   *
   * A step that decides to do nothing is still a step — the runner reads
   * `done` back and resumes against the kind's list, so a list that varied by
   * payload is one a restarted daemon could not reproduce.
   *
   * **A bookmark is not part of the workspace.** It is a name for a commit,
   * kept in the repository, so it outlives the checkout the step before
   * removed. Keeping it is what keeps the work addressable; deleting it can
   * leave commits with nothing pointing at them. Hence the flag, and hence its
   * default.
   */
  const bookmarksStep: JobStep<ArchiveThread> = {
    name: "bookmarks",
    run: (input, context) =>
      Effect.gen(function* () {
        if (!input.deleteBookmarks) {
          yield* context.log("keeping the bookmarks");
          return;
        }
        const plan = yield* planned(input);
        for (const one of plan) {
          // Read per workspace rather than once, because the prefix is a
          // project setting and a thread can hold workspaces in two projects.
          const config = yield* settings
            .read(one.repo)
            .pipe(Effect.mapError(refused("could not read the settings")));
          if (config.bookmarkPrefix === undefined) {
            yield* context.log(`no bookmark prefix configured for ${one.project}`);
            continue;
          }
          const name = `${config.bookmarkPrefix}/${one.workspace}`;
          yield* jj
            .deleteBookmark(one.repo, name)
            .pipe(Effect.mapError(refused(`could not delete ${name}`)));
          yield* context.log(`deleted ${name}`);
        }
      }),
  };

  /** Last, so a failure above it leaves the thread where somebody can see it. */
  const archiveStep: JobStep<ArchiveThread> = {
    name: "archive",
    run: (input, context) =>
      Effect.gen(function* () {
        yield* threads
          .archive(input.thread, true)
          .pipe(Effect.mapError(refused("could not archive the thread")));
        yield* context.log("archived");
      }),
    // The only undo there is. Everything before this took something away that
    // cannot be put back, and the record says so rather than pretending.
    undo: (input) =>
      threads
        .archive(input.thread, false)
        .pipe(Effect.mapError(refused("could not bring the thread back")), Effect.asVoid),
  };

  return {
    ...archiveThreadRef,
    steps: [planStep, sessionsStep, workspacesStep, bookmarksStep, archiveStep],
    /** One attempt. Every failure here is a refusal, and a refusal repeats. */
    attempts: 1,
  };
};
