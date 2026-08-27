import { homedir } from "node:os";
import { join } from "node:path";
import { Context, Effect, Layer, Schema } from "effect";

// What the person using awp has configured.
//
// Read rather than replaced: these files were written by the Go implementation
// and are already on this machine, so a rewrite that ignored one would silently
// change the agent someone launches and the bookmarks they get, and neither
// would look like a settings problem.
//
// ── two files, and the project one wins ────────────────────────────────────
//
//   ~/.config/awp/config.json    global — the agent, the bookmark prefix
//   <repo>/.awp/config.json      per project — how *this* repository is set up
//
// Merged per field, and the rule is replace-if-empty rather than a deep merge,
// because that is what the Go implementation does and both files are already
// written against it. A project that says nothing about hooks inherits the
// global ones; a project that lists one inherits none of them. Concatenating
// instead would be defensible and would silently change what every existing
// config means.
//
// **Read from the source repository, not from the workspace.** `.awp/` is not
// tracked, so a fresh `jj workspace add` has no copy of it — the Go
// implementation symlinked one in for exactly this reason. The create-workspace
// job already carries `input.repo`, which is the repository the workspace was
// made *from*, and that is the honest place to look.
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

/** Either file, as much of it as is read. Unknown keys are ignored. */
const File = Schema.Struct({
  agent: Schema.optional(Schema.String),
  hooks: Schema.optional(
    Schema.Struct({ bootstrap: Schema.optional(Schema.Array(Schema.String)) }),
  ),
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
  /**
   * What to run in a new workspace, in order, before its agent is briefed.
   *
   * `hooks.bootstrap` in either file, the project's winning outright when it
   * lists any. Whole shell lines rather than argv, unlike {@link agent} — a
   * hook is a line somebody writes in a config file and `mise trust && bun
   * install` is its ordinary shape. See `bootstrap.ts` for the rest of that.
   *
   * Empty by default, and empty is a real answer: most repositories need
   * nothing, and inventing a default here would run somebody's package manager
   * without being asked.
   */
  readonly bootstrap: ReadonlyArray<string>;
  /** What went wrong reading the file, if anything. See above. */
  readonly problem: string | undefined;
}

/** With no config at all. The agent is what awp has always launched. */
export const DEFAULTS: AwpSettings = {
  agent: ["claude"],
  bootstrap: [],
  bookmarkPrefix: undefined,
  problem: undefined,
};

export const SETTINGS_FILE = join(homedir(), ".config", "awp", "config.json");

export class Settings extends Context.Service<
  Settings,
  { readonly read: (repo?: string) => Effect.Effect<AwpSettings> }
>()("awp/Settings") {}

const parse = (text: string): AwpSettings => {
  const decoded = Schema.decodeUnknownSync(File)(JSON.parse(text) as unknown);
  const agent = (decoded.agent ?? "").trim();
  const prefix = (decoded.deck?.bookmark_prefix ?? "").trim();
  return {
    agent: agent === "" ? DEFAULTS.agent : agent.split(/\s+/u),
    // Blank lines dropped. A config file people edit by hand accumulates them,
    // and `sh -c ""` succeeds silently — so keeping them would put a step in
    // the log that says nothing and did nothing.
    bootstrap: (decoded.hooks?.bootstrap ?? [])
      .map((one) => one.trim())
      .filter((one) => one !== ""),
    bookmarkPrefix: prefix === "" ? undefined : prefix,
    problem: undefined,
  };
};

const isMissing = (cause: unknown): boolean =>
  typeof cause === "object" &&
  cause !== null &&
  "code" in cause &&
  (cause as { readonly code?: unknown }).code === "ENOENT";

/** `<repo>/.awp/config.json` — where a project says how it is set up. */
export const projectConfigPath = (repo: string): string => join(repo, ".awp", "config.json");

/**
 * One file, or the defaults and a sentence about why not.
 *
 * `Effect.promise`, so this has no error channel at all — the decision about a
 * missing or malformed file is made here rather than pushed into one for every
 * caller to handle identically. See the note at the top.
 */
const readFileAt = (path: string): Effect.Effect<AwpSettings> =>
  Effect.promise(async (): Promise<AwpSettings> => {
    try {
      const { readFile } = await import("node:fs/promises");
      return parse(await readFile(path, "utf8"));
    } catch (cause) {
      return {
        ...DEFAULTS,
        // A missing file is not worth a sentence — it is what a machine that
        // has never run awp looks like, and a project with nothing to say.
        // Anything else is.
        problem: isMissing(cause) ? undefined : `${path}: ${String(cause)}`,
      };
    }
  });

/**
 * Project over global, per field, replace-if-empty.
 *
 * Not a deep merge and not a concatenation, because the Go implementation does
 * exactly this and both files on this machine were written against it. A
 * project that lists any hooks gets *only* its own — inheriting the global ones
 * as well would mean a repository could never turn one off.
 */
export const merge = (global: AwpSettings, project: AwpSettings): AwpSettings => ({
  agent: project.agent === DEFAULTS.agent ? global.agent : project.agent,
  bootstrap: project.bootstrap.length === 0 ? global.bootstrap : project.bootstrap,
  bookmarkPrefix: project.bookmarkPrefix ?? global.bookmarkPrefix,
  // Whichever file was unreadable, said once. Two problems is a rarer case than
  // the message being lost, and the project's is the one a person can fix.
  problem: project.problem ?? global.problem,
});

export const make = (path: string = SETTINGS_FILE) =>
  Effect.succeed({
    /**
     * @param repo  the repository whose `.awp/config.json` applies, when there
     *              is one. Absent gives the global file alone — which is right
     *              for a caller that is not standing in a project, and is why
     *              this is optional rather than required.
     */
    read: (repo?: string) =>
      Effect.gen(function* () {
        const global = yield* readFileAt(path);
        if (repo === undefined) {
          return global;
        }
        return merge(global, yield* readFileAt(projectConfigPath(repo)));
      }),
  });

export const layer = (path: string = SETTINGS_FILE): Layer.Layer<Settings> =>
  Layer.effect(Settings)(make(path));

// ── overriding what the config says ────────────────────────────────────────
//
// The new-thread modal offers a model and an effort, and the agent command is
// already in the config — `claude --permission-mode auto --model opus`. So a
// chosen model has to *replace* the flag that is there rather than follow it.
//
//   configured  claude --permission-mode auto --model opus
//   chosen      model: sonnet, effort: high
//   result      claude --permission-mode auto --model sonnet --effort high
//                                                      └─ replaced, not appended
//
// Appending would leave `--model opus --model sonnet`, which the CLI resolves
// by some rule — probably last-wins — that nothing here should depend on and
// no test here could pin. A person reading the resulting session's command
// line would also have to know that rule to predict what they got.
//
// Both spellings are handled because both appear in real configs: `--model x`
// is two argv elements and `--model=x` is one.

/**
 * `argv` with `flag` set to `value`, replacing whichever spelling is there.
 *
 * `undefined` leaves the argv exactly as configured, which is the whole point
 * of the "from settings" option in the modal: choosing nothing has to be
 * different from choosing a default, or the config could never win.
 */
export const withFlag = (
  argv: ReadonlyArray<string>,
  flag: string,
  value: string | undefined,
): ReadonlyArray<string> => {
  if (value === undefined || value === "" || argv.length === 0) {
    return argv;
  }

  const joined = argv.findIndex((arg) => arg.startsWith(`${flag}=`));
  if (joined !== -1) {
    return argv.with(joined, `${flag}=${value}`);
  }

  const separate = argv.indexOf(flag);
  if (separate !== -1) {
    // The element after it is the value — unless there is no element, or it is
    // itself a flag, which is what `--verbose --model x` looks like when the
    // config author left the value out. Inserting is right in both cases;
    // overwriting the next flag would silently drop it.
    const next = argv[separate + 1];
    return next === undefined || next.startsWith("-")
      ? [...argv.slice(0, separate + 1), value, ...argv.slice(separate + 1)]
      : argv.with(separate + 1, value);
  }

  return [...argv, flag, value];
};

/** The configured agent command with the modal's choices applied. */
export const agentWith = (
  settings: AwpSettings,
  chosen: { readonly model?: string | undefined; readonly effort?: string | undefined },
): ReadonlyArray<string> =>
  withFlag(withFlag(settings.agent, "--model", chosen.model), "--effort", chosen.effort);
