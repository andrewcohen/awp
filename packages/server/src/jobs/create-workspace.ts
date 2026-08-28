import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { type JobError, type JobKind, type JobRef, type JobStep, permanent } from "@awp-kit/jobs";
import { type CreateWorkspace, CreateWorkspace as CreateWorkspaceSchema } from "@awp-kit/protocol";
import { Effect } from "effect";
import type { Github } from "../github";
import type { Jj } from "../jj";
import type { Multiplexer } from "../multiplexer";
import { trustWorkspace, untrustWorkspace } from "../claude-trust";
import { identityLabels, sessionName } from "../naming";
import type { Bootstrap } from "../bootstrap";
import type { Settings } from "../settings";
import type { Threads } from "../threads";
import { type WorkspaceIntent, nameFrom } from "../intent";
import { fetchHead } from "../review-head";

// Making a workspace: the first job that does anything.
//
// Nine steps, and nearly every one of them can leave something behind — which
// is the entire reason the jobs package exists and, until now, had nothing real
// to point at.
//
//   1  thread      check it is there        undo: remove it, if empty
//   2  name        ask a model for one       undo: none — nothing outside
//   3  fetch       a review's base, if any   undo: none — refs are additive
//   4  workspace   jj workspace add          undo: forget it, remove it
//   5  bookmark    jj bookmark set           undo: delete it
//   6  session     zmx run -d, then labels   undo: kill it
//   7  bootstrap   the configured hooks      undo: none — the directory goes
//   8  claim       the thread takes it       undo: the thread lets it go
//   9  brief       type into the agent       undo: none — impossible
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
// whatever the first one managed. Each of them is idempotent underneath:
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
  /**
   * Only for a review, and only for a fork. See the `fetch` step — a fork's
   * head branch is not on `origin`, so `jj git fetch` does not bring it down.
   */
  readonly github: Github["Service"];
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

/**
 * Resolve the placeholders a hook line may carry.
 *
 * ── the bug this exists for ───────────────────────────────────────────────
 *
 * A hook is handed to `sh -c` whole, and the Go implementation substituted
 * `<root>` first — `strings.ReplaceAll(raw, "<root>", root)`. amoeba did not,
 * so a real project's first hook reached the shell untouched:
 *
 *   cp <root>/.env .env
 *
 * `<` is a redirect. sh read that as `cp` with its input redirected from a
 * file called `root`, and said so:
 *
 *   sh: root: No such file or directory
 *
 * Which names neither the hook, the placeholder, nor the repository — it is
 * the shell answering a question nobody asked. And because a failing hook
 * fails the job, a configuration written for the Go implementation took the
 * whole workspace back out.
 *
 * The angle brackets are why it had to be found rather than noticed: an
 * unresolved placeholder in any other spelling would have been passed through
 * as a literal and shown up in the error. This one turned into syntax.
 *
 * ── what it resolves to ───────────────────────────────────────────────────
 *
 * The **source repository**, not the new workspace. That is the whole point of
 * the placeholder: `.env` and `.shopify` are untracked, so they exist in the
 * repository the workspace was made from and nowhere else. A hook copying them
 * from the workspace would copy nothing, succeed, and leave an agent without
 * its environment.
 *
 * Not quoted, deliberately. The line is a shell line and the person writing it
 * owns its quoting — putting quotes on would break `cp <root>/a <root>/b` and
 * every glob. A path with a space in it is theirs to quote, as it already is
 * for every other word on the line.
 */
export const expandHook = (command: string, repo: string): string =>
  command.replaceAll("<root>", repo);

