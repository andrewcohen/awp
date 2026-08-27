import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { type JobError, type JobKind, type JobRef, type JobStep, permanent } from "@awp-kit/jobs";
import { type CreateWorkspace, CreateWorkspace as CreateWorkspaceSchema } from "@awp-kit/protocol";
import { Effect } from "effect";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import { identityLabels, sessionName } from "../naming";
import type { Bootstrap } from "../bootstrap";
import type { Settings } from "../settings";
import type { Threads } from "../threads";
import { type WorkspaceIntent, nameFrom } from "../intent";

// Making a workspace: the first job that does anything.
//
// Four steps, and every one of them can leave something behind — which is the
// entire reason the jobs package exists and, until now, had nothing real to
// point at.
//
//   1  thread      check it is there        undo: remove it, if empty
//   2  name        ask a model for one       undo: none — nothing outside
//   3  workspace   jj workspace add          undo: forget it, remove it
//   4  bookmark    jj bookmark set           undo: delete it
//   5  session     zmx run -d, then labels   undo: kill it
//   6  bootstrap   the configured hooks      undo: none — the directory goes
//   7  claim       the thread takes it       undo: the thread lets it go
//   8  brief       type into the agent       undo: none — impossible
//
// `bootstrap` sits after `session` rather than straight after `workspace`, and
// the reason is what a person sees: a hook can take minutes — `bun install` on
// a cold cache — and with the session already made there is something to watch
// while it does. It stays before `brief`, because briefing an agent into a
// workspace whose dependencies are not installed yet is asking it to discover
// and fix that itself, which is the whole thing hooks exist to stop.
//
// `thread` is first so that its undo is *last*: compensation runs backwards, so
// the front of the list is the only place from which a step can ask "does this
// thread still hold anything" after everything else has let go.
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
  // The description, because that is all there is at enqueue — the name does
  // not exist until the job's first step has run. It is also the better title:
  // it is what the person asked for, in their words, rather than the slug a
  // model made of it.
  title: (input) => `${input.project} — ${input.description.trim()}`,
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
  /** Turns what a person typed into a name. See the `name` step. */
  readonly intent: WorkspaceIntent["Service"];
  /** Read per job, so editing the file takes effect without a restart. */
  readonly settings: Settings["Service"];
  /** Runs the configured bootstrap hooks. See the `bootstrap` step. */
  readonly run: Bootstrap["Service"];
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

/**
 * The name the first step recorded, or a refusal saying it did not.
 *
 * `workspace` is optional on the schema because it does not exist until `name`
 * has run, and every step after that one needs it. Checked rather than
 * asserted: the alternative is `input.workspace!`, which turns a missing name
 * into a directory called `undefined` several steps later.
 */
const named = (input: CreateWorkspace): Effect.Effect<string, JobError> =>
  input.workspace === undefined || input.workspace.trim() === ""
    ? Effect.fail(permanent("the workspace has no name — the naming step recorded none"))
    : Effect.succeed(input.workspace);

