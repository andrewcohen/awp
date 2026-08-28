import { spawn } from "node:child_process";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NodeChildProcessSpawner, NodeFileSystem, NodePath } from "@effect/platform-node-shared";
import { Effect, Layer } from "effect";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { Multiplexer } from "./multiplexer";
import * as Zmx from "./zmx";

// What `send` hands to zmx, checked against a zmx that writes down its argv.
//
// ── why not the real one, and why not a fake spawner ───────────────────────
//
// `zmx.test.ts` runs against a real zmx and can, because everything it calls
// answers a question and changes nothing. `send` types into a session, so it
// cannot go there: the sessions on this machine are somebody's work.
//
// A fake `ChildProcessSpawner` was the other option and is worse. That service
// is a wide interface — handles, sinks, streams, file descriptors — so faking
// it is mostly writing a process, and the one thing under test is *what the
// process was asked to run*. A script on PATH that appends its arguments to a
// file is the smallest thing that answers that, and it answers it the way
// AGENTS.md asks: by looking at what the child actually received rather than
// at what was handed to the layer above it.
//
// ── what is being pinned ───────────────────────────────────────────────────
//
// Two invocations: the text, and then a lone carriage return. Both halves
// matter and neither is obvious from reading `send`.
//
//   the byte     a TUI runs in raw mode, where no LF/CR translation happens
//                and Return arrives as CR. LF worked for months because a
//                *shell* reads in canonical mode, where the line discipline
//                ends a line on LF — so every check by hand passed.
//
//   the split    a TUI treats a multi-byte chunk as pasted text, which is the
//                feature that stops a pasted snippet running line by line. A
//                CR on the end of the prompt is inside that chunk, so it is
//                part of the paste rather than a keypress.
//
// Measured against a raw-mode reader in a real session before this was
// written; the numbers are in the comment on `send`. This test is what stops
// it being quietly folded back into one write.

/** Argument separator, and record separator. Neither can occur in an argv. */
const UNIT = "";
const RECORD = "";

let scratch = "";
let log = "";
/** What the fake `zmx ls` prints. Written per test. */
let listing = "";

beforeAll(() => {
  scratch = mkdtempSync(join(tmpdir(), "awp-send-"));
  log = join(scratch, "argv");
  listing = join(scratch, "listing");
  writeFileSync(listing, "");
  writeFileSync(
    join(scratch, "zmx"),
    [
      "#!/bin/sh",
      // One line per invocation, arguments separated by a byte that cannot
      // appear in one — so a prompt containing spaces or newlines still reads
      // back as a single argument.
      `printf '%s\\037' "$@" >> "${log}"`,
      `printf '\\036' >> "${log}"`,
      // `ls` answers from a file a test writes, which is what lets one say
      // "this session exists and is running" and another "it exists and its
      // task has exited" without a real zmx.
      `if [ "$1" = "ls" ]; then cat "${listing}"; fi`,
      "",
    ].join("\n"),
  );
  chmodSync(join(scratch, "zmx"), 0o755);
  process.env["PATH"] = `${scratch}:${process.env["PATH"] ?? ""}`;
});

afterAll(() => rmSync(scratch, { recursive: true, force: true }));

const platform = NodeChildProcessSpawner.layer.pipe(
  Layer.provideMerge(NodeFileSystem.layer),
  Layer.provideMerge(NodePath.layer),
);

/** Every invocation since the last read, as argument lists. Consumes the log. */
const calls = (): ReadonlyArray<ReadonlyArray<string>> => {
  let raw = "";
  try {
    raw = readFileSync(log, "utf8");
  } catch {
    // Never invoked, which for the empty-text case is the whole assertion.
    return [];
  }
  rmSync(log, { force: true });
  return raw
    .split(RECORD)
    .filter((one) => one !== "")
    .map((one) => one.split(UNIT).filter((arg) => arg !== ""));
};

const start = (name: string): Promise<void> =>
  Effect.runPromise(
    Effect.gen(function* () {
      const mux = yield* Multiplexer;
      return yield* mux.start({ name, cwd: scratch, command: ["claude"] });
    }).pipe(Effect.provide(Zmx.layer), Effect.provide(platform), Effect.orDie),
  );

const send = (name: string, text: string): Promise<void> =>
  Effect.runPromise(
    Effect.gen(function* () {
      const mux = yield* Multiplexer;
      return yield* mux.send(name, text);
    }).pipe(Effect.provide(Zmx.layer), Effect.provide(platform), Effect.orDie),
  );

