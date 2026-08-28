// zmx as a Multiplexer.
//
// This is the only file that knows the multiplexer is zmx. Everything above it
// depends on the tag, so a test can hand a caller a different implementation
// without a subprocess anywhere in sight.
//
// ── how a Layer answers the tag ─────────────────────────────────────────────
// `Layer.effect(Multiplexer, make)` says: to build a `Multiplexer`, run `make`.
// `make` itself asks for `ChildProcessSpawner` — so this layer's own type
// records that it cannot be built without one, and the compiler makes whoever
// assembles the program supply it. Dependencies are values, and they compose.
//
// ── why no pty ──────────────────────────────────────────────────────────────
// Every command here captures output and exits. `attach` is the one that needs
// a pty, and it lives behind its own tag because it is the one with a
// consequence — see multiplexer.ts.

import { Effect, Layer } from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { Multiplexer, MultiplexerError } from "./multiplexer";
import { capture, said } from "./run";
import { parseProcessTable, parseSessionList, requireName, withProcesses } from "./zmx-parse";
import { childEnv } from "./zmx-session";

/**
 * Refuse an empty name rather than handing a process manager a blank argument
 * and finding out what it decides that means.
 *
 * Every method takes a name a caller computed, and a caller computing one from
 * a record with a field missing is a thing this codebase has been bitten by
 * before. An empty name is a bug upstream, and saying so is more useful than
 * acting on it.
 */
const named = (op: string, name: string) =>
  Effect.suspend(() => {
    const problem = requireName(op, name);
    return problem === undefined
      ? Effect.void
      : Effect.fail(new MultiplexerError({ op, reason: problem }));
  });