export const createWorkspace = (deps: WorkspaceDeps): JobKind<CreateWorkspace> => {
  const { jj, mux, threads, files, intent, settings, run, github } = deps;

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
   * ── and then nothing could be retried ─────────────────────────────────────
   *
   * `run` used to be a check and only a check: the thread is there, or the job
   * is refused. Put beside an undo that *removes* the thread, that made every
   * rolled-back create permanently unretryable —
   *
   *   attempt   handler makes the thread → … → a later step fails
   *   rollback  walks back, and the last undo deletes the thread
   *   retry     `done` was emptied, so this step runs first — and refuses,
   *             naming a thread that existed until the rollback removed it
   *
   * — and the retry button reported it as "there is no thread to build for",
   * which reads as a job asking for something that was never there rather than
   * as the rollback having taken it. The general shape is worth keeping:
   * **compensation has to leave the world in a state `run` can be re-entered
   * from.** An undo that destroys its own step's precondition does not.
   *
   * So `run` ensures rather than asserts. It still refuses nothing silently —
   * a restored thread is logged, because a thread reappearing in the sidebar
   * with no explanation is its own small mystery.
   *
   * The title is `label ?? description`, which is exactly what the thread had:
   * the handler titles it with the description, and the `name` step renames it
   * to the label. On a retry both are already on the record, so the restored
   * thread comes back with the better of the two.
   */
  const threadStep: JobStep<CreateWorkspace> = {
    name: "thread",
    run: (input, context) =>
      threads.list().pipe(
        Effect.mapError(refused("could not read the threads")),
        Effect.flatMap((all) =>
          all.some((entry) => entry.id === input.thread)
            ? Effect.void
            : threads
                .restore(
                  input.thread,
                  input.label !== undefined && input.label.trim() !== ""
                    ? input.label
                    : input.description,
                  input.threadParent,
                  // Part of what the thread was. A review whose rollback took
                  // the thread and whose retry put it back without the link
                  // would leave the inbox row unable to find the work being
                  // done for it — which is the same failure this step exists to
                  // prevent for the thread itself.
                  input.review === undefined
                    ? undefined
                    : { project: input.project, number: input.review.number },
                )
                .pipe(
                  Effect.mapError(refused("could not restore the thread")),
                  Effect.flatMap((back) =>
                    back
                      ? context.log(`put back the thread ${input.thread}, removed by a rollback`)
                      : Effect.void,
                  ),
                ),
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
        const resolved = yield* intent.resolve(input.description, input.project, input.repo).pipe(
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

  /**
   * Make the base revision exist, and say what it turned out to be.
   *
   * A no-op for ordinary work, and the whole of what a review needs before a
   * workspace can be made: a pull request's head is a branch on a remote, and
   * `jj workspace add -r <branch>` against a repository that has not fetched
   * says the revision does not exist — one step in, in a message about
   * revisions rather than about GitHub.
   *
   * ── it patches `base`, and that is what the patch mechanism is for ────────
   *
   * The handler enqueues a *branch name*, which is not a revision. Which revset
   * that branch is depends on what the fetch produced, and only this step is
   * standing there when it does:
   *
   *   fetched from origin   `feature@origin` — a remote bookmark, because jj
   *                         does not track fetched branches locally by default
   *   fetched from a fork   `feature` — written to refs/heads by git, so it is
   *                         a local bookmark once jj has imported the refs
   *
   * **The remote one wins when both exist**, and that is deliberate: a local
   * bookmark of the same name is somebody's own copy, which may be behind the
   * pull request. Reviewing a stale branch is worse than not reviewing, because
   * nothing about it says so.
   *
   * No undo. A fetch adds refs to a repository and takes nothing away, and
   * un-fetching them would be undoing something a person's own `jj git fetch`
   * does daily.
   */
  const fetchStep: JobStep<CreateWorkspace> = {
    name: "fetch",
    run: (input, context) =>
      Effect.gen(function* () {
        const review = input.review;
        if (review === undefined) {
          // Silent, like the bookmark step's own no-op: "nothing to fetch" in
          // every ordinary job's log is a line that teaches the eye to skip it.
          return;
        }

        yield* context.log("fetching from the git remotes");
        if (review.fork !== undefined) {
          yield* context.log(
            `fetching ${review.fork.owner}/${review.fork.repo} ${review.headRef} — the head is on a fork`,
          );
        }
        // Shared with `WorkspaceRepair`, which asks the same three questions
        // when a pull request has moved since the workspace was made. See
        // `review-head.ts`.
        const base = yield* fetchHead(jj, github, { repo: input.repo, review }).pipe(
          // By tag, not by reading one: `catchTag` narrows, and the two failures
          // want different words — a head that is not there after a fetch is a
          // refusal about this pull request, and a jj that would not run is a
          // refusal about jj.
          Effect.catchTag("HeadMissing", (error) => Effect.fail(permanent(error.reason))),
          Effect.mapError(refused("could not resolve the pull request's head")),
        );
        yield* context.log(`reviewing #${review.number} from ${base}`);
        return { base };
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

  // ── trust, between the directory and the agent ──────────────────────────
  //
  // Claude Code stops at a prompt in a directory it has not seen, and every
  // workspace here is a fresh one — so every agent stopped there. It became an
  // *exit* rather than a hang once `send` started delivering a real Return: the
  // brief arrives, the Return lands on the highlighted option, and that option
  // is "No, exit".
  //
  // After `workspace` because the directory has to exist, and before `session`
  // because the agent has to be trusted before it launches. See claude-trust.ts
  // for why this touches somebody else's file so carefully.
  const trustStep: JobStep<CreateWorkspace> = {
    name: "trust",
    run: (input, context) =>
      Effect.gen(function* () {
        const dir = workspacePath(input.project, yield* named(input));
        const changed = yield* trustWorkspace(dir).pipe(
          Effect.mapError(refused("could not mark the workspace trusted")),
        );
        // Said only when it did something. "already trusted" and "no Claude
        // Code config here" are both ordinary and neither is worth a line in
        // every job's log.
        if (changed) {
          yield* context.log("trusted the workspace with Claude Code");
        }
      }),
    undo: (input) =>
      Effect.gen(function* () {
        const dir = workspacePath(input.project, yield* named(input));
        yield* untrustWorkspace(dir).pipe(
          Effect.mapError(refused("could not remove the trust entry")),
        );
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
            // What the status hooks read. A person's Claude Code settings run
            // `awp internal report-status` on PreToolUse, Stop and
            // AskUserQuestion, and every one of those is gated on
            // `$AWP_WORKSPACE` or `$TMUX` — so an agent started without them
            // reports nothing at all.
            //
            // **Necessary and not sufficient**, which was worth finding out
            // before believing this fixed anything. `writeWorkspaceStatus` in
            // the archive reads the entry and gives up when there is not one:
            //
            //   entry, ok := entries[name]
            //   if !ok { return entries }      // ← writes nothing
            //
            // So a workspace with no row in `~/.awp/workspace-state.json` never
            // gains one by reporting, and amoeba writes no row. These two
            // variables make an amoeba workspace report *once something has
            // created its row* — which is the Go implementation's job today and
            // will be ACP's tomorrow. They cost nothing and are right either
            // way; see the note in `workspace-state.ts` on ranked sources.
            //
            // The repo root and not the workspace directory: the state file is
            // keyed by the repository.
            env: { AWP_WORKSPACE: workspace, AWP_REPO_ROOT: input.repo },
          })
          .pipe(Effect.mapError(refused("could not start the session")));
      }),
    undo: (input, context) =>
      Effect.gen(function* () {
        const name = agentSession(input.project, yield* named(input));
        yield* mux.kill(name).pipe(Effect.mapError(refused("could not kill the session")));
        yield* context.log(`killed ${name}`);
      }),
  };

  /**
   * The labels, as a step of their own.
   *
   * **Its own step because a step's undo only runs once the step finished**,
   * and these two used to be one. `start` succeeded, `setLabels` refused a
   * label with a colon in it, and the step failed — so `session` never entered
   * `done`, so its undo never ran, so the rollback removed the workspace
   * directory out from under a shell that was still sitting in it:
   *
   *   The current directory no longer exists (it was deleted or moved).
   *   Start Claude Code from an existing directory.
   *
   * A session nothing would ever kill, in a directory that no longer existed,
   * left behind by a rollback that reported itself clean. The general shape is
   * worth stating: **a step may make at most one externally visible change**,
   * because a change made before the failure is a change with no undo
   * registered for it. Two acts, two steps.
   *
   * No undo of its own. Labels live and die with the session, so killing it —
   * which is the step before this one's undo — takes them with it.
   *
   * What they are for: the name is shortened to fit a socket path and cannot
   * be split back into its parts, so the labels are the only unshortened
   * truth. See `identityLabels`.
   */
  const labelsStep: JobStep<CreateWorkspace> = {
    name: "labels",
    run: (input) =>
      Effect.gen(function* () {
        const workspace = yield* named(input);
        const name = agentSession(input.project, workspace);
        yield* mux
          .setLabels(name, identityLabels(input.project, workspace, AGENT, input.label))
          .pipe(Effect.mapError(refused("could not label the session")));
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

        for (const raw of hooks) {
          const command = expandHook(raw, input.repo);
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
      // Before `workspace`, because it is what makes `base` name something jj
      // can find. A no-op for everything that is not a review.
      fetchStep,
      workspaceStep,
      bookmarkStep,
      trustStep,
      sessionStep,
      labelsStep,
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
