// Does the sqlite store work in the runtime that actually uses it?
//
// `store.test.ts` runs under vitest, which is Node, which has `node:sqlite`.
// The daemon runs under Bun, which does not — it has `bun:sqlite`, and the
// store picks between them at open time. So the tested arm and the shipped arm
// are different code, and no amount of running the suite says anything about
// the one the daemon takes.
//
// This is the shape AGENTS.md already names once, for `zmxChildEnv`: when the
// thing being checked happens in a different process, assert on what that
// process sees. Run it after touching `sqlite.ts`.
//
//   bun run probe:jobs-store
//
// Safe anywhere. It touches a temporary file and nothing else — no zmx, no
// session, no daemon.

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Effect } from "effect";
import type { Job } from "../job";
import { makeSqlite } from "../sqlite";

const scratch = mkdtempSync(join(tmpdir(), "awp-jobs-probe-"));
const path = join(scratch, "jobs.sqlite");

const record: Job = {
  id: "20260101-aaaa",
  kind: "probe",
  title: "a job written under bun",
  key: "probe-key",
  input: { name: "a", nested: [1, 2, 3] },
  status: "running",
  attempt: 2,
  attempts: 3,
  steps: ["one", "two", "three"],
  done: ["one", "two"],
  step: "three",
  error: undefined,
  cleanup: undefined,
  createdAt: new Date(1_700_000_000_000),
  startedAt: new Date(1_700_000_001_000),
  endedAt: undefined,
};

const write = Effect.gen(function* () {
  const store = yield* makeSqlite(path);
  yield* store.put(record);
  yield* store.append(record.id, ["wrote it", "under bun"]);
}).pipe(Effect.scoped);

// A second connection, because the interesting claim is that the row outlives
// the process that wrote it rather than that a Map held it.
const read = Effect.gen(function* () {
  const store = yield* makeSqlite(path);
  return {
    get: yield* store.get(record.id),
    byKey: yield* store.byKey("probe-key"),
    list: (yield* store.list()).length,
    log: yield* store.log(record.id),
  };
}).pipe(Effect.scoped);

const say = (label: string, ok: boolean, detail: string): void => {
  process.stdout.write(`${ok ? "  ok  " : "FAIL  "}${label.padEnd(28)}${detail}\n`);
};

const main = Effect.gen(function* () {
  process.stdout.write(`runtime: ${typeof Bun === "undefined" ? "node" : `bun ${Bun.version}`}\n`);
  process.stdout.write(`file:    ${path}\n\n`);

  yield* write;
  const back = yield* read;

  say("row survives the close", back.get?.id === record.id, String(back.get?.id));
  say(
    "dates round-trip",
    back.get?.startedAt?.getTime() === 1_700_000_001_000,
    String(back.get?.startedAt?.toISOString()),
  );
  say("absent stays absent", back.get?.endedAt === undefined, String(back.get?.endedAt));
  say(
    "json input round-trips",
    JSON.stringify(back.get?.input) === JSON.stringify(record.input),
    JSON.stringify(back.get?.input),
  );
  say(
    "done list round-trips",
    JSON.stringify(back.get?.done) === '["one","two"]',
    JSON.stringify(back.get?.done),
  );
  say("unique key finds it", back.byKey?.id === record.id, String(back.byKey?.id));
  say("listing sees one row", back.list === 1, String(back.list));
  say("log survives the close", back.log.join("|") === "wrote it|under bun", back.log.join("|"));
});

try {
  await Effect.runPromise(main);
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
