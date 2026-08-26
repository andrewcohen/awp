// The contract, implemented over the services.
//
// Deliberately thin, and the thinness is the point: everything here is either a
// call into a service or a translation between the daemon's vocabulary and the
// client's. Anything that needed a decision would belong in a service, where it
// could be tested without an rpc around it.
//
// Kept apart from the transport for the same reason. These handlers are a
// Layer, so `RpcTest` can drive them with no socket at all — which is how the
// contract's own tests run, and how these do.

import { Jobs } from "@awp-kit/jobs";
import {
  AttachRefused,
  AwpRpcs,
  DiffUnavailable,
  JobNotFound,
  SessionNotFound,
  ThreadStartFailed,
  type SessionIdentity,
  type SessionInfo,
} from "@awp-kit/protocol";
import { Effect, Stream } from "effect";
import { Jj } from "./jj";
import { createWorkspaceRef } from "./jobs/create-workspace";
import { Settings, agentWith } from "./settings";
import { localBookmarks } from "./jj-parse";
import { Threads } from "./threads";
import { refusalFor } from "./attachment";
import { Multiplexer, type Session, identities } from "./multiplexer";
import { currentZmxSession } from "./zmx-session";
import { Sessions } from "./sessions";

/**
 * The daemon's `Session` as the client is promised it.
 *
 * A function and not a cast. The two types are allowed to drift — one is what
 * zmx reports and the other is what a client is owed — and this is the single
 * place that would stop compiling when they do.
 */
const toWire = (
  session: Session,
  ownSession: string | undefined,
  identity: SessionIdentity | undefined,
): SessionInfo => ({
  name: session.name,
  pid: session.pid,
  clients: session.clients,
  startDir: session.startDir,
  ended: session.ended,
  exitCode: session.exitCode,
  created: session.created,
  cmd: session.cmd,
  labels: session.labels,
  identity,
  refusal: refusalFor(session, session.name, ownSession),
});

/**
 * The project's main line.
 *
 * jj resolves it through the remote's default bookmark and then main, master,
 * trunk — so the usual answer needs no configuration and no guessing here.
 */
const TRUNK = "trunk()";

/**
 * What a diff panel calls a stack: the working copy, and everything since the
 * main line.
 *
 * `@` is named separately rather than left to `trunk()..@` to cover the case
 * where the working copy *is* on trunk — a fresh workspace with nothing done
 * in it yet. That is a stack of one empty commit, which is the honest answer,
 * and it is not the same as an empty list.
 */
const STACK = "@ | trunk()..@";

/**
 * The fallback when the revset above will not resolve.
 *
 * `trunk()` is a revset alias with a definition that depends on the
 * repository — the remote's default bookmark, then main, then master, then
 * `root()` — and jj refuses the *whole* expression when it cannot settle on
 * one, as it does when the name it lands on is conflicted. A panel that showed
 * an error there would be showing an error about a repository whose diff it
 * can read perfectly well, so the second attempt drops the part that needed a
 * trunk and keeps the row that matters.
 *
 * When this fails too, the directory is not a repository, and *that* is worth
 * reporting — which is what happens, because nothing catches the second one.
 */
const NO_TRUNK = "@";

/** How many commits a listing hands back unless the client asks for fewer. */
const STACK_LIMIT = 50;

/**
 * The revision that means "the files as they are on disk right now".
 *
 * The literal `@` and not a change id, deliberately — see `Diff` in the
 * contract. It is also what the reply echoes, so a client can tell the live
 * answer apart from a historical one it asked for by name.
 */
const WORKING_COPY = "@";

/**
 * The workspace a bookmark names, if it names one of awp's.
 *
 * Convention, not a lookup: `create-workspace` composes a bookmark as
 * `<prefix>/<workspace>`, so the prefix comes back off. It is a guess in
 * exactly one direction — a person's own `andrew/some-branch` looks the
 * same and will be reported as a workspace called `some-branch`. That is
 * harmless: the field decides which entry is preselected and which thread
 * gets recorded as the parent, and being wrong means neither happens.
 */
const workspaceOf = (bookmark: string, prefix: string | undefined): string | undefined =>
  prefix !== undefined && bookmark.startsWith(`${prefix}/`)
    ? bookmark.slice(prefix.length + 1)
    : undefined;

