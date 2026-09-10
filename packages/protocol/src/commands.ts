// What a slash at the start of the box means.
//
// ── here rather than in either face ───────────────────────────────────────
//
// Two clients read a slash now — the window's composer and the TUI's — and
// which commands are the *client's own* is a rule, not a rendering: a face
// that decided for itself would be a second implementation, and the copy that
// drifts is the one nobody tests. The same argument that puts `SessionIdentity`
// on the wire. Nothing here is a schema and nothing here is sent; it sits
// beside `client.ts`, which is the other thing in this package a client runs.
//
// ── two sets, and only one of them is intercepted ─────────────────────────
//
// `/new` and `/mcp` are things *amoeba* does, and neither is expressible as a
// prompt: sent as text they reach the agent as a sentence about a command,
// which it then answers. Everything else — the agent's own commands, its
// skills, `/bro` — **is** a prompt: the adapter passes `/bro` through to the
// CLI verbatim, which is why a face has nothing to do but send it.
//
//   /new · /mcp   the client acts, nothing is sent          `mine: true`
//   /bro · …      delivered as ordinary text                `mine: false`
//
// The rule for intercepting is narrow on purpose: an exact match on the whole
// draft, and only against the client's own two. A person who types
// `/tmp/build.log is missing` is talking about a path, and a prefix match
// would eat their message.
//
// ── the menu is a listing, not a completion engine ────────────────────────
//
// What it is for is discovery — a person who types `/` finds out that anything
// is there at all, which for a skill they wrote last week is the whole feature
// — and Tab completes the highlighted one so nobody types the rest.

/** One thing the composer can be asked to do. */
export interface Command {
  /** With the slash, because that is what somebody types and what is matched. */
  readonly name: string;
  /** What it does, in the words the menu shows. */
  readonly said: string;
  /**
   * Whether the client runs it, rather than the agent.
   *
   * The whole of the difference. `commandOf` only ever answers one of these,
   * so an agent command with a name that collides with `/new` is still sent —
   * which is the right way round: the client's two are documented in this
   * file, and anything else is somebody's own.
   */
  readonly mine: boolean;
  /** What arguments it takes, when it takes any: `[file]`, `<message>`. */
  readonly hint?: string;
}

/** The agent's commands, in the shape the menu lists. */
export const agentCommands = (
  commands: ReadonlyArray<{
    readonly name: string;
    readonly description: string;
    readonly hint?: string | undefined;
  }>,
): ReadonlyArray<Command> =>
  commands.map((one) => ({
    name: one.name,
    said: one.description,
    mine: false,
    ...(one.hint === undefined ? {} : { hint: one.hint }),
  }));

/**
 * Every command, in the order the menu lists them.
 *
 * `/new` first because it is the one somebody reaches for mid-work — a
 * conversation that has gone somewhere unhelpful, or one whose context is
 * nearly full. `/mcp` is a question, and questions come after acts.
 */
export const COMMANDS: ReadonlyArray<Command> = [
  {
    name: "/new",
    said: "start a fresh conversation — the old one is kept on disk, and stops being this one",
    mine: true,
  },
  {
    name: "/mcp",
    said: "what tools this conversation was handed, and where they are bound",
    mine: true,
  },
];

/**
 * Whether the draft is somebody reaching for a command.
 *
 * A slash first, and nothing but a word after it. `/` on its own qualifies —
 * that is the discovery case, and it lists everything.
 */
const reaching = (draft: string): boolean => /^\/[\w-]*$/u.test(draft);

/**
 * The commands worth listing for what has been typed so far.
 *
 * Empty for anything that is not a bare `/word`, which is what keeps the menu
 * off the screen while somebody writes an ordinary message about a path.
 */
export const matching = (
  draft: string,
  theirs: ReadonlyArray<Command> = [],
): ReadonlyArray<Command> => {
  if (!reaching(draft)) {
    return [];
  }
  // The client's own first, then the agent's by name. Not because ours matter
  // more, but because they are the two that are always there — a list whose
  // first rows move as an agent discovers skills is a list nobody can build a
  // habit on.
  return [...COMMANDS, ...theirs.toSorted((a, b) => a.name.localeCompare(b.name))].filter((one) =>
    one.name.startsWith(draft),
  );
};

/**
 * The command the draft *is*, if it is one.
 *
 * Exact, after trimming: `/new` is a command and `/new please` is a message.
 * The trim is for the trailing space a keyboard leaves behind, and nothing
 * else — a draft with a newline in it is prose whatever it starts with.
 */
export const commandOf = (draft: string): Command | undefined => {
  const said = draft.trim();
  // The client's own only. An agent command that reached here would be run by
  // the client and never delivered, which for a skill is the difference
  // between it working and it silently doing nothing.
  return COMMANDS.find((one) => one.name === said);
};

/**
 * The draft with the highlighted command filled in, ready to be run.
 *
 * The name and no trailing space for the client's own: a space would make
 * `commandOf` trim it back off on every keystroke, and the next thing to press
 * is Return rather than anything that needs separating.
 *
 * A command that takes arguments gets the space, because the next thing to
 * press there is the first character of an argument — and `commandOf` never
 * looks at one of those, so nothing is confused by it.
 */
export const completed = (command: Command): string =>
  command.hint === undefined ? command.name : `${command.name} `;
