// Two processes, one session, one store — the thing a unit test cannot show.
//
//     bun run probe:claim                     a scratch database
//     bun run probe:claim ~/.awp/awp.sqlite   the real one
//
// `session-claim.test.ts` drives the table through a single connection, which
// proves the logic and not the property: the failure this guard exists for is
// **two processes**, and one of them is always somebody else. So this takes a
// claim, spawns a child that tries to take the same one, and prints what the
// child was told — then releases and sends the child in again to show the
// conversation is openable once nobody is in it.
//
// It runs no agent, spawns no adapter and touches no session: the id below is
// invented and belongs to nothing. Safe inside a zmx session, and safe against
// the daemon somebody is working in — the row it writes names a conversation
// that does not exist, and it is deleted on the way out.

import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Db, layer } from "@awp-kit/store";
import { Effect, Result } from "effect";
import { claimMigration, claims as claimsOn } from "../session-claim";

const SESSION = "probe-claim-0000-not-a-real-session";

const path =
  process.env["AWP_CLAIM_DB"] ??
  process.argv[2] ??
  join(mkdtempSync(join(tmpdir(), "awp-claim-probe-")), "claims.sqlite");

const child = process.env["AWP_CLAIM_CHILD"] === "1";
const owner = child ? "the child probe" : "the parent probe";

const attempt = Effect.gen(function* () {
  const db = yield* Db;
  const claims = claimsOn(db, { owner, pid: process.pid });
  const took = yield* Effect.result(claims.take(SESSION));
  if (Result.isSuccess(took)) {
    return { ok: true as const, claims };
  }
  return { ok: false as const, reason: took.failure.reason };
}).pipe(Effect.provide(layer(path, [claimMigration])), Effect.scoped);

if (child) {
  // The child holds nothing: it reports what it was told and goes, so the
  // parent's claim is still the parent's when it returns.
  const asked = await Effect.runPromise(attempt);
  console.log(asked.ok ? "TOOK IT" : `REFUSED — ${asked.reason}`);
  process.exit(0);
}

const ask = (): string =>
  spawnSync(process.execPath, [import.meta.filename], {
    env: { ...process.env, AWP_CLAIM_CHILD: "1", AWP_CLAIM_DB: path },
    encoding: "utf8",
  }).stdout.trim();

await Effect.runPromise(
  Effect.gen(function* () {
    const db = yield* Db;
    const claims = claimsOn(db, { owner, pid: process.pid });

    console.log(`store   ${path}`);
    console.log(`session ${SESSION}\n`);

    yield* claims.take(SESSION);
    console.log(`held by pid ${String(process.pid)}`);
    console.log(`  a second process asks →  ${ask()}`);

    yield* claims.release(SESSION);
    console.log("\nreleased");
    console.log(`  a second process asks →  ${ask()}`);

    // Tidy up after the child, which took it and went.
    const left = yield* claims.holder(SESSION);
    if (left !== undefined) {
      yield* Effect.ignore(
        Effect.sync(() => db.prepare("delete from chat_claims where session_id = ?").run(SESSION)),
      );
    }
    console.log("\nthe row is gone — nothing of this probe is left in the store");
  }).pipe(Effect.provide(layer(path, [claimMigration])), Effect.scoped),
);
