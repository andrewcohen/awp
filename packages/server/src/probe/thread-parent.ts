// Does a thread started from another thread actually land on its bookmark?
//
//   bun run probe:thread-parent
//
// `handlers.test.ts` proves which string `baseOfThread` returns, against a fake
// jj. That is the right shape for the *decision* and it cannot reach the thing
// that actually goes wrong: a base is a revset, and a revset that jj cannot
// resolve fails inside the job — one step in, in a message about bookmarks.
// This repository has already paid for that lesson once, when `bookmark set -r`
// was handed a workspace name and jj answered `Revision 'probe-1' doesn't
// exist`. Only a real run finds that class of mistake.
//
// So this makes a throwaway repository, builds a parent workspace in it, starts
// a second thread *from the first*, and then asks jj — from outside — whether
// the new workspace really descends from the parent's bookmark.
//
// ── what it touches, and what it refuses to ────────────────────────────────
// The repository is made here, in a temp directory, and thrown away. The
// workspaces and sessions are its own, named `awp-probe`, and it kills only
// what it made. The guard is `ours` below, which is the same one
// `workspace-create.ts` carries and is stronger than a blanket refusal: every
// zmx call that changes anything goes through it, and it rejects any name
// outside `awp.awp-probe.*`.
//
// It does not refuse to run inside a zmx session, for the same reason that one
// does not: the sessions are created by the daemon, which is already outside
// any session, and everything this file runs against zmx is either read-only
// or names a session it made.
//
// ── one model call, not two ────────────────────────────────────────────────
// The parent workspace is built with `WorkspaceCreate` directly, which takes a
// name; only the child goes through `ThreadStart`, which asks a model for one.
// The new code is entirely on the child's path, and a second ten-second call
// would buy nothing but a second chance for the model to be down.

import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { homedir } from "node:os";
import { join } from "node:path";
import { AwpClient, layerClient } from "@awp-kit/protocol/client";
import { Effect } from "effect";
import { SETTINGS_FILE } from "../settings";

const PROJECT = "awp-probe";
const PARENT = "parent-1";
const OURS = `awp.${PROJECT}.`;

const say = (label: string, ok: boolean, detail: string): void => {
  process.stdout.write(`${ok ? "  ok  " : "FAIL  "}${label.padEnd(32)}${detail}\n`);
};

const jj = (args: ReadonlyArray<string>): string => {
  try {
    return execFileSync("jj", [...args], { encoding: "utf8", stdio: "pipe" });
  } catch {
    return "";
  }
};

const ours = (name: string): string => {
  if (!name.startsWith(OURS)) {
    throw new Error(`refusing to touch ${name}: this probe only owns ${OURS}*`);
  }
  return name;
};

const zmx = (args: ReadonlyArray<string>): string => {
  try {
    return execFileSync("zmx", [...args], {
      encoding: "utf8",
      stdio: "pipe",
      // Neutralised by setting, never by omitting — an absent key is a request
      // the spawner is free to ignore, and this one does. See `zmxChildEnv`.
      env: { ...process.env, ZMX_SESSION: "" },
    });
  } catch {
    return "";
  }
};

/**
 * The bookmark prefix the daemon will use, read from the same file it reads.
 *
 * Not invented. The whole question is whether `baseOfThread` finds the bookmark
 * awp itself created, and it composes that name as `<prefix>/<workspace>` — so
 * a probe that made up its own prefix would be testing the fallback path while
 * appearing to test the hit.
 */
const prefix = (): string | undefined => {
  try {
    const parsed = JSON.parse(readFileSync(SETTINGS_FILE, "utf8")) as {
      readonly deck?: { readonly bookmark_prefix?: unknown };
    };
    const found = parsed.deck?.bookmark_prefix;
    return typeof found === "string" && found.trim() !== "" ? found.trim() : undefined;
  } catch {
    return undefined;
  }
};

/**
 * The threads this run made, so it can put them back afterwards.
 *
 * Module scope rather than a return value, because the tidying has to happen
 * whether the checks passed or threw — and a probe that leaves threads in a
 * person's sidebar claiming workspaces it has just deleted is a probe that
 * makes work rather than answering a question. There is no way to delete a
 * thread on purpose, which is deliberate; archiving is the honest end for one.
 */
