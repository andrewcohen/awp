// Whether a workspace with no session can be brought back, asked of a real
// daemon.
//
// ── what a test cannot say here ────────────────────────────────────────────
//
// `handlers.test.ts` proves what `SessionStart` hands the multiplexer — the
// directory, the command, the two environment variables. It cannot prove that
// zmx accepts any of it, and the one thing worth knowing is whether a session
// appears and stays: the workspace this was reported about had a session
// listed as `ended=… exit_code=127`, which is a session zmx made and a process
// that was not there.
//
//     bun run probe:session-start                      ws://127.0.0.1:5274
//     bun run probe:session-start ws://127.0.0.1:5284  a second instance
//
// ── the guard is on the property that matters ──────────────────────────────
//
// This one *acts*: it starts a real agent in a real workspace. So it refuses
// any pair outside `awp/*`, which is this repository's own — the same shape
// `probe:workspace` uses, and stronger than a blanket refusal to run inside a
// zmx session would be. Starting a session is not attaching, so nothing here
// resizes anything; `Multiplexer.start` is `zmx run -d` and leaves it alone.

import { Effect } from "effect";
import * as client from "@awp-kit/protocol/client";

const url = process.argv[2] ?? client.DEFAULT_DAEMON_URL;
const project = process.argv[3] ?? "awp";
const workspace = process.argv[4] ?? "test";

if (project !== "awp") {
  console.error(`refusing ${project}/${workspace}: this probe starts an agent, so it only`);
  console.error("touches a workspace this repository made. Pass an awp/* pair.");
  process.exit(1);
}

const program = Effect.gen(function* () {
  const rpc = yield* client.AwpClient;

  // Asked first, because it is the call the renderer makes for a workspace
  // with nothing running in it — and a wrong path here would send the diff
  // panel somewhere else entirely.
  const dir = yield* rpc.WorkspaceDir({ project, workspace });
  console.log(`  dir           ${dir}`);

  const before = yield* rpc.SessionList();
  const was = before.find(
    (session) => session.identity?.project === project && session.identity.workspace === workspace,
  );
  console.log(
    `  before        ${was === undefined ? "no session at all" : `${was.name} ended=${was.ended} exit=${was.exitCode}`}`,
  );

  const name = yield* rpc.SessionStart({ project, workspace });
  console.log(`  started       ${name}`);

  yield* Effect.sleep("2 seconds");
  const after = yield* rpc.SessionList();
  const now = after.find((session) => session.name === name);
  console.log(
    `  after         ${now === undefined ? "NOT IN THE LISTING" : `ended=${now.ended} exit=${now.exitCode} pid=${now.pid}`}`,
  );
  // The labels, because the name is shortened and cannot be split back apart —
  // so a session that started unlabelled is one the sidebar cannot group.
  console.log(
    `  identity      ${now?.identity === undefined ? "NONE — the labels did not land" : `${now.identity.project}/${now.identity.workspace}/${now.identity.kind}`}`,
  );

  // ── the wire cannot answer whether the AGENT came up ─────────────────────
  //
  // Every field in the listing above is about the *session*, and this repo has
  // a note about exactly that: `zmx ls` reports `ended` and `exit_code` for
  // the last **task**, not for the session, and the daemon overwrites `ended`
  // from the process table. So a session that has just been handed a command
  // reads, correctly and unhelpfully:
  //
  //   after   ended=false exit=127 pid=48016
  //           └─ alive   └─ the PREVIOUS task's exit, still the newest one
  //
  // Which is byte for byte what the same read said *before* the start, and
  // was taken as "nothing happened" once. `busy` is the field that answers it
  // and it is not on the wire — deliberately; `start` is its only caller.
  //
  // So it is read where it lives: a child of the session's pid, which is the
  // first half of the same rule `withProcesses` applies. A `-bash` with no
  // children is the one shape that is genuinely idle.
  if (now !== undefined) {
    const table = yield* Effect.promise(async () => {
      const ps = Bun.spawn(["ps", "-eo", "pid=,ppid=,comm="], { stdout: "pipe" });
      return await new Response(ps.stdout).text();
    });
    const children = table
      .split("\n")
      .map((line) => line.trim().split(/\s+/u))
      .filter((parts) => Number(parts[1]) === now.pid)
      .map((parts) => parts.slice(2).join(" "));
    console.log(
      `  running in it ${children.length === 0 ? "NOTHING — an idle shell, so the agent did not come up" : children.join(", ")}`,
    );
  }

  // Twice, because the daemon says it is idempotent and a second press is the
  // ordinary thing a person does when the first appeared to do nothing.
  const again = yield* rpc.SessionStart({ project, workspace });
  const twice = yield* rpc.SessionList();
  console.log(
    `  twice         ${again} · ${twice.filter((session) => session.name === name).length} session(s) by that name`,
  );
});

await Effect.runPromise(Effect.scoped(program).pipe(Effect.provide(client.layerClient(url))));
