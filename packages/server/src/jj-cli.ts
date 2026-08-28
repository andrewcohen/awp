import { Effect, FileSystem, Layer, Path, Result } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { type AddWorkspace, type DiffOf, Jj, type RevisionsIn, JjError } from "./jj";
import {
  REVISION_TEMPLATE,
  localBookmarks,
  parseBookmarks,
  parseRevisions,
  parseWorkspaces,
} from "./jj-parse";
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

  // ── the two that name a workspace instead of a repository ────────────────
  //
  // Both take a directory and hand it to `-R` unchanged, which is the same
  // thing `workspaceRoot` does above and the opposite of what every other
  // method here does. It is deliberate: `@` is *the working copy of the
  // workspace jj was pointed at*, so pointing these at the repository would
  // answer about the default workspace — never the one on screen.
  //
  // The wrong-repo mistake `-R` exists to prevent is still prevented. The
  // argument is required, it is checked, and it comes from a session's own
  // start directory rather than from the daemon's cwd.

  const revisions = ({ dir, revset, limit }: RevisionsIn) =>
    Effect.gen(function* () {
      const op = "list revisions";
      yield* required(op, "directory", dir);
      yield* required(op, "revset", revset);

      // `--no-graph`, because the graph characters are drawing for a terminal
      // and this answer is going through a template. `-n` because a stack
      // measured against a trunk that has not moved in a month is hundreds of
      // commits, and the caller is the one who knows how many it can show.
      const out = yield* run(op, [
        "-R",
        dir,
        "--ignore-working-copy",
        "log",
        "--no-graph",
        "-n",
        String(Math.max(1, Math.trunc(limit))),
        "-r",
        revset,
        "-T",
        REVISION_TEMPLATE,
      ]);
      return parseRevisions(out);
    });

  const diff = ({ dir, revision, snapshot }: DiffOf) =>
    Effect.gen(function* () {
      const op = `diff ${revision}`;
      yield* required(op, "directory", dir);
      yield* required(op, "revision", revision);

      // The one read in this file that may leave `--ignore-working-copy` off,
      // and the caller decides. See `DiffOf.snapshot` for why that is the
      // correct answer rather than a hole in the rule.
      return yield* run(op, [
        "-R",
        dir,
        ...(snapshot ? [] : ["--ignore-working-copy"]),
        "diff",
        "--git",
        "-r",
        revision,
      ]);
    });

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

    revisions,

    diff,

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

        const add = () =>
          write(op, repo, [
            "workspace",
            "add",
            "--name",
            name,
            ...(revision === undefined ? [] : ["-r", revision]),
            destination,
          ]);

        // ── a stale working copy is not this job's fault ──────────────────
        //
        // `jj workspace add` is a write, so it does not pass
        // `--ignore-working-copy` — and it *cannot*: jj refuses the flag
        // outright with "This command must be able to update the working
        // copy. Hint: Don't use --ignore-working-copy." That was the first
        // thing tried and it is a dead end, which is worth recording so it is
        // not tried again.
        //
        // So the command has to be able to touch a working copy, and the one
        // it touches is whichever workspace the *cwd* sits in — the daemon's
        // own. When that one is stale, creating an unrelated workspace fails
        // with a message about it:
        //
        //   Error: The working copy is stale (not updated since operation …)
        //   Hint: Run `jj workspace update-stale` to update it.
        //
        // jj's own hint, so it is followed rather than second-guessed, and
        // only when jj has said so. `update-stale` exits 0 and prints
        // "Attempted recovery, but the working copy is not stale" on a healthy
        // repository, so the recovery cannot make a different failure worse.
        //
        // Not reproducible synthetically: every stale state built by hand —
        // abandoning another workspace's working-copy commit, operating from
        // a foreign cwd, operating from a sibling workspace — was auto-updated
        // by jj without complaint. This path is written from the real failure
        // and its remedy, and is matched on jj's sentence for that reason.
        const first = yield* Effect.result(add());
        if (Result.isSuccess(first)) {
          return;
        }
        if (!isStale(first.failure)) {
          return yield* Effect.fail(first.failure);
        }
        yield* write(`${op} (recovering a stale working copy)`, repo, [
          "workspace",
          "update-stale",
        ]);
        yield* add();
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

    // Both of these are writes, so neither takes `--ignore-working-copy`: they
    // change the repository, and a snapshot out of step with the refs it just
    // imported is worse than the snapshot itself.
    fetch: (repo: string) =>
      write("fetch from git remotes", repo, ["git", "fetch"]).pipe(Effect.asVoid),

    importGit: (repo: string) =>
      write("import git refs", repo, ["git", "import"]).pipe(Effect.asVoid),

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
 * Did jj refuse because a working copy needs updating?
 *
 * Matched on jj's sentence, which is the only signal there is — the CLI exits 1
 * for every refusal alike. Narrow on purpose: `update-stale` is a real change
 * to a checkout a person may be standing in, so it runs when jj has named this
 * exact condition and never as a general retry.
 */
const isStale = (error: JjError): boolean =>
  error.reason.includes("working copy is stale") || error.reason.includes("workspace update-stale");

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
