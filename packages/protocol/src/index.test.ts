import { Effect, Result, Schema, Stream } from "effect";
import { RpcTest } from "effect/unstable/rpc";
import { describe, expect, it } from "vitest";
import type { Job } from "@awp-kit/jobs";
import {
  AttachRefused,
  AwpRpcs,
  type Patch,
  type ReviewComment,
  type Revision,
  SessionInfo,
  type Thread,
} from "./index";

// A contract is only a contract if it survives the wire.
//
// Two different questions here, and conflating them would leave a hole:
// RpcTest connects a client straight to the handlers through the
// *no-serialization* path, so it proves the shapes, the streaming and the
// failure channel — but it never encodes anything. Anything that could not be
// represented as JSON would sail through it untouched. So the codec gets its
// own tests, run through `Schema.toCodecJson`, which is precisely what the
// transport's `RpcSerialization.json` uses.

const codec = Schema.toCodecJson(SessionInfo);
const encode = Schema.encodeSync(codec);
const decode = Schema.decodeSync(codec);

const example: SessionInfo = {
  name: "awp.rowan.pr-2336-dev-mlwzqyrmxslo.action_dev",
  pid: 51234,
  clients: 1,
  startDir: "/Users/someone/src/awp",
  ended: false,
  exitCode: 0,
  created: new Date("2026-08-25T09:14:00.000Z"),
  cmd: "claude",
  // The real keys, underscored. A dot cannot appear in one: the reduction that
  // makes a name safe to split turns it into an underscore.
  labels: { awp_project: "rowan", awp_workspace: "pr-2336-dev-mlwzqyrmxslo", awp_kind: "dev" },
  identity: {
    project: "rowan",
    workspace: "pr-2336-dev-mlwzqyrmxslo",
    kind: "dev",
    label: undefined,
  },
  refusal: undefined,
};

describe("SessionInfo on the wire", () => {
  it("round-trips through the codec the transport uses", () => {
    expect(decode(encode(example))).toEqual(example);
  });

  // Schema.Date is a `declare`, which describes an in-memory Date and not a
  // wire value. It carries a JSON codec that writes ISO 8601, but that is a
  // property of the codec rather than of the schema, and a test that reached
  // for JSON.stringify instead of toCodecJson would prove nothing about it.
  it("writes the timestamp as ISO 8601 and reads a real Date back", () => {
    const wire = encode(example);
    expect((wire as { readonly created: unknown }).created).toBe("2026-08-25T09:14:00.000Z");
    expect(decode(wire).created).toBeInstanceOf(Date);
  });

  // A refusal is a sentence written for a person, and it is the entire
  // explanation a disabled row gets. Losing it on the wire would leave the UI
  // unable to say anything at all.
  it("carries the reason a session cannot be attached to", () => {
    const refused = { ...example, refusal: "this is the session awp is running in" };
    expect(decode(encode(refused)).refusal).toBe(refused.refusal);
  });

  it("carries a session with no start time", () => {
    const undated = { ...example, created: undefined };
    expect(decode(encode(undated)).created).toBeUndefined();
  });

  // The labels are whatever zmx printed that was not a known field, so their
  // keys are not known here either. A Record schema is the honest shape; a
  // Struct would silently drop anything zmx grows later.
  it("keeps label keys it has never seen", () => {
    const odd = { ...example, labels: { "zmx.something.new": "1" } };
    expect(decode(encode(odd)).labels).toEqual(odd.labels);
  });
});

// A pane clear, a line, and a newline: the shortest thing that is unmistakably
// terminal output rather than text. Written as an escape rather than pasted,
// because a literal control byte in a source file is invisible in review.
const ESC = "\u001B";
const output = [`${ESC}[2J`, "hello", "\r\n"];

// The handlers a client would talk to, with nothing behind them. What is under
// test is the contract, so the daemon is exactly the part to leave out.
// A job in the most awkward state to send: a rolled-back failure, with an
// opaque `input`, a Date that is set and a Date that is not.
const job: Job = {
  id: "20260101-aaaa",
  kind: "demo",
  title: "a demonstration",
  key: "once",
  input: { steps: 3, nested: [1, 2] },
  status: "failed",
  attempt: 3,
  attempts: 3,
  steps: ["one", "two", "three"],
  done: [],
  step: "two",
  error: "two refused",
  cleanup: "dirty",
  createdAt: new Date(1_700_000_000_000),
  startedAt: new Date(1_700_000_001_000),
  endedAt: new Date(1_700_000_002_000),
};

const thread: Thread = {
  id: "20260826-aaaa",
  title: "tiered discounts",
  createdAt: new Date(1_700_000_000_000),
  archivedAt: undefined,
  parentId: undefined,
  members: [{ project: "rowan", workspace: "discounts" }],
};

const revision: Revision = {
  changeId: "tkzuwuvztzulwvnrxtyoyzxqkymmykxu",
  commitId: "4b13c06af0e8617aa5be5308e71fb18b5d3925cb",
  description: "feat: a diff view\n\nThe body, which a row does not show.\n",
  author: "A Person",
  authored: new Date(1_700_000_000_000),
  empty: false,
  workingCopy: true,
  bookmarks: ["andrew/diff-view"],
};