describe("send", () => {
  test("writes the text and the Return key separately", async () => {
    await send("awp.thicket.lantern.agent", "review this");
    expect(calls()).toEqual([
      ["send", "awp.thicket.lantern.agent", "review this"],
      ["send", "awp.thicket.lantern.agent", "\r"],
    ]);
  });

  test("the text carries no trailing byte of its own", async () => {
    // The shape this replaced was `${text}\n` in one write, which a TUI reads
    // as pasted text with a newline in it rather than as a submitted line. A
    // multi-line prompt is the ordinary case here — a review is several
    // comments — so the newlines inside it must survive untouched while the
    // end of it must not gain one.
    await send("s", "one\ntwo");
    const [text] = calls();
    expect(text?.[2]).toBe("one\ntwo");
  });

  test("says nothing at all rather than a bare Return", async () => {
    // The empty guard predates this and matters more now. A newline on its own
    // is still a keypress, and with the write split in two, dropping the guard
    // would submit an empty line into somebody's agent rather than doing
    // nothing.
    await send("s", "");
    expect(calls()).toEqual([]);
  });
});

describe("start", () => {
  // `zmx ls` prints one session per line as tab-separated `key=value`.
  //
  // The pid is what decides this now, not zmx's `ended` — that field turned
  // out to be about the last *task* rather than the session, so `start` asks
  // the process table instead. See `withProcesses`.
  //
  //   this process   alive, and not a shell   → busy
  //   999999         not in the table         → over
  const listed = (name: string, pid: number) =>
    writeFileSync(
      listing,
      `  name=${name}\tpid=${pid}\tclients=0\tcreated=1\tstart_dir=/w\tawp_kind=agent\n`,
    );

  /** A pid nothing is using. Chosen high; checked, rather than assumed. */
  const GONE = 999_999;

  test("creates the session when there is none", async () => {
    await start("awp.thicket.lantern.agent");
    const [, run] = calls();
    expect(run).toEqual(["run", "awp.thicket.lantern.agent", "-d", "claude"]);
  });

  test("leaves a live session alone", async () => {
    // The guard, and the reason it exists: `zmx run` types into whatever is in
    // there, which for a name that was not ours is somebody's editor.
    listed("awp.thicket.lantern.agent", process.pid);
    await start("awp.thicket.lantern.agent");
    expect(calls().map(([verb]) => verb)).toEqual(["ls"]);
  });

  test("runs again in a session whose process is gone", async () => {
    // What "the session had ended" was. A session whose task exited is still
    // listed, so skipping on the name alone adopted the corpse: the retry did
    // nothing, and the job briefed a dead shell —
    //
    //   ZMX_TASK_COMPLETED:1
    //   $ Port the review capability from …
    //   -bash: syntax error near unexpected token `('
    //
    // `ended` is exactly the distinction the guard was reaching for. An idle
    // session is what `zmx run` is for, and running in it keeps the scrollback
    // that killing and recreating would lose.
    listed("awp.thicket.lantern.agent", GONE);
    await start("awp.thicket.lantern.agent");
    const [, run] = calls();
    expect(run).toEqual(["run", "awp.thicket.lantern.agent", "-d", "claude"]);
  });

  test("runs again in a session sitting at a shell prompt", async () => {
    // The case the whole fix is about, against a real idle shell rather than a
    // hand-written process table.
    //
    // A session whose agent has exited is still listed and its shell is still
    // alive, so neither "the name exists" nor "the process is gone" answers
    // it. Skipping here is what left a job briefing a dead prompt:
    //
    //   ZMX_TASK_COMPLETED:1
    //   $ Port the review capability from …
    //   -bash: syntax error near unexpected token `('
    const idle = spawn("/bin/sh", ["-c", "read line"], { stdio: ["pipe", "ignore", "ignore"] });
    try {
      // Give it a moment to be in the process table under its own name rather
      // than still wearing the parent's.
      await new Promise((done) => setTimeout(done, 150));
      listed("awp.thicket.lantern.agent", idle.pid ?? GONE);
      await start("awp.thicket.lantern.agent");
      const [, run] = calls();
      expect(run).toEqual(["run", "awp.thicket.lantern.agent", "-d", "claude"]);
    } finally {
      idle.kill();
    }
  });

  test("a different session being live says nothing about this one", async () => {
    listed("awp.orchard.something-else.agent", process.pid);
    await start("awp.thicket.lantern.agent");
    expect(calls().map(([verb]) => verb)).toEqual(["ls", "run"]);
  });
});
