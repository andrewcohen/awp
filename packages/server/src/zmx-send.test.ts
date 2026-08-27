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

beforeAll(() => {
  scratch = mkdtempSync(join(tmpdir(), "awp-send-"));
  log = join(scratch, "argv");
  writeFileSync(
    join(scratch, "zmx"),
    [
      "#!/bin/sh",
      // One line per invocation, arguments separated by a byte that cannot
      // appear in one — so a prompt containing spaces or newlines still reads
      // back as a single argument.
      `printf '%s\\037' "$@" >> "${log}"`,
      `printf '\\036' >> "${log}"`,
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
