// How an awp session is named.
//
// Pure functions, no service, no Effect. A name is a value computed from a
// project, a workspace and a kind — there is nothing to inject and nothing to
// fail. Imported by the multiplexer and by anything displaying a session.
//
// Ported from the Go implementation. Its comments are evidence that a rule was
// once right, not proof that it is: one was found wrong on the way in — see the
// note on identityLabels. So the properties the rest of the system depends on
// are proved in naming.test.ts, and the claims that are really claims about
// *zmx* were re-measured by `bun run probe:claims` on 2026-08-25 rather than
// inherited. The limit and the silent slash refusal both held; only the
// illustration of the limit was wrong.

import { createHash } from "node:crypto";

/**
 * The longest name zmx will accept. **Re-measured 2026-08-25** by
 * `bun run probe:claims`: 46 accepted, 47 refused with
 *
 *   error: session name is too long (47 bytes, max 46 for socket directory
 *   "/var/folders/…/T/zmx-502")
 *
 * zmx turns a name into a socket path, so the bound is what a unix socket
 * address holds: `sun_path` is 104 bytes on darwin, and the daemon's socket
 * directory under a macOS per-user TMPDIR spends 56 of them.
 *
 * 46 is a floor rather than a guess: that TMPDIR is a fixed width by
 * construction and a Linux socket directory is shorter, so a name that fits
 * here fits there. And zmx names its own max in the refusal, which is a better
 * message than any check here would write.
 *
 * Worth not getting wrong later. Too high and zmx refuses a name awp thinks is
 * legal. Too low and names are shortened that need not have been — and since a
 * name is an address, changing this renames every session that was being
 * shortened, which reads to a developer as all of them having disappeared.
 */
export const MAX_SESSION_NAME = 46;

/**
 * The budget for a kind on its own.
 *
 * A choice rather than a measurement: room for `action_` plus a nine-character
 * action name. The kind is bounded separately from the stem because it is
 * compared *without* reference to a workspace — a pane resolves its user action
 * by matching kinds — whereas a stem is only ever compared against a workspace
 * that can reproduce it.
 */
const MAX_KIND = 16;

/**
 * How much of a name's fingerprint survives shortening: 4 hex characters,
 * 65536 buckets.
 *
 * Not there to make collisions impossible, only to make them not happen among
 * the handful of workspaces one project has. What it replaces is plain
 * truncation, under which every workspace sharing a prefix collapses onto one
 * session — and two agents would be one agent.
 */
const SHORTEN_HASH_LEN = 4;

/** Label keys awp writes on every session it creates. */
export const LABEL_PROJECT = "awp_project";
export const LABEL_WORKSPACE = "awp_workspace";
export const LABEL_KIND = "awp_kind";

/**
 * The spelling of a name segment that survives being written into a session
 * name and read back out of one.
 *
 * Exported because a caller recognising a segment on the way back — a
 * user-action pane matching its kind against the configured actions — has to
 * compare against the same reduction, not against the original name.
 *
 * Everything outside a conservative ASCII set becomes `_`, which is also what
 * makes a dot-split of a name safe: no segment can contain a dot.
 *
 * The slash is the one that matters most. Measured 2026-08-25: zmx refuses a
 * name containing one and says **nothing at all** — no error, no session, no
 * exit code to notice. Without this reduction that failure is invisible, and a
 * workspace named after a branch (`feature/thing`) would silently never get a
 * session.
 */
export const sanitize = (value: string): string => {
  let out = "";
  for (const char of value) {
    out += /^[A-Za-z0-9_-]$/u.test(char) ? char : "_";
  }
  return out === "" ? "_" : out;
};

/**
 * Bound a name segment, keeping the front of it and a fingerprint of the whole.
 *
 * Truncation alone cannot be used: two workspaces named after the same PR
 * (`pr-2336-dev-mlwzqyrmxslo`, `pr-2336-dev-qqtnvbdlrxzz`) share every
 * character the budget has room for and would address one session — so one
 * workspace's agent key would open the other's agent. The fingerprint is of the
 * untruncated input, so it differs exactly when the inputs do.
 *
 * Deterministic, because the name is an address: the same row and kind must
 * resolve to the same session on every pass, across restarts, from a deck and
 * from a detached create alike.
 *
 * Lengths are counted in UTF-16 code units rather than the Go original's bytes.
 * Equivalent in practice — every caller passes an already-sanitized segment,
 * and sanitize leaves only ASCII.
 */
const shortenTo = (value: string, max: number): string => {
  if (value.length <= max) {
    return value;
  }
  const fingerprint = createHash("sha256").update(value).digest("hex").slice(0, SHORTEN_HASH_LEN);

  const keep = max - SHORTEN_HASH_LEN - 1;
  if (keep < 1) {
    // No room to keep anything recognisable. The fingerprint alone is still a
    // unique address, which is the part that cannot be given up.
    return fingerprint;
  }
  // The separator is what stops a truncated name from reading as a real one
  // that happens to end in hex.
  return `${value.slice(0, keep).replace(/[-_.]+$/u, "")}-${fingerprint}`;
};

/**
 * The spelling of a kind inside a session name.
 *
 * A caller holding a kind must compare against this rather than against the
 * kind it started with — that is what keeps a shortened kind resolving to the
 * action it came from.
 */
