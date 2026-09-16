import type { Message, ThreadMember } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer, PubSub, Stream } from "effect";

// What one agent said to another, and the three marks a message carries.
//
// ── a row, not a pipe ──────────────────────────────────────────────────────
// The recipient is usually mid-turn when a message is sent, so there is nothing
// to hand it to. The message has to sit somewhere until the agent is free and
// survive the daemon restarting under it, which is a table — the archive's
// design was an append-only JSONL file under an advisory lock, decided before
// this repository had a store to put it in.
//
// ── three timestamps, because there are three parties ──────────────────────
// `sent_at` is the sender's act, `notified_at` is the daemon's, `read_at` is
// the recipient's. Folding any two of them together loses the distinction the
// deliverer runs on: a message that is unread *and* un-nudged is the only kind
// that needs pushing, and one that is nudged and still unread is an agent that
// has been told and has not got to it — which is waiting, not a failure.
//
// ── the cap is here rather than in the handler ─────────────────────────────
// Two agents can answer each other indefinitely, and nothing in a model stops
// it. Since no human approves a send, the loop guard is the only thing standing
// between a thread and a night of two agents talking. It lives beside the
// insert so every writer gets it, including one added later.

/** The database would not answer. */
export class MessageStoreError extends Data.TaggedError("MessageStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The messages table, as a list that only ever grows.
 *
 * Named rather than numbered, so appending one here cannot renumber the jobs or
 * threads migrations sharing the same table of applied names.
 *
 * `on delete cascade` for the reason `thread_members` has it: archiving is a
 * flag, but a thread that is genuinely deleted must not leave messages pointing
 * at work that is gone.
 *
 * The index is on the recipient and `read_at` together because that is the one
 * read on a hot path — the inbox fetch, which every agent makes every time it
 * is nudged.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "messages.001-initial",
    up: [
      `create table messages (
         id             text not null primary key,
         thread_id      text not null references threads (id) on delete cascade,
         from_project   text not null,
         from_workspace text not null,
         to_project     text not null,
         to_workspace   text not null,
         body           text not null,
         sent_at        integer not null,
         notified_at    integer,
         read_at        integer
       ) strict`,
      `create index messages_inbox on messages (to_project, to_workspace, read_at)`,
      `create index messages_thread on messages (thread_id, sent_at)`,
    ],
  },
];

/**
 * How many messages one thread may carry in an hour before it goes quiet.
 *
 * A number rather than a rate limiter, and deliberately a generous one: what it
 * has to stop is a runaway, not a busy afternoon. Two agents coordinating a test
 * exchange a handful of lines; two agents stuck in a loop exchange one every few
 * seconds, and the difference between those is two orders of magnitude, so the
 * threshold does not have to be accurate to be right.
 *
 * Per thread and not per sender, because a loop needs two participants and
 * capping each of them separately would permit twice the traffic it is meant to
 * stop.
 */
export const CAP = 40;

/** The window the cap is counted over. */
export const WINDOW = 60 * 60 * 1000;

/** A message's id: the day it was sent, and four characters. Job and thread ids are spelled the same way. */
export const messageId = (now: Date, random: number): string => {
  const day = now.toISOString().slice(0, 10).replaceAll("-", "");
  const tail = Math.floor(random * 36 ** 4)
    .toString(36)
    .padStart(4, "0");
  return `m${day}-${tail}`;
};

export class Messages extends Context.Service<
  Messages,
  {
    /** Everything anyone has said, newest first. Read and unread alike. */
    readonly list: () => Effect.Effect<ReadonlyArray<Message>, MessageStoreError>;

    /** The same list again, each time anything about a message changes. */
    readonly changes: () => Stream.Stream<ReadonlyArray<Message>>;

    /**
     * Record a message, unless the thread has already said too much this hour.
     *
     * Answers `undefined` when the cap has been reached, rather than failing:
     * the caller turns that into the sentence the agent reads, and this file
     * has no business composing one.
     */
    readonly send: (message: {
      readonly thread: string;
      readonly from: ThreadMember;
      readonly to: ThreadMember;
      readonly body: string;
    }) => Effect.Effect<Message | undefined, MessageStoreError>;

    /**
     * Everything waiting for a workspace, marked read on the way out.
     *
     * One statement each way and the read happens first, so a caller that dies
     * between them has handed the messages over and marked them — which is the
     * failure worth having. The other order loses a message to a crash, and a
     * message nobody can find is worse than one delivered twice.
     */
    readonly inbox: (to: ThreadMember) => Effect.Effect<ReadonlyArray<Message>, MessageStoreError>;

    /**
     * Who has mail they have not been told about.
     *
     * What the deliverer runs on. Unread and un-nudged, oldest first, so a
     * recipient with several is nudged once about all of them.
     */
    readonly waiting: () => Effect.Effect<ReadonlyArray<Message>, MessageStoreError>;

    /** Mark that the recipient has been told. Safe to run twice. */
    readonly notified: (ids: ReadonlyArray<string>) => Effect.Effect<void, MessageStoreError>;
  }
>()("awp/Messages") {}

const ask = <A>(reason: string, run: () => A): Effect.Effect<A, MessageStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error) => new MessageStoreError({ reason, cause: error.cause })),
  );

