import type { Project } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, FileSystem, Layer, Path } from "effect";

// The repositories awp has been told about.
//
// ── why this is a record at all ────────────────────────────────────────────
// The window used to derive its project list from the running sessions: a
// project existed because something was running in it. That is backwards. The
// moment a person wants to name a project is usually the moment *nothing* is
// running in it yet, so the very first thread in any repository could not be
// started from the window — the picker was empty exactly when it was needed.
//
// So an import is a claim somebody made, and claims get written down. The Go
// implementation wrote a `default` workspace entry into its state file for the
// same reason; a table is the same idea with the constraint enforced rather
// than remembered.
//
// ── the name is the identity, and it is the basename ───────────────────────
// Not a surrogate id. Everything downstream is already built on the name —
// `sessionName` composes `awp.<project>.<workspace>.<kind>`, the sidebar groups
// on it, the address in the URL carries it — so a project whose name did not
// match its sessions' would be a project the window could not connect to
// anything.
//
// Two repositories with the same basename are therefore refused rather than
// disambiguated. There is nowhere to put the second one: a session name has one
// slot for a project, and inventing `widgets-2` would make an address that
// nothing else in the system would ever produce.
//
// ── forgetting takes nothing with it ───────────────────────────────────────
// No workspace removed, no session killed, no thread touched. That is what
// makes it safe to offer beside a name in a picker. A project with sessions
// still running simply reappears in the list, derived — which reads correctly:
// awp does still know about it, it is just no longer *claimed*.

/** The database would not answer. */
export class ProjectStoreError extends Data.TaggedError("ProjectStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The projects table.
 *
 * `root` is UNIQUE as well as `name`, and both constraints earn their place:
 * the name is the address, and the root is what stops one repository being
 * imported twice through two paths that resolve to it. `Jj.sourceRoot` gives
 * the second one — the caller resolves before recording — so by the time a row
 * is written the two are one-to-one.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "projects.001-initial",
    up: [
      `create table projects (
         name        text primary key,
         root        text not null unique,
         imported_at integer not null
       ) strict`,
    ],
  },
];

/** A name that is taken, and by what. Distinct from a store failure. */
export class ProjectNameTaken extends Data.TaggedError("ProjectNameTaken")<{
  readonly name: string;
  readonly root: string;
  /** The root already holding the name — what the message has to say. */
  readonly held: string;
}> {}

export class Projects extends Context.Service<
  Projects,
  {
    /** Every imported project, by name. */
    readonly list: () => Effect.Effect<ReadonlyArray<Project>, ProjectStoreError>;

    /**
     * Write one down.
     *
     * Idempotent for the pair it already holds — re-importing the same
     * repository answers with the row that is there rather than failing, which
     * is what lets a person click a name in a candidate list twice without
     * being told off. A *different* root under a name that is taken is the one
     * real conflict, and it refuses by name.
     */
    readonly record: (
      name: string,
      root: string,
    ) => Effect.Effect<Project, ProjectStoreError | ProjectNameTaken>;

    /** Forget one. Answers whether there was anything to forget. */
    readonly forget: (name: string) => Effect.Effect<boolean, ProjectStoreError>;
  }
>()("awp/Projects") {}

const ask = <A>(reason: string, run: () => A): Effect.Effect<A, ProjectStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error) => new ProjectStoreError({ reason, cause: error.cause })),
  );

const rowToProject = (row: Record<string, unknown>): Project => ({
  name: String(row["name"]),
  root: String(row["root"]),
  importedAt: new Date(Number(row["imported_at"])),
});

export const make = Effect.gen(function* () {
  const db = yield* Db;

  const readAll = db.prepare("select * from projects order by name");
  const readOne = db.prepare("select * from projects where name = ?");
  const insert = db.prepare("insert into projects (name, root, imported_at) values (?, ?, ?)");
  const drop = db.prepare("delete from projects where name = ?");

  return {
    list: (): Effect.Effect<ReadonlyArray<Project>, ProjectStoreError> =>
      ask("cannot list projects", () => readAll.all().map(rowToProject)),

    // A generator rather than a pipe chain, and not by taste: a `flatMap`
    // whose branches answer with different error types degrades to
    // `Effect<unknown, unknown, unknown>` here, and the failure lands as an
    // unreadable assignability error about the layer four lines below.
    record: (name: string, root: string) =>
      Effect.gen(function* () {
        const [existing] = yield* ask(`cannot import ${name}`, () =>
          readOne.all(name).map(rowToProject),
        );
        if (existing !== undefined) {
          if (existing.root === root) {
            return existing;
          }
          return yield* Effect.fail(new ProjectNameTaken({ name, root, held: existing.root }));
        }
        const made: Project = { name, root, importedAt: new Date() };
        return yield* ask(`cannot import ${name}`, () => {
          insert.run(made.name, made.root, made.importedAt?.getTime() ?? 0);
          return made;
        });
      }),

    forget: (name: string) =>
      ask(`cannot forget ${name}`, () => {
        const before = readOne.all(name).length;
        drop.run(name);
        return before > 0;
      }),
  };
});

