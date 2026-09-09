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
// ── read a key when something needs it ─────────────────────────────────────
// The file also carries `actions`, and this reads none of it. A setting is read
// when something needs it, so that the shape it is read *into* is decided by a
// caller that exists. `deck.project_roots` was in that same list until project
// import arrived and became the caller — which is the rule working, not an
// exception to it.
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
  deck: Schema.optional(
    Schema.Struct({
      bookmark_prefix: Schema.optional(Schema.String),
      project_roots: Schema.optional(Schema.Array(Schema.String)),
    }),
  ),
  // ── the system defaults, in one block both faces read ──────────────────
  //
  // These lived in two places and neither could be read: the model and the
  // permission mode were words inside the `agent` string —
  // `claude --permission-mode auto --model opus` — which is the terminal's
  // command line and says nothing to the chat, and the effort was nowhere at
  // all. So the chat ran on whatever the adapter defaulted to while the
  // terminal ran on opus, and no file said what the machine's answer was.
  //
  // `defaults`, not `chat` or `agent`: it is one answer for both faces, and a
  // block named after either would be a second one waiting to disagree.
  defaults: Schema.optional(
    Schema.Struct({
      model: Schema.optional(Schema.String),
      effort: Schema.optional(Schema.String),
      /** Claude Code's permission mode — `auto`, `default`, and so on. */
      mode: Schema.optional(Schema.String),
    }),
  ),
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
  /**
   * Directories to look under for repositories to offer as projects.
   *
   * Tilde-expanded where it is used rather than here, because expansion needs a
   * home directory and this is a parser. Empty is the ordinary answer and is
   * not a problem: importing a path works with no roots configured at all, and
   * the walk is a convenience over it.
   */
  readonly projectRoots: ReadonlyArray<string>;
  /**
   * The model every face should use unless something says otherwise.
   *
   * Applied to the terminal's argv through {@link agentWith} and to the chat
   * through the adapter's own config option, so one line in one file answers
   * for both. A choice in the new-thread modal still wins — see `withFlag`,
   * where the whole point of "from settings" is that choosing nothing has to
   * differ from choosing a default.
   */
  readonly model: string | undefined;
  /** The reasoning effort, same rule. Nowhere at all before this. */
  readonly effort: string | undefined;
  /**
   * Claude Code's permission mode.
   *
   * Undefined leaves each face where it was: the terminal keeps whatever the
   * `agent` line says, and the chat stays in Manual — see `MODE` in chat.ts
   * and the argument for it. Setting `auto` here is how somebody opts out of
   * being asked, which is a decision worth having to write down.
   */
  readonly mode: string | undefined;
  /** What went wrong reading the file, if anything. See above. */
  readonly problem: string | undefined;
}

/** With no config at all. The agent is what awp has always launched. */
export const DEFAULTS: AwpSettings = {
  agent: ["claude"],
  bootstrap: [],
  projectRoots: [],
  bookmarkPrefix: undefined,
  model: undefined,
  effort: undefined,
  mode: undefined,
  problem: undefined,
};

export const SETTINGS_FILE = join(homedir(), ".config", "awp", "config.json");

export class Settings extends Context.Service<
  Settings,
  { readonly read: (repo?: string) => Effect.Effect<AwpSettings> }
>()("awp/Settings") {}

/** A configured string, or nothing when it is absent or empty. */
const blank = (value: string | undefined): string | undefined => {
  const said = (value ?? "").trim();
  return said === "" ? undefined : said;
};

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
    projectRoots: (decoded.deck?.project_roots ?? [])
      .map((one) => one.trim())
      .filter((one) => one !== ""),
    // Blank is absent, for the same reason it is everywhere else in this file:
    // a key somebody emptied rather than deleted means "nothing set", and
    // `--model ""` on a command line is a refusal several steps later.
    model: blank(decoded.defaults?.model),
    effort: blank(decoded.defaults?.effort),
    mode: blank(decoded.defaults?.mode),
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
  // Global-only in practice — a repository listing the directories to scan for
  // *other* repositories is a strange thing to write — but merged by the same
  // rule as everything else rather than by an exception, because an exception
  // here would be a second rule for a reader to know about.
  projectRoots: project.projectRoots.length === 0 ? global.projectRoots : project.projectRoots,
  // Per field, replace-if-empty, like everything above: a project that names a
  // model gets its own and inherits the other two.
  model: project.model ?? global.model,
  effort: project.effort ?? global.effort,
  mode: project.mode ?? global.mode,
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

/**
 * The configured agent command with the defaults and then the modal's choices
 * applied.
 *
 * Three layers, and the order is the whole of it:
 *
 *   the `agent` line          claude --permission-mode auto --model opus
 *   `defaults`                model · effort · mode, for whatever the line
 *                             does not already say — and it overwrites what it
 *                             does, because a block naming the model while the
 *                             command line names another is a file that
 *                             contradicts itself and `defaults` is the newer,
 *                             clearer half
 *   the modal's choice        wins over both. "From settings" means choosing
 *                             nothing, which is why these are `undefined`
 *                             rather than a value
 */
export const agentWith = (
  settings: AwpSettings,
  chosen: { readonly model?: string | undefined; readonly effort?: string | undefined },
): ReadonlyArray<string> => {
  const withDefaults = withFlag(
    withFlag(
      withFlag(settings.agent, "--permission-mode", settings.mode),
      "--model",
      settings.model,
    ),
    "--effort",
    settings.effort,
  );
  return withFlag(withFlag(withDefaults, "--model", chosen.model), "--effort", chosen.effort);
};