export const sessionKind = (kind: string): string => shortenTo(sanitize(kind), MAX_KIND);

/**
 * The part of a name every one of a workspace's sessions shares: `awp`, the
 * project, and the workspace, with only the kind still to come.
 *
 * Unshortened — this is the stem a name would have if it fit, and
 * {@link sessionName} shortens it against the room the kind leaves. A caller
 * matching a session back to a workspace uses {@link stemMatches} rather than
 * comparing against this, because the stem it holds may be a shortened one.
 */
export const sessionStem = (project: string, workspace: string): string =>
  `awp.${sanitize(project)}.${sanitize(workspace)}`;

/**
 * The only way to name an awp session, so every call site spells it the same
 * and `zmx ls` stays readable.
 *
 * The stem gets whatever the kind does not need, so a name only loses anything
 * when it would not otherwise exist. Every session in the socket directory
 * today is one that fits, and a spelling that changed for no reason would leave
 * all of them unrecognised — awp would start a second of each.
 */
export const sessionName = (project: string, workspace: string, kind: string): string => {
  const shortKind = sessionKind(kind);
  const stem = shortenTo(sessionStem(project, workspace), MAX_SESSION_NAME - 1 - shortKind.length);
  return `${stem}.${shortKind}`;
};

/**
 * Whether `stem` — read off a session name — is the one this workspace would
 * produce.
 *
 * Two spellings can be right, and which one depends on how much room the kind
 * left, which the stem alone does not say. So the comparison reproduces the
 * shortening at the length the stem actually has: shortening is deterministic
 * given an input and a budget, so a stem this workspace generated matches at
 * exactly one length and no other workspace's does.
 *
 * This is why a caller asks per workspace rather than looking a stem up in a
 * map. With a handful of workspaces the loop costs nothing, and it means the
 * name generator is free to spend the budget however it likes without a second
 * place having to agree on the arithmetic.
 */
export const stemMatches = (project: string, workspace: string, stem: string): boolean => {
  const full = sessionStem(project, workspace);
  return stem === full || (stem.length < full.length && stem === shortenTo(full, stem.length));
};

/**
 * Separate a name into stem and kind, for a caller holding a name and a set of
 * stems it knows.
 *
 * The kind is whatever followed the last dot, which is safe on a shortened
 * name: shortening only ever touches the stem, because the kind is what reopens
 * a pane and what resolves a user action's command.
 */
export const splitSessionName = (
  name: string,
): { readonly stem: string; readonly kind: string } | undefined => {
  const trimmed = name.trim();
  const index = trimmed.lastIndexOf(".");
  if (index <= 0 || !trimmed.startsWith("awp.")) {
    return undefined;
  }
  return { stem: trimmed.slice(0, index), kind: trimmed.slice(index + 1) };
};

export interface SessionIdentity {
  readonly project: string;
  readonly workspace: string;
  readonly kind: string;
}

/**
 * Read a name {@link sessionName} produced back into its parts, reporting
 * whether it was one of ours at all — `zmx ls` lists every session on the
 * machine, including ones awp did not create.
 *
 * The split is safe because {@link sanitize} replaces a dot with an underscore,
 * so no segment can contain one.
 *
 * Lossy in two ways worth knowing: a project or workspace whose real name
 * contained a dot comes back with an underscore, and one whose name was
 * shortened to fit comes back shortened. Both match no workspace, which is why
 * anything that must find one should generate the name it expects rather than
 * read the name it got. Prefer the labels — see {@link identityLabels}.
 */
export const parseSessionName = (name: string): SessionIdentity | undefined => {
  const parts = name.split(".");
  if (parts.length !== 4 || parts[0] !== "awp") {
    return undefined;
  }
  const [, project, workspace, kind] = parts;
  return project === undefined || workspace === undefined || kind === undefined
    ? undefined
    : { project, workspace, kind };
};

/**
 * What awp sets on a session so it can be recognised later.
 *
 * A name cannot keep carrying the identity: names are bounded by the socket
 * path they become, and `awp.<project>.<workspace>.<kind>` passes that for
 * ordinary input — a workspace named after a PR's head branch spends 24
 * characters on its own, and
 * `awp.thicket.pr-2357-lantern-lantern-email-link-identity.agent` is 60. A name
 * shortened to exist can no longer be split back into the parts it was made
 * from, so the parts are stated separately.
 *
 * (The Go original cited `awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev` as 47
 * and therefore over the limit. It is 45, and fits. The rule is right; that
 * example was not, which is why the tests below check the property rather than
 * reproduce the anecdote.)
 *
 * Labels rather than a file awp keeps: they live and die with the session, so
 * there is nothing to reconcile when one is killed from outside awp, and
 * `zmx ls` prints them inline — the read costs no extra call.
 *
 * Unsanitized, deliberately: a label is data rather than an address, so it can
 * hold the workspace's real name, which is what has to be matched against a
 * workspace and what a human reading `zmx ls` wants to see.
 */
export const identityLabels = (
  project: string,
  workspace: string,
  kind: string,
): Record<string, string> => ({
  [LABEL_PROJECT]: project.trim(),
  [LABEL_WORKSPACE]: workspace.trim(),
  [LABEL_KIND]: kind.trim(),
});
