// What a tool call is called, in one word.
//
// ── here rather than in either face, and for the reason commands.ts is ────
//
// Two clients draw a tool row and they have to read the same: a `Bash` call
// labelled `bash` in the terminal and `ran` in the window is two vocabularies
// for one conversation, and somebody moving between them has to learn both.
// A face deciding for itself is the second implementation, and the copy that
// drifts is the one nobody tests.
//
// ── the name, not the kind ────────────────────────────────────────────────
//
// `toolKind` is ACP's enum and the adapter maps every Claude Code tool onto
// ten values, so a column of rows reads `execute` and `other` and says less
// than the titles beside it:
//
//   Bash                                    execute      ← most of a terminal
//   Read                                    read
//   Edit · Write                            edit
//   Grep · Glob                             search
//   WebFetch · WebSearch                    fetch
//   Task · TodoWrite · Task{Create,Get,…}   think
//   Skill · AskUserQuestion · mcp__*        other
//   ExitPlanMode                            switch_mode
//
// The tool's own name is on every notification as `_meta.claudeCode.toolName`
// and reaches a client as `ChatUpdate.toolName`.

/** As much of a call as naming it needs. */
export interface Named {
  /** The tool's own name: `Bash`, `Read`, `mcp__awp__awp_thread`. */
  readonly toolName?: string | undefined;
  /** ACP's kind, which is the fallback and nothing more. */
  readonly toolKind?: string | undefined;
  /** What was delegated to, when the call was a spawn. */
  readonly subagent?: string | undefined;
}

/** `mcp__<server>__<tool>`, whose interesting half is the last one. */
const MCP = /^mcp__[^_]+(?:_[^_]+)*__(?<tool>.+)$/u;

/** Two words where one will do. Nothing else is renamed. */
const SHORTER: Readonly<Record<string, string>> = {
  webfetch: "fetch",
  websearch: "search",
};

/**
 * What to call a tool call.
 *
 * Passed through, not translated: a list of tools known here would report
 * every tool this repo has not heard of as `other`, which is the failure this
 * repairs. Three shortenings, and they are the only rules:
 *
 *   mcp__awp__awp_thread  →  awp_thread    the server is already in the title
 *   WebFetch              →  fetch         two words where one will do
 *   a subagent            →  its own type  `spawned` said neither what was
 *                                          handed off nor to what
 *
 * With no name — an older daemon, or a row replayed from a transcript written
 * by one — the kind stands in, because a row with no label at all is worse
 * than a coarse one.
 */
export const toolVerb = (call: Named): string => {
  if (call.subagent !== undefined && call.subagent !== "") {
    return call.subagent.toLowerCase();
  }
  const name = call.toolName;
  if (name === undefined || name === "") {
    return call.toolKind === undefined || call.toolKind === "" ? "did" : call.toolKind;
  }
  const bare = MCP.exec(name)?.groups?.["tool"] ?? name;
  const lower = bare.toLowerCase();
  return SHORTER[lower] ?? lower;
};

/**
 * The title worth drawing, which is sometimes none.
 *
 * ── `Terminal` is not a title, it is the absence of one ──────────────────
 *
 * The adapter titles a Bash call `input?.command ? input.command : "Terminal"`
 * — read in its own `tools.js` — so a call whose input has not finished
 * streaming arrives titled `Terminal`, and the row reads
 *
 *   …  bash  Terminal
 *
 * which names no command and says nothing the verb has not already said.
 * Reported as "i dont know what that is and i do not like it". A second
 * later the real command arrives on the same id and replaces it, so what
 * this removes is a word that is only ever on screen while a call is
 * starting.
 *
 * Narrow on purpose: only that pair, and only from the tool that mints it. A
 * call genuinely titled `Terminal` by anything else keeps its title.
 */
