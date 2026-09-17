import type { Connection, DbError, Migration } from "@awp-kit/store";
import { attempt } from "@awp-kit/store";
import { Data, Effect, Stream } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";

// Who is writing a conversation, as something more than one process can ask.
//
// ── the address and the resource are not the same thing ───────────────────
//
// `Chat` already guarantees one adapter per workspace: an `RcMap` keyed by
// `project\nworkspace`, so a second window joins the first rather than
// spawning beside it. That guarantee is a map **in one process's memory**.
//
// What it guards is not in memory. A conversation is a session id in
// `~/.awp/awp.sqlite`, shared by every daemon on this machine, and the thing
// that opens it is `claude --resume=<id>` — which nothing prevents two
// processes from running at once. `chat.ts` says so in its own comment about
// `session/list`: two writers on one transcript, "and neither process knows
// about the other".
//
// It is not hypothetical. Two agents ran on one session for two and a half
// minutes and produced two implementations of one task in one working copy,
// interleaved a second apart in a single transcript. Three ways in, all of
// them ordinary here:
//
//   two daemons        the two-instance dev workflow. 5274 and 5284 share the
//                      store, so each has its own RcMap and neither sees the
//                      other's
//   a background agent the harness holds the session; the daemon's stored
//                      pointer names it and loads it again
//   a hand-typed       `claude --resume=<id>` in any terminal
//
// So the claim is on the **session id**, in the shared store, and it is taken
// by whoever is about to write. Two checks, in this order, because they cost
// different amounts and cover different ground:
//
//   the table    every awp daemon, exactly. One row, one writer
//   the process  everything else, approximately. A `--resume` nobody told the
//                store about
//
// ── why a heartbeat, and it is not about the network ──────────────────────
//
// A claim with no heartbeat is a lock, and a lock survives the process that
// took it: one `kill -9` on a daemon and that conversation is unopenable
// forever, with no way to tell a dead owner from a busy one. The beat is what
// makes the row an assertion about *now* — stop beating and the claim decays
// into something the next opener may take. Nothing here keeps a socket alive
// or notices a client going away; those are different problems with the same
// word attached.

/** The table. Keyed by the session, which is what is actually contended. */
export const claimMigration: Migration = {
  name: "chat.003-claims",
  up: [
    `create table chat_claims (
       session_id text primary key,
       owner      text not null,
       pid        integer not null,
       at         integer not null
     ) strict`,
  ],
};

/** A claim, as it was found. */
export interface Claim {
  readonly sessionId: string;
  /** Something a person can act on: which daemon, on which port. */
  readonly owner: string;
  readonly pid: number;
  /** The last beat, as epoch milliseconds. */
  readonly at: number;
}

/**
 * Somebody else is writing this conversation.
 *
 * Its own error and not `ChatError`, so this module can be tested without the
 * two thousand lines `chat.ts` would drag in — and `import/no-cycle` is on.
 * `chat.ts` maps it, keeping the sentence.
 */
export class SessionHeld extends Data.TaggedError("SessionHeld")<{
  readonly sessionId: string;
  readonly reason: string;
}> {}

/**
 * How long a claim outlives its last beat.
 *
 * Three beats. One missed beat is a daemon that was busy — a sweep, a long
 * sqlite write — and treating that as death is how two writers happen for the
 * *second* reason. The cost of the other direction is a wait, once, after a
 * crash.
 */
export const STALE_AFTER = 60_000;

/** How often a held claim says it is still there. */
export const BEAT_EVERY = 20_000;

export interface Claims {
  /**
   * Take the claim, or refuse naming who has it.
   *
   * Answers **whether this daemon already held it**, which is the one thing
   * the caller has to know: a claim already ours is this process's own
   * adapter, so nothing outside can have crept in underneath it and the
   * slower check below can be skipped. A claim that was free, stale or held
   * by a process that has died answers false, and is then worth looking at
   * the process table for.
   */
  readonly take: (sessionId: string) => Effect.Effect<boolean, SessionHeld | DbError>;
  /** Say the claim is still held. Failures are nothing to act on — see `beat`. */
  readonly beat: (sessionId: string) => Effect.Effect<void>;
  /** Give it up. Only ours: a claim taken over by somebody else is not ours to delete. */
  readonly release: (sessionId: string) => Effect.Effect<void>;
  readonly holder: (sessionId: string) => Effect.Effect<Claim | undefined, DbError>;
}

