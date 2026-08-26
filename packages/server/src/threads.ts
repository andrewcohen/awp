import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { type Thread, ThreadNotFound, type ThreadMember } from "@awp-kit/protocol";
import { Context, Data, Effect, Layer, Ref, Schema } from "effect";

// Where threads are kept, and the rules about what may be in them.
//
// ── a file, not a database ─────────────────────────────────────────────────
// Jobs went to sqlite because a job has an unbounded log, is written to several
// times a second while it runs, and must survive being killed halfway through
// one of those writes. A thread has none of those properties: a dozen records,
// written when a person types a title, read once at startup. What a plain file
// buys instead is that it can be opened in an editor and understood — which,
// while the shape of a thread is still being argued about, is the more useful
// property.
//
// It is written whole and moved into place, so a crash mid-write leaves the
// previous file rather than half of the new one. That is the one durability
// property sqlite was giving us that is actually needed here.
//
// ── a workspace belongs to one thread ──────────────────────────────────────
// Enforced in `attach`, which removes the pair from every other thread before
// adding it. The alternative — allowing two and resolving it on read — has no
// rendering: the sidebar would draw the workspace under both, and a person
// would have to work out which claim was the real one. Better to make the
// second claim win outright and say so.

/** The file could not be read or written. */
export class ThreadStoreError extends Data.TaggedError("ThreadStoreError")<{
  readonly op: string;
  readonly reason: string;
  readonly cause?: unknown;
}> {}

