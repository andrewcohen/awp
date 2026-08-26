import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { type JobKind, type JobRef, type JobStep, permanent } from "@awp-kit/jobs";
import { type CreateWorkspace, CreateWorkspace as CreateWorkspaceSchema } from "@awp-kit/protocol";
import { Effect } from "effect";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import { identityLabels, sessionName } from "../naming";
import type { Threads } from "../threads";

// Making a workspace: the first job that does anything.
//
// Four steps, and every one of them can leave something behind — which is the
// entire reason the jobs package exists and, until now, had nothing real to
// point at.
//
//   1  workspace   jj workspace add          undo: forget it, remove it
//   2  bookmark    jj bookmark set           undo: delete it
//   3  session     zmx run -d, then labels   undo: kill it
//   4  claim       the thread takes it       undo: the thread lets it go
//
// The order is the order they depend on each other, and the claim is last on
// purpose: a workspace appears in the sidebar under its thread once it is
// claimed, so claiming first would show a half-built workspace as a finished
// one for as long as the rest took.
//
// ── the services are closed over, not asked for ────────────────────────────
// A `JobStep.run` is `Effect<void, JobError>` with no requirements, and that is
// deliberate in the jobs package: a step resumed by a restarted daemon has no
// caller to inherit a context from. So the kind is a *function* of its
// services, built once where the layers exist — see `daemon.ts`.
//
// ── every step is safe to run twice ────────────────────────────────────────
// The runner re-enters the step that failed, so the second attempt finds
// whatever the first one managed. Each of the four is idempotent underneath:
// `addWorkspace` and `forgetWorkspace` check first, `bookmark set` is
// idempotent in jj, `start` leaves an existing session alone, and a thread
// claiming a workspace it already holds changes nothing.

/**
 * The kind by name and schema, without its steps.
 *
 * All `enqueue` needs, and the reason it needs no more: the steps that run come
 * from the registry the runner was built with. A caller that only starts a job
 * therefore does not have to build the services those steps close over — which
 * is otherwise how a handler ends up constructing a jj client to enqueue
 * something.
 */
export const createWorkspaceRef: JobRef<CreateWorkspace> = {
  name: "create-workspace",
  input: CreateWorkspaceSchema,
  title: (input) => `make ${input.project}/${input.workspace}`,
};

/**
 * The two filesystem calls this job makes, and no more.
 *
 * Narrower than `FileSystem` on purpose. A step that asked for the whole thing
 * would need a fake with thirty methods to test four, and the width of that
 * fake is a lie about what the job can do — the real one satisfies this
 * structurally, so nothing is given up.
 */
export interface WorkspaceFiles {
  readonly exists: (path: string) => Effect.Effect<boolean, unknown>;
  readonly makeDirectory: (
    path: string,
    options?: { readonly recursive?: boolean | undefined },
  ) => Effect.Effect<void, unknown>;
  readonly remove: (
    path: string,
    options?: { readonly recursive?: boolean | undefined },
  ) => Effect.Effect<void, unknown>;
}

export interface WorkspaceDeps {
  readonly jj: Jj["Service"];
  readonly mux: Multiplexer["Service"];
  readonly threads: Threads["Service"];
  readonly files: WorkspaceFiles;
}

/**
 * Where a workspace goes.
 *
 * `~/.awp/workspaces/<project>/<workspace>`, which is the convention the rest
 * of awp already reads — `suggestedBy` in `multiplexer.ts` recovers a
 * workspace's identity from exactly this shape when a session has no labels.
 * Changing it here would quietly break that.
 */
export const workspacePath = (project: string, workspace: string): string =>
  join(homedir(), ".awp", "workspaces", project, workspace);

/** The kind a new workspace opens to. Matches PRIMARY in the renderer. */
const AGENT = "agent";

/**
 * Anything a service refused becomes a permanent failure.
 *
 * Retrying a refusal produces the same refusal: jj will not create a workspace
 * whose destination is occupied on the second attempt either. The runner's
 * retries exist for conditions that pass — and the one real candidate here, jj
 * declining because another operation holds the repo, is worth classifying as
 * transient the day it is actually seen rather than guessed at.
 */
const refused = (what: string) => (error: unknown) => permanent(`${what}: ${said(error)}`, error);

/**
 * The sentence a service failure carries.
 *
 * Most of them have a `reason` written for a person — jj's own words, or zmx's.
 * `ThreadNotFound` does not, because its whole content is the id, so it falls
 * through to the default rendering rather than being special-cased into a
 * sentence this file would have to keep in step with the tag.
 */
const said = (error: unknown): string =>
  typeof error === "object" && error !== null && "reason" in error
    ? String((error as { readonly reason: unknown }).reason)
    : String(error);