// A real `jj diff --git` fragment, tabs and all. The tab is the point: the
// revision listing uses tabs as a field separator, so a patch that carries one
// is the thing most likely to be quietly mangled somewhere on this path.
const patch: Patch = {
  revision: "@",
  patch: [
    "diff --git a/go.mod b/go.mod",
    "--- a/go.mod",
    "+++ b/go.mod",
    "@@ -1,2 +1,2 @@",
    "-\told\tline",
    "+\tnew\tline",
    "",
  ].join("\n"),
};

const comment: ReviewComment = {
  id: "20260827-a1b2",
  project: "thicket",
  workspace: "lantern",
  revision: "vtknsnwv",
  path: "src/router.ts",
  side: "additions",
  line: 42,
  endLine: 42,
  body: "this branch never runs",
  createdAt: new Date("2026-08-27T09:14:00.000Z"),
  sentAt: undefined,
};

const handlers = AwpRpcs.toLayer({
  SessionList: () => Effect.succeed([example]),
  Attach: ({ session }) =>
    session === "gone"
      ? Stream.fail(new AttachRefused({ session, reason: "no such session" }))
      : Stream.fromArray(output),
  Write: () => Effect.void,
  Resize: () => Effect.void,
  JobList: () => Effect.succeed([job]),
  JobChanges: () => Stream.fromArray([job]),
  JobLog: () => Effect.succeed(["a line"]),
  JobRetry: () => Effect.succeed(job),
  JobCancel: () => Effect.void,
  JobClear: () => Effect.succeed(0),
  ThreadList: () => Effect.succeed([thread]),
  ThreadCreate: () => Effect.succeed(thread),
  ThreadRename: () => Effect.succeed(thread),
  ThreadArchive: () => Effect.succeed(thread),
  ThreadAttach: () => Effect.succeed(thread),
  ThreadDetach: () => Effect.succeed(thread),
  WorkspaceCreate: () => Effect.succeed(job),
  ThreadBases: () => Effect.succeed([{ revset: "trunk()", label: "trunk", workspace: undefined }]),
  ThreadStart: () => Effect.succeed({ thread, job }),
  Revisions: () => Effect.succeed([revision]),
  Diff: () => Effect.succeed(patch),
  ReviewList: () => Effect.succeed([comment]),
  ReviewAdd: () => Effect.succeed(comment),
  ReviewRemove: () => Effect.succeed(true),
  ReviewSend: () => Effect.succeed({ sent: [comment], prompt: "Review feedback — 1 comment:" }),
});

const client = RpcTest.makeClient(AwpRpcs).pipe(Effect.provide(handlers));

describe("the contract", () => {
  it("answers SessionList", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          expect(yield* rpc.SessionList()).toEqual([example]);
        }),
      ),
    ));

  it("streams a session's output as strings", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          const chunks = yield* Stream.runCollect(
            rpc.Attach({ session: "rowan", cols: 100, rows: 30 }),
          );
          // Escape sequences arrive intact, byte for byte. There is no byte
          // stage anywhere on this path and nothing should have introduced one.
          expect(chunks.join("")).toBe(output.join(""));
        }),
      ),
    ));

  it("carries a whole job record, opaque input and absent dates included", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          const [listed] = yield* rpc.JobList();
          // The fields most likely to be quietly lost by a codec: an `unknown`
          // with structure in it, and Dates that have to come back as Dates
          // rather than as the numbers they were sent as.
          expect(listed).toEqual(job);
          expect(listed?.createdAt).toBeInstanceOf(Date);
          expect(listed?.input).toEqual({ steps: 3, nested: [1, 2] });
        }),
      ),
    ));

  it("carries a revision, absent dates and all", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          const [listed] = yield* rpc.Revisions({ from: "/w/rowan" });
          expect(listed).toEqual(revision);
          expect(listed?.authored).toBeInstanceOf(Date);
        }),
      ),
    ));

  it("carries a patch as the text jj wrote, byte for byte", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          // The whole reason the patch crosses as a string: what arrives is
          // what jj said, not a re-rendering of something that was parsed.
          expect(yield* rpc.Diff({ from: "/w/rowan" })).toEqual(patch);
        }),
      ),
    ));

  it("delivers a refusal as itself, not as a string", () =>
    Effect.runPromise(
      Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* client;
          // Effect.result turns a failure into a value, so the test can look
          // at it rather than catch it. Result.isFailure is a type guard, which
          // is why .failure is reachable below without a cast — reading the
          // discriminant by hand would narrow nothing.
          const result = yield* Effect.result(
            Stream.runCollect(rpc.Attach({ session: "gone", cols: 100, rows: 30 })),
          );
          expect(Result.isFailure(result)).toBe(true);
          if (Result.isFailure(result)) {
            // The point of a schema-backed error: it arrives as itself, with
            // its fields, rather than as a message to be parsed.
            expect(result.failure).toBeInstanceOf(AttachRefused);
            expect(result.failure.session).toBe("gone");
          }
        }),
      ),
    ));
});
