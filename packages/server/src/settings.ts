import { homedir } from "node:os";
import { join } from "node:path";
import { Context, Effect, Layer, Schema } from "effect";

// What the person using awp has configured.
//
// `~/.config/awp/config.json`, which the Go implementation wrote and which is
// already on this machine. Read rather than replaced: a rewrite that ignored an
// existing config would silently change the agent someone launches and the
// bookmarks they get, and neither would look like a settings problem.
//
// ── only two keys, deliberately ────────────────────────────────────────────
// The file also carries `actions` and `deck.project_roots`, and this reads
// neither. A setting is read when something needs it, so that the shape it is
// read *into* is decided by a caller that exists. Parsing the rest now would be
// four types and no consumers.
//
// ── read every time, not held ──────────────────────────────────────────────
// It is a few hundred bytes and it is read when a job starts, so caching it
// would buy nothing and cost the thing that matters: editing the file has to
// take effect without restarting a daemon that may be holding a dozen ptys.
//
// ── it never fails ─────────────────────────────────────────────────────────
// A missing file is the ordinary case on a machine that has never run awp, and
// a malformed one is a typo. Neither is a reason for a daemon to refuse to
// start, so both give the defaults and say what happened in `problem` — which
// is a field rather than a log line because the only useful place for it is in
// front of the person who made the typo.

/** The file, as much of it as is read. Unknown keys are ignored. */
const File = Schema.Struct({
  agent: Schema.optional(Schema.String),
  deck: Schema.optional(Schema.Struct({ bookmark_prefix: Schema.optional(Schema.String) })),
});

export interface AwpSettings {
  /**
   * The agent command, already split into argv.
   *
   * Split on whitespace, which is what the Go implementation did and is wrong
   * for a quoted argument — `--flag "two words"` becomes two. No configured
   * agent needs one today; the day one does, this is the line to fix rather
   * than the call site to work around.
   */
  readonly agent: ReadonlyArray<string>;
  /**
   * What new bookmarks are prefixed with — `andrew` gives `andrew/<name>`.
   *
   * Absent means no bookmark at all, not an unprefixed one. A bare workspace
   * name in a shared repository's bookmark list is a name nobody can attribute,
   * and silently creating one is worse than creating none.
   */
  readonly bookmarkPrefix: string | undefined;
  /** What went wrong reading the file, if anything. See above. */
  readonly problem: string | undefined;
}

/** With no config at all. The agent is what awp has always launched. */
export const DEFAULTS: AwpSettings = {
  agent: ["claude"],
  bookmarkPrefix: undefined,
  problem: undefined,
};

export const SETTINGS_FILE = join(homedir(), ".config", "awp", "config.json");

export class Settings extends Context.Service<
  Settings,
  { readonly read: () => Effect.Effect<AwpSettings> }
>()("awp/Settings") {}

const parse = (text: string): AwpSettings => {
  const decoded = Schema.decodeUnknownSync(File)(JSON.parse(text) as unknown);
  const agent = (decoded.agent ?? "").trim();
  const prefix = (decoded.deck?.bookmark_prefix ?? "").trim();
  return {
    agent: agent === "" ? DEFAULTS.agent : agent.split(/\s+/u),
    bookmarkPrefix: prefix === "" ? undefined : prefix,
    problem: undefined,
  };
};

export const make = (path: string = SETTINGS_FILE) =>
  Effect.succeed({
    // `Effect.promise`, so this has no error channel at all — the decision
    // about a missing or malformed file is made here rather than pushed into
    // one for every caller to handle identically. See the note at the top.
    read: () =>
      Effect.promise(async (): Promise<AwpSettings> => {
        try {
          const { readFile } = await import("node:fs/promises");
          return parse(await readFile(path, "utf8"));
        } catch (cause) {
          return {
            ...DEFAULTS,
            // A missing file is not worth a sentence — it is what a machine
            // that has never run awp looks like. Anything else is.
            problem: isMissing(cause) ? undefined : `${path}: ${String(cause)}`,
          };
        }
      }),
  });

const isMissing = (cause: unknown): boolean =>
  typeof cause === "object" &&
  cause !== null &&
  "code" in cause &&
  (cause as { readonly code?: unknown }).code === "ENOENT";

export const layer = (path: string = SETTINGS_FILE): Layer.Layer<Settings> =>
  Layer.effect(Settings)(make(path));
