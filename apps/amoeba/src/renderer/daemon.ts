import { AwpClient, layerClient } from "@awp-kit/protocol/client";
import type { SessionInfo } from "@awp-kit/protocol";
import { Effect, Fiber, Stream } from "effect";
import { ManagedRuntime } from "effect";

// The renderer's one connection to the daemon.
//
// A ManagedRuntime and not a runtime per call, because building a client opens
// the socket — a client per call would be a connection per keystroke.
//
// This is the seam between Effect and React, and it is deliberately narrow.
// Everything above it is plain promises and callbacks, which is what React
// components are good at; everything below it keeps its Scopes, error channels
// and interruption, which is what makes the pty get killed when a pane closes.
// Widening this file is how those two worlds start leaking into each other.

const runtime = ManagedRuntime.make(layerClient());

/** Every session the multiplexer knows about. */
export const listSessions = (): Promise<ReadonlyArray<SessionInfo>> =>
  runtime.runPromise(Effect.flatMap(AwpClient, (rpc) => rpc.SessionList()));

/** Keystrokes. Fire and forget — see below. */
export const write = (session: string, data: string): void => {
  runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) => rpc.Write({ session, data })).pipe(
      // Deliberately not awaited and deliberately not surfaced. The reply is an
      // acknowledgement; the echo a typist waits for arrives through the attach
      // stream instead, so awaiting this would put a round trip between
      // keystrokes for nothing. A write that fails means the session went away,
      // which the stream ending says more clearly than a toast would.
      Effect.catchCause(() => Effect.void),
    ),
  );
};

export const resize = (session: string, cols: number, rows: number): void => {
  runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) => rpc.Resize({ session, cols, rows })).pipe(
      Effect.catchCause(() => Effect.void),
    ),
  );
};

export interface Attachment {
  /** Stops the stream, which releases the pty if this was the last client. */
  readonly detach: () => void;
}

/**
 * Attach to a session and receive its output until `detach`.
 *
 * The returned function is the whole lifecycle, and interrupting the fiber is
 * not merely tidy: the stream's lifetime is the request's lifetime, so this
 * interrupt travels down the socket, cancels the handler, closes the pty's
 * Scope and kills the process. Forgetting to call it leaves a zmx client
 * attached, which means a session sized to a window that no longer exists.
 */
export const attach = (
  session: string,
  cols: number,
  rows: number,
  handlers: {
    readonly onChunk: (chunk: string) => void;
    readonly onRefused: (reason: string) => void;
  },
): Attachment => {
  const fiber = runtime.runFork(
    Effect.flatMap(AwpClient, (rpc) =>
      Stream.runForEach(rpc.Attach({ session, cols, rows }), (chunk) =>
        Effect.sync(() => handlers.onChunk(chunk)),
      ),
    ).pipe(
      // A refusal is a sentence written for a person — the session ended, or it
      // is the daemon's own — and the pane shows it instead of going blank.
      Effect.catchTag("AttachRefused", (error) =>
        Effect.sync(() => handlers.onRefused(error.reason)),
      ),
      // Interruption is how detach works, so it is not a failure to report.
      Effect.catchCause(() => Effect.void),
    ),
  );

  return {
    detach: () => {
      runtime.runFork(Fiber.interrupt(fiber));
    },
  };
};
