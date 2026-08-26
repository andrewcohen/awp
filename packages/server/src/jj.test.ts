import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Result } from "effect";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { Jj } from "./jj";
import { localBookmarks } from "./jj-parse";
import * as JjCli from "./jj-cli";

// These run against a REAL jj, in a repository made for the purpose.
//
// Unlike the zmx suite — which can only ask questions, because attaching has a
// consequence for a session someone is looking at — jj can be tested in full.
// A repository in a temp directory is nobody's work, so the mutating half is
// as safe to exercise as the reading half, and the mutating half is where every
// claim on this service actually lives.
//
// Nothing here ever names a repository outside the temp directory. That is not
// a convention: `-R` is a required argument on every method, so there is no
// call that could reach one by accident.
//
// What is being proved is idempotence. The jobs runner re-enters the step it
// failed on, so a step that ran and then failed later is run a second time —
// `addWorkspace` on a workspace that exists has to succeed, not error. Every
// one of these does the thing twice.

const platform = NodeChildProcessSpawner.layer.pipe(
  Layer.provideMerge(NodeFileSystem.layer),
  Layer.provideMerge(NodePath.layer),
);

const scratch = mkdtempSync(join(tmpdir(), "awp-jj-"));
const repo = join(scratch, "repo");

beforeAll(() => {
  execFileSync("jj", ["git", "init", repo], { stdio: "pipe" });
});
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

const on = <A, E>(f: (jj: Jj["Service"]) => Effect.Effect<A, E>): Promise<A> =>
  Effect.runPromise(
    Effect.gen(function* () {
      const jj = yield* Jj;
      return yield* f(jj);
    }).pipe(Effect.provide(JjCli.layer), Effect.provide(platform)) as Effect.Effect<A, E>,
  );

/** The failure, for the cases where the refusal is the point. */
const failure = <A, E>(f: (jj: Jj["Service"]) => Effect.Effect<A, E>): Promise<E | undefined> =>
  on((jj) => Effect.result(f(jj))).then((outcome) =>
    Result.isFailure(outcome) ? outcome.failure : undefined,
  );

describe("reading", () => {
  test("a fresh repository has one workspace, called default", async () => {
    const found = await on((jj) => jj.workspaces(repo));
    expect(found.map((entry) => entry.name)).toEqual(["default"]);
  });

  test("a workspace reports the commit it is sitting on", async () => {
    const found = await on((jj) => jj.workspaces(repo));
    expect(found[0]?.commitId).toMatch(/^[0-9a-f]{40}$/u);
    expect(found[0]?.changeId).not.toBe("");
  });

  test("workspaceRoot turns a directory into the workspace containing it", async () => {
    const found = await on((jj) => jj.workspaceRoot(repo));
    // Compared by suffix rather than to `repo` directly: macOS puts the temp
    // directory behind /private, and jj answers with the real path.
    expect(found.endsWith("/repo")).toBe(true);
  });

  test("sourceRoot of a primary workspace is the workspace itself", async () => {
    // `.jj/repo` is a directory here, so there is no pointer to follow and the
    // answer is unchanged. This is the case that makes the fallback ordinary
    // rather than exceptional.
    const found = await on((jj) => jj.sourceRoot(repo));
    expect(found.endsWith("/repo")).toBe(true);
  });

  test("sourceRoot of a secondary workspace is the repository it belongs to", async () => {
    // The reason `sourceRoot` exists and `root` was renamed. `jj root` is a
    // shortcut for `jj workspace root`, so inside a secondary workspace it
    // answers with that workspace — and this repository *is* a secondary
    // workspace, which makes the wrong reading the one you get while
    // developing here. A secondary workspace's `.jj/repo` is a file holding a
    // path to the real repository's, and following it is the whole method.
    const secondary = join(scratch, "elsewhere");
    await on((jj) => jj.addWorkspace({ repo, name: "elsewhere", destination: secondary }));

    const workspace = await on((jj) => jj.workspaceRoot(secondary));
    const source = await on((jj) => jj.sourceRoot(secondary));

    expect(workspace.endsWith("/elsewhere")).toBe(true);
    expect(source.endsWith("/repo")).toBe(true);
    expect(source).not.toBe(workspace);

    await on((jj) => jj.forgetWorkspace(repo, "elsewhere"));
    rmSync(secondary, { recursive: true, force: true });
  });

  test("reading does not write — the working copy is not snapshotted", async () => {
    // Every read passes --ignore-working-copy. Without it `workspace list`
    // takes a snapshot, which makes a question into a change to the thing it is
    // asking about. The operation log is where that would show.
    const before = execFileSync(
      "jj",
      ["-R", repo, "--ignore-working-copy", "op", "log", "-T", "'x'"],
      {
        encoding: "utf8",
      },
    );
    await on((jj) => jj.workspaces(repo));
    const after = execFileSync(
      "jj",
      ["-R", repo, "--ignore-working-copy", "op", "log", "-T", "'x'"],
      {
        encoding: "utf8",
      },
    );

    expect(after).toBe(before);
  });
});

