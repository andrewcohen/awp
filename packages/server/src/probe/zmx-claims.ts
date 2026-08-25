// Re-proves what the Go implementation asserted about zmx, rather than
// inheriting it.
//
// Run from a plain terminal, OUTSIDE zmx and outside zdeck:
//
//   bun run probe:claims
//
// Motivation: one of those assertions was already found wrong. The Go comment
// justifying the identity labels cites
// `awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev` as 47 characters and
// therefore over the limit. It is 45. The rule it was defending is fine; the
// evidence for it was not. So the rest gets checked.
//
// Claims under test:
//
//   1. 46 is the longest name zmx accepts, and it reports its own max.
//   2. A name containing a slash is refused.
//   3. `zmx ls` marks the caller's own session with "→".
//
// Nothing here attaches. Every session it creates is detached, named with this
// process's pid, and killed on the way out.

import { requireOutsideZmxSession, zmxChildEnv } from "../zmx-session";

requireOutsideZmxSession();

const env = zmxChildEnv();
const created: string[] = [];

const zmx = (...args: string[]): { code: number; out: string } => {
  const result = Bun.spawnSync(["zmx", ...args], { env });
  return {
    code: result.exitCode,
    out: `${result.stdout.toString()}${result.stderr.toString()}`.trim(),
  };
};

/** Try to create a detached session, and say whether zmx accepted the name. */
const tryCreate = (name: string): { accepted: boolean; out: string } => {
  // `run -d` creates without attaching, so nothing opens a client and nothing
  // reflows. `true` exits immediately.
  const { code, out } = zmx("run", name, "-d", "true");
  const listed = zmx("ls").out.includes(`name=${name}\t`);
  if (listed) {
    created.push(name);
  }
  return { accepted: code === 0 && listed, out };
};

const say = (...parts: unknown[]) => console.log("[claims]", ...parts);
const verdict = (holds: boolean) => (holds ? "HOLDS" : "FALSE");

// ── 1. the length limit ─────────────────────────────────────────────────────
// Binary search would create a session per probe. Instead: check the two names
// either side of the claimed boundary. A refusal creates nothing.
const nameOfLength = (n: number): string => {
  const suffix = `-${process.pid}`;
  return `awpprobe${"x".repeat(Math.max(0, n - 8 - suffix.length))}${suffix}`.slice(0, n);
};

const at46 = nameOfLength(46);
const at47 = nameOfLength(47);

say(`46-char name (${at46.length}): ${at46}`);
const r46 = tryCreate(at46);
say(`  accepted: ${r46.accepted}`, r46.out === "" ? "" : `— ${r46.out}`);

say(`47-char name (${at47.length}): ${at47}`);
const r47 = tryCreate(at47);
say(`  accepted: ${r47.accepted}`, r47.out === "" ? "" : `— ${r47.out}`);

const limitClaim = r46.accepted && !r47.accepted;
say(`CLAIM 1 — 46 is the limit: ${verdict(limitClaim)}`);
if (!r47.accepted) {
  say(`  refusal text: ${JSON.stringify(r47.out)}`);
  say(`  does it name its own max? ${/max\s*\d+/iu.test(r47.out)}`);
}

// ── 2. a slash is refused ───────────────────────────────────────────────────
const slashName = `awpprobe-${process.pid}/child`;
const rSlash = tryCreate(slashName);
say(`CLAIM 2 — a slash is refused: ${verdict(!rSlash.accepted)}`);
say(`  said: ${JSON.stringify(rSlash.out)}`);
say(`  silently? ${rSlash.out === "" ? "yes — no message at all" : "no, it explains"}`);

// ── 3. `zmx ls` marks the caller's own session ──────────────────────────────
// Cannot be observed from here: this process is not running inside a session,
// which is the condition the probe requires. Recorded as untested rather than
// guessed, since the parser strips "→" and getting that wrong loses exactly one
// row — the caller's own, which is the one a developer looks for first.
say("CLAIM 3 — `zmx ls` marks the caller with →: UNTESTABLE from outside a session");
say("  the parser strips it regardless, which is the safe direction");

// ── cleanup ─────────────────────────────────────────────────────────────────
for (const name of created) {
  zmx("kill", name, "--force");
  say(`cleaned up ${name}`);
}
if (created.length === 0) {
  say("nothing to clean up");
}
