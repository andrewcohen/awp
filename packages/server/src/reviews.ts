import type { ReviewComment } from "@awp-kit/protocol";
import { Db, type Migration, attempt } from "@awp-kit/store";
import { Context, Data, Effect, Layer } from "effect";

// What a person said about a diff, before the agent has heard it.
//
// ── why these are stored at all ────────────────────────────────────────────
// A comment could be typed straight into the agent and never written down. It
// is kept because of the one decision that shapes this whole file: comments are
// **batched**, and sent as a review rather than one at a time. An agent
// interrupted once per comment loses the thread it is holding; "here are six
// things about this change" is one prompt it can act on.
//
// Batching means a comment exists for a while with nobody having seen it, and a
// thing that exists between two moments has to live somewhere that survives a
// reload of the window and a restart of the daemon.
//
// ── `sent_at` is the whole state machine ───────────────────────────────────
// A comment is a draft until it has been sent, and sent afterwards. That is one
// nullable column rather than a status word, because there are exactly two
// states and the interesting question — *when* did the agent hear this — is
// answered by the same field.
//
//   sent_at is null      a draft. Editable, deletable, counted in "3 unsent".
//   sent_at is a time    the agent has been told. Kept, and shown greyed.
//
// Sent comments are not deleted, and that is deliberate. The point of a review
// is that it is a record of what was asked for; deleting it the moment it was
// delivered would leave the panel looking like nobody had said anything.
//
// ── a comment outlives the revision it was made against ────────────────────
// It is anchored to `(revision, path, side, line)`, and a revision is rebased
// away routinely — that is what working in jj looks like. The comment is kept
// and reads as stale rather than being deleted, on the same reasoning as
// `parentId` being recorded rather than re-derived: what someone said about a
// line is still true after the commit it sat on has moved. Deciding a comment
// is worthless because jj rewrote a change id is the store guessing at
// something only a person knows.

