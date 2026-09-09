#!/usr/bin/env bun
// Can two clients hold one conversation?
//
// That is the whole ACP question, and the contract already answers it in
// prose — `ChatOpen` says "one adapter process per workspace, shared by
// everyone looking at it". This is the measurement behind the sentence, taken
// one level below the daemon: one `conversation`, two subscribers, and a turn
// driven from each of them.
//
//   web     subscribes first, sends the first prompt
//   tui     subscribes LATE, mid-conversation, and sends the second
//
// The late join is the case worth proving. A subscriber gets the transcript so
// far and then the live feed, and the seam between those two is where a naive
// implementation either loses an update or shows it twice — which is what the
// sequence numbers in chat.ts exist for.
//
// ── safe anywhere ──────────────────────────────────────────────────────────
// A temporary directory and two one-word questions. It never invokes zmx,
// never attaches, never names a session, and reaches no workspace of anybody's.

import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import type { ChatUpdate } from "@awp-kit/protocol";
import { Effect, Layer, Ref, type Scope, Stream } from "effect";
import { ChildProcessSpawner } from "effect/unstable/process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { conversation } from "../../../../packages/server/src/chat";

const FIRST = "heron";
const SECOND = "kestrel";

/** What a client holds: every update it was given, in the order it got them. */
const subscribe = (chat: {
  readonly updates: Effect.Effect<Stream.Stream<ChatUpdate>, never, Scope.Scope>;
}) =>
  Effect.gen(function* () {
    const updates = yield* chat.updates;
    const seen = yield* Ref.make<ReadonlyArray<ChatUpdate>>([]);
    yield* Effect.forkScoped(
      Effect.ignore(
        Stream.runForEach(updates, (update) => Ref.update(seen, (all) => [...all, update])),
      ),
    );
    return seen;
  });

const said = (updates: ReadonlyArray<ChatUpdate>): string =>
  updates
    .filter((update) => update.kind === "message" && update.role === "agent")
    .map((update) => update.text ?? "")
    .join("");

const asked = (updates: ReadonlyArray<ChatUpdate>): ReadonlyArray<string> =>
  updates
    .filter((update) => update.kind === "message" && update.role === "user")
    .map((update) => (update.text ?? "").trim());

/** Poll a reading until it holds. A turn takes as long as the work does. */
const until = (
  label: string,
  seen: Ref.Ref<ReadonlyArray<ChatUpdate>>,
  holds: (u: ReadonlyArray<ChatUpdate>) => boolean,
) =>
  Effect.gen(function* () {
    for (let tried = 0; tried < 300; tried++) {
      if (holds(yield* Ref.get(seen))) return;
      yield* Effect.sleep("200 millis");
    }
    return yield* Effect.die(new Error(`timed out waiting for ${label}`));
  });

/** The last few updates, as something two clients can be compared on. */
const tail = (all: ReadonlyArray<ChatUpdate>) =>
  JSON.stringify(all.slice(-6).map((u) => [u.kind, u.role, u.status, (u.text ?? "").trim()]));

const say = (label: string, value: unknown) =>
  console.log(`  ${label.padEnd(14)}${typeof value === "string" ? value : JSON.stringify(value)}`);

const program = Effect.gen(function* () {
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
  const dir = mkdtempSync(join(tmpdir(), "poc-fanout-"));
  console.log(`\n  cwd           ${dir}\n`);

  return yield* Effect.scoped(
    Effect.gen(function* () {
      const chat = yield* conversation(spawner, { cwd: dir, model: "sonnet" });
      say("session", chat.sessionId);

      // ── the web client ──────────────────────────────────────────────────
      const web = yield* subscribe(chat);
      const firstDelivery = yield* chat.send(`Reply with the single word: ${FIRST}`);
      yield* until("the first answer", web, (all) => said(all).includes(FIRST));
      const atJoin = yield* Ref.get(web);
      say("web sent", `${firstDelivery} · ${atJoin.length} updates, said "${said(atJoin).trim()}"`);

      // ── the TUI, joining a conversation already under way ───────────────
      const tui = yield* subscribe(chat);
      yield* Effect.sleep("500 millis");
      const replayed = yield* Ref.get(tui);
      say("tui joined", `${replayed.length} updates replayed with no send of its own`);
      say("tui replay", {
        askedBack: asked(replayed),
        said: said(replayed).trim().slice(0, 40),
      });

      // ── and driving it from the TUI end ─────────────────────────────────
      const secondDelivery = yield* chat.send(`Reply with the single word: ${SECOND}`);
      yield* until("the second answer, at the tui", tui, (all) => said(all).includes(SECOND));
      yield* until("the second answer, at the web client", web, (all) =>
        said(all).includes(SECOND),
      );
      const endWeb = yield* Ref.get(web);
      const endTui = yield* Ref.get(tui);
      say("tui sent", `${secondDelivery} · both ends saw "${SECOND}"`);

      // The tail both were subscribed for has to be the same sequence, or the
      // two clients are drawing different conversations.
      const agree = tail(endWeb) === tail(endTui);
      say("agree", agree ? "the last six updates are identical at both ends" : "DIVERGED");
      say("counts", {
        web: endWeb.length,
        tui: endTui.length,
        replayedAtJoin: replayed.length,
        webAtJoin: atJoin.length,
      });
      // The duplicate a naive seam produces: the same user message twice.
      const twice = asked(endTui).filter((text) => text.includes(FIRST)).length;
      say(
        "no dupes",
        twice === 1 ? `the first prompt appears once at the tui` : `APPEARS ${twice} TIMES`,
      );

      rmSync(dir, { recursive: true, force: true });
      return agree && twice === 1 ? 0 : 1;
    }),
  );
}).pipe(
  Effect.provide(
    NodeChildProcessSpawner.layer.pipe(
      Layer.provide(NodeFileSystem.layer),
      Layer.provide(NodePath.layer),
    ),
  ),
);

process.exit(await Effect.runPromise(Effect.orDie(program) as Effect.Effect<number>));
