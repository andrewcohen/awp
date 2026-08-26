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
import {
  LABEL_KIND,
  LABEL_LABEL,
  LABEL_PROJECT,
  LABEL_WORKSPACE,
  parseSessionName,
  splitSessionName,
  stemMatches,
} from "./naming";
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
    // The label is not part of the address and is therefore not recoverable
    // from the name — a session without one simply has none, which is what
    // every session predating this looks like.
    const label = session.labels[LABEL_LABEL];
    return {
      project,
      workspace,
      kind: session.labels[LABEL_KIND] ?? "",
      label: label === undefined || label === "" ? undefined : label,
    };
  }
  return parseSessionName(session.name);
};

/**
 * Whether this session's identity came from its labels, which is the only
 * source that is not lossy.
 */
const labelled = (session: Session): boolean => {
  const project = session.labels[LABEL_PROJECT];
  const workspace = session.labels[LABEL_WORKSPACE];
  return project !== undefined && project !== "" && workspace !== undefined && workspace !== "";
};

/**
 * The identities of a whole listing, with the unlabelled ones repaired against
 * the labelled.
 *
 * Needed because a name cannot group a workspace, and the reason is worth
 * stating precisely. `sessionName` gives the stem whatever budget the kind does
 * not need, so **one workspace's sessions can have differently shortened
 * stems**:
 *
 *   awp.thicket.effect-ts-tiered-d-f500.action_dev
 *   awp.thicket.effect-ts-tiered-disco-f500.editor
 *   awp.thicket.effect-ts-tiered-discou-f500.agent
 *
 * Three names, three stems, one workspace —
 * `thicket/effect-ts-tiered-discount-timemachine`. Read individually they are
 * three workspaces, which is what the sidebar showed. Nothing about that is a
 * bug in the shortening: a name is an address, and the address only has to
 * resolve, not to be legible.
 *
 * So the repair is the use `stemMatches` was written for — asked per workspace
 * rather than looked up in a map, because only the workspace can reproduce the
 * shortening at the length a given stem actually has. One labelled session is
 * enough to recover every one of its siblings.
 *
 * A workspace where *no* session carries labels keeps its shortened name and
 * stays split. That is not repairable from here, and it is also temporary:
 * every session awp creates is labelled. These are the ones that predate it.
 */
/**
 * Workspace names a session's working directory suggests.
 *
 * awp puts a workspace at `~/.awp/workspaces/<project>/<workspace>`, and that
 * path is written out in full — it is a directory rather than a session name,
 * so nothing shortened it. A `default` workspace is the repository itself, whose
 * directory says only the project.
 *
 * Candidates, not answers. Every one of them is put to `stemMatches` before it
 * is believed, so a wrong guess produces nothing rather than a wrong name — which
 * is what makes it safe to guess at all.
 */
const suggestedBy = (startDir: string): ReadonlyArray<Pair> => {
  const parts = startDir.split("/").filter((part) => part !== "");
  const marker = parts.lastIndexOf("workspaces");
  const found: Pair[] = [];
  if (marker >= 0 && parts.length > marker + 2) {
    found.push({ project: parts[marker + 1] ?? "", workspace: parts[marker + 2] ?? "" });
  }
  const base = parts.at(-1);
  if (base !== undefined) {
    found.push({ project: base, workspace: "default" });
  }
  return found;
};

interface Pair {
  readonly project: string;
  readonly workspace: string;
}

export const identities = (
  sessions: ReadonlyArray<Session>,
): ReadonlyMap<string, SessionIdentity | undefined> => {
  const resolved = new Map<string, SessionIdentity | undefined>();
  const known: Pair[] = [];

  for (const session of sessions) {
    const found = identity(session);
    resolved.set(session.name, found);
    if (found !== undefined && labelled(session)) {
      known.push({ project: found.project, workspace: found.workspace });
    }
  }

  for (const session of sessions) {
    if (labelled(session)) {
      continue;
    }
    const split = splitSessionName(session.name);
    if (split === undefined) {
      continue;
    }
    // Siblings first, then the working directory. A labelled sibling is a
    // statement; a path is a convention, and one that only counts when it can
    // reproduce the name actually observed.
    const match = [...known, ...suggestedBy(session.startDir)].find((pair) =>
      stemMatches(pair.project, pair.workspace, split.stem),
    );
    if (match !== undefined) {
      // No label: this branch exists for a session whose own labels are
      // missing, so the only thing recovered is the address. A repaired
      // sibling's label would be that sibling's, not this one's.
      resolved.set(session.name, { ...match, kind: split.kind, label: undefined });
    }
  }

  return resolved;
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

    /**
     * Start a session, running `command` in `cwd`, without attaching to it.
     *
     * `zmx run -d`, not `zmx attach`. Attaching is what creates a session for
     * an interactive caller, and it needs a pty and a client — a session takes
     * its size from whoever is looking at it, so a daemon attaching to make one
     * would size someone else's terminal to nothing. This makes the session and
     * leaves it alone; a window attaches later if a person opens it.
     *
     * Does nothing if a session of that name is already there. That is the
     * jobs contract — a step re-runs after a later one fails — and it is also
     * the thing that keeps this from ever touching a session it did not create:
     * an existing name is left exactly as it was.
     */
    start(options: {
      readonly name: string;
      readonly cwd: string;
      readonly command: ReadonlyArray<string>;
    }): Effect.Effect<void, MultiplexerError>;

    /**
     * Type text into a session, as though a person had.
     *
     * `zmx send`, which writes to the session's pty. What it is for is handing
     * a new agent its instruction — the thing that turns a workspace that
     * exists into work that has started.
     *
     * **Not idempotent, and cannot be.** Sending twice sends twice; there is no
     * way to ask a terminal what it has already been told. A caller that must
     * not repeat itself has to know it has not — which for the create job means
     * this is the last step, so nothing after it can fail and cause a re-run.
     */
    send(name: string, text: string): Effect.Effect<void, MultiplexerError>;

    /** End a session and every client attached to it. */
    kill(name: string): Effect.Effect<void, MultiplexerError>;

    /** Set labels on a session. A key with an empty value removes it. */
    setLabels(
      name: string,
      labels: Readonly<Record<string, string>>,
    ): Effect.Effect<void, MultiplexerError>;

    /** A session's scrollback, as the multiplexer renders it. */
    /**
     * A session's scrollback.
     *
     * `vt` keeps the escape sequences, which is what replaying into a terminal
     * needs — without it the text arrives stripped of every colour and cursor
     * move, which is fine to read and wrong to render.
     */
    history(
      name: string,
      options?: { readonly vt?: boolean },
    ): Effect.Effect<string, MultiplexerError>;
  }
>()("@awp-kit/server/Multiplexer") {}
