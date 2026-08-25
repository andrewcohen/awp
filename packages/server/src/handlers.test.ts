import { AwpRpcs } from "@awp-kit/protocol";
import { Effect, Layer, Result, Stream } from "effect";
import type { RpcClient } from "effect/unstable/rpc";
import { RpcTest } from "effect/unstable/rpc";
import { describe, expect, it } from "vitest";
import * as attachment from "./attachment";
import * as handlers from "./handlers";
import { Multiplexer, type Session } from "./multiplexer";
import { makeFake } from "./pty-fake";
import * as sessions from "./sessions";

// The contract, its handlers and the services under them — everything except
// the socket.
//
// Worth having separately from the services' own tests, because what it checks
// is the seam: that a refusal the daemon states as `AttachError` reaches the
// client as `AttachRefused`, and that the daemon's `Session` arrives as the
// wire shape. Those translations live only here and are invisible to both
// sides' tests.

const LIVE = "awp.awp.other.agent";
const DEAD = "awp.awp.finished.agent";

const session = (over: Partial<Session>): Session => ({
  name: LIVE,
  pid: 4242,
  clients: 0,
  startDir: "/tmp",
  ended: false,
  exitCode: 0,
  created: new Date("2026-08-25T09:14:00.000Z"),
  cmd: "claude",
  labels: { "awp.kind": "agent" },
  ...over,
});

const all = [session({}), session({ name: DEAD, ended: true, exitCode: 130 })];

const fakeMux = Layer.succeed(Multiplexer, {
  list: () => Effect.succeed(all),
  lookup: (name: string) => Effect.succeed(all.find((s) => s.name === name)),
  kill: () => Effect.void,
  setLabels: () => Effect.void,
  history: () => Effect.succeed(""),
});

type Client = RpcClient.RpcClient<
  (typeof AwpRpcs)["requests"] extends ReadonlyMap<string, infer R> ? R : never
>;

const run = <A>(body: (rpc: Client) => Effect.Effect<A, unknown>) =>
  Effect.runPromise(
    Effect.gen(function* () {
      const fake = yield* makeFake({ chunks: ["\u001B[2J", "ready$ "] });
      const stack = handlers.layer.pipe(
        Layer.provide(sessions.layer),
        Layer.provide(attachment.layer),
        Layer.provide(fake.layer),
        Layer.provide(fakeMux),
      );
      return yield* Effect.scoped(
        Effect.gen(function* () {
          const rpc = yield* RpcTest.makeClient(AwpRpcs);
          return yield* body(rpc);
        }),
      ).pipe(Effect.provide(stack));
    }),
  );

describe("the daemon over its contract", () => {
  it("reports sessions in the wire shape", async () => {
    const listed = await run((rpc) => rpc.SessionList());
    expect(listed).toHaveLength(2);
    expect(listed[0]).toMatchObject({ name: LIVE, cmd: "claude", labels: { "awp.kind": "agent" } });
    // `ended` is on the wire because listed and running are different
    // questions, and a client that conflates them attaches to a dead screen.
    expect(listed[1]).toMatchObject({ name: DEAD, ended: true, exitCode: 130 });
  });

  it("streams a session's bytes to the client unaltered", async () => {
    const chunks = await run((rpc) =>
      Stream.runCollect(Stream.take(rpc.Attach({ session: LIVE, cols: 100, rows: 30 }), 2)),
    );
    expect(chunks.join("")).toBe("\u001B[2Jready$ ");
  });

  it("turns the daemon's refusal into the client's, with the reason intact", async () => {
    const result = await run((rpc) =>
      Effect.result(Stream.runCollect(rpc.Attach({ session: DEAD, cols: 100, rows: 30 }))),
    );
    expect(Result.isFailure(result)).toBe(true);
    if (Result.isFailure(result)) {
      // AttachError on the daemon's side, AttachRefused on the client's. The
      // reason is written for a person and survives the translation, which is
      // the only way the pane can say why it did not open.
      expect(result.failure).toMatchObject({ _tag: "AttachRefused", session: DEAD });
      expect(String((result.failure as { reason: string }).reason)).toContain("ended");
    }
  });

  it("reports a write to an unattached session as SessionNotFound", async () => {
    const result = await run((rpc) => Effect.result(rpc.Write({ session: LIVE, data: "ls\r" })));
    expect(Result.isFailure(result)).toBe(true);
    if (Result.isFailure(result)) {
      expect(result.failure).toMatchObject({ _tag: "SessionNotFound", session: LIVE });
    }
  });
});