export const layer: Layer.Layer<Projects, never, Db> = Layer.effect(Projects)(make);

// ── finding repositories to offer ──────────────────────────────────────────
//
// `deck.project_roots` is a list of directories to look under. The walk stops
// at the first repository it finds on a branch rather than descending through
// it, because a repository's own subdirectories are not projects — and because
// a jj repo holds `.jj/repo/store`, which is thousands of files nobody wants
// walked to find nothing.
//
// This is a convenience over importing a path, and it is deliberately second:
// a machine with no roots configured gets an empty list, which is not a failure
// and does not stop anything. The path route works with no config at all.

/** How far under a root to look. The Go implementation's number. */
const MAX_DEPTH = 4;

/**
 * The marker. `.jj` and not `.git`, and that is not an oversight.
 *
 * Every operation awp performs on a project is a jj one — `workspace add`,
 * `bookmark set`, the revsets the diff panel asks for — so a git-only
 * repository offered here would be a row that fails on import and, if it did
 * not, a project nothing else could act on. Offering it would be offering a
 * failure, which is the same rule that keeps remote-only bookmarks out of the
 * thread-base picker.
 */
const MARKER = ".jj";

/** A directory that will never hold a project worth offering. */
const SKIP = new Set(["node_modules", "target", "vendor", "Library", "Applications"]);

/** Tilde-expanded and absolute, which is what a config file will not be. */
export const expand = (path: string, home: string): string =>
  path.startsWith("~") ? `${home}${path.slice(1)}` : path;

/**
 * The nearest ancestor of `from` that is a repository, or nothing.
 *
 * **`jj -R <dir> root` does not walk up**, and finding that out is what this
 * function exists for. `-R` names a repository *exactly* — the walking-up that
 * jj does from a working directory happens only when `-R` is absent, which the
 * daemon never omits because its own cwd is a real repository. So handing
 * `~/code/thicket/src` straight to `sourceRoot` answers:
 *
 *   Error: There is no jj repo in ".../thicket/src"
 *
 * which is a true sentence about a path a person had every reason to type. The
 * walk is the difference between "point awp at your project" and "give awp the
 * exact directory holding `.jj`".
 *
 * Stops at the filesystem root rather than counting levels: the caller is a
 * person naming a directory, not a scan, so there is no runaway to bound.
 */
export const nearestRepo = (
  from: string,
): Effect.Effect<string | undefined, never, FileSystem.FileSystem | Path.Path> =>
  Effect.gen(function* () {
    const files = yield* FileSystem.FileSystem;
    const path = yield* Path.Path;
    let at = path.resolve(from);
    for (;;) {
      const here = yield* files
        .exists(path.join(at, MARKER))
        .pipe(Effect.catchCause(() => Effect.succeed(false)));
      if (here) {
        return at;
      }
      const up = path.dirname(at);
      if (up === at) {
        return undefined;
      }
      at = up;
    }
  });

/**
 * The repositories under `roots`, shallowest first, each named by its basename.
 *
 * Never fails: a root that has been deleted, renamed or is unreadable is
 * skipped. A picker that refused to open because one entry in somebody's config
 * pointed at a disk they unplugged would be worse than a short list.
 *
 * `importedAt` is absent on everything here — these are *found*, not claimed,
 * and the window needs to tell them apart to know which one an import button
 * applies to.
 */
export const discover = (
  roots: ReadonlyArray<string>,
): Effect.Effect<ReadonlyArray<Project>, never, FileSystem.FileSystem | Path.Path> =>
  Effect.gen(function* () {
    const files = yield* FileSystem.FileSystem;
    const path = yield* Path.Path;
    const found = new Map<string, string>();

    const isRepo = (dir: string) =>
      files.exists(path.join(dir, MARKER)).pipe(Effect.catchCause(() => Effect.succeed(false)));

    const walk = (dir: string, depth: number): Effect.Effect<void> =>
      Effect.gen(function* () {
        if (yield* isRepo(dir)) {
          // Named once. Two roots overlapping — `~/p` and `~/p/work` — is an
          // ordinary way to write that config, and the same repository arriving
          // twice under two names is the thing the map prevents.
          const name = path.basename(dir);
          if (!found.has(name)) {
            found.set(name, dir);
          }
          return;
        }
        if (depth >= MAX_DEPTH) {
          return;
        }
        const entries = yield* files
          .readDirectory(dir)
          .pipe(Effect.catchCause(() => Effect.succeed([] as ReadonlyArray<string>)));
        for (const entry of entries) {
          if (entry.startsWith(".") || SKIP.has(entry)) {
            continue;
          }
          const child = path.join(dir, entry);
          const directory = yield* files.stat(child).pipe(
            Effect.map((info) => info.type === "Directory"),
            Effect.catchCause(() => Effect.succeed(false)),
          );
          if (directory) {
            yield* walk(child, depth + 1);
          }
        }
      });

    for (const root of roots) {
      yield* walk(root, 0);
    }

    return [...found].map(([name, root]) => ({ name, root, importedAt: undefined }));
  });
