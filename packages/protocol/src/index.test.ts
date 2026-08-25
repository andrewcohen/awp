import { Effect, Result, Schema, Stream } from "effect";
import { RpcTest } from "effect/unstable/rpc";
import { describe, expect, it } from "vitest";
import { AttachRefused, AwpRpcs, SessionInfo } from "./index";

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
  name: "awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev",
  pid: 51234,
  clients: 1,
  startDir: "/Users/someone/src/awp",
  ended: false,
  exitCode: 0,
  created: new Date("2026-08-25T09:14:00.000Z"),
  cmd: "claude",
  labels: { "awp.project": "awp", "awp.kind": "action_dev" },
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
const handlers = AwpRpcs.toLayer({
  SessionList: () => Effect.succeed([example]),
  Attach: ({ session }) =>
    session === "gone"
      ? Stream.fail(new AttachRefused({ session, reason: "no such session" }))
      : Stream.fromArray(output),
  Write: () => Effect.void,
  Resize: () => Effect.void,
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
            rpc.Attach({ session: "alpha", cols: 100, rows: 30 }),
          );
          // Escape sequences arrive intact, byte for byte. There is no byte
          // stage anywhere on this path and nothing should have introduced one.
          expect(chunks.join("")).toBe(output.join(""));
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
