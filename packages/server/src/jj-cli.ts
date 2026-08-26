import { Effect, FileSystem, Layer, Path } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { type AddWorkspace, Jj, JjError } from "./jj";
import { localBookmarks, parseBookmarks, parseWorkspaces } from "./jj-parse";
import { capture, said } from "./run";

// The jj CLI as a Jj.
//
// The only file that knows the service is a binary. Everything above it depends
// on the tag, so a caller can be handed a fake with no subprocess in sight.
//
// ── the two flags on every command ─────────────────────────────────────────
//
//   -R <repo>                names the repository, always
//   --ignore-working-copy    on reads, so a question changes nothing
//
// `-R` is the one that matters. jj finds a repository by walking up from the
// working directory, and the daemon's working directory is wherever it was
// launched — which is a real repository. Without `-R`, a command meant for a
// workspace under ~/.awp/workspaces would act on that one instead. Passing it
// on every call means the wrong-repo mistake is not available.
//
// `--ignore-working-copy` is the quieter one. jj snapshots the working copy
// before almost every command, including `workspace list`, so a read without
// it writes to the repository it was asked about. The reads here take it and
// the writes deliberately do not: a write is expected to touch the repo, and
// suppressing the snapshot there would create a commit out of step with the
// files beside it.
//
// ── the exit code is the answer ────────────────────────────────────────────
// Every command goes through `capture` rather than `spawner.string`, because
// `string` collects stdout and discards the exit code. jj reports a refusal by
// printing to stderr and exiting 1 — `Error: Workspace named 'second' already
// exists` — which through `string` is a successful empty answer. See run.ts.