const made: { project: string; workspace: string; thread: string }[] = [];

const scratch = mkdtempSync(join(tmpdir(), "awp-parent-"));
const repo = join(scratch, "repo");
const workspaces = join(homedir(), ".awp", "workspaces", PROJECT);

/** The commit a revset resolves to, or "" — for comparing two of them. */
const commitAt = (revset: string): string =>
  jj([
    "-R",
    repo,
    "--ignore-working-copy",
    "log",
    "-r",
    revset,
    "--no-graph",
    "-T",
    "commit_id",
  ]).trim();

const program = Effect.gen(function* () {
  const rpc = yield* AwpClient;
  const bookmarkPrefix = prefix();

  /**
   * Poll a job to a terminal state.
   *
   * A closure over `rpc` rather than a free function, which is not only tidier:
   * the client's type is derived from the contract and naming it at a module
   * boundary means restating a type the RpcGroup already computes.
   *
   * Polled rather than streamed. The change stream is the interesting path for
   * a window; here the only question is the outcome.
   */
  const settle = (id: string) =>
    Effect.gen(function* () {
      for (let tick = 0; tick < 900; tick += 1) {
        const found = yield* rpc.JobList();
        const current = found.find((entry) => entry.id === id);
        if (
          current !== undefined &&
          (current.status === "succeeded" || current.status === "failed")
        ) {
          return current;
        }
        yield* Effect.sleep("100 millis");
      }
      return yield* Effect.die(new Error(`job ${id} never settled`));
    });

  process.stdout.write(`repo:   ${repo}\n`);
  process.stdout.write(`prefix: ${bookmarkPrefix ?? "(none configured)"}\n\n`);

  // ── a parent thread with a real workspace and a real bookmark ────────────

  const parentBookmark = bookmarkPrefix === undefined ? undefined : `${bookmarkPrefix}/${PARENT}`;

  const first = yield* rpc.ThreadCreate({ title: "a probe parent" });
  made.push({ project: PROJECT, workspace: PARENT, thread: first.id });
  const built = yield* rpc.WorkspaceCreate({
    thread: first.id,
    label: first.title,
    // Named up front: the naming step short-circuits, so the parent costs no
    // model call. Only the child goes through ThreadStart.
    description: first.title,
    project: PROJECT,
    workspace: PARENT,
    repo,
    base: "@",
    bookmark: parentBookmark,
    // `sh` rather than an agent: this is about the wiring, and a real agent
    // would start talking to an API.
    agent: ["sh"],
  });
  const settledFirst = yield* settle(built.id);
  say("parent workspace built", settledFirst.status === "succeeded", settledFirst.error ?? "");

  // A commit only the parent has, so "descends from the parent" is a question
  // with a visible answer rather than one trunk would satisfy by accident.
  //
  // **`-R` is the parent's own directory, not the repository.** jj snapshots
  // the working copy of the workspace the command is pointed at, and pointing
  // it at `repo` snapshots the *default* workspace — so the file lands in the
  // parent's directory and never reaches `parent-1@`. The first run of this
  // probe did exactly that: every other check passed, because the branching
  // was right, and only this one caught that the commit was empty.
  const parentDir = join(workspaces, PARENT);
  writeFileSync(join(parentDir, "only-on-the-parent.txt"), "marker\n");
  jj(["-R", parentDir, "describe", "-m", "work done on the parent"]);
  if (parentBookmark !== undefined) {
    jj(["-R", parentDir, "bookmark", "set", "-r", "@", parentBookmark]);
  }

  const tip = parentBookmark === undefined ? commitAt(`${PARENT}@`) : commitAt(parentBookmark);
  say(
    "the parent has a tip to branch from",
    tip !== "",
    `${parentBookmark ?? `${PARENT}@`} ${tip.slice(0, 12)}`,
  );

  // ── and now the thing under test ─────────────────────────────────────────

  const started = yield* rpc.ThreadStart({
    description: "follow on from the parent probe",
    project: PROJECT,
    from: repo,
    parent: first.id,
  });

  say(
    "the child records its parent",
    started.thread.parentId === first.id,
    started.thread.parentId ?? "(none)",
  );

  const queued = started.job.input as { readonly base?: unknown; readonly workspace?: unknown };
  const base = String(queued.base ?? "");

  // The correction this probe exists for. `<name>@` is the working copy and
  // carries whatever is uncommitted; the bookmark is where the work is named.
  say(
    "it branched from the bookmark",
    parentBookmark === undefined ? base === `${PARENT}@` : base === parentBookmark,
    base,
  );

  // **Not on the record yet, and that is the point.** Naming moved into the
  // job's first step so this call could return immediately, so the name only
  // exists once the job has run. Reading it from the *enqueued* record is what
  // this probe did first, and it produced a workspace path with nothing on the
  // end of it — which is exactly the shape of the bug a probe is for.
  say("no name at enqueue — the call did not wait", queued.workspace === undefined, "");

  const settledChild = yield* settle(started.job.id);
  const child = String((settledChild.input as { readonly workspace?: unknown }).workspace ?? "");
  made.push({ project: PROJECT, workspace: child, thread: started.thread.id });
  say("the job recorded the name it was given", child !== "", child);
  say(
    "the child's job finished",
    settledChild.status === "succeeded",
    `${settledChild.status} ${settledChild.error ?? ""}`,
  );

  // ── what jj actually sees, which is the only answer that counts ──────────

  const landed = commitAt(`${child}@-`);
  say(
    "jj put it on the parent's tip",
    landed !== "" && landed === tip,
    `${landed.slice(0, 12)} vs ${tip.slice(0, 12)}`,
  );
  say(
    "the parent's file came with it",
    existsSync(join(workspaces, child, "only-on-the-parent.txt")),
    join(workspaces, child),
  );

  return { child };
});

