// What port a service actually bound, asked of the kernel rather than of the
// program's own output.
//
// ── why the kernel and not the log ─────────────────────────────────────────
//
// A dev server prints `Local: http://localhost:5273/` and scraping that line is
// right by construction — it is the number the program itself chose. It is also
// a regex over somebody else's output format, and every framework spells it
// differently. `lsof` is true whatever was printed, and it answers for a server
// that prints nothing at all.
//
// The log line is still worth having one day, as the *label* — a server may
// bind three ports and only one of them is the one a person wants. Not yet:
// the first useful version of this is "there is a server and it is on 5273".

import { ChildProcess, ChildProcessSpawner } from "effect/unstable/process";
import { Context, Effect, Layer } from "effect";
import { capture } from "./run";

/**
 * Every descendant of a pid, including itself.
 *
 * **A service's listener is a grandchild, and that is the ordinary case rather
 * than an edge.** The session's own process is a shell; it runs `bun run dev`,
 * which runs vite. Measured against exactly that shape:
 *
 *     lsof -a -p <shell pid>     nothing
 *     lsof -a -p <whole tree>    *:51999 (LISTEN)
 *
 * So asking about the session's pid alone answers "no port" for a server that
 * is running perfectly, every time.
 *
 * `pgrep -P` per level rather than one `ps` parse: the tree is three deep at
 * most and this needs no output format to be agreed with.
 */
const descendants = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  pid: number,
): Effect.Effect<ReadonlyArray<number>> =>
  Effect.gen(function* () {
    const found: number[] = [pid];
    let frontier = [pid];
    // Bounded rather than recursive-until-empty: a cycle is impossible in a
    // process tree, but a loop that trusts that is a loop with no floor under
    // it, and this runs against a list somebody is looking at.
    for (let depth = 0; depth < 6 && frontier.length > 0; depth += 1) {
      const next: number[] = [];
      for (const parent of frontier) {
        const out = yield* capture(
          spawner,
          ChildProcess.make("pgrep", ["-P", String(parent)]),
        ).pipe(Effect.orElseSucceed(() => ({ stdout: "", stderr: "", exitCode: 1 })));
        // `pgrep` exits 1 for "no matches", which is the ordinary answer for a
        // leaf. Not a failure, and `capture` is what makes that distinguishable
        // from a command that could not be run at all.
        for (const line of out.stdout.split("\n")) {
          const child = Number(line.trim());
          if (Number.isInteger(child) && child > 0 && !found.includes(child)) {
            found.push(child);
            next.push(child);
          }
        }
      }
      frontier = next;
    }
    return found;
  });

/**
 * The lowest TCP port anything in the tree is listening on.
 *
 * **`-a`, and it is the whole correctness of this call.** `lsof` combines
 * selection options with OR unless told otherwise, so `lsof -p <pids> -iTCP`
 * answers with every listening socket on the machine — and the first row of
 * that is some other application's port, reported as this service's, with
 * nothing anywhere saying so. It was written without `-a` first and read as
 * working.
 *
 * The lowest rather than the first: a dev server binds its HTTP port and often
 * an ephemeral one beside it for HMR, and the low number is the one somebody
 * wants to click. A wrong guess here is visible and harmless; the `-a` above is
 * neither.
 */
export const portOf = (
  spawner: ChildProcessSpawner.ChildProcessSpawner["Service"],
  pid: number,
): Effect.Effect<number | undefined> =>
  Effect.gen(function* () {
    const tree = yield* descendants(spawner, pid);
    const out = yield* capture(
      spawner,
      ChildProcess.make("lsof", ["-nP", "-a", "-p", tree.join(","), "-iTCP", "-sTCP:LISTEN"]),
    ).pipe(Effect.orElseSucceed(() => ({ stdout: "", stderr: "", exitCode: 1 })));

    return lowestPort(out.stdout);
  });

/**
 * The lowest port in `lsof`'s output, or nothing.
 *
 * Split out and exported so the parse has a test without a process in it —
 * `lsof` is not available in CI on every platform, and a parser that can only
 * be exercised by running the tool is a parser nothing checks.
 *
 * The NAME column is the last field and spells the address several ways:
 * `*:5273`, `127.0.0.1:5273`, `[::1]:5273`. Matching the tail after the final
 * colon covers all three and does not need to know which it is looking at.
 */
export const lowestPort = (out: string): number | undefined => {
  const ports: number[] = [];
  for (const line of out.split("\n")) {
    if (!line.includes("LISTEN")) {
      continue;
    }
    const name = line.trim().split(/\s+/u).at(-2);
    const port = Number((name ?? "").split(":").at(-1));
    if (Number.isInteger(port) && port > 0) {
      ports.push(port);
    }
  }
  return ports.length === 0 ? undefined : Math.min(...ports);
};

/**
 * What port a service bound, as a service, so a caller can be tested without
 * `lsof` on the machine.
 *
 * A tag for the reason every tag here exists: the two commands under it are
 * the kind a test must not run. It answers a question and costs nothing, so it
 * sits beside `Multiplexer` rather than beside `Attachment` — there is no act
 * in it and no `Scope` in the return type.
 */
export class ServicePorts extends Context.Service<
  ServicePorts,
  { readonly portOf: (pid: number) => Effect.Effect<number | undefined> }
>()("awp/ServicePorts") {}

export const layer: Layer.Layer<ServicePorts, never, ChildProcessSpawner.ChildProcessSpawner> =
  Layer.effect(ServicePorts)(
    Effect.gen(function* () {
      const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
      return { portOf: (pid: number) => portOf(spawner, pid) };
    }),
  );