const make = Effect.gen(function* () {
  // `effect/unstable/process` exports namespaces, so the tag is nested inside
  // the module of the same name — not the import itself.
  const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;

  /**
   * Run a zmx command and capture what it said.
   *
   * The environment always has ZMX_SESSION removed, which does more than avoid
   * the attach hijack here: `zmx ls` marks the caller's own session with an
   * arrow, and that arrow is what breaks the first field of a line. Without the
   * marker in the environment there is no marker in the output, so the row that
   * would have gone missing is the row of a session like any other.
   *
   * Which session we are in is then answered by reading the environment
   * directly — see currentZmxSession — rather than by parsing a glyph out of a
   * list. The parser still strips the arrow, because a defence that costs
   * nothing is worth keeping.
   */
  const run = (op: string, args: ReadonlyArray<string>) =>
    capture(spawner, ChildProcess.make("zmx", [...args], { env: childEnv() })).pipe(
      Effect.mapError(
        (cause) =>
          new MultiplexerError({
            op,
            // Naming zmx and PATH here because "command failed" is what this
            // looks like when zmx simply is not installed.
            reason: `${op}: zmx failed (is it installed and on PATH?)`,
            cause,
          }),
      ),
      // `capture` rather than `spawner.string`, because `string` collects
      // stdout and discards the exit code. A `zmx ls` that failed came back as
      // an empty string, parsed to an empty list, and reached the sidebar as
      // "no sessions" — which is exactly what having no sessions looks like.
      // Found while building the Jj service; see run.ts.
      Effect.flatMap((captured) =>
        captured.exitCode === 0
          ? Effect.succeed(captured.stdout)
          : Effect.fail(new MultiplexerError({ op, reason: `${op}: ${said(captured)}` })),
      ),
    );

  /**
   * Every session, with liveness answered by the process table.
   *
   * Two subprocesses rather than one, and the second is the point: `zmx ls`
   * cannot say whether a session is still there — its `ended` is about the
   * last task it ran — so `ps` is asked as well. See `withProcesses`.
   *
   * `ps` failing is not this function failing. A listing with liveness
   * unknown is far better than no listing at all, and the parser's defaults
   * are the safe way round: every session reads as live, which shows a row
   * that might be stale rather than hiding one that is not.
   */
  const list = () =>
    Effect.all(
      [
        run("list", ["ls"]).pipe(Effect.map(parseSessionList)),
        capture(spawner, ChildProcess.make("ps", ["-eo", "pid=,ppid=,comm="])).pipe(
          Effect.map((seen) => parseProcessTable(seen.stdout)),
          Effect.orElseSucceed(() => []),
        ),
      ],
      { concurrency: 2 },
    ).pipe(Effect.map(([sessions, processes]) => withProcesses(sessions, processes)));

  /**
   * Run a zmx command from a given directory.
   *
   * A session's `startDir` is the working directory of whatever created it, so
   * this is how a new session ends up rooted in its workspace rather than in
   * wherever the daemon was launched. Everything else zmx is asked here is a
   * question about a session that already exists, and those do not care.
   */
  const runIn = (
    op: string,
    cwd: string,
    args: ReadonlyArray<string>,
    extra?: Readonly<Record<string, string>>,
  ) =>
    capture(
      spawner,
      // The caller's additions go under `childEnv`, not over it. A caller
      // able to set `ZMX_SESSION` back could reintroduce the client hijack this
      // whole file is arranged to prevent, and the guard has to be the last
      // word rather than a default.
      ChildProcess.make("zmx", [...args], { cwd, env: childEnv({ ...process.env, ...extra }) }),
    ).pipe(
      Effect.mapError(
        (cause) =>
          new MultiplexerError({
            op,
            reason: `${op}: zmx failed (is it installed and on PATH?)`,
            cause,
          }),
      ),
      Effect.flatMap((captured) =>
        captured.exitCode === 0
          ? Effect.succeed(captured.stdout)
          : Effect.fail(new MultiplexerError({ op, reason: `${op}: ${said(captured)}` })),
      ),
    );

  return {
    list,

    lookup: (name: string) =>
      named("look up", name).pipe(
        Effect.andThen(list()),
        // zmx has no per-session query, so a lookup is a filtered list. Cheap
        // enough — the list is tens of rows — and it means one parser.
        Effect.map((sessions) => sessions.find((session) => session.name === name)),
      ),

    start: (options: {
      readonly name: string;
      readonly cwd: string;
      readonly command: ReadonlyArray<string>;
      readonly env?: Readonly<Record<string, string>> | undefined;
    }) =>
      Effect.gen(function* () {
        const op = `start ${options.name}`;
        yield* named("start", options.name);
        if (options.cwd.trim() === "") {
          return yield* Effect.fail(
            new MultiplexerError({ op, reason: `${op}: no working directory given` }),
          );
        }
        if (options.command.length === 0) {
          return yield* Effect.fail(
            new MultiplexerError({ op, reason: `${op}: no command given` }),
          );
        }

        // Asked first, and this is the guard as much as the idempotence. A
        // session that already exists **and is still running something** is
        // left exactly as it is — `zmx run` against one would type a command
        // into whatever is in there, which for a name that was not ours is
        // someone's editor.
        //
        // ── live, not merely present ──────────────────────────────────────
        //
        // This used to skip on the name alone, and that adopted corpses. A
        // session whose task has exited is still listed by `zmx ls`, so a
        // retry after a failed create found the name, did nothing, and left
        // the job briefing a dead shell:
        //
        //   ZMX_TASK_COMPLETED:1
        //   $ Port the review capability from …
        //   -bash: syntax error near unexpected token `('
        //
        // The prompt was typed at bash, which is what "the session had ended"
        // looks like from the outside. `ended` is exactly the distinction the
        // original guard was reaching for: a session with nothing running is
        // idle, and typing a command into an idle session is what `zmx run`
        // is for. zmx supports it directly — `handleRun` resets its task
        // tracking so a second run on one session is not ignored — and it
        // keeps the scrollback, which killing and recreating would not.
        //
        // `busy` and not `ended`, because zmx's own `ended` turned out to be
        // about the last *task* rather than the session — see
        // `withProcesses`. What this needs to know is whether typing a command
        // in would interrupt something, which is a question about the process
        // table.
        const existing = yield* list();
        const already = existing.find((session) => session.name === options.name);
        if (already !== undefined && already.busy) {
          return;
        }

        // `-d` so zmx does not wait for the command to finish. Without it a
        // long-lived agent process would hold this effect open for as long as
        // the session lives.
        yield* runIn(op, options.cwd, ["run", options.name, "-d", ...options.command], options.env);
      }),

    // ── two writes, and both halves of that are measured ────────────────────
    //
    // The point is to *submit* the text rather than leave it sitting at a
    // prompt, and a trailing byte on the same write does not do it. This was
    // `${text}\n` in one write for months, and what it produced was a prompt
    // typed into the agent's box that somebody then had to press Enter on —
    // which is the whole of what the button was for.
    //
    // Two things were wrong with it, and only one of them is about the byte.
    //
    // **`\n` is not Enter.** A shell reads its input in canonical mode, where
    // the line discipline ends a line on LF, so the old code worked everywhere
    // it was tested by hand. An agent's TUI runs in *raw* mode: no translation
    // happens, and what a terminal actually sends for the Return key is CR.
    // An LF arriving there is a newline in the text.
    //
    // **A trailing byte is inside the burst.** A TUI reads whole chunks and
    // treats a multi-byte one as pasted text — that is the feature that stops
    // a pasted snippet running line by line — so even a CR on the end of the
    // prompt is part of the paste rather than a keypress. Measured against a
    // raw-mode reader in a real session:
    //
    //   one write, text + LF    chunk 6:  68 65 6c 6c 6f 0a
    //   one write, text + CR    chunk 6:  68 65 6c 6c 6f 0d   ← still one chunk
    //   two writes              chunk 5:  68 65 6c 6c 6f
    //                           chunk 1:  0d                  ← its own chunk
    //
    // **No delay between them.** Each `zmx send` is its own process doing its
    // own write, so the CR lands in a chunk of its own even back to back —
    // checked at zero gap and at 30ms, and both split. A sleep here would be
    // superstition with a cost.
    //
    // Still not idempotent, and still cannot be: sending twice sends twice.
    send: (name: string, text: string) =>
      named("send", name).pipe(
        Effect.andThen(
          Effect.suspend(() =>
            text === ""
              ? Effect.void
              : run("send", ["send", name, text]).pipe(
                  // The Return key, as a keypress rather than as a character on
                  // the end of a paste.
                  Effect.andThen(run("send", ["send", name, "\r"])),
                  Effect.asVoid,
                ),
          ),
        ),
      ),

    kill: (name: string) =>
      named("kill", name).pipe(
        Effect.andThen(run("kill", ["kill", name, "--force"])),
        Effect.asVoid,
      ),

    setLabels: (name: string, labels: Readonly<Record<string, string>>) =>
      named("label", name).pipe(
        Effect.andThen(
          Effect.suspend(() => {
            // `k=` with no value is how zmx removes a label, so an empty string
            // is meaningful rather than something to filter out.
            const pairs = Object.entries(labels).map(([key, value]) => `${key}=${value}`);
            return pairs.length === 0
              ? Effect.void
              : run("label", ["set", name, ...pairs]).pipe(Effect.asVoid);
          }),
        ),
      ),

    history: (name: string, options?: { readonly vt?: boolean }) =>
      named("read history of", name).pipe(
        Effect.andThen(
          run("history", options?.vt === true ? ["history", name, "--vt"] : ["history", name]),
        ),
      ),
  };
});

/**
 * zmx as the Multiplexer.
 *
 * Needs a `ChildProcessSpawner`, which is what actually runs a subprocess.
 * `NodeChildProcessSpawner.layer` from `@effect/platform-node-shared` provides
 * one, and is what `@effect/platform-bun`'s spawner re-exports anyway — so it
 * serves both the daemon under Bun and the tests under vitest, which cannot
 * load the Bun barrel at all.
 */
export const layer = Layer.effect(Multiplexer, make);