const rowAsClaim = (row: Record<string, unknown> | undefined): Claim | undefined =>
  row === undefined
    ? undefined
    : {
        sessionId: String(row["session_id"]),
        owner: String(row["owner"]),
        pid: Number(row["pid"]),
        at: Number(row["at"]),
      };

/** How long ago, in words a sentence can carry. */
const ago = (millis: number): string =>
  millis < 2000 ? "just now" : `${String(Math.round(millis / 1000))}s ago`;

/** The sentence when another awp daemon has it. */
const heldBy = (sessionId: string, who: Claim, at: number): SessionHeld =>
  new SessionHeld({
    sessionId,
    reason:
      `that conversation (session ${sessionId}) is already open in ${who.owner} ` +
      `(pid ${String(who.pid)}, last seen ${ago(at - who.at)}) — open the chat there rather ` +
      "than here, or start a fresh one with /new. Two processes on one transcript is how a " +
      "conversation ends up with two agents in it.",
  });

/**
 * Whether a process is still there.
 *
 * Signal 0 is the ask-do-not-send form, so this costs nothing and answers
 * exactly the question. The store is `~/.awp/awp.sqlite` — one machine — so a
 * pid in it is a pid here; the day that stops being true this needs a host
 * column, not a better guess.
 */
const running = (pid: number): boolean => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (cause) {
    // EPERM says it exists and is somebody else's, which for this question is
    // a yes. Only "no such process" is a no.
    return (cause as { readonly code?: string } | undefined)?.code === "EPERM";
  }
};

export const claims = (
  db: Connection,
  self: { readonly owner: string; readonly pid: number },
  now: () => number = Date.now,
  alive: (pid: number) => boolean = running,
): Claims => {
  // Write first, then read back, and the read is the arbiter.
  //
  // Two daemons can both judge a row takeable in the same instant — a dead
  // owner, or one that stopped beating — and both will write. The upsert is
  // atomic, so one of them is last, and then *both* read the row: whoever
  // finds a pid that is not theirs refuses. There is no ordering in which both
  // believe they took it, which is the property worth having; deciding which
  // one wins is not.
  const claim = db.prepare(
    `insert into chat_claims (session_id, owner, pid, at) values (?, ?, ?, ?)
       on conflict (session_id) do update
          set owner = excluded.owner, pid = excluded.pid, at = excluded.at`,
  );
  const read = db.prepare("select * from chat_claims where session_id = ?");
  const touch = db.prepare("update chat_claims set at = ? where session_id = ? and pid = ?");
  const drop = db.prepare("delete from chat_claims where session_id = ? and pid = ?");

  const holder = (sessionId: string) =>
    attempt("read the session claim", () => rowAsClaim(read.all(sessionId)[0]));

  return {
    holder,

    take: (sessionId: string) =>
      Effect.gen(function* () {
        const at = now();
        const before = yield* holder(sessionId);
        const ours = before !== undefined && before.pid === self.pid;

        // Refused before anything is written, and on two questions rather
        // than one. The beat alone would make every daemon restart wait out
        // `STALE_AFTER` — `bun run dev restart daemon` is a thing that happens
        // here several times an hour, and a minute of "already open" after
        // each one would teach everybody to ignore the sentence. A pid that is
        // gone is gone now.
        if (before !== undefined && !ours && before.at > at - STALE_AFTER && alive(before.pid)) {
          return yield* Effect.fail(heldBy(sessionId, before, at));
        }

        yield* attempt("claim the session", () => claim.run(sessionId, self.owner, self.pid, at));
        const after = yield* holder(sessionId);
        if (after === undefined || after.pid !== self.pid) {
          // Somebody wrote between the check and the write. The read decides,
          // and this side lost.
          return yield* Effect.fail(
            after === undefined
              ? new SessionHeld({
                  sessionId,
                  reason:
                    "the session claim could not be written — nothing is holding this conversation, and nothing can take it either",
                })
              : heldBy(sessionId, after, at),
          );
        }
        return ours;
      }),

    // Ignored, deliberately, and it is the one place in this module that
    // swallows. A beat that could not be written is a beat, not a failure to
    // hold: the claim is still ours until it decays, and taking a conversation
    // down because one write lost a race with a checkpoint would be a cure
    // worse than the disease.
    beat: (sessionId: string) =>
      Effect.ignore(attempt("beat the session claim", () => touch.run(now(), sessionId, self.pid))),

    release: (sessionId: string) =>
      Effect.ignore(attempt("release the session claim", () => drop.run(sessionId, self.pid))),
  };
};