/** The database would not answer. */
export class ReviewStoreError extends Data.TaggedError("ReviewStoreError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The reviews tables, as a list that only ever grows.
 *
 * Named rather than numbered, so appending here cannot renumber the jobs or
 * threads migrations sharing the same table of applied names.
 *
 * No foreign key to anything. A comment names a workspace by `(project,
 * workspace)` — the same pair `thread_members` uses — and those are not rows in
 * a table anyone owns: a workspace is a directory jj knows about. A constraint
 * would have to point at something, and there is nothing to point at.
 */
export const migrations: ReadonlyArray<Migration> = [
  {
    name: "reviews.001-comments",
    up: [
      `create table review_comments (
         id         text primary key,
         project    text not null,
         workspace  text not null,
         revision   text not null,
         path       text not null,
         side       text not null,
         line       integer not null,
         body       text not null,
         created_at integer not null,
         sent_at    integer
       ) strict`,
      // The read is always "every comment for this workspace" — the panel shows
      // them all and greys the sent ones — so the index is on the pair, and
      // `created_at` after it so the ordering comes off the index too.
      `create index review_comments_workspace
         on review_comments (project, workspace, created_at)`,
    ],
  },
  {
    // Appended rather than folded into the migration above, because that one
    // has run — on this machine, at least — and a name is fixed the moment it
    // has. Renaming or editing an applied migration makes it run a second time
    // against a schema it already changed.
    //
    // `not null default` is what makes this safe to run over rows that exist:
    // sqlite needs a value for the column it is adding, and the honest one for
    // a comment written before ranges existed is the line it was already on.
    // -1 stands for "same as `line`" and is resolved on the way out, because a
    // default cannot name another column.
    name: "reviews.002-range",
    up: [`alter table review_comments add column end_line integer not null default -1`],
  },
  {
    // Who filed it, what kind of remark it is, and the line's text as they saw
    // it. Appended for the reason 002 was: a name is fixed once it has run.
    //
    // The defaults are what the rows that exist actually are. Every comment
    // written before an agent could file one was written by a person in the
    // window, and every one of those is an observation — a `suggestion` default
    // would relabel work somebody already did.
    //
    // `text` is nullable rather than defaulted: a comment written in the window
    // never had one, and the empty string would read as "the line was blank".
    name: "reviews.003-author-kind",
    up: [
      `alter table review_comments add column author text not null default 'human'`,
      `alter table review_comments add column kind text not null default 'comment'`,
      `alter table review_comments add column text text`,
    ],
  },
];

export class Reviews extends Context.Service<
  Reviews,
  {
    /** Every comment on a workspace, oldest first. Drafts and sent alike. */
    readonly list: (
      project: string,
      workspace: string,
    ) => Effect.Effect<ReadonlyArray<ReviewComment>, ReviewStoreError>;
    readonly add: (comment: ReviewComment) => Effect.Effect<ReviewComment, ReviewStoreError>;
    /**
     * Delete one, whether or not it has been sent.
     *
     * Answers whether there was one to delete. A caller that asked twice is not
     * an error — the second answer is simply `false`.
     */
    readonly remove: (id: string) => Effect.Effect<boolean, ReviewStoreError>;
    /**
     * Mark every draft on a workspace as sent, and answer with what was marked.
     *
     * The drafts are chosen *here* rather than by the caller passing ids, and
     * that is the point: the caller composes a prompt out of exactly the rows
     * this marked, so there is no window in which a comment written between
     * "which are unsent" and "mark them sent" is silently skipped.
     */
    readonly markSent: (
      project: string,
      workspace: string,
      at: Date,
    ) => Effect.Effect<ReadonlyArray<ReviewComment>, ReviewStoreError>;
  }
>()("awp/Reviews") {}

/**
 * A comment's id: the day, then four characters of the caller's randomness.
 *
 * The same spelling as a job id and a thread id. Three id formats in one system
 * is three things to recognise for no gain.
 */
export const commentId = (now: Date, random: number): string => {
  const day = now.toISOString().slice(0, 10).replaceAll("-", "");
  const tail = Math.floor(random * 36 ** 4)
    .toString(36)
    .padStart(4, "0");
  return `${day}-${tail}`;
};

const ask = <A>(reason: string, run: () => A): Effect.Effect<A, ReviewStoreError> =>
  attempt(reason, run).pipe(
    Effect.mapError((error) => new ReviewStoreError({ reason, cause: error.cause })),
  );

const date = (value: unknown): Date | undefined =>
  typeof value === "number" ? new Date(value) : undefined;

/** A nullable text column, as the wire's `string | undefined`. */
const text = (value: unknown): string | undefined =>
  typeof value === "string" && value !== "" ? value : undefined;

/**
 * A stored row, as the shape on the wire.
 *
 * `side` is read back as a string and narrowed here rather than trusted.
 * `strict` stops `side` being a number; it has nothing to say about it being
 * the word "banana", and a value the schema will not encode would fail at the
 * edge of the daemon rather than here where the row can be named.
 */
const toComment = (row: Record<string, unknown>): ReviewComment => ({
  id: String(row["id"]),
  project: String(row["project"]),
  workspace: String(row["workspace"]),
  revision: String(row["revision"]),
  path: String(row["path"]),
  side: row["side"] === "deletions" ? "deletions" : "additions",
  line: Number(row["line"]),
  // The sentinel the migration writes, resolved here — see `reviews.002-range`.
  // A comment made before ranges existed is a comment about one line, and this
  // is the only place that has to know the two spellings of that.
  endLine: Number(row["end_line"]) < 0 ? Number(row["line"]) : Number(row["end_line"]),
  body: String(row["body"]),
  // Both narrowed rather than cast. `strict` stops `author = 7` reaching the
  // column and does nothing about `author = 'nobody'`, and the value crossing
  // the wire is a union — so an unknown string becomes the honest default here
  // rather than a decode failure two layers up, in a message about a schema.
  author: row["author"] === "agent" ? "agent" : "human",
  kind:
    row["kind"] === "suggestion" || row["kind"] === "question" || row["kind"] === "praise"
      ? row["kind"]
      : "comment",
  text: text(row["text"]),
  createdAt: new Date(Number(row["created_at"])),
  sentAt: date(row["sent_at"]),
});

export const make = Effect.gen(function* () {
  const db = yield* Db;

  // Columns named rather than positional, for the reason threads.ts gives: the
  // column order is a function of which migrations have run, so a later
  // `alter table` would silently shift every value along.
  const insert = db.prepare(
    `insert into review_comments
       (id, project, workspace, revision, path, side, line, end_line, body,
        author, kind, text, created_at, sent_at)
     values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  );
  const readAll = db.prepare(
    `select * from review_comments
       where project = ? and workspace = ?
       order by created_at, id`,
  );
  const readOne = db.prepare("select * from review_comments where id = ?");
  const readDrafts = db.prepare(
    `select * from review_comments
       where project = ? and workspace = ? and sent_at is null
       order by created_at, id`,
  );
  const drop = db.prepare("delete from review_comments where id = ?");
  const send = db.prepare(
    `update review_comments set sent_at = ?
       where project = ? and workspace = ? and sent_at is null`,
  );

  return {
    list: (project: string, workspace: string) =>
      ask("list review comments", () => readAll.all(project, workspace).map(toComment)),

    add: (comment: ReviewComment) =>
      ask("add a review comment", () => {
        insert.run(
          comment.id,
          comment.project,
          comment.workspace,
          comment.revision,
          comment.path,
          comment.side,
          comment.line,
          comment.endLine,
          comment.body,
          comment.author,
          comment.kind,
          comment.text ?? null,
          comment.createdAt.getTime(),
          comment.sentAt?.getTime() ?? null,
        );
        return comment;
      }),

    remove: (id: string) =>
      ask("remove a review comment", () => {
        // Read first, because neither driver agrees about what `run` reports
        // and only the intersection of the two APIs is used here — see the note
        // in @awp-kit/store. A row count is exactly the kind of thing that
        // differs, so the answer is taken from a read the two do agree on.
        const existed = readOne.all(id).length > 0;
        drop.run(id);
        return existed;
      }),

    markSent: (project: string, workspace: string, at: Date) =>
      ask("mark review comments sent", () => {
        // Read the drafts, then mark them, in that order and with no await
        // between: this is one synchronous block and the daemon is the only
        // writer, so the set marked is the set returned. Marking first and
        // reading back would need a second predicate to find what was just
        // marked, which is the same question asked twice.
        const sending = readDrafts.all(project, workspace).map(toComment);
        if (sending.length === 0) {
          return sending;
        }
        send.run(at.getTime(), project, workspace);
        return sending.map((comment) => ({ ...comment, sentAt: at }));
      }),
  };
});

export const layer: Layer.Layer<Reviews, never, Db> = Layer.effect(Reviews)(make);
