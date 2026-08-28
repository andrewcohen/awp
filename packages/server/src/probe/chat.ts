// Does a conversation actually work, from where the daemon stands?
//
// `chat.test.ts` exercises the parsing against updates copied off a real turn.
// What it cannot do is spawn an adapter, so every one of these lives here:
//
//   the session resolves        session/list, and the cwd it answers with is
//                               the RESOLVED path — the reason nothing here
//                               composes a slug
//   a turn arrives in order     one renderer draws history and present alike,
//                               which only holds if they are the same shape
//   a tool is five updates      merged by id, or one `cat` draws five rows
//   load replays                open it twice and the second sees the first
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// A temporary directory, one file in it, one question. It never invokes zmx,
// never attaches and never names a session.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Ref, Stream } from "effect";
import { ChildProcessSpawner } from "effect/unstable/process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ChatUpdate } from "@awp-kit/protocol";
import { conversation } from "../chat";

const WORD = "heron";

/** What a window would hold: messages in order, tools merged by id. */
const draw = (updates: ReadonlyArray<ChatUpdate>): void => {
  const tools = new Map<string, ChatUpdate>();
  let said = "";
  for (const update of updates) {
    if (update.kind === "message" && update.role === "agent") {
      said += update.text ?? "";
    }
    if (update.kind === "tool" && update.id !== undefined) {
      tools.set(update.id, { ...tools.get(update.id), ...update });
    }
  }
  console.log(`  said        "${said.trim().replaceAll("\n", " ").slice(0, 120)}"`);
  console.log(`  tools       ${tools.size}`);
  for (const tool of tools.values()) {
    console.log(
      `    ${String(tool.status).padEnd(10)} ${String(tool.title).slice(0, 60)}` +
        `  →  ${String(tool.output ?? "")
          .replaceAll("\n", " ")
          .slice(0, 60)}`,
    );
  }
};

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  const dir = mkdtempSync(join(tmpdir(), "awp-chat-"));
  writeFileSync(join(dir, "notes.txt"), `the word is: ${WORD}\n`);
  console.log(`\n  cwd         ${dir}\n`);

  // First conversation: a new session, one turn, a tool call in it.
  //
  // Collected into a Ref by a forked reader rather than taken from the stream
  // with a count. There is no end-of-turn update on this wire — the stream's
  // end is the process's end — so a `take(n)` is a guess about how many
  // updates a turn produces, and the probe would hang on the run where it
  // guessed high.
  let opened = "";
  const first = yield* Effect.scoped(
    Effect.gen(function* () {
      const chat = yield* conversation(spawner, { cwd: dir, model: "sonnet" });
      opened = chat.sessionId;
      const updates = yield* chat.updates;
      const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
        ),
      );
      yield* chat.send("Read notes.txt with Bash and tell me the word it names.");
      yield* Effect.sleep("40 seconds");
      return yield* Ref.get(seen);
    }),
  );
  console.log("1. a new session");
  draw(first);

  // Second conversation, a new process, given the id the first ended up on.
  //
  // Handed over rather than looked up, because looking it up is the bug: every
  // session ever held in a directory is in `session/list`, the terminal's
  // included, and taking the newest joins whatever somebody else is doing.
  const second = yield* Effect.scoped(
    Effect.gen(function* () {
      const chat = yield* conversation(spawner, { cwd: dir, model: "sonnet", session: opened });
      const updates = yield* chat.updates;
      const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
        ),
      );
      yield* Effect.sleep("10 seconds");
      return yield* Ref.get(seen);
    }),
  );
  console.log("\n2. opened again, in a new process");
  console.log(`  replayed    ${second.length} updates`);
  const words = second
    .filter((update) => update.kind === "message")
    .map((update) => `${String(update.role)}: ${String(update.text).trim().slice(0, 40)}`);
  for (const line of words.slice(0, 4)) {
    console.log(`    ${line}`);
  }

  // A conversation with no id, in a directory that already has one.
  //
  // This is the check that would have caught the bug, and neither a test nor
  // the two steps above can make it: a fake adapter has no `session/list`, and
  // both of those steps are *supposed* to end up on the same session. What is
  // being proved is a refusal — that a chat which does not know its own session
  // starts a new one rather than joining whatever else is in the directory,
  // which in a real workspace is the terminal's own agent.
  const stranger = yield* Effect.scoped(
    Effect.gen(function* () {
      const chat = yield* conversation(spawner, { cwd: dir, model: "sonnet" });
      const updates = yield* chat.updates;
      const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
        ),
      );
      yield* Effect.sleep("10 seconds");
      return { id: chat.sessionId, seen: yield* Ref.get(seen) };
    }),
  );
  console.log("\n3. opened with no id, beside the one that exists");
  console.log(`   a different session   ${stranger.id !== opened ? "yes" : "NO — it joined it"}`);
  console.log(`   replayed              ${stranger.seen.length} updates`);

  rmSync(dir, { recursive: true, force: true });

  // The one check that separates a working conversation from a plausible one.
  const heard = first.some(
    (update) => update.kind === "message" && (update.text ?? "").includes(WORD),
  );
  const merged = new Set(
    first.filter((update) => update.kind === "tool").map((update) => update.id),
  );
  console.log(
    `\n  the word    ${heard ? "came back" : "DID NOT come back"}` +
      `\n  tool ids    ${merged.size} for ${String(first.filter((u) => u.kind === "tool").length)} updates` +
      `\n  replay      ${second.length > 0 ? "yes" : "NOTHING — the session was not found"}\n`,
  );
  return heard && second.length > 0 && stranger.id !== opened && stranger.seen.length === 0 ? 0 : 1;
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