describe("workspaces", () => {
  const name = "second";
  const destination = () => join(scratch, "second");

  test("adding one puts it on disk and in the list", async () => {
    await on((jj) => jj.addWorkspace({ repo, name, destination: destination() }));

    const found = await on((jj) => jj.workspaces(repo));
    expect(found.map((entry) => entry.name)).toContain(name);
    expect(existsSync(destination())).toBe(true);
  });

  test("adding it again succeeds and changes nothing", async () => {
    // The whole point. A job step that made this workspace and then failed at
    // step four is re-entered here on the next attempt.
    await on((jj) => jj.addWorkspace({ repo, name, destination: destination() }));

    const found = await on((jj) => jj.workspaces(repo));
    expect(found.filter((entry) => entry.name === name)).toHaveLength(1);
  });

  test("forgetting one removes it from the list but leaves the directory", async () => {
    await on((jj) => jj.forgetWorkspace(repo, name));

    const found = await on((jj) => jj.workspaces(repo));
    expect(found.map((entry) => entry.name)).not.toContain(name);
    // jj says so in its own help, and it matters: the job that undoes a
    // workspace creation has to forget it *and* remove the directory.
    expect(existsSync(destination())).toBe(true);
  });

  test("forgetting one that is already gone succeeds", async () => {
    await on((jj) => jj.forgetWorkspace(repo, name));
    const found = await on((jj) => jj.workspaces(repo));
    expect(found.map((entry) => entry.name)).toEqual(["default"]);
  });

  test("forgetting with no name is refused, not defaulted", async () => {
    // `jj workspace forget` with no argument forgets the workspace it is
    // standing in. For the daemon that is the awp repository itself. This is
    // the one refusal on the service that prevents a destructive default rather
    // than a confusing one.
    const error = await failure((jj) => jj.forgetWorkspace(repo, "  "));
    expect(error).toMatchObject({ reason: expect.stringContaining("name is empty") });

    // And nothing happened.
    const found = await on((jj) => jj.workspaces(repo));
    expect(found.map((entry) => entry.name)).toEqual(["default"]);
  });

  test("a call with no repository is refused before jj runs", async () => {
    const error = await failure((jj) => jj.workspaces(""));
    expect(error).toMatchObject({ reason: expect.stringContaining("repository is empty") });
  });

  test("a directory that is not a repository fails, and says what jj said", async () => {
    // The regression test for the hole this service was built through.
    // `ChildProcessSpawner.string` collects stdout and discards the exit code,
    // so a jj that printed an error to stderr and exited 1 came back as a
    // successful empty answer — and `workspaces` reported none rather than
    // failing. Everything here goes through `capture` instead; see run.ts.
    const error = await failure((jj) => jj.workspaces(scratch));

    expect(error).toBeDefined();
    // jj's own sentence, not one composed at this distance.
    expect(String((error as { readonly reason: string }).reason)).toMatch(/repo|jj/iu);
  });
});

describe("bookmarks", () => {
  const name = "andrew/a-test-bookmark";

  test("setting one creates it", async () => {
    await on((jj) => jj.setBookmark(repo, name, "@"));

    const found = await on((jj) => jj.bookmarks(repo));
    expect(found.map((entry) => entry.name)).toContain(name);
  });

  test("a set bookmark is listed twice — locally, and on the git remote", async () => {
    // Not a quirk of this repository. `jj git init` gives the repo a git
    // backend, and setting a bookmark exports it there, so the same name comes
    // back on two rows. Counting names without filtering is therefore wrong
    // everywhere, not only here — which is what this test is for, because the
    // first draft of the suite below got it wrong exactly that way.
    const all = await on((jj) => jj.bookmarks(repo));
    const named = all.filter((entry) => entry.name === name);

    expect(named.length).toBeGreaterThan(1);
    expect(named.filter((entry) => entry.remote === undefined)).toHaveLength(1);
    expect(named.some((entry) => entry.remote === "git")).toBe(true);
  });

  test("setting it again to the same place succeeds", async () => {
    await on((jj) => jj.setBookmark(repo, name, "@"));

    const local = localBookmarks(await on((jj) => jj.bookmarks(repo)));
    expect(local.filter((entry) => entry.name === name)).toHaveLength(1);
  });

  test("deleting one removes the local bookmark", async () => {
    await on((jj) => jj.deleteBookmark(repo, name));

    const local = localBookmarks(await on((jj) => jj.bookmarks(repo)));
    expect(local.map((entry) => entry.name)).not.toContain(name);
  });

  test("deleting one that is not there succeeds", async () => {
    // `jj bookmark delete` fails on a name it cannot find, so this only works
    // because the service asks first — and asks the *local* list, which is the
    // reason the row above matters.
    await on((jj) => jj.deleteBookmark(repo, name));

    const local = localBookmarks(await on((jj) => jj.bookmarks(repo)));
    expect(local.map((entry) => entry.name)).not.toContain(name);
  });
});
