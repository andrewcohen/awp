// Live attachments: one pty per session, shared by everyone looking at it.
//
// The piece between the contract and the three services under it. The contract
// keys Write and Resize by session name rather than by a handle from Attach,
// so something has to know which ptys are alive and answer for a name that has
// none. That is all this is.
//
// ── why one pty per session and not one per client ─────────────────────────
// A session takes its size from the client looking at it. Two ptys on one
// session would be two zmx clients, so the session would reflow to whichever
// resized last and neither window would show what it thought it was showing.
// Sharing is not an optimisation here — it is the only arrangement in which two
// windows on the same agent agree.

import { Context, Data, Effect, Layer, RcMap, Ref, type Scope, Stream } from "effect";
import { type AttachError, Attachment } from "./attachment";
import type { MultiplexerError } from "./multiplexer";
import type { PtyError, PtyHandle, PtySize } from "./pty";

/** Asked to write to or resize a session that nothing is attached to. */
export class NotAttached extends Data.TaggedError("NotAttached")<{
  readonly session: string;
}> {}

/**
 * The size a session gets when nothing has said otherwise yet.
 *
 * Only reachable if an attach loses the race described in `attach` below. The
 * client resizes to its real geometry as soon as it has measured itself, which
 * is the same sequence any terminal emulator goes through on startup.
 */
const DEFAULT_SIZE: PtySize = { cols: 80, rows: 24 };

interface Live {
  readonly pty: PtyHandle;
  /**
   * The pty's output, multicast.
   *
   * `PtyHandle.output` is built with `Stream.callback`, so every *run* of it
   * registers with the pty again — two consumers would depend on whatever
   * bun-pty does with a second `onData` listener, which is either both getting
   * everything or the first silently going quiet. Neither is a contract worth
   * resting on, so the fan-out is stated here instead.
   *
   * `Stream.share` also fixes an ordering problem that a PubSub and a pump
   * fiber would not: it subscribes upstream when the *first* consumer starts,
   * so there is no window in which the pty has produced its opening screen and
   * nobody is listening yet.
   */
  readonly output: Stream.Stream<string, PtyError>;
}

export class Sessions extends Context.Service<
  Sessions,
  {
    /**
     * Attach, and receive the session's output for as long as the caller's
     * Scope is open.
     *
     * The Scope is the whole lifecycle. When the last caller's scope closes,
     * the pty is released and the process dies; while any caller holds one, a
     * second attach joins the existing pty rather than opening another.
     */
    readonly attach: (
      session: string,
      size: PtySize,
    ) => Effect.Effect<
      Stream.Stream<string, PtyError>,
      AttachError | MultiplexerError | PtyError,
      Scope.Scope
    >;

    /** Send keystrokes to a session someone is attached to. */
    readonly write: (session: string, data: string) => Effect.Effect<void, NotAttached | PtyError>;

    /** Tell a session's pty its new size. */
    readonly resize: (
      session: string,
      size: PtySize,
    ) => Effect.Effect<void, NotAttached | PtyError>;
  }
>()("awp/Sessions") {}

export const make = Effect.gen(function* () {
  const attachment = yield* Attachment;

  // The size the next attach should open with.
  //
  // RcMap's lookup receives only the key, and the key is the session name —
  // deliberately, because a zmx session has exactly one size and keying by
  // size too would open a second pty for something that cannot be two things.
  // So the size travels beside the key rather than in it.
  const requested = yield* Ref.make(new Map<string, PtySize>());

  // The index that `write` and `resize` read.
  //
  // Not a second source of truth: the RcMap owns the lifecycle, and the only
  // writes here happen in that lookup's acquire and release. What it adds is a
  // way to ask "is anything attached to this name" *without* acquiring, which
  // RcMap deliberately does not offer — every `get` is a reference. Writing to
  // an unattached session has to fail, not quietly attach one and tear it down
  // again when the effect ends.
  const index = yield* Ref.make(new Map<string, Live>());

  const ptys = yield* RcMap.make({
    lookup: (session: string) =>
      Effect.gen(function* () {
        const size = (yield* Ref.get(requested)).get(session) ?? DEFAULT_SIZE;
        const { pty } = yield* attachment.attach({ session, size });
        // Unbounded for the reason the pty's own stream is unbounded: dropping
        // output corrupts a terminal permanently, because an escape sequence
        // delivered by halves leaves the emulator in a state nothing later
        // corrects. A slow reader costs memory; a lossy one costs correctness.
        const output = yield* Stream.share(pty.output, { capacity: "unbounded" });

        const live: Live = { pty, output };
        yield* Effect.acquireRelease(
          Ref.update(index, (m) => new Map(m).set(session, live)),
          () => Ref.update(index, (m) => without(m, session)),
        );
        return live;
      }),
  });

  const current = (session: string) =>
    Ref.get(index).pipe(
      Effect.flatMap((m) => {
        const live = m.get(session);
        return live === undefined
          ? Effect.fail(new NotAttached({ session }))
          : Effect.succeed(live);
      }),
    );

  const resize = (session: string, size: PtySize) =>
    current(session).pipe(Effect.flatMap((live) => live.pty.resize(size)));

  return {
    attach: (session: string, size: PtySize) =>
      Effect.gen(function* () {
        yield* Ref.update(requested, (m) => new Map(m).set(session, size));
        const live = yield* RcMap.get(ptys, session);

        // Resize even when this attach opened the pty, and it is not
        // redundant: two clients arriving at once both write `requested`
        // before either runs the lookup, so the one that lost may have opened
        // at the other's size. Resizing after settles it either way, and the
        // outcome — the session takes the size of whoever looked at it last —
        // is what zmx does regardless of what this file prefers.
        yield* live.pty.resize(size);
        return live.output;
      }),

    write: (session: string, data: string) =>
      current(session).pipe(Effect.flatMap((live) => live.pty.write(data))),

    resize,
  };
});

export const layer = Layer.effect(Sessions)(make);

const without = <K, V>(map: ReadonlyMap<K, V>, key: K): Map<K, V> => {
  const next = new Map(map);
  next.delete(key);
  return next;
};
