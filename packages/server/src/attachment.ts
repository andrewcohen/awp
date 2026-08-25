// Attaching to a session.
//
// The half of the multiplexer with consequences. Everything on `Multiplexer`
// answers a question; this opens a client, and a session takes its size from
// the client looking at it — so attaching reflows whatever is running in there,
// for everyone watching, and closing the pane reflows it back. Nothing avoids
// that; it is what attaching means. A UI has to say so.
//
// Five steps, of which two have a track record of being got wrong:
//
//   name ──▶ refuse to attach to our own session   ◀ steals the caller's terminal
//        ──▶ check it is live, not merely listed
//        ──▶ strip ZMX_SESSION from the child
//        ──▶ open a pty
//        ──▶ hand back a scoped handle             ◀ the probe leaked here

import { Context, Data, Effect, Layer, type Scope } from "effect";
import { Multiplexer, type MultiplexerError, isLive } from "./multiplexer";
import { PtySpawner, type PtyError, type PtyHandle, type PtySize } from "./pty";
import { currentZmxSession, zmxChildEnv } from "./zmx-session";

export class AttachError extends Data.TaggedError("AttachError")<{
  readonly session: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

export interface AttachOptions {
  readonly session: string;
  readonly size: PtySize;
}

/** A live attachment. The pty handle, plus who it belongs to. */
export interface Attached {
  readonly session: string;
  readonly pty: PtyHandle;
}

export class Attachment extends Context.Service<
  Attachment,
  {
    /**
     * Attach to a session, for as long as the scope lives.
     *
     * The `Scope` is the promise that the client goes away again — and with a
     * multiplexer, a client that does not go away is a session stuck at a
     * window's dimensions.
     */
    attach(
      options: AttachOptions,
    ): Effect.Effect<Attached, AttachError | MultiplexerError | PtyError, Scope.Scope>;
  }
>()("@awp-kit/server/Attachment") {}

const make = Effect.gen(function* () {
  const mux = yield* Multiplexer;
  const spawner = yield* PtySpawner;

  return {
    attach: (options: AttachOptions) =>
      Effect.gen(function* () {
        const { session, size } = options;

        // Refusing our own session is not politeness. `zmx attach` branches on
        // ZMX_SESSION, and stripping it stops that — but the daemon may be
        // running inside a session anyway, and attaching to *that* one means
        // the pane and the terminal awp was launched from are the same client,
        // fighting over one size. gdeck disabled the row rather than explain it
        // afterwards.
        if (session === currentZmxSession()) {
          return yield* new AttachError({
            session,
            reason:
              "refusing to attach to the session this process is running in — " +
              "the pane and its own terminal would fight over one size",
          });
        }

        const existing = yield* mux.lookup(session);
        if (existing === undefined) {
          return yield* new AttachError({ session, reason: "no such session" });
        }

        // Listed is not running. zmx keeps a session listed after its command
        // exits so the output can still be read, and attaching to one of those
        // renders a dead program's last screen — which looks like a live pane
        // that has stopped responding.
        if (!isLive(existing)) {
          return yield* new AttachError({
            session,
            reason: `session has ended (exit ${existing.exitCode}); its output is still readable through history`,
          });
        }

        const pty = yield* spawner.spawn({
          command: "zmx",
          args: ["attach", session],
          size,
          // The complete environment, minus the marker. A merge could not
          // express the removal, which is why PtyCommand takes the whole thing.
          env: zmxChildEnv(),
        });

        return { session, pty };
      }),
  };
});

/**
 * Attaching, over whatever `PtySpawner` and `Multiplexer` are provided.
 *
 * Both are tags, so this same layer serves the daemon — real pty, real zmx —
 * and a test, with a fake pty and no zmx at all. That is the only reason any of
 * this is testable from inside a zmx session.
 */
export const layer = Layer.effect(Attachment, make);
