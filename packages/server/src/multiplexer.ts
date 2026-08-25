// The multiplexer: "there are sessions, and here is what you can ask about
// them."
//
// ── what a tag is ──────────────────────────────────────────────────────────
// `Multiplexer` below is a *tag*: a typed key naming a capability, with no
// implementation. Code that needs it writes `yield* Multiplexer` and its type
// gains `Multiplexer` in the third slot of `Effect<A, E, R>` — a debt the
// compiler carries upward until something provides an implementation. Forget to
// wire one and it is a type error rather than a crash.
//
// The debt is why this file has no zmx in it. zmx.ts provides one
// implementation; a test provides another. Neither is named here.
//
// ── what is deliberately NOT here ──────────────────────────────────────────
// `attach`. Everything on this service answers a question and costs nothing;
// attach is an act with a consequence — it opens a client, and a session takes
// its size from the client looking at it, so attaching reflows whatever is
// running in there. That line is also the line between "testable from inside a
// session" and "must run outside zmx", which makes it worth a separate tag.

import { Context, type Effect } from "effect";
import { Data } from "effect";
import { LABEL_KIND, LABEL_PROJECT, LABEL_WORKSPACE, parseSessionName } from "./naming";
import type { SessionIdentity } from "./naming";

/**
 * A session as the multiplexer reports it.
 *
 * `ended` is the field worth reading twice. zmx keeps a session listed after
 * its command exits so the output can still be read, so **listed and running
 * are different questions** — a caller that treats "present in the list" as
 * "alive" will attach to a dead program's last screen.
 */
export interface Session {
  readonly name: string;
  readonly pid: number;
  /** How many clients are looking at it. Each one imposes its size. */
  readonly clients: number;
  readonly startDir: string;
  /** True once the command has exited. The session is still listed. */
  readonly ended: boolean;
  readonly exitCode: number;
  /** When zmx started it. `zmx ls` reports a unix stamp; this is a Date. */
  readonly created: Date | undefined;
  readonly cmd: string;
  /** Everything `zmx ls` printed that was not a known field. */
  readonly labels: Readonly<Record<string, string>>;
}

/** Whether the session's process is still running. See {@link Session.ended}. */
export const isLive = (session: Session): boolean => !session.ended;

/**
 * Which workspace and kind a session belongs to, and whether it is awp's at
 * all — `zmx ls` lists every session on the machine.
 *
 * Labels first, then the name. The fallback is not only for sessions predating
 * the labels: a session comes into existence through an attach, and its labels
 * are set afterwards, so there is a window in which the name is all there is.
 *
 * The name's answer is lossy — a dot in a real name comes back as an
 * underscore, and a shortened name comes back shortened — which is why anything
 * that must find a workspace should generate the name it expects rather than
 * read the name it got.
 */
export const identity = (session: Session): SessionIdentity | undefined => {
  const project = session.labels[LABEL_PROJECT];
  const workspace = session.labels[LABEL_WORKSPACE];
  if (project !== undefined && project !== "" && workspace !== undefined && workspace !== "") {
    return { project, workspace, kind: session.labels[LABEL_KIND] ?? "" };
  }
  return parseSessionName(session.name);
};

/**
 * Anything the multiplexer could not do.
 *
 * `Data.TaggedError` gives a class whose instances carry a `_tag`, so a caller
 * can recover from one kind of failure and not another. It is in the `E` slot
 * of `Effect<A, E, R>` — declared, not thrown, and the compiler knows which
 * failures a call site still has to answer for.
 */
export class MultiplexerError extends Data.TaggedError("MultiplexerError")<{
  /** What was being attempted, for a message a person can act on. */
  readonly op: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The multiplexer.
 *
 * Every method is a question with an answer, or a change with no live
 * aftermath. Nothing here needs a pty, so an implementation sits on ordinary
 * subprocesses with captured output — which is also why the read-only methods
 * are safe to test against a real zmx from inside a session.
 */
export class Multiplexer extends Context.Service<
  Multiplexer,
  {
    /** Every session the multiplexer knows about, awp's or not. */
    list(): Effect.Effect<ReadonlyArray<Session>, MultiplexerError>;

    /** One session by name, or `undefined` if there is none. */
    lookup(name: string): Effect.Effect<Session | undefined, MultiplexerError>;

    /** End a session and every client attached to it. */
    kill(name: string): Effect.Effect<void, MultiplexerError>;

    /** Set labels on a session. A key with an empty value removes it. */
    setLabels(
      name: string,
      labels: Readonly<Record<string, string>>,
    ): Effect.Effect<void, MultiplexerError>;

    /** A session's scrollback, as the multiplexer renders it. */
    history(name: string): Effect.Effect<string, MultiplexerError>;
  }
>()("@awp-kit/server/Multiplexer") {}