export const layer = AwpRpcs.toLayer(
  Effect.gen(function* () {
    const mux = yield* Multiplexer;
    const sessions = yield* Sessions;
    const jobs = yield* Jobs;
    const threads = yield* Threads;
    const config = yield* Settings;
    const jj = yield* Jj;

    /**
     * The revision a thread's work is at, for a thread branching off it.
     *
     * **The bookmark, not the working copy**, and that is the whole point of
     * this function. `<name>@` is jj's revset for a workspace's working-copy
     * commit, which carries whatever is half-finished in it *right now* — so a
     * thread based on it inherits someone's uncommitted edits, which is not
     * what "branch off this work" means. The bookmark is where the work is
     * named, and it moves when a person decides it should:
     *
     *   andrew/tiered-discounts   the branch, moved deliberately     ← this
     *   tiered-discounts@         the working copy, moving constantly
     *
     * The fallback to the working copy is deliberate rather than a failure. A
     * thread created with no `bookmark_prefix` configured has no bookmark at
     * all, and refusing there would make the feature unavailable to anyone who
     * has not set one. Starting from the working copy is worse; having nothing
     * to start from is worse still.
     *
     * Only the first member is consulted. A thread can hold several workspaces
     * and there is no rule yet for which of them a child should follow; the
     * first is the one it was created with, which is the only one this can
     * claim to know something about.
     *
     * `repo` is the repository the *new* workspace is being made in, and the
     * parent's workspace has to live in it — a revision is only meaningful
     * inside one repository. Refused rather than resolved across projects,
     * which would produce a revset jj cannot find and a failure one backoff
     * later inside the job.
     */
    /** The thread holding this workspace, if any does. */
    const threadOwning = (workspace: string | undefined, project: string) =>
      workspace === undefined
        ? Effect.succeed(undefined)
        : threads.list().pipe(
            Effect.orDie,
            Effect.map(
              (all) =>
                all.find(
                  (thread) =>
                    thread.archivedAt === undefined &&
                    thread.members.some(
                      (member) => member.project === project && member.workspace === workspace,
                    ),
                )?.id,
            ),
          );

    const baseOfThread = (id: string, project: string, repo: string) =>
      Effect.gen(function* () {
        const found = yield* threads.list().pipe(Effect.orDie);
        const thread = found.find((entry) => entry.id === id);
        const member = thread?.members[0];
        if (member === undefined) {
          // A thread with no workspace yet has no work to branch from. Its own
          // sentence rather than a silent fall back to trunk, which would put
          // the new thread somewhere the person did not ask for and say
          // nothing about it.
          return yield* Effect.fail(
            new ThreadStartFailed({
              reason:
                thread === undefined
                  ? `no thread ${id} to start from`
                  : `"${thread.title}" has no workspace yet, so there is nothing to start from`,
            }),
          );
        }

        if (member.project !== project) {
          return yield* Effect.fail(
            new ThreadStartFailed({
              reason: `"${thread?.title ?? id}" is in ${member.project}, so ${project} cannot start from it`,
            }),
          );
        }

        const settings = yield* config.read();
        const prefix = settings.bookmarkPrefix;
        if (prefix === undefined) {
          return `${member.workspace}@`;
        }

        // Asked rather than assumed. The prefix says what awp *would* have
        // named it; only jj says whether that bookmark is there, and a
        // revision that does not exist fails inside the job — one backoff
        // later, in a message about the wrong thing.
        const wanted = `${prefix}/${member.workspace}`;
        const all = yield* jj
          .bookmarks(repo)
          .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));
        return localBookmarks(all).some((entry) => entry.name === wanted)
          ? wanted
          : `${member.workspace}@`;
      });

    // A job the client named and the daemon has never heard of. Its own
    // failure rather than a defect: asking about a job that was cleaned up, or
    // typing an id, is a question with a negative answer.
    const known = (id: string) =>
      jobs.get(id).pipe(
        Effect.orDie,
        Effect.flatMap((job) =>
          job === undefined ? Effect.fail(new JobNotFound({ job: id })) : Effect.succeed(job),
        ),
      );

    return {
      // No declared error, so a failure here is a defect. That is the honest
      // shape: listing fails when zmx is missing or unrunnable, which is the
      // daemon being broken rather than a question with a negative answer. An
      // empty list is what "no sessions" looks like.
      SessionList: () =>
        mux.list().pipe(
          Effect.map((all) => {
            // Read once per listing rather than once per session: it is the
            // same answer for all of them, and it comes from this process's
            // environment, which does not change while a list is being built.
            const own = currentZmxSession();
            // Resolved for the listing rather than per session: repairing an
            // unlabelled session's workspace needs its labelled siblings, which
            // only exist in the context of the whole list.
            const found = identities(all);
            return all.map((session) => toWire(session, own, found.get(session.name)));
          }),
          Effect.orDie,
        ),

      Attach: ({ session, cols, rows }) =>
        Stream.unwrap(
          sessions.attach(session, { cols, rows }).pipe(
            // Two error channels here, and they need different answers. This
            // one is the stream's: the pty failing partway through a session
            // that attached successfully. There is nothing a client can do
            // about it and no refusal to render, so it becomes a defect.
            Effect.map(Stream.orDie),
            // And this one is the attach's, handled a tag at a time.
            //
            // `catchTags` and not `catchTag` followed by `orDie`: orDie is not
            // selective, so it would convert the AttachRefused just created
            // into a defect, and the client would see the daemon crash instead
            // of a session politely declining to open. Listing every tag also
            // means a new failure mode in a service below is a type error here
            // rather than a silent reclassification.
            Effect.catchTags({
              // Declining is a normal outcome the client is expected to show:
              // the session ended, or it is the daemon's own. It crosses the
              // wire as itself, reason and all.
              AttachError: (error) =>
                Effect.fail(new AttachRefused({ session, reason: error.reason })),
              // zmx missing or unrunnable, and a pty that would not open. The
              // daemon being broken, rather than the request being refusable.
              MultiplexerError: (error) => Effect.die(error),
              PtyError: (error) => Effect.die(error),
            }),
          ),
        ),

      Write: ({ session, data }) =>
        sessions.write(session, data).pipe(
          Effect.catchTags({
            NotAttached: () => Effect.fail(new SessionNotFound({ session })),
            PtyError: (error) => Effect.die(error),
          }),
        ),

      Resize: ({ session, cols, rows }) =>
        sessions.resize(session, { cols, rows }).pipe(
          Effect.catchTags({
            NotAttached: () => Effect.fail(new SessionNotFound({ session })),
            PtyError: (error) => Effect.die(error),
          }),
        ),

      // No declared error, for the same reason SessionList has none: a store
      // that cannot be read is the daemon being broken, and an empty list is
      // what "no jobs" looks like.
      JobList: () => jobs.list().pipe(Effect.orDie),

      // The stream's lifetime is the request's, so a client that goes away
      // unsubscribes from the feed. Nothing has to notice the disconnection.
      JobChanges: () => jobs.changes,

      JobLog: ({ job }) => known(job).pipe(Effect.flatMap(() => jobs.log(job).pipe(Effect.orDie))),

      // `retry` returns the record unchanged for a job that is still running,
      // which is a truer answer than a failure — it *is* already trying.
      JobRetry: ({ job }) =>
        known(job).pipe(
          Effect.flatMap(() => jobs.retry(job).pipe(Effect.orDie)),
          Effect.flatMap((updated) =>
            updated === undefined ? Effect.fail(new JobNotFound({ job })) : Effect.succeed(updated),
          ),
        ),

      JobCancel: ({ job }) =>
        known(job).pipe(Effect.flatMap(() => jobs.cancel(job).pipe(Effect.orDie))),

      JobClear: () => jobs.forgetFinished().pipe(Effect.orDie),

      // ── the diff panel's two calls ──────────────────────────────────────

      Revisions: ({ from, limit }) =>
        jj.revisions({ dir: from, revset: STACK, limit: limit ?? STACK_LIMIT }).pipe(
          // Retried with a smaller question rather than reported. See NO_TRUNK:
          // the first refusal is about the revset, not about the repository.
          Effect.catchTag("JjError", () => jj.revisions({ dir: from, revset: NO_TRUNK, limit: 1 })),
          Effect.mapError((error) => new DiffUnavailable({ reason: error.reason })),
        ),

      /**
       * The patch, and the rule about snapshotting on the way.
       *
       * A revision named by the client is history: it cannot change, so the
       * read passes `--ignore-working-copy` and writes nothing. No revision
       * means the working copy, which *does* change and changes for the reason
       * the panel is open — so that one read is allowed to snapshot first.
       *
       * `@` arriving explicitly is treated as absent. It is the same request
       * said a different way, and the alternative is a call that silently
       * returns yesterday's answer because of which spelling was used.
       */
      Diff: ({ from, revision }) => {
        const at = revision === undefined || revision === WORKING_COPY ? WORKING_COPY : revision;
        return jj.diff({ dir: from, revision: at, snapshot: at === WORKING_COPY }).pipe(
          Effect.map((patch) => ({ revision: at, patch })),
          Effect.mapError((error) => new DiffUnavailable({ reason: error.reason })),
        );
      },

      // Returns the record rather than the workspace, because the work outlives
      // the request. Whether it succeeded is a question for JobChanges.
      WorkspaceCreate: (payload) => jobs.enqueue(createWorkspaceRef, payload).pipe(Effect.orDie),

      /**
       * The one call the new-thread box makes: resolve, make the thread,
       * enqueue the job.
       *
       * In this order for a reason. The thread is made *after* the model
       * answers, so a failed resolve leaves nothing behind — an empty thread
       * called "add tiered dis…" would be litter a person then has to tidy.
       */
      ThreadBases: ({ from }) =>
        Effect.gen(function* () {
          const repo = yield* jj
            .sourceRoot(from)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const settings = yield* config.read();
          const all = yield* jj
            .bookmarks(repo)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          // Local only. A name that exists solely on a remote cannot be
          // branched from without fetching first, so offering it would be
          // offering a failure.
          const bookmarks = localBookmarks(all).map((entry) => ({
            revset: entry.name,
            label: entry.name,
            workspace: workspaceOf(entry.name, settings.bookmarkPrefix),
          }));

          return [
            { revset: TRUNK, label: "trunk", workspace: undefined },
            ...bookmarks.toSorted((a, b) => a.label.localeCompare(b.label)),
          ];
        }),

      ThreadStart: ({ description, project, from, parent, base, model, effort }) =>
        Effect.gen(function* () {
          // Refused before anything else, and cheaply. Naming happens inside
          // the job now, so this is no longer caught by the model declining an
          // empty sentence — and a thread whose whole content is a blank line
          // is litter a person then has to tidy.
          if (description.trim() === "") {
            return yield* Effect.fail(
              new ThreadStartFailed({ reason: "say what you are working on first" }),
            );
          }

          // A directory into the repository it belongs to. `jj root` would
          // answer with a workspace, which is the wrong thing to create a
          // second workspace from.
          const repo = yield* jj
            .sourceRoot(from)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const settings = yield* config.read();

          // Where the new workspace starts. An explicit revision wins; then a
          // parent thread, resolved to *its* bookmark; then the main line.
          const startFrom =
            base ?? (parent === undefined ? TRUNK : yield* baseOfThread(parent, project, repo));

          // Which thread this follows on from. Named outright when a caller
          // said so; otherwise recovered from the base, because the window
          // picks a *bookmark* and a bookmark names a workspace, and a
          // workspace may belong to a thread.
          //
          // Recovered rather than required, so branching off a workspace that
          // no thread has claimed still works — which is the ordinary case on
          // a machine whose workspaces predate threads, and was the whole
          // reason the picker used to come up empty.
          const followsFrom =
            parent ??
            (startFrom === TRUNK
              ? undefined
              : yield* threadOwning(workspaceOf(startFrom, settings.bookmarkPrefix), project));

          // Titled with what was typed, because nothing better exists yet. The
          // job's first step asks a model for a proper one and renames it —
          // which is a title that improves ten seconds later, rather than a
          // window that will not close for ten seconds.
          const thread = yield* threads.create(description.trim(), followsFrom).pipe(Effect.orDie);

          const job = yield* jobs
            .enqueue(createWorkspaceRef, {
              thread: thread.id,
              project,
              description,
              repo,
              base: startFrom,
              agent: agentWith(settings, { model, effort }),
            })
            .pipe(Effect.orDie);

          return { thread, job };
        }),

      // Threads. A store that cannot be read or written is the daemon being
      // broken — the disk is full, or the file is owned by someone else — so
      // ThreadStoreError dies here rather than crossing the wire. ThreadNotFound
      // does cross it: naming a thread that is not there is a question with a
      // negative answer, which is a different thing entirely.
      ThreadList: () => threads.list().pipe(Effect.orDie),

      ThreadCreate: ({ title }) => threads.create(title).pipe(Effect.orDie),

      ThreadRename: ({ thread, title }) =>
        threads.rename(thread, title).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadArchive: ({ thread, archived }) =>
        threads.archive(thread, archived).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadAttach: ({ thread, member }) =>
        threads.attach(thread, member).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),

      ThreadDetach: ({ thread, member }) =>
        threads.detach(thread, member).pipe(Effect.catchTag("ThreadStoreError", Effect.die)),
    };
  }),
);