export const createWorkspace = (deps: WorkspaceDeps): JobKind<CreateWorkspace> => {
  const { jj, mux, threads, files, intent, settings, run } = deps;

  const agentSession = (project: string, workspace: string): string =>
    sessionName(project, workspace, AGENT);

  /**
   * Turn what a person typed into a workspace name, a title and a brief.
   *
   * **First, and inside the job rather than before it.** This is the ten
   * seconds: a model reads the sentence and answers with a slug. It used to
   * happen in the `ThreadStart` handler, which meant a person watched a form
   * that would not close while work with a progress panel of its own went
   * unrepresented. Now the job exists the instant it is asked for and this is
   * the step it spends its first ten seconds in.
   *
   * What it learns goes back into the input — see `JobStep.run` — so the four
   * steps after it read the name from the record, and a resumed job reads it
   * too instead of asking again and getting a second, different answer.
   *
   * No undo. Nothing outside the record changed, and the thread's title is a
   * better description of the same work either way.
   */
  /**
   * The thread this job is for — checked on the way in, removed on the way out.
   *
   * **First, so that its undo runs last.** Compensation walks the completed
   * steps backwards, so the step at the front is the one whose undo happens
   * after every other has released what it held — which is exactly when it is
   * safe to ask whether the thread still holds anything.
   *
   * The job does not create the thread: the handler does, before enqueuing,
   * because the window is given the thread back the moment it asks. But the job
   * can be the thing that takes it away, and until it was, a create that failed
   * left an empty thread in the sidebar with no way to remove one — the failure
   * was undone everywhere except the place a person was looking.
   *
   * `run` is a real check rather than a formality. A job naming a thread that
   * is not there would build a workspace nothing can claim, and would find that
   * out four steps later.
   */
  const threadStep: JobStep<CreateWorkspace> = {
    name: "thread",
    run: (input) =>
      threads.list().pipe(
        Effect.mapError(refused("could not read the threads")),
        Effect.flatMap((all) =>
          all.some((entry) => entry.id === input.thread)
            ? Effect.void
            : Effect.fail(permanent(`there is no thread ${input.thread} to build for`)),
        ),
      ),
    undo: (input, context) =>
      Effect.gen(function* () {
        // Only while it holds nothing. A thread that ended up with a workspace
        // — one this job did not make, or one a person attached by hand — is
        // the record that those checkouts are one piece of work, and no
        // rollback of this job has any business destroying it.
        const gone = yield* threads
          .deleteIfEmpty(input.thread)
          .pipe(Effect.mapError(refused("could not remove the thread")));
        yield* context.log(
          gone ? `removed the empty thread ${input.thread}` : `left thread ${input.thread} alone`,
        );
      }),
  };

  const nameStep: JobStep<CreateWorkspace> = {
    name: "name",
    run: (input, context) =>
      Effect.gen(function* () {
        // Already named: a resumed job, or a retry of a later step. Doing this
        // again would spend ten seconds to overwrite a good answer with a
        // different one, and every step after it is built on the first.
        if (input.workspace !== undefined && input.workspace.trim() !== "") {
          yield* context.log(`already named ${input.workspace}`);
          return;
        }

        yield* context.log("asking for a name");
        // ── the model is asked, and is allowed to be unreachable ─────────
        //
        // The naming call runs `claude`, takes about twelve seconds and needs
        // a network, and every one of those can be missing. What a person
        // asked for was a workspace, and refusing the whole job because the
        // *caption* could not be composed loses the work over the label on it.
        //
        // This job has one attempt, deliberately — every other failure it has
        // is a refusal that will not pass on its own. A failure here is the
        // one that might, and it is also the one nothing is lost by working
        // around, so it does not consume the attempt either way.
        //
        // The fallback is not a second namer. It makes the words a person
        // already typed into a directory name; what the model adds is reading
        // the sentence, and `nameFrom` does not pretend to.
        const resolved = yield* intent.resolve(input.description, input.project).pipe(
          Effect.catchTag("IntentError", (error) =>
            Effect.gen(function* () {
              const made = nameFrom(input.description);
              // Said out loud, at the moment it happens, in the panel already
              // open. A name that quietly differs from the one a person would
              // have got is a name they have to work out the provenance of.
              yield* context.log(`could not reach the model (${error.reason})`);
              yield* context.log(`naming it ${made.name} from the description instead`);
              return made;
            }),
          ),
        );

        const config = yield* settings.read();
        const patch = {
          workspace: resolved.name,
          label: resolved.label,
          ...(resolved.prompt === undefined ? {} : { prompt: resolved.prompt }),
          // Composed here because neither half is known earlier: the prefix is
          // configuration and the name has only just been decided. Absent
          // means no bookmark rather than an unprefixed one.
          ...(config.bookmarkPrefix === undefined
            ? {}
            : { bookmark: `${config.bookmarkPrefix}/${resolved.name}` }),
        };
        yield* context.log(`named ${resolved.name}`);

        // The thread was made with the raw sentence as its title, so the
        // sidebar had something to show immediately. This is the better one.
        // Failure here is not worth losing the job over — the title is a label,
        // and the workspace is the work.
        yield* threads.rename(input.thread, resolved.label).pipe(Effect.ignore);

        return patch;
      }),
  };

  const workspaceStep: JobStep<CreateWorkspace> = {
    name: "workspace",
    run: (input, context) =>
      Effect.gen(function* () {
        const workspace = yield* named(input);
        const destination = workspacePath(input.project, workspace);

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
            name: workspace,
            destination,
            revision: input.base,
          })
          .pipe(Effect.mapError(refused("could not add the workspace")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        const workspace = yield* named(input);
        const destination = workspacePath(input.project, workspace);
        yield* jj
          .forgetWorkspace(input.repo, workspace)
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

  const bookmarkStep: JobStep<CreateWorkspace> = {
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
        const at = `${yield* named(input)}@`;
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

  const sessionStep: JobStep<CreateWorkspace> = {
    name: "session",
    run: (input, context) =>
      Effect.gen(function* () {
        const workspace = yield* named(input);
        const name = agentSession(input.project, workspace);
        yield* context.log(`starting ${name}`);
        yield* mux
          .start({
            name,
            cwd: workspacePath(input.project, workspace),
            command: input.agent,
          })
          .pipe(Effect.mapError(refused("could not start the session")));

        // Written after the session exists, and this is the pair that makes a
        // workspace recoverable at all: the name is shortened to fit a socket
        // path and cannot be split back into its parts, so the labels are the
        // only unshortened truth. See `identityLabels`.
        yield* mux
          .setLabels(name, identityLabels(input.project, workspace, AGENT, input.label))
          .pipe(Effect.mapError(refused("could not label the session")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        const name = agentSession(input.project, yield* named(input));
        yield* mux.kill(name).pipe(Effect.mapError(refused("could not kill the session")));
        yield* context.log(`killed ${name}`);
      }),
  };

  const bootstrapStep: JobStep<CreateWorkspace> = {
    name: "bootstrap",
    run: (input, context) =>
      Effect.gen(function* () {
        const workspace = yield* named(input);
        const cwd = workspacePath(input.project, workspace);
        // Read per job, like every other setting here, so editing the config
        // takes effect on the next workspace rather than the next daemon.
        //
        // **From the source repository, not the new workspace.** `.awp/` is
        // untracked, so a fresh `jj workspace add` has no copy of it — the Go
        // implementation symlinked one in for exactly this reason. `input.repo`
        // is the repository this workspace was made from, which is where the
        // project's own hooks actually live.
        const { bootstrap: hooks } = yield* settings.read(input.repo);

        if (hooks.length === 0) {
          // A step that does nothing is still a step — the runner reads `done`
          // back from the store and resumes against the kind's list, so a list
          // that varied by configuration is a list a restarted daemon could not
          // reproduce. Silent, too: "no hooks configured" in every job's log is
          // a line that teaches the eye to skip the log.
          return;
        }

        for (const command of hooks) {
          yield* context.log(`$ ${command}`);
          const output = yield* run
            .run({ command, cwd })
            .pipe(Effect.mapError(refused("a bootstrap hook failed")));
          const tail = output.trim().split("\n").slice(-3).join("\n").trim();
          if (tail !== "") {
            yield* context.log(tail);
          }
        }
      }),
    // No undo, and it needs none: everything a hook wrote is inside the
    // workspace directory, which the `workspace` step's undo removes. A hook
    // that reached outside it — writing to a shared cache, starting something
    // — is beyond what this job can reason about, and inventing an undo that
    // pretended otherwise would be worse than saying so here.
  };

  const briefStep: JobStep<CreateWorkspace> = {
    name: "brief",
    run: (input, context) =>
      Effect.gen(function* () {
        if (input.prompt === undefined || input.prompt.trim() === "") {
          yield* context.log("nothing to tell the agent");
          return;
        }
        yield* context.log("telling the agent what to do");
        yield* mux
          .send(agentSession(input.project, yield* named(input)), input.prompt)
          .pipe(Effect.mapError(refused("could not brief the agent")));
      }),
    // No undo, and none is possible: there is no way to un-type something into
    // a terminal. That is why this is **last** — nothing after it can fail and
    // send the runner back through a step that cannot be run twice safely.
  };

  const claimStep: JobStep<CreateWorkspace> = {
    name: "claim",
    run: (input, context) =>
      Effect.gen(function* () {
        yield* threads
          .attach(input.thread, { project: input.project, workspace: yield* named(input) })
          .pipe(Effect.mapError(refused("could not claim the workspace")));
        yield* context.log(`claimed by thread ${input.thread}`);
      }),
    undo: (input) =>
      named(input).pipe(
        Effect.flatMap((workspace) =>
          threads.detach(input.thread, { project: input.project, workspace }),
        ),
        Effect.mapError(refused("could not release the workspace")),
        Effect.asVoid,
      ),
  };

  return {
    ...createWorkspaceRef,
    // `brief` last, and `claim` before it. Sending text into a terminal cannot
    // be undone or safely repeated, so it goes after everything that might
    // fail — see the note on the step.
    // Named `…Step` so the locals inside them can keep the words that matter —
    // `workspace` is the name of a workspace far more often than it is the name
    // of a step.
    steps: [
      threadStep,
      nameStep,
      workspaceStep,
      bookmarkStep,
      sessionStep,
      bootstrapStep,
      claimStep,
      briefStep,
    ],
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
