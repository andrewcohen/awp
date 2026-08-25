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
import { parseSessionList, requireName } from "./zmx-parse";
import { zmxChildEnv } from "./zmx-session";

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
    spawner.string(ChildProcess.make("zmx", [...args], { env: zmxChildEnv() })).pipe(
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
    );

  const list = () => run("list", ["ls"]).pipe(Effect.map(parseSessionList));

  return {
    list,

    lookup: (name: string) =>
      named("look up", name).pipe(
        Effect.andThen(list()),
        // zmx has no per-session query, so a lookup is a filtered list. Cheap
        // enough — the list is tens of rows — and it means one parser.
        Effect.map((sessions) => sessions.find((session) => session.name === name)),
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
