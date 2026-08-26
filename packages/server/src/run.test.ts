import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { describe, expect, test } from "vitest";
import { type Captured, capture, said } from "./run";

// The exit code, which is the whole reason this file exists.
//
// `ChildProcessSpawner.string` collects stdout and discards the exit code. That
// is a reasonable contract for a function returning a string and the wrong one
// for a command whose failure matters — a jj that printed an error and exited 1
// arrived as a successful empty answer, and the service above it reported that
// it had done something it had not.
//
// The first test below is the one that would have caught it.

const platform = NodeChildProcessSpawner.layer.pipe(
  Layer.provideMerge(NodeFileSystem.layer),
  Layer.provideMerge(NodePath.layer),
);

const sh = (script: string): Promise<Captured> =>
  Effect.runPromise(
    Effect.gen(function* () {
      const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
      return yield* capture(spawner, ChildProcess.make("sh", ["-c", script]));
    }).pipe(Effect.provide(platform), Effect.orDie) as Effect.Effect<Captured>,
  );

describe("capture", () => {
  test("a non-zero exit is reported, not swallowed", async () => {
    const captured = await sh("echo out; echo err >&2; exit 3");

    expect(captured.exitCode).toBe(3);
    expect(captured.stdout).toBe("out\n");
    expect(captured.stderr).toBe("err\n");
  });

  test("a command that works reports zero", async () => {
    const captured = await sh("echo fine");

    expect(captured.exitCode).toBe(0);
    expect(captured.stdout).toBe("fine\n");
    expect(captured.stderr).toBe("");
  });

  test("output larger than a pipe buffer does not deadlock", async () => {
    // stdout and stderr are read concurrently with each other and with the
    // wait for the exit code. Reading one to the end first blocks forever as
    // soon as the command writes more to the unread stream than its pipe
    // holds — which is rare enough to pass a small test and then hang on a
    // long jj error. 256KiB on each is well past the 64KiB pipes are usually
    // given.
    const captured = await sh(
      `yes abcdefghij | head -c 262144; yes abcdefghij | head -c 262144 >&2; exit 0`,
    );

    expect(captured.stdout).toHaveLength(262_144);
    expect(captured.stderr).toHaveLength(262_144);
  });
});

describe("said", () => {
  test("prefers stderr, where a CLI explains itself", () => {
    expect(said({ stdout: "out", stderr: "the real reason", exitCode: 1 })).toBe("the real reason");
  });

  test("falls back to stdout when stderr is empty", () => {
    // Not every command writes its refusal to stderr, and a reason field that
    // is blank is worse than one carrying the wrong stream.
    expect(said({ stdout: "the reason", stderr: "  ", exitCode: 1 })).toBe("the reason");
  });

  test("keeps the first few lines, because jj prints a hint block", () => {
    const long = ["one", "two", "three", "four", "five"].join("\n");
    expect(said({ stdout: "", stderr: long, exitCode: 1 })).toBe("one; two; three");
  });
});