export const toolTitleOf = (
  call: Named & { readonly title?: string | undefined; readonly purpose?: string | undefined },
): string => {
  // ── intent over mechanism, where there is any ─────────────────────────
  //
  // A row gets one line, and for most of what an agent does that line is a
  // command — regularly a forty-line heredoc, drawn as its first forty
  // characters. The description says what it is *for*, and it is the more
  // useful of the two in the one line a row has:
  //
  //   bash  python3 - <<'PY' … forty lines …
  //   bash  Show where the adapter reads a tool's description field
  //
  // The command is not lost: a caller that shows a purpose here must keep
  // the title reachable — the window puts it on the tooltip and in the
  // expanded row, and `heldBack` below is how each one knows.
  const purpose = call.purpose ?? "";
  if (purpose !== "") return purpose;
  const title = call.title ?? "";
  if (call.toolName === "Bash" && title === "Terminal") {
    // Nothing worth drawing, and the row cannot be empty: the label is
    // suppressed for Bash — see `toolLabel` — so with no title there would
    // be a mark and a blank line. The name stands in until the command
    // lands on the same id a moment later.
    return toolVerb(call);
  }
  return title === "" ? toolVerb(call) : title;
};

/**
 * The word a row puts *before* its title, and usually there is none.
 *
 * ── a label on the majority is a label on the baseline ──────────────────
 *
 * The same arithmetic as the accent and as the inbox's leading icon: most of
 * what an agent does in a terminal is `Bash`, so a column that says `bash`
 * on every other row has spent its left edge on the thing nobody is scanning
 * for. What is worth a word is the row that is *not* a command.
 *
 *   ✓ Check the types                  a command. The row is the whole label
 *   ✓ read apps/tui/src/lines.ts       not a command, so it says so
 *   ✓ awp_tasks                        names itself; saying it twice is not
 *                                      two pieces of information
 *
 * Reported as "i think you can remove bash and all the spaces", against a
 * column of `bash` padded to nine cells.
 */
export const toolLabel = (
  call: Named & { readonly title?: string | undefined; readonly purpose?: string | undefined },
): string => {
  const verb = toolVerb(call);
  // `execute` as well as `bash`: an older daemon sends no name, and ACP's
  // kind for the same tool is the one word that stands in for it.
  if (verb === "bash" || verb === "execute" || verb === "did") return "";
  return verb === toolTitleOf(call) ? "" : verb;
};

/**
 * Whether the row is showing something other than the call's own title.
 *
 * Which is the question a disclosure has to answer: a row drawn as its
 * purpose has a command behind it worth opening, where a row drawn as its
 * command has nothing more to give.
 */
export const heldBack = (
  call: Named & { readonly title?: string | undefined; readonly purpose?: string | undefined },
): boolean => {
  // The purpose standing in for a real title, and nothing else. Not
  // `toolTitleOf(call) !== title`, which is also true of a pending call
  // whose only title is the `Terminal` placeholder — and the placeholder is
  // the adapter saying the command has not arrived, not a command to open.
  const purpose = call.purpose ?? "";
  return purpose !== "" && purpose !== (call.title ?? "");
};

/**
 * The frames a call in flight turns through.
 *
 * ── one clock, so two faces do not turn differently ─────────────────────
 *
 * Braille, because it turns inside one cell and reads as motion without
 * spending a colour — and because the terminal has nothing else. Here
 * rather than in either face for the reason the naming rule is: somebody
 * moving between the window and the TUI should not have to learn that a
 * spinning notch and a turning braille dot mean the same thing.
 *
 * The window drives it with a timer only while a turn is in flight, and
 * not at all under `prefers-reduced-motion` — where the mark falls back to
 * the dots it had, which is a state and not an animation.
 */
export const TURNING = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"] as const;

/** The frame for a tick, wrapping. `…` when nothing is turning. */
export const turningAt = (tick: number | undefined): string =>
  tick === undefined ? "…" : (TURNING[tick % TURNING.length] ?? "…");
