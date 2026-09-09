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

/**
 * The transcript's own updates, which is what "replayed nothing" is about.
 *
 * A fresh session emits an `available_commands_update` of its own accord — the
 * adapter pushes the slash-command list on open — so counting *every* update
 * would report a brand-new conversation as having replayed something. That is
 * a check that would fail for a working fork, and it did on the first run
 * after commands stopped being dropped.
 */
const spoken = (updates: ReadonlyArray<ChatUpdate>): ReadonlyArray<ChatUpdate> =>
  updates.filter((update) => update.kind === "message" || update.kind === "tool");

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

  // ── the commands, which is where a skill lives ──────────────────────────
  //
  // The whole reason this is here: `available_commands_update` used to be
  // dropped, and no test could have said whether the adapter ever sends one —
  // a fixture would only agree with itself. What is being checked is that a
  // list arrives at all, and that a *skill* is in it, since a skill is
  // indistinguishable from a built-in command on this wire.
  //
  // This directory is a fresh temp dir with no `.claude` of its own, so
  // anything here came from the machine's own — which is the case the chat
  // needs to work for.
  const advertised = first.filter((update) => update.kind === "commands").at(-1)?.commands ?? [];
  console.log(`\n  commands    ${advertised.length}`);
  for (const command of advertised.slice(0, 8)) {
    console.log(
      `    ${command.name.padEnd(18)} ${(command.hint ?? "").padEnd(12)} ` +
        `${command.description.split("\n")[0]?.slice(0, 50) ?? ""}`,
    );
  }
  console.log(
    `  updates     ${String(first.filter((update) => update.kind === "commands").length)} ` +
      `command list(s) — pushed, never asked for`,
  );

  // Second conversation, a new process, given the id the first ended up on.
  //
  // Handed over rather than looked up, because looking it up is the bug: every
  // session ever held in a directory is in `session/list`, the terminal's
  // included, and taking the newest joins whatever somebody else is doing.
  const second = yield* Effect.scoped(
    Effect.gen(function* () {
      // Seeded with a reading, because a load sends none of its own — see
      // `ChatOptions.usage`. The count below is what says the seed arrives.
      const chat = yield* conversation(spawner, {
        cwd: dir,
        model: "sonnet",
        session: opened,
        usage: { used: 1234, size: 200_000 },
      });
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
  console.log(`  replayed    ${spoken(second).length} updates`);
  // ── the context figure, on a conversation nobody has spoken to ──────────
  //
  // Measured 2026-09-09: a live turn sends four `usage_update`s and a load
  // sends **none**, so the composer's figure was absent exactly when somebody
  // was deciding whether to carry on in an old conversation. There is no call
  // that asks, so the daemon stores the reading and hands it back — and this
  // is the line that says the hand-back works.
  const seeded = second.filter((update) => update.kind === "usage");
  console.log(
    `  usage       ${seeded.length === 0 ? "NONE — the seeded reading did not arrive" : `used=${String(seeded[0]?.used)} size=${String(seeded[0]?.size)}`}`,
  );
  const words = second
    .filter((update) => update.kind === "message")
    .map((update) => `${String(update.role)}: ${String(update.text).trim().slice(0, 40)}`);
  for (const line of words.slice(0, 4)) {
    console.log(`    ${line}`);
  }

  // A fork of the conversation somebody else is having.
  //
  // The check that says the feature is safe as well as working. Two properties,
  // and the second is the one that shaped the design:
  //
  //   its own id      so nobody becomes a second writer of the original
  //   its memory      asked for, not read off a replay. The adapter forks by
  //                   resume + forkSession, and resume "replays nothing,
  //                   remembers everything" — so an empty replay proves
  //                   nothing either way, and only a question does
  //
  // Forked at open, in the process that will hold it, because a fork made
  // somewhere else cannot be loaded here: measured, `session/load` on a fresh
  // fork fails and quietly lands on a new session instead.
  const forked = yield* Effect.scoped(
    Effect.gen(function* () {
      const copy = yield* conversation(spawner, { cwd: dir, model: "sonnet", fork: true });
      const updates = yield* copy.updates;
      const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
        ),
      );
      yield* Effect.sleep("5 seconds");
      const replayed = spoken(yield* Ref.get(seen)).length;
      yield* copy.send("What word did the file name? Reply with just that word.");
      yield* Effect.sleep("30 seconds");
      return { id: copy.sessionId, replayed, seen: yield* Ref.get(seen) };
    }),
  );

  // And can it be opened again once it has said something?
  //
  // This is what the whole feature rests on rather than a curiosity: the
  // adapter is released two minutes after the last window closes, so every
  // later visit is a fresh process loading the fork by id. A fresh fork is
  // NOT loadable — measured — and if that were still true after a turn, a
  // forked conversation would silently become a new empty one while somebody
  // was away from it.
  const reopened = yield* Effect.scoped(
    Effect.gen(function* () {
      const again = yield* conversation(spawner, {
        cwd: dir,
        model: "sonnet",
        session: forked.id,
      });
      const updates = yield* again.updates;
      const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
      yield* Effect.forkScoped(
        Effect.ignore(
          Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
        ),
      );
      yield* Effect.sleep("10 seconds");
      return { id: again.sessionId, seen: yield* Ref.get(seen) };
    }),
  );
  // Joined before it is checked. A model answers in chunks — this one arrived
  // as "he" then "ron" — so asking whether any single update contains the word
  // is asking whether the model happened to emit it whole. That reported a
  // working fork as a fork with no memory, twice, which is the same mistake
  // the panel's fold exists to prevent.
  const said = forked.seen
    .filter((update) => update.kind === "message" && update.role === "agent")
    .map((update) => update.text ?? "")
    .join("");
  const remembered = said.includes(WORD);
  console.log("\n3. forked, so the conversation it copied is untouched");
  console.log(
    `   reopened by id        ${
      reopened.id === forked.id
        ? `yes, replaying ${String(spoken(reopened.seen).length)} updates`
        : `NO — a fresh process got ${reopened.id.slice(0, 8)}… instead`
    }`,
  );
  console.log(
    `   a session of its own  ${forked.id !== opened ? "yes" : "NO — it is the one it copied"}` +
      `\n   replayed              ${String(forked.replayed)} updates before being asked anything` +
      `\n   remembers the word    ${remembered ? `yes — "${said.trim().slice(0, 40)}"` : `NO — it said "${said.trim().slice(0, 40)}"`}`,
  );

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
  console.log("\n4. opened with no id, beside the ones that exist");
  console.log(`   a different session   ${stranger.id !== opened ? "yes" : "NO — it joined it"}`);
  console.log(`   replayed              ${spoken(stranger.seen).length} updates`);

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
      `\n  replay      ${spoken(second).length > 0 ? "yes" : "NOTHING — the session was not found"}\n`,
  );
  const readingCame = second.some((update) => update.kind === "usage" && update.used === 1234);
  return heard &&
    readingCame &&
    spoken(second).length > 0 &&
    stranger.id !== opened &&
    spoken(stranger.seen).length === 0 &&
    forked.id !== opened &&
    remembered &&
    reopened.id === forked.id
    ? 0
    : 1;
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