export const createWorkspace = (deps: WorkspaceDeps): JobKind<CreateWorkspace> => {
  const { jj, mux, threads, files } = deps;

  const agentSession = (input: CreateWorkspace): string =>
    sessionName(input.project, input.workspace, AGENT);

  const workspace: JobStep<CreateWorkspace> = {
    name: "workspace",
    run: (input, context) =>
      Effect.gen(function* () {
        const destination = workspacePath(input.project, input.workspace);

        // The parent, not the destination. `jj workspace add` creates the
        // workspace directory itself and refuses if the directory *above* it is
        // missing — "Cannot access <path>: No such file or directory" — which
        // is what the first end-to-end run of this job did, on a project that
        // had never had a workspace before. Every project's first one would
        // have failed.
        yield* files
          .makeDirectory(dirname(destination), { recursive: true })
          .pipe(Effect.mapError(refused("could not make the workspace directory")));

        yield* context.log(`making a jj workspace at ${destination}`);
        yield* jj
          .addWorkspace({
            repo: input.repo,
            name: input.workspace,
            destination,
            revision: input.base,
          })
          .pipe(Effect.mapError(refused("could not add the workspace")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        const destination = workspacePath(input.project, input.workspace);
        yield* jj
          .forgetWorkspace(input.repo, input.workspace)
          .pipe(Effect.mapError(refused("could not forget the workspace")));

        // Forgetting does not touch the directory — jj says so in its own help
        // — so the undo has to do both or it leaves a directory that the next
        // attempt cannot create into.
        //
        // Removed only when it looks like a jj workspace. A directory that is
        // not one is something this job did not make, and deleting a person's
        // files because a step failed is a far worse outcome than leaving a
        // stray directory behind.
        const ours: boolean = yield* files
          .exists(join(destination, ".jj"))
          .pipe(Effect.orElseSucceed(() => false));
        if (!ours) {
          yield* context.log(`left ${destination} alone — it is not a jj workspace`);
          return;
        }
        yield* files.remove(destination, { recursive: true }).pipe(Effect.orElseSucceed(() => {}));
        yield* context.log(`removed ${destination}`);
      }),
  };

  const bookmark: JobStep<CreateWorkspace> = {
    name: "bookmark",
    run: (input, context) =>
      Effect.gen(function* () {
        // A step that decides to do nothing is still a step. The list has to be
        // the same for every payload — the runner reads `done` back from the
        // store and resumes against it — so an optional bookmark cannot be an
        // optional *step*.
        if (input.bookmark === undefined) {
          yield* context.log("no bookmark asked for");
          return;
        }
        // `<name>@` is jj's revset for a workspace's working-copy commit. The
        // bare workspace name is not a revision — the first end-to-end run said
        // so, in jj's own words: "Revision `probe-1` doesn't exist".
        const at = `${input.workspace}@`;
        yield* context.log(`pointing ${input.bookmark} at ${at}`);
        yield* jj
          .setBookmark(input.repo, input.bookmark, at)
          .pipe(Effect.mapError(refused("could not set the bookmark")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        if (input.bookmark === undefined) {
          return;
        }
        yield* jj
          .deleteBookmark(input.repo, input.bookmark)
          .pipe(Effect.mapError(refused("could not delete the bookmark")));
        yield* context.log(`deleted ${input.bookmark}`);
      }),
  };

  const session: JobStep<CreateWorkspace> = {
    name: "session",
    run: (input, context) =>
      Effect.gen(function* () {
        const name = agentSession(input);
        yield* context.log(`starting ${name}`);
        yield* mux
          .start({
            name,
            cwd: workspacePath(input.project, input.workspace),
            command: input.agent,
          })
          .pipe(Effect.mapError(refused("could not start the session")));

        // Written after the session exists, and this is the pair that makes a
        // workspace recoverable at all: the name is shortened to fit a socket
        // path and cannot be split back into its parts, so the labels are the
        // only unshortened truth. See `identityLabels`.
        yield* mux
          .setLabels(name, identityLabels(input.project, input.workspace, AGENT))
          .pipe(Effect.mapError(refused("could not label the session")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        const name = agentSession(input);
        yield* mux.kill(name).pipe(Effect.mapError(refused("could not kill the session")));
        yield* context.log(`killed ${name}`);
      }),
  };

  const claim: JobStep<CreateWorkspace> = {
    name: "claim",
    run: (input, context) =>
      Effect.gen(function* () {
        yield* threads
          .attach(input.thread, { project: input.project, workspace: input.workspace })
          .pipe(Effect.mapError(refused("could not claim the workspace")));
        yield* context.log(`claimed by thread ${input.thread}`);
      }),
    undo: (input) =>
      threads
        .detach(input.thread, { project: input.project, workspace: input.workspace })
        .pipe(Effect.mapError(refused("could not release the workspace")), Effect.asVoid),
  };

  return {
    ...createWorkspaceRef,
    steps: [workspace, bookmark, session, claim],
    /**
     * One attempt.
     *
     * Every failure this job can have is a refusal — a name taken, a directory
     * occupied, zmx missing — and none of them pass on their own. Retrying
     * would only delay the rollback, and the rollback is the thing a person is
     * waiting for.
     */
    attempts: 1,
  };
};