const cleanup = (child: string | undefined): void => {
  process.stdout.write("\ncleaning up\n");
  for (const name of [PARENT, child].filter((entry): entry is string => entry !== undefined)) {
    // Every destructive call names something this probe made, and `ours`
    // refuses anything else.
    zmx(["kill", ours(`awp.${PROJECT}.${name}.agent`)]);
    jj(["-R", repo, "workspace", "forget", name]);
    rmSync(join(workspaces, name), { recursive: true, force: true });
    say("removed", !existsSync(join(workspaces, name)), join(workspaces, name));
  }
  rmSync(scratch, { recursive: true, force: true });
  rmSync(workspaces, { recursive: true, force: true });
};

/** Release the workspaces this run claimed, and archive the threads. */
const tidyThreads = Effect.gen(function* () {
  const rpc = yield* AwpClient;
  for (const entry of made) {
    yield* rpc
      .ThreadDetach({
        thread: entry.thread,
        member: { project: entry.project, workspace: entry.workspace },
      })
      .pipe(Effect.ignore);
    yield* rpc.ThreadArchive({ thread: entry.thread, archived: true }).pipe(Effect.ignore);
  }
  say("threads put back", true, made.map((entry) => entry.thread).join(" · "));
});

const main = async (): Promise<void> => {
  execFileSync("jj", ["git", "init", repo], { stdio: "pipe" });
  writeFileSync(join(repo, "readme.md"), "a probe\n");
  jj(["-R", repo, "describe", "-m", "the first commit"]);
  jj(["-R", repo, "bookmark", "set", "-r", "@", "main"]);
  jj(["-R", repo, "new"]);

  let child: string | undefined;
  try {
    const out = await Effect.runPromise(program.pipe(Effect.provide(layerClient()), Effect.scoped));
    child = out.child;
  } catch (cause) {
    process.stdout.write(`\nFAIL  ${String(cause)}\n`);
    process.exitCode = 1;
  } finally {
    cleanup(child);
    await Effect.runPromise(
      tidyThreads.pipe(Effect.provide(layerClient()), Effect.scoped, Effect.ignore),
    );
  }
};

await main();