// ── the check the table cannot make ───────────────────────────────────────
//
// A background agent, or somebody's terminal, holds a session by running
// `claude --resume=<id>`. No row is written for that and no row ever will be:
// it is not awp's process. What is true of it is visible in the process table,
// which is the real tool to ask.
//
// `ps -Ao pid=,command=` and not `pgrep -af`: `-a` is Linux-only and this runs
// on macOS. Parsed here, as a pure function, because a scan that matched the
// wrong thing would refuse to open a conversation nobody else has.

/** A process holding a session, as the process table describes it. */
export interface Holder {
  readonly pid: number;
  readonly command: string;
}

/**
 * The processes resuming this session, out of `ps` output.
 *
 * `--resume=<id>` and not the bare id: the id appears in a transcript path,
 * in a log tail somebody is following, and in the argv of anything grepping
 * for it — including the check itself. Matching the flag is what separates a
 * process *holding* the session from a process *mentioning* it.
 */
export const holdersIn = (
  listing: string,
  sessionId: string,
  ours: ReadonlySet<number>,
): ReadonlyArray<Holder> => {
  const wanted = `--resume=${sessionId}`;
  const found: Array<Holder> = [];
  for (const line of listing.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed.includes(wanted)) {
      continue;
    }
    const at = trimmed.indexOf(" ");
    if (at < 1) {
      continue;
    }
    const pid = Number(trimmed.slice(0, at));
    if (!Number.isInteger(pid) || ours.has(pid)) {
      continue;
    }
    found.push({ pid, command: trimmed.slice(at + 1) });
  }
  return found;
};

/**
 * Ask the process table who else is resuming this session.
 *
 * Fails open. Every path out of here that is not a clean answer is an empty
 * list, because this is the *second* guard: the table above is the one that
 * has to be right, and a `ps` that is missing, slow or shaped differently on
 * some future machine must not be able to stop a conversation opening.
 */
export const outsideHolders = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  sessionId: string,
  ours: ReadonlySet<number>,
): Effect.Effect<ReadonlyArray<Holder>> =>
  Effect.gen(function* () {
    const handle = yield* spawner.spawn(ChildProcess.make("ps", ["-Ao", "pid=,command="]));
    const listing = yield* Stream.mkString(Stream.decodeText(handle.stdout));
    return holdersIn(listing, sessionId, ours);
  }).pipe(
    Effect.scoped,
    Effect.orElseSucceed((): ReadonlyArray<Holder> => []),
  );

/** The sentence a person reads when something outside awp has the session. */
export const heldOutside = (sessionId: string, holders: ReadonlyArray<Holder>): SessionHeld =>
  new SessionHeld({
    sessionId,
    reason:
      `that conversation (session ${sessionId}) is already being written by ${holders
        .map((one) => `pid ${String(one.pid)}`)
        .join(", ")} — the previous one still going after a daemon restart, a ` +
      "background agent, or a claude resumed by hand. After a restart this clears itself " +
      "as that process exits, so try again in a moment; otherwise stop it (`claude agents`) " +
      "or start a fresh one with /new. Two processes on one transcript is how a " +
      "conversation ends up with two agents in it.",
  });
