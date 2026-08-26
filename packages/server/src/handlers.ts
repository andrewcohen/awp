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
  JobNotFound,
  SessionNotFound,
  ThreadStartFailed,
  type SessionIdentity,
  type SessionInfo,
} from "@awp-kit/protocol";
import { Effect, Stream } from "effect";
import { WorkspaceIntent } from "./intent";
import { Jj } from "./jj";
import { createWorkspaceRef } from "./jobs/create-workspace";
import { Settings, agentWith } from "./settings";
import { demo } from "./jobs/demo";
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

export const layer = AwpRpcs.toLayer(
  Effect.gen(function* () {
    const mux = yield* Multiplexer;
    const sessions = yield* Sessions;
    const jobs = yield* Jobs;
    const threads = yield* Threads;
    const intent = yield* WorkspaceIntent;
    const config = yield* Settings;
    const jj = yield* Jj;

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

      JobDemo: (payload) => jobs.enqueue(demo, payload).pipe(Effect.orDie),

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
      ThreadStart: ({ description, project, from, base, model, effort }) =>
        Effect.gen(function* () {
          // A directory into the repository it belongs to. `jj root` would
          // answer with a workspace, which is the wrong thing to create a
          // second workspace from.
          const repo = yield* jj
            .sourceRoot(from)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const resolved = yield* intent
            .resolve(description, project)
            .pipe(Effect.mapError((error) => new ThreadStartFailed({ reason: error.reason })));

          const settings = yield* config.read();
          const thread = yield* threads.create(resolved.label).pipe(Effect.orDie);

          const job = yield* jobs
            .enqueue(createWorkspaceRef, {
              thread: thread.id,
              project,
              workspace: resolved.name,
              label: resolved.label,
              prompt: resolved.prompt,
              repo,
              // jj resolves the default bookmark of origin/upstream, falling
              // back through main, master and trunk. So the usual answer needs
              // no configuration and no guessing.
              base: base ?? "trunk()",
              bookmark:
                settings.bookmarkPrefix === undefined
                  ? undefined
                  : `${settings.bookmarkPrefix}/${resolved.name}`,
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