export class Threads extends Context.Service<
  Threads,
  {
    /** Every thread, newest first. Archived ones included — the caller filters. */
    readonly list: () => Effect.Effect<ReadonlyArray<Thread>, ThreadStoreError>;

    readonly create: (title: string) => Effect.Effect<Thread, ThreadStoreError>;

    readonly rename: (
      thread: string,
      title: string,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /** Archive, or bring back — `archived: false` undoes it. */
    readonly archive: (
      thread: string,
      archived: boolean,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /**
     * Claim a workspace for this thread, releasing it from any other.
     *
     * Idempotent: claiming a pair the thread already holds changes nothing and
     * is not an error, which is what lets a job step call it twice.
     */
    readonly attach: (
      thread: string,
      member: ThreadMember,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;

    /** Release a workspace. Also idempotent, and for the same reason. */
    readonly detach: (
      thread: string,
      member: ThreadMember,
    ) => Effect.Effect<Thread, ThreadStoreError | ThreadNotFound>;
  }
>()("awp/Threads") {}

/**
 * The shape of the file.
 *
 * Here for the reason the jobs database carries one, and with the opposite
 * consequence: this file is small and legible, so a shape change can be
 * migrated by reading it rather than by discarding it. A version that does not
 * match refuses to load — which is loud, and the right kind of loud, because
 * the alternative is a daemon that silently forgets every thread.
 */
const VERSION = 1;

/** The list with one thread swapped for a new version of itself. */
const replace = (all: ReadonlyArray<Thread>, updated: Thread): ReadonlyArray<Thread> =>
  all.map((entry) => (entry.id === updated.id ? updated : entry));

const same = (a: ThreadMember, b: ThreadMember): boolean =>
  a.project === b.project && a.workspace === b.workspace;

/**
 * A thread's id: the day it was made, and four characters to tell it from the
 * others made that day.
 *
 * The same spelling as a job id, deliberately. Both are things a person may end
 * up reading in a log line, and two id formats in one system is two things to
 * recognise for no gain.
 */
export const threadId = (now: Date, random: number): string => {
  const day = now.toISOString().slice(0, 10).replaceAll("-", "");
  const tail = Math.floor(random * 36 ** 4)
    .toString(36)
    .padStart(4, "0");
  return `${day}-${tail}`;
};

const read = (path: string): Effect.Effect<ReadonlyArray<Thread>, ThreadStoreError> =>
  Effect.tryPromise({
    try: async () => {
      const text = await readFile(path, "utf8").catch((cause: NodeJS.ErrnoException) => {
        // A missing file is an empty list, not a failure. The first run of a
        // daemon on a new machine is the common case, not the exceptional one.
        if (cause.code === "ENOENT") {
          return undefined;
        }
        throw cause;
      });
      if (text === undefined) {
        return [];
      }
      const parsed: unknown = JSON.parse(text);
      const decoded = Schema.decodeUnknownSync(
        Schema.Struct({ version: Schema.Int, threads: Schema.Array(Schema.Unknown) }),
      )(parsed);
      if (decoded.version !== VERSION) {
        throw new Error(`threads file is version ${decoded.version}, expected ${VERSION}`);
      }
      return decoded.threads as ReadonlyArray<Thread>;
    },
    catch: (cause) => new ThreadStoreError({ op: "read", reason: `cannot read ${path}`, cause }),
  }).pipe(Effect.map((found) => found.map(revive)));

/** JSON has no Date, so the two timestamps come back as strings. */
const revive = (raw: Thread): Thread => ({
  ...raw,
  createdAt: new Date(raw.createdAt),
  archivedAt: raw.archivedAt === undefined ? undefined : new Date(raw.archivedAt),
});

const write = (
  path: string,
  threads: ReadonlyArray<Thread>,
): Effect.Effect<void, ThreadStoreError> =>
  Effect.tryPromise({
    try: async () => {
      await mkdir(dirname(path), { recursive: true });
      const temporary = `${path}.writing`;
      // Written whole, then moved. `rename` is atomic within a filesystem, so a
      // crash between these two lines leaves the file that was already there —
      // which is the one failure mode a plain `writeFile` would get wrong.
      await writeFile(
        temporary,
        `${JSON.stringify({ version: VERSION, threads }, undefined, 2)}\n`,
      );
      await rename(temporary, path);
    },
    catch: (cause) => new ThreadStoreError({ op: "write", reason: `cannot write ${path}`, cause }),
  });

export const make = (
  path: string,
  now: () => Date = () => new Date(),
  random: () => number = Math.random,
) =>
  Effect.gen(function* () {
    // Read once, then held. The daemon is the only writer, so the file and this
    // Ref cannot disagree unless someone edits it underneath us — which is a
    // thing a person may well do, and which they will have to restart for. Said
    // plainly rather than defended against, because watching the file to
    // reconcile an edit is a great deal of machinery for a case that ends in a
    // restart anyway.
    const state = yield* Ref.make(yield* read(path));

    const save = (next: ReadonlyArray<Thread>) =>
      write(path, next).pipe(Effect.flatMap(() => Ref.set(state, next)));

    /** Find a thread, replace it, write the file, and hand back the new one. */
    const change = (
      thread: string,
      edit: (found: Thread, all: ReadonlyArray<Thread>) => ReadonlyArray<Thread>,
    ) =>
      Effect.gen(function* () {
        const all = yield* Ref.get(state);
        const found = all.find((entry) => entry.id === thread);
        if (found === undefined) {
          return yield* Effect.fail(new ThreadNotFound({ thread }));
        }
        const next = edit(found, all);
        yield* save(next);
        const updated = next.find((entry) => entry.id === thread);
        return updated ?? found;
      });

    return {
      list: () =>
        Ref.get(state).pipe(
          Effect.map((all) =>
            all.toSorted((a, b) => b.createdAt.getTime() - a.createdAt.getTime()),
          ),
        ),

      create: (title: string) =>
        Effect.gen(function* () {
          const all = yield* Ref.get(state);
          const made: Thread = {
            id: threadId(now(), random()),
            title: title.trim(),
            createdAt: now(),
            archivedAt: undefined,
            members: [],
          };
          yield* save([...all, made]);
          return made;
        }),

      rename: (thread: string, title: string) =>
        change(thread, (found, all) => replace(all, { ...found, title: title.trim() })),

      archive: (thread: string, archived: boolean) =>
        change(thread, (found, all) =>
          replace(all, { ...found, archivedAt: archived ? now() : undefined }),
        ),

      attach: (thread: string, member: ThreadMember) =>
        change(thread, (_found, all) =>
          all.map((entry) =>
            entry.id === thread
              ? {
                  ...entry,
                  members: entry.members.some((held) => same(held, member))
                    ? entry.members
                    : [...entry.members, member],
                }
              : // Every other thread lets it go — the one-thread rule, here
                // because this is the only place both threads are in hand.
                {
                  ...entry,
                  members: entry.members.filter((held) => !same(held, member)),
                },
          ),
        ),

      detach: (thread: string, member: ThreadMember) =>
        change(thread, (found, all) =>
          replace(all, {
            ...found,
            members: found.members.filter((held) => !same(held, member)),
          }),
        ),
    };
  });

export const layer = (path: string): Layer.Layer<Threads, ThreadStoreError> =>
  Layer.effect(Threads)(make(path));
