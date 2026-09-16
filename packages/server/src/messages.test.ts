import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { layer as dbLayer } from "@awp-kit/store";
import { Effect, Layer } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { CAP, Messages, layer, messageId, migrations } from "./messages";
import { migrations as threadMigrations, layer as threadsLayer, Threads } from "./threads";
import { byRecipient, nudge } from "./deliver";

// What a message is allowed to be, proved against a real file.
//
// A temp directory rather than a fake connection, for the reason
// `threads.test.ts` uses one: two of the three properties worth having here are
// claims about sqlite rather than about this file — that the cap counts rows
// inside a window, and that reading an inbox leaves nothing behind for the next
// read.
//
// The threads migrations are applied beside the messages ones because the
// foreign key is real: `on delete cascade` needs a `threads` table to point at,
// and a suite that migrated only its own tables would prove the schema against
// a database the daemon never has.

const scratch = mkdtempSync(join(tmpdir(), "awp-messages-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

let files = 0;
const file = (): string => join(scratch, `messages-${(files += 1)}.sqlite`);

const at = (path: string) =>
  Layer.merge(layer, threadsLayer).pipe(
    Layer.provide(dbLayer(path, [...threadMigrations, ...migrations])),
  );

const on = <A>(
  path: string,
  program: (services: {
    readonly messages: Messages["Service"];
    readonly threads: Threads["Service"];
  }) => Effect.Effect<A, unknown>,
): Promise<A> =>
  Effect.gen(function* () {
    const messages = yield* Messages;
    const threads = yield* Threads;
    return yield* program({ messages, threads });
  }).pipe(Effect.provide(at(path)), Effect.scoped, Effect.orDie, Effect.runPromise);

const rowan = { project: "rowan", workspace: "tabular-exports" };
const beta = { project: "beta", workspace: "tabular-exports" };

describe("messageId", () => {
  test("is the day it was sent and four characters", () => {
    expect(messageId(new Date("2026-09-16T10:00:00Z"), 0.5)).toMatch(/^m20260916-[\da-z]{4}$/u);
  });
});

describe("a message", () => {
  test("survives the process that wrote it", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const thread = yield* threads.create("tabular exports");
        yield* messages.send({ thread: thread.id, from: rowan, to: beta, body: "  it is live  " });
      }),
    );
    // A second connection to the same file, which is the only shape that can
    // say the row is on disk rather than in the first one's memory.
    const found = await on(path, ({ messages }) => messages.list());
    expect(found).toHaveLength(1);
    // Trimmed on the way in, so a model that wrapped its body in newlines does
    // not become a row that draws with a blank first line.
    expect(found[0]?.body).toBe("it is live");
    expect(found[0]?.from.project).toBe("rowan");
    expect(found[0]?.to.project).toBe("beta");
    expect(found[0]?.notifiedAt).toBeUndefined();
    expect(found[0]?.readAt).toBeUndefined();
  });

  test("is waiting until it has been nudged, and then it is not", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const thread = yield* threads.create("tabular exports");
        const sent = yield* messages.send({
          thread: thread.id,
          from: rowan,
          to: beta,
          body: "try it",
        });
        expect(yield* messages.waiting()).toHaveLength(1);

        yield* messages.notified([sent?.id ?? ""]);
        // The point of the second timestamp: still unread, and no longer
        // something the deliverer will push again. Without the distinction the
        // sweep either re-nudges forever or gives up after one try.
        expect(yield* messages.waiting()).toHaveLength(0);
        const all = yield* messages.list();
        expect(all[0]?.notifiedAt).toBeTypeOf("number");
        expect(all[0]?.readAt).toBeUndefined();
      }),
    );
  });

  test("is only in the inbox once, because reading is the acknowledgement", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const thread = yield* threads.create("tabular exports");
        yield* messages.send({ thread: thread.id, from: rowan, to: beta, body: "one" });
        yield* messages.send({ thread: thread.id, from: rowan, to: beta, body: "two" });

        const first = yield* messages.inbox(beta);
        expect(first.map((message) => message.body)).toEqual(["one", "two"]);
        // Oldest first, unlike the viewer's list — an inbox is read forwards.
        expect(yield* messages.inbox(beta)).toHaveLength(0);
      }),
    );
  });

  test("is not in somebody else's inbox", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const thread = yield* threads.create("tabular exports");
        yield* messages.send({ thread: thread.id, from: rowan, to: beta, body: "for beta" });
        expect(yield* messages.inbox(rowan)).toHaveLength(0);
        expect(yield* messages.inbox(beta)).toHaveLength(1);
      }),
    );
  });
});

describe("the cap", () => {
  // The one guard that replaced a person approving each send, so it is the one
  // thing in this file whose absence is a night of two agents talking.
  test("stops a thread that has said too much, and says so by answering nothing", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const thread = yield* threads.create("tabular exports");
        for (let index = 0; index < CAP; index += 1) {
          const sent = yield* messages.send({
            thread: thread.id,
            from: rowan,
            to: beta,
            body: `line ${index}`,
          });
          expect(sent).toBeDefined();
        }
        expect(
          yield* messages.send({ thread: thread.id, from: rowan, to: beta, body: "one too many" }),
        ).toBeUndefined();
        expect(yield* messages.list()).toHaveLength(CAP);
      }),
    );
  });

  test("is per thread, so a loop in one does not silence another", async () => {
    const path = file();
    await on(path, ({ messages, threads }) =>
      Effect.gen(function* () {
        const loud = yield* threads.create("tabular exports");
        const quiet = yield* threads.create("lantern header");
        for (let index = 0; index < CAP; index += 1) {
          yield* messages.send({ thread: loud.id, from: rowan, to: beta, body: `line ${index}` });
        }
        expect(
          yield* messages.send({ thread: quiet.id, from: rowan, to: beta, body: "unaffected" }),
        ).toBeDefined();
      }),
    );
  });
});

describe("the deliverer", () => {
  const waiting = (to: { project: string; workspace: string }, id: string) => ({
    id,
    thread: "th-1",
    from: rowan,
    to,
    body: "try it",
    sentAt: 0,
    notifiedAt: undefined,
    readAt: undefined,
  });

  test("nudges a recipient once for everything it is holding", () => {
    // The reason grouping exists at all: three messages to one workspace are
    // one interruption, not three turns.
    const groups = byRecipient([waiting(beta, "a"), waiting(beta, "b"), waiting(rowan, "c")]);
    expect(groups).toHaveLength(2);
    expect(groups[0]?.ids).toEqual(["a", "b"]);
    expect(groups[1]?.ids).toEqual(["c"]);
  });

  test("tells the recipient how many and which tool, and never the body", () => {
    expect(nudge(1)).toContain("1 new message");
    expect(nudge(3)).toContain("3 new messages");
    for (const count of [1, 3]) {
      expect(nudge(count)).toContain("awp_messages");
      // The property the whole design rests on: a body delivered as a turn
      // would wear the operator's face, so no part of one is in the nudge.
      expect(nudge(count)).not.toContain("try it");
    }
  });
});