const make = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
  const fs = yield* FileSystem.FileSystem;
  const path = yield* Path.Path;

  const run = (op: string, args: ReadonlyArray<string>) =>
    capture(spawner, ChildProcess.make("jj", [...args])).pipe(
      Effect.mapError(
        (cause) =>
          new JjError({
            op,
            // jj is named here because "command failed" is exactly what this
            // looks like when jj simply is not installed.
            reason: `${op}: jj failed (is it installed and on PATH?)`,
            cause,
          }),
      ),
      // On a refusal, jj's own sentence rather than one written here. It names
      // the workspace or the bookmark and what was wrong with it, and no
      // message composed at this distance would say it better.
      Effect.flatMap((captured) =>
        captured.exitCode === 0
          ? Effect.succeed(captured.stdout)
          : Effect.fail(new JjError({ op, reason: `${op}: ${said(captured)}` })),
      ),
    );

  /** A read: names the repo, changes nothing, answers in JSON. */
  const ask = (op: string, repo: string, args: ReadonlyArray<string>) =>
    required(op, "repository", repo).pipe(
      Effect.flatMap(() => run(op, ["-R", repo, "--ignore-working-copy", ...args])),
    );

  const write = (op: string, repo: string, args: ReadonlyArray<string>) =>
    required(op, "repository", repo).pipe(Effect.flatMap(() => run(op, ["-R", repo, ...args])));

  const workspaceRoot = (dir: string) =>
    required("find the workspace root", "directory", dir).pipe(
      Effect.flatMap(() => run("find the workspace root", ["-R", dir, "root"])),
      Effect.map((out) => out.trim()),
    );

  const workspaces = (repo: string) =>
    ask("list workspaces", repo, ["workspace", "list", "-T", TEMPLATE]).pipe(
      Effect.map(parseWorkspaces),
    );

  const bookmarks = (repo: string) =>
    ask("list bookmarks", repo, ["bookmark", "list", "--all-remotes", "-T", TEMPLATE]).pipe(
      Effect.map(parseBookmarks),
    );

  return {
    workspaceRoot,

    sourceRoot: (dir: string) =>
      Effect.gen(function* () {
        const workspace = yield* workspaceRoot(dir);
        const pointer = path.join(workspace, ".jj", "repo");

        // A primary workspace's `.jj/repo` is a directory and it *is* the
        // repository; only a secondary one has a file pointing elsewhere. So an
        // unreadable pointer is the ordinary case, not a failure.
        const contents = yield* fs.readFileString(pointer).pipe(
          Effect.map((text: string) => text.trim()),
          Effect.orElseSucceed(() => ""),
        );
        if (contents === "") {
          return workspace;
        }

        // Relative to the `.jj` directory the file sits in, which is what jj
        // writes — see the example on the tag.
        const resolved = path.resolve(path.join(workspace, ".jj"), contents);
        const repo = path.dirname(path.dirname(resolved));
        return repo === "" ? workspace : repo;
      }),

    workspaces,

    bookmarks,

    addWorkspace: ({ repo, name, destination, revision }: AddWorkspace) =>
      Effect.gen(function* () {
        const op = `add workspace ${name}`;
        yield* required(op, "name", name);
        yield* required(op, "destination", destination);

        // Asked first, so a retry after a later step failed is a no-op rather
        // than an error about a workspace that already exists. The runner
        // re-enters the step it failed on, so this is the ordinary path and not
        // the exceptional one.
        const found = yield* workspaces(repo);
        if (found.some((entry) => entry.name === name)) {
          return;
        }

        yield* write(op, repo, [
          "workspace",
          "add",
          "--name",
          name,
          ...(revision === undefined ? [] : ["-r", revision]),
          destination,
        ]);
      }),

    forgetWorkspace: (repo: string, name: string) =>
      Effect.gen(function* () {
        const op = `forget workspace ${name}`;
        // Refused rather than defaulted. `jj workspace forget` with no argument
        // forgets the workspace it is standing in, which for the daemon is this
        // repository — there is no reading of an empty name that is correct.
        yield* required(op, "name", name);

        const found = yield* workspaces(repo);
        if (!found.some((entry) => entry.name === name)) {
          return;
        }
        yield* write(op, repo, ["workspace", "forget", name]);
      }),

    setBookmark: (repo: string, name: string, revision: string) =>
      Effect.gen(function* () {
        const op = `set bookmark ${name}`;
        yield* required(op, "name", name);
        yield* required(op, "revision", revision);
        // Already idempotent: `bookmark set` creates or moves by name. Setting
        // it to where it already is does nothing, which is what a retry needs.
        yield* write(op, repo, ["bookmark", "set", "-r", revision, name]);
      }),

    deleteBookmark: (repo: string, name: string) =>
      Effect.gen(function* () {
        const op = `delete bookmark ${name}`;
        yield* required(op, "name", name);

        // `bookmark delete` fails on a name that is not there, so the question
        // comes first. Local only: a name can also appear as a remote row, and
        // deleting is about the local bookmark.
        const found = localBookmarks(yield* bookmarks(repo));
        if (!found.some((entry) => entry.name === name)) {
          return;
        }
        yield* write(op, repo, ["bookmark", "delete", name]);
      }),
  };
});

/**
 * One JSON object per line.
 *
 * jj's human output puts the name, a change id, a bookmark list and a
 * description on one line, and taking that apart is guesswork that breaks the
 * first time a description contains a colon. `json(self)` is a shape this
 * codebase asked for rather than one that happens to be current.
 */
const TEMPLATE = 'json(self) ++ "\\n"';

/**
 * Refuse an empty argument rather than handing jj a blank one and finding out
 * what it decides that means.
 *
 * Every one of these is computed by a caller from a record, and a caller
 * computing one from a record with a field missing is a thing this codebase has
 * been bitten by before. It matters most for `forgetWorkspace`, where jj's
 * reading of a missing name is destructive.
 */
const required = (op: string, what: string, value: string): Effect.Effect<void, JjError> =>
  value.trim() === ""
    ? Effect.fail(new JjError({ op, reason: `${op}: the ${what} is empty` }))
    : Effect.void;

export const layer = Layer.effect(Jj)(make);
