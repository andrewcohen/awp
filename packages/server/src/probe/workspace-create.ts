// Does the create-workspace job actually work?
//
// `create-workspace.test.ts` proves the order of the steps and, above all, the
// order they are undone in — against fakes, because that is what makes the
// order visible. This proves the other half: that the four real services do
// what the fakes stood in for, over the wire, under Bun, in the daemon.
//
// ── what it touches, and what it refuses to ────────────────────────────────
// The repository is made here, in a temp directory, and thrown away. The
// workspace and the session it creates are its own — named `awp-probe` — and
// it kills only what it made. It never lists, attaches to, resizes or kills
// anything else.
//
// It does create a real zmx session for a moment, which is why it is a probe a
// person runs rather than a test:
//
//   bun run probe:workspace
//
// ── why this one does not refuse to run inside a session ───────────────────
// The other zmx probes refuse, and that rule is right for them: they call zmx
// in ways that resolve ZMX_SESSION, and a probe that merely *stripped* the
// marker still opened a new client — a session takes its size from whoever is
// looking at it, so it reflowed the session it was being run from.
//
// Nothing here does that. The session is created by the **daemon**, which is
// already outside any session. What this file runs is `zmx ls` and `zmx get`,
// which are read-only and safe from inside, and one `zmx kill` — which names
// the session this probe made and nothing else.
//
// So the guard is on the thing that actually matters, and it is stronger than
// a refusal would be: `ours` below rejects any name that is not this probe's,
// and every zmx call that changes anything goes through it. A blanket refusal
// would have been easier to write and would have guarded the wrong property.

import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { AwpClient, layerClient } from "@awp-kit/protocol/client";
import { Effect } from "effect";

const PROJECT = "awp-probe";
const WORKSPACE = "probe-1";
const SESSION = `awp.${PROJECT}.${WORKSPACE}.agent`;

const say = (label: string, ok: boolean, detail: string): void => {
  process.stdout.write(`${ok ? "  ok  " : "FAIL  "}${label.padEnd(30)}${detail}\n`);
};

const jj = (args: ReadonlyArray<string>): string => {
  try {
    return execFileSync("jj", [...args], { encoding: "utf8", stdio: "pipe" });
  } catch {
    return "";
  }
};

/**
 * The one prefix this probe is allowed to change.
 *
 * Not a formality. This machine has a dozen live sessions holding real work,
 * and the failure this prevents — a name computed from a record with a field
 * missing, landing on somebody's editor — is the failure `requireName` exists
 * for one layer down.
 */
const OURS = `awp.${PROJECT}.`;

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
      // The marker is neutralised by *setting* it, never by omitting it — an
      // absent key is a request the spawner is free to ignore, and this one
      // does. See `zmxChildEnv`.
      env: { ...process.env, ZMX_SESSION: "" },
    });
  } catch {
    return "";
  }
};

const scratch = mkdtempSync(join(tmpdir(), "awp-probe-"));
const repo = join(scratch, "repo");
const destination = join(process.env["HOME"] ?? "", ".awp", "workspaces", PROJECT, WORKSPACE);

