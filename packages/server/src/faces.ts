import type { Face } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer } from "effect";

// Which of a workspace's two agents is the one with the work in it.
//
// ── a fact, not a view preference ──────────────────────────────────────────
//
// This was `awp.face` in the window's localStorage, and it decided one thing:
// which panel the agent column drew. That reading came apart the moment
// anything was *sent* to an agent, because the two are not the same question:
//
//   which one I am looking at   a view. Cheap, reversible, per window
//   which one has the work      the briefed agent, its transcript, its context.
//                               Only ever one of the two
//
// A workspace has both halves whatever happens — the create job starts a zmx
// session on either face, deliberately — so the terminal beside a briefed chat
// is a bare shell prompt. A review routed by the *view* lands there as a shell
// command, which is `command not found` and a review nobody ever reads. That
// is the failure this table exists to make unreachable.
//
// So the face is recorded per workspace, in the daemon, and every send follows
// it. Nothing in the window decides where a prompt goes any more.
//
// ── absent means the terminal, and that is not a default worth arguing ──────
//
// Every workspace on this machine predates the table, and the old default was
// the terminal — so their work really is in the pty and reading them as chat
// would point sends at a conversation that has never said anything. The create
// job writes a row for what it makes, so a new workspace says so for itself.
//
// ── no change stream ───────────────────────────────────────────────────────
//
// Same argument threads are on the wire without one: a face changes when a
// person swaps it, in this window, so the reply to the swap *is* the update.
// Nothing changes it on its own, which is the property that makes a stream
// worth having.

/** The database would not answer. */
export class FaceStoreError extends Data.TaggedError("FaceStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * One row per workspace, and only for the ones that have been decided.
 *
 * A row is written by the create job and by a swap, and by nothing else. The
 * absence of one is meaningful — see the header — so this is deliberately not
 * backfilled from anything.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "faces.001-face",
    up: [
      `create table workspace_faces (
         project   text not null,
         workspace text not null,
         face      text not null,
         primary key (project, workspace)
       ) strict`,
    ],
  },
];

export class Faces extends Context.Service<
  Faces,
  {
    /**
     * Which agent holds this workspace's work.
     *
     * Never fails to answer: a workspace with no row is the terminal, which is
     * what every workspace made before this existed actually is.
     */
    readonly face: (project: string, workspace: string) => Effect.Effect<Face, FaceStoreError>;

    /** Record which one it is from now on. Idempotent. */
    readonly set: (
      project: string,
      workspace: string,
      face: Face,
    ) => Effect.Effect<void, FaceStoreError>;

    /** Forget it, so the workspace reads as the terminal again. */
    readonly forget: (project: string, workspace: string) => Effect.Effect<void, FaceStoreError>;
  }
>()("awp/Faces") {}

/** The store's own error, so a caller catching it reasons about faces. */
const ask = <A>(reason: string, run: () => A): Effect.Effect<A, FaceStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error) => new FaceStoreError({ reason, cause: error.cause })),
  );

/**
 * A stored value, narrowed to the two the contract has.
 *
 * Text with no `check` on the column, for the reason `tasks` gives about
 * `status`: a constraint here turns a third face into a daemon that will not
 * start, where an unrecognised value is a row this reads as the terminal and
 * a swap overwrites. The narrowing has to happen somewhere and the read is
 * the place a wrong value costs least.
 */
const faceOf = (value: unknown): Face => (value === "chat" ? "chat" : "terminal");

export const make = Effect.gen(function* () {
  const db = yield* Db;

  const read = db.prepare("select face from workspace_faces where project = ? and workspace = ?");
  // The pair is the primary key, so the row already there is the row this
  // rewrites. One statement, so a swap cannot half happen.
  const write = db.prepare(
    `insert into workspace_faces (project, workspace, face) values (?, ?, ?)
     on conflict (project, workspace) do update set face = excluded.face`,
  );
  const drop = db.prepare("delete from workspace_faces where project = ? and workspace = ?");

  return {
    face: (project: string, workspace: string) =>
      ask("read a workspace's face", () => {
        const row = read.all(project, workspace)[0] as { readonly face?: unknown } | undefined;
        return faceOf(row?.face);
      }),

    set: (project: string, workspace: string, face: Face) =>
      ask("record a workspace's face", () => {
        write.run(project, workspace, face);
      }),

    forget: (project: string, workspace: string) =>
      ask("forget a workspace's face", () => {
        drop.run(project, workspace);
      }),
  };
});

export const layer: Layer.Layer<Faces, never, Db> = Layer.effect(Faces)(make);
