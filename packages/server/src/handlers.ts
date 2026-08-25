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

import { AttachRefused, AwpRpcs, SessionNotFound, type SessionInfo } from "@awp-kit/protocol";
import { Effect, Stream } from "effect";
import { Multiplexer, type Session } from "./multiplexer";
import { Sessions } from "./sessions";

/**
 * The daemon's `Session` as the client is promised it.
 *
 * A function and not a cast. The two types are allowed to drift — one is what
 * zmx reports and the other is what a client is owed — and this is the single
 * place that would stop compiling when they do.
 */
const toWire = (session: Session): SessionInfo => ({
  name: session.name,
  pid: session.pid,
  clients: session.clients,
  startDir: session.startDir,
  ended: session.ended,
  exitCode: session.exitCode,
  created: session.created,
  cmd: session.cmd,
  labels: session.labels,
});

export const layer = AwpRpcs.toLayer(
  Effect.gen(function* () {
    const mux = yield* Multiplexer;
    const sessions = yield* Sessions;

    return {
      // No declared error, so a failure here is a defect. That is the honest
      // shape: listing fails when zmx is missing or unrunnable, which is the
      // daemon being broken rather than a question with a negative answer. An
      // empty list is what "no sessions" looks like.
      SessionList: () =>
        mux.list().pipe(
          Effect.map((all) => all.map((session) => toWire(session))),
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
    };
  }),
);
