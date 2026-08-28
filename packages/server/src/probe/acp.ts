// Does asking Claude over ACP actually work, from where the daemon stands?
//
// Nothing about this is reachable from a test. `intent.test.ts` exercises the
// parsing and the validation against strings; what it cannot do is spawn a
// real adapter, which is where every one of the following lives:
//
//   the adapter resolves           a path into node_modules that bun may hoist
//   the version speaks to this CLI  0.16.2 initialises, opens a session, and
//                                   then answers a prompt with silence — see
//                                   below, it cost an hour
//   CLAUDECODE is neutralised       Claude Code refuses to run inside Claude
//                                   Code, and the daemon is usually started
//                                   from inside a session
//   the model answers               a login that has expired looks, from the
//                                   client's side, exactly like a slow model
//
// ── the version finding ────────────────────────────────────────────────────
//
// `@zed-industries/claude-code-acp` is renamed to
// `@agentclientprotocol/claude-agent-acp`. Both still publish, and the old
// name is eleven minor versions behind. Against claude 2.1.250:
//
//   0.16.2   initialize ✓   session/new ✓   session/prompt → nothing, ever
//   0.70.0   initialize ✓   session/new ✓   session/prompt ✓ 4.2s
//
// No error, no stderr, no stop reason. A client with a timeout reports that
// as "the model did not answer", which is a sentence about the wrong thing.
// That is the reason this probe prints each step's timing rather than a
// verdict: the shape of the failure is what names it.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// It runs in a temporary directory, disables tools, asks one question and
// leaves. It never invokes zmx, never attaches and never names a session.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Result } from "effect";
import { ChildProcessSpawner } from "effect/unstable/process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { INSTALL, adapterPath, ask, claudePath } from "../acp";
import { childEnv } from "../zmx-session";

const QUESTION = 'Reply with this JSON object and nothing else: {"ok": true}';

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  const adapter = adapterPath();
  console.log("\n  adapter  " + (adapter ?? `NOT INSTALLED — run:\n             ${INSTALL}`));
  // The binary the adapter drives. It is deliberately not the SDK's own copy:
  // that is a single 306MB file, and it would land in every workspace this
  // repo makes. See the note in acp.ts.
  console.log("  claude   " + (claudePath() ?? "NOT ON THE PATH"));
  // The parent's marker and the child's, side by side. The daemon is normally
  // started from inside a session, so `parent set` is the ordinary state and
  // the row is only interesting when the child's is not empty.
  const parent = process.env["CLAUDECODE"];
  const child = childEnv()["CLAUDECODE"];
  console.log(
    `  CLAUDECODE  parent [${parent === undefined ? "" : "set"}]   child [${child ?? "<absent>"}]`,
  );
  if (child !== "") {
    console.log("\n  the marker is not empty in the child. Claude Code will refuse to run.\n");
    return 1;
  }

  // A directory with no CLAUDE.md in it, deliberately: this measures the
  // protocol, and a project's own instructions change both the timing and
  // whether the answer arrives wrapped in a sentence.
  const dir = mkdtempSync(join(tmpdir(), "awp-acp-"));
  const started = Date.now();
  const answer = yield* Effect.result(ask(spawner, { cwd: dir, model: "haiku", prompt: QUESTION }));
  const took = Date.now() - started;
  rmSync(dir, { recursive: true, force: true });

  if (Result.isFailure(answer)) {
    console.log(`\n  failed after ${took}ms: ${answer.failure.reason}`);
    if (answer.failure.cause !== undefined) {
      console.log(`  cause: ${String(answer.failure.cause)}`);
    }
    console.log("");
    return 1;
  }

  const text = answer.success.trim();
  console.log(`  answered in ${took}ms\n`);
  console.log(`  ${text.replaceAll("\n", "\n  ")}\n`);
  // Not an equality check on the text. What is being proved is that a turn
  // completed and its words came back; what the model chose to say around the
  // object is the thing `findObject` exists to survive.
  if (text === "") {
    console.log("  the turn completed and said nothing.\n");
    return 1;
  }
  return 0;
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
