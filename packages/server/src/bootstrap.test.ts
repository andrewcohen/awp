import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer, Result } from "effect";
import { afterAll, describe, expect, test } from "vitest";
import { Bootstrap, layer } from "./bootstrap";

// Against a real shell, not a fake one.
//
// Everything interesting about this service is what the *child process* sees —
// the working directory it starts in, the environment it inherits, and the exit
// code it reports — and none of that is observable from a fake. The lesson is
// `probe/child-env.ts`'s, given a test instead of a probe because nothing here
// touches zmx or jj: it runs `sh`, and `sh` is safe anywhere.

const scratch = mkdtempSync(join(tmpdir(), "awp-bootstrap-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

const run = (command: string, cwd: string = scratch) =>
  Effect.gen(function* () {
    const bootstrap = yield* Bootstrap;
    return yield* Effect.result(bootstrap.run({ command, cwd }));
  }).pipe(
    Effect.provide(
      layer.pipe(
        // FileSystem and Path are the spawner's, not this service's — it
        // resolves an executable before running it. main.ts says the same
        // thing; the R channel is what surfaces them either way.
        Layer.provide(
          NodeChildProcessSpawner.layer.pipe(
            Layer.provide(NodeFileSystem.layer),
            Layer.provide(NodePath.layer),
          ),
        ),
      ),
    ),
    Effect.runPromise,
  );

describe("a bootstrap hook", () => {
  test("is a shell line, not an argv", async () => {
    // The whole reason this goes to `sh -c`. Splitting on whitespace the way
    // `Settings.agent` does would make `&&` an argument to echo, and a quoted
    // path with a space in it into two paths.
    const outcome = await run(`echo one && echo "two words"`);

    expect(Result.isSuccess(outcome)).toBe(true);
    if (Result.isSuccess(outcome)) {
      expect(outcome.success.trim().split("\n")).toEqual(["one", "two words"]);
    }
  });

  test("starts in the workspace it was given", async () => {
    const here = mkdtempSync(join(scratch, "ws-"));
    writeFileSync(join(here, "marker"), "");

    const outcome = await run("ls", here);

    expect(Result.isSuccess(outcome)).toBe(true);
    if (Result.isSuccess(outcome)) {
      expect(outcome.success.trim()).toBe("marker");
    }
  });

  test("neutralises ZMX_SESSION by setting it, not by omitting it", async () => {
    // Asserted on what the child prints, which is the only way to know — the
    // bug this guards against was a spawner that merged onto the parent
    // environment, so a key left out was a key left alone. A hook is free to
    // run zmx, and one that inherited the marker would switch whichever session
    // the daemon happens to be running in.
    const outcome = await run(
      `printf 'marker=[%s] set=%s' "$ZMX_SESSION" "${"${ZMX_SESSION+yes}"}"`,
    );

    expect(Result.isSuccess(outcome)).toBe(true);
    if (Result.isSuccess(outcome)) {
      // Empty, and *present* — the pair. An absent variable would print
      // `set=` rather than `set=yes`, and absence is a request a spawner is
      // free to ignore.
      expect(outcome.success).toBe("marker=[] set=yes");
    }
  });

  test("fails on a non-zero exit, carrying what the command itself said", async () => {
    // `ChildProcessSpawner.string` discards the exit code, so a hook that
    // failed would arrive as a successful empty answer — the same hole that
    // once had this repo reporting a workspace it had not created.
    const outcome = await run("echo 'no such package' >&2; exit 3");

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect(outcome.failure.reason).toContain("no such package");
      expect(outcome.failure.command).toBe("echo 'no such package' >&2; exit 3");
    }
  });

  test("says the exit code when the command said nothing at all", async () => {
    // A silent failure still has to produce a sentence. "exited 1" is thin and
    // it is not nothing, which is what an empty reason field would be.
    const outcome = await run("exit 1");

    expect(Result.isFailure(outcome)).toBe(true);
    if (Result.isFailure(outcome)) {
      expect(outcome.failure.reason).toBe("exited 1");
    }
  });
});