const program = Effect.gen(function* () {
  const rpc = yield* AwpClient;

  process.stdout.write(`repo:      ${repo}\n`);
  process.stdout.write(`workspace: ${destination}\n\n`);

  const thread = yield* rpc.ThreadCreate({ title: "a probe" });
  say("made a thread", thread.id !== "", thread.id);

  const queued = yield* rpc.WorkspaceCreate({
    thread: thread.id,
    project: PROJECT,
    workspace: WORKSPACE,
    repo,
    base: "@",
    bookmark: "awp-probe/probe-1",
    // `sh` rather than an agent: this is about the wiring, and a real agent
    // would start talking to an API.
    agent: ["sh"],
  });
  say("enqueued", queued.kind === "create-workspace", `${queued.id} · ${queued.title}`);

  // Polled rather than streamed. The stream is the interesting path for a
  // window; here the only question is the outcome.
  let settled = queued;
  for (let tick = 0; tick < 300; tick += 1) {
    const found = yield* rpc.JobList();
    const current = found.find((entry) => entry.id === queued.id);
    if (current !== undefined) {
      settled = current;
      if (current.status === "succeeded" || current.status === "failed") {
        break;
      }
    }
    yield* Effect.sleep("100 millis");
  }

  say("job finished", settled.status === "succeeded", `${settled.status} ${settled.error ?? ""}`);
  say("every step done", settled.done.length === 4, settled.done.join(" · "));

  // ── and now what the other processes actually see ────────────────────────

  const workspaces = jj([
    "-R",
    repo,
    "--ignore-working-copy",
    "workspace",
    "list",
    "-T",
    'name ++ "\\n"',
  ]);
  say(
    "jj knows the workspace",
    workspaces.includes(WORKSPACE),
    workspaces.trim().split("\n").join(", "),
  );
  say("the directory is there", existsSync(destination), destination);

  const bookmarks = jj([
    "-R",
    repo,
    "--ignore-working-copy",
    "bookmark",
    "list",
    "-T",
    'name ++ "\\n"',
  ]);
  say(
    "the bookmark is set",
    bookmarks.includes("awp-probe/probe-1"),
    bookmarks.trim().split("\n").join(", "),
  );

  const sessions = zmx(["ls"]);
  say("the session exists", sessions.includes(SESSION), SESSION);

  const labels = zmx(["get", SESSION]);
  say(
    "the session is labelled",
    labels.includes(PROJECT) && labels.includes(WORKSPACE),
    labels.trim().split("\n").join(" "),
  );

  const threads = yield* rpc.ThreadList();
  const mine = threads.find((entry) => entry.id === thread.id);
  const claimed =
    mine?.members.some((m) => m.project === PROJECT && m.workspace === WORKSPACE) ?? false;
  say("the thread claims it", claimed, JSON.stringify(mine?.members ?? []));

  // ── the log, which is what a person reads when it goes wrong ─────────────
  const log = yield* rpc.JobLog({ job: queued.id });
  process.stdout.write(`\nwhat the job said:\n${log.map((line) => `  ${line}`).join("\n")}\n`);

  // The thread goes too. There is no delete — only archive — and archived
  // threads are filtered out of the sidebar, which is the property being used
  // here. Without this every run of the probe leaves a thread called "a probe"
  // on the strip, which is exactly what four runs did.
  yield* rpc.ThreadArchive({ thread: thread.id, archived: true });
  process.stdout.write("\n");
  say("thread archived", true, thread.id);
});

const cleanup = (): void => {
  process.stdout.write("\ncleaning up\n");
  // Only what this probe made, and by name every time.
  zmx(["kill", ours(SESSION), "--force"]);
  jj(["-R", repo, "workspace", "forget", WORKSPACE]);
  rmSync(destination, { recursive: true, force: true });
  // The project directory too — the job makes it, so the probe unmakes it.
  // Left behind, it is an empty `awp-probe` sitting beside real projects.
  rmSync(dirname(destination), { recursive: true, force: true });
  rmSync(scratch, { recursive: true, force: true });
  say("session gone", !zmx(["ls"]).includes(SESSION), SESSION);
  say("directory gone", !existsSync(destination), destination);
  say("project dir gone", !existsSync(dirname(destination)), dirname(destination));
};

execFileSync("jj", ["git", "init", repo], { stdio: "pipe" });
// A commit to start from, so `-r @` means something.
execFileSync("jj", ["-R", repo, "describe", "-m", "root"], { stdio: "pipe" });

try {
  await Effect.runPromise(
    program.pipe(Effect.provide(layerClient()), Effect.scoped) as Effect.Effect<void>,
  );
} finally {
  cleanup();
}