const stamp = (value: unknown): number | undefined =>
  typeof value === "number" ? value : undefined;

const rowToMessage = (row: Record<string, unknown>): Message => ({
  id: String(row["id"]),
  thread: String(row["thread_id"]),
  from: { project: String(row["from_project"]), workspace: String(row["from_workspace"]) },
  to: { project: String(row["to_project"]), workspace: String(row["to_workspace"]) },
  body: String(row["body"]),
  sentAt: Number(row["sent_at"]),
  notifiedAt: stamp(row["notified_at"]),
  readAt: stamp(row["read_at"]),
});

export const make = Effect.gen(function* () {
  const db = yield* Db;

  // Columns named rather than positional, for the reason threads.ts names
  // them: the order is a function of which migrations have run.
  const insert = db.prepare(
    `insert into messages
       (id, thread_id, from_project, from_workspace, to_project, to_workspace, body, sent_at,
        notified_at, read_at)
     values (?, ?, ?, ?, ?, ?, ?, ?, null, null)`,
  );
  // ── ordered by rowid, not by id ─────────────────────────────────────────
  //
  // `sent_at` is milliseconds and two messages in one exchange land inside the
  // same one, so it needs a tiebreaker. The id is the wrong one: its tail is
  // four random characters, so a tie resolves differently every run — which is
  // a conversation drawn in an order nobody sent it in, and it showed up as a
  // test that passed alone and failed in the suite.
  //
  // sqlite's rowid is insert order and never ties. `strict` does not remove it;
  // only `without rowid` would, and this table has neither reason nor need to
  // be one.
  const readAll = db.prepare("select * from messages order by sent_at desc, rowid desc");
  const readInbox = db.prepare(
    `select * from messages
       where to_project = ? and to_workspace = ? and read_at is null
       order by sent_at asc, rowid asc`,
  );
  const markRead = db.prepare(
    "update messages set read_at = ? where to_project = ? and to_workspace = ? and read_at is null",
  );
  const readWaiting = db.prepare(
    `select * from messages
       where read_at is null and notified_at is null
       order by sent_at asc, rowid asc`,
  );
  const markNotified = db.prepare(
    "update messages set notified_at = ? where id = ? and notified_at is null",
  );
  const countRecent = db.prepare(
    "select count(*) as n from messages where thread_id = ? and sent_at >= ?",
  );

  const hub = yield* PubSub.sliding<ReadonlyArray<Message>>(16);

  /**
   * Say that something changed, having already changed it.
   *
   * Failure is swallowed for the reason threads.ts swallows it: the write has
   * happened, so a read that could not answer must not turn a delivered message
   * into a refusal. The cost is a stale viewer until the next question, which
   * every client here is already built to survive.
   */
  const announce = ask("cannot read messages", () => readAll.all().map(rowToMessage)).pipe(
    Effect.flatMap((all) => PubSub.publish(hub, all)),
    Effect.ignore,
  );

  return {
    list: () => ask("cannot list messages", () => readAll.all().map(rowToMessage)),

    changes: () => Stream.fromPubSub(hub),

    send: (message: {
      readonly thread: string;
      readonly from: ThreadMember;
      readonly to: ThreadMember;
      readonly body: string;
    }) =>
      ask(`cannot send to ${message.to.project}/${message.to.workspace}`, () => {
        const at = Date.now();
        // Counted and inserted in one call so two sends cannot both read a
        // count below the cap and both write. The daemon is the only writer,
        // but "the only writer" has already been false once here — a second
        // instance shares this file.
        const rows = countRecent.all(message.thread, at - WINDOW);
        const said = Number(rows[0]?.["n"] ?? 0);
        if (said >= CAP) {
          return undefined;
        }
        const made: Message = {
          id: messageId(new Date(at), Math.random()),
          thread: message.thread,
          from: message.from,
          to: message.to,
          body: message.body.trim(),
          sentAt: at,
          notifiedAt: undefined,
          readAt: undefined,
        };
        insert.run(
          made.id,
          made.thread,
          made.from.project,
          made.from.workspace,
          made.to.project,
          made.to.workspace,
          made.body,
          made.sentAt,
        );
        return made;
      }).pipe(Effect.tap((made) => (made === undefined ? Effect.void : announce))),

    inbox: (to: ThreadMember) =>
      ask(`cannot read the inbox of ${to.project}/${to.workspace}`, () => {
        const found = readInbox.all(to.project, to.workspace).map(rowToMessage);
        if (found.length > 0) {
          markRead.run(Date.now(), to.project, to.workspace);
        }
        return found;
      }).pipe(Effect.tap((found) => (found.length === 0 ? Effect.void : announce))),

    waiting: () => ask("cannot read waiting messages", () => readWaiting.all().map(rowToMessage)),

    notified: (ids: ReadonlyArray<string>) =>
      ask("cannot mark messages notified", () => {
        const at = Date.now();
        for (const id of ids) {
          markNotified.run(at, id);
        }
      }).pipe(Effect.tap(() => (ids.length === 0 ? Effect.void : announce))),
  };
});

/** Messages over whichever connection was provided. */
export const layer: Layer.Layer<Messages, never, Db> = Layer.effect(Messages)(make);
