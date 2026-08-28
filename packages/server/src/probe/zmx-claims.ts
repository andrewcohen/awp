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
//   4. What a label value may hold — and that `labelValue` never exceeds it.
//
// Nothing here attaches. Every session it creates is detached, named with this
// process's pid, and killed on the way out.

import { labelValue } from "../naming";
import { requireOutsideZmxSession, childEnv } from "../zmx-session";

requireOutsideZmxSession();

const env = childEnv();
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

// ── 4. what a label value may hold ──────────────────────────────────────────
//
// Added after a colon in a sentence failed a real job. `create-workspace`
// labels the session it made, so `awp_label=Review: Inbox UI` refused the step,
// and the compensation took the whole workspace back out — the person got no
// workspace and a message about key-value pairs.
//
// zmx names its own rule in the refusal:
//
//   error: key-value kvs can only contain [a-z, A-Z, 0-9, -_.] characters:
//          value=[Review:]
//
// and hides a second one inside it. The value it reports is `Review:` and not
// the whole string, so the pair was split on whitespace *before* it was
// validated: a space is as fatal as the colon, and a value made entirely of
// legal characters with a space in it fails the same way. That half is not
// stated anywhere in zmx's own text, which is why it is measured here.
//
// One session for the whole claim, and it is the one this probe already needs.
const labelSession = `awpprobe-label-${process.pid}`;
const labelHost = tryCreate(labelSession);

if (!labelHost.accepted) {
  say(`CLAIM 4 — could not create ${labelSession}, skipped: ${labelHost.out}`);
} else {
  /** Set one label and say whether zmx took it, and what it reads back as. */
  const setLabel = (value: string): { accepted: boolean; back: string; out: string } => {
    const { code, out } = zmx("set", labelSession, `awp_label=${value}`);
    const got = zmx("get", labelSession).out;
    const found = /awp_label=(?<value>\S*)/u.exec(got);
    return { accepted: code === 0, back: found?.groups?.value ?? "", out };
  };

  // The raw values first, to state what is actually refused rather than to
  // assume the error message is complete.
  say("CLAIM 4a — what zmx refuses, unfiltered:");
  for (const raw of ["Review:", "Review Inbox", "fifty%", "a/b", "plain-legal_1.0"]) {
    const r = setLabel(raw);
    say(
      `  ${JSON.stringify(raw).padEnd(20)} accepted=${r.accepted} back=${JSON.stringify(r.back)}`,
    );
    if (!r.accepted) {
      say(`    said: ${JSON.stringify(r.out)}`);
    }
  }

  // Then the property that matters: everything `labelValue` produces survives.
  // This is the assertion the unit test structurally cannot make — it checks
  // the output against a regex written here, and this checks it against zmx.
  say("CLAIM 4b — every labelValue result is accepted, and reads back unchanged:");
  const said = [
    "Review: Inbox UI",
    "fix(jobs): trust a new workspace before its agent starts",
    "50% faster!",
    "a/b\\c",
    "tiered-discounts",
    "port the review capability from the deck starting with the inbox scope",
  ];
  let held = true;
  for (const one of said) {
    const value = labelValue(one);
    if (value === "") {
      say(`  ${JSON.stringify(one)} → no label, nothing to set`);
      continue;
    }
    const r = setLabel(value);
    const ok = r.accepted && r.back === value;
    held &&= ok;
    say(`  ${JSON.stringify(value).padEnd(52)} accepted=${r.accepted} intact=${r.back === value}`);
    if (!ok) {
      say(`    said: ${JSON.stringify(r.out)} back: ${JSON.stringify(r.back)}`);
    }
  }
  say(`CLAIM 4 — labelValue only ever produces values zmx takes: ${verdict(held)}`);

  // And the length, where the expected answer is "no bound at all".
  // `label.zig`'s assertLabel checks the character set and nothing else, and
  // ipc.zig grows its read buffer, so nothing in zmx 0.7.0 refuses a long
  // value. amoeba's MAX_LABEL is about `zmx ls` staying readable, not about
  // being rejected — and if a future zmx does start refusing, this is where it
  // shows up rather than in somebody's failed job.
  say("CLAIM 4c — how long a value zmx will take (expecting: no limit):");
  for (const length of [48, 64, 128, 256, 512]) {
    const r = setLabel("x".repeat(length));
    say(
      `  ${String(length).padStart(4)} chars  accepted=${r.accepted} intact=${r.back.length === length}`,
    );
    if (!r.accepted) {
      say(`    said: ${JSON.stringify(r.out)}`);
      break;
    }
  }
}

// ── cleanup ─────────────────────────────────────────────────────────────────
for (const name of created) {
  zmx("kill", name, "--force");
  say(`cleaned up ${name}`);
}
if (created.length === 0) {
  say("nothing to clean up");
}
