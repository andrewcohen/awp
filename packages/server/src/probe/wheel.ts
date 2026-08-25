// What does a wheel notch actually cost?
//
// "Laggy and looks bad" is two complaints, and they have different causes. This
// separates them: how long before anything comes back, and how much arrives in
// how many pieces. A slow round trip is a transport problem; a thousand tiny
// chunks is a rendering problem, because every chunk is a `term.write` and a
// parse whether or not the screen changed meaningfully.
//
// ── the session this touches ───────────────────────────────────────────────
// Whichever is named on the command line, and nothing else. Attaching reflows
// the session it lands on, so this must only ever be pointed at one whose owner
// has said so.
//
//     bun run daemon                       # in another terminal
//     bun packages/server/src/probe/wheel.ts dotfiles.default

import * as client from "@awp-kit/protocol/client";
import { Effect, Result, Stream } from "effect";
import { DAEMON_HOST, DAEMON_PORT } from "../daemon";

const url = `ws://${DAEMON_HOST}:${DAEMON_PORT}`;
const SESSION = process.argv[2];

const COLS = 120;
const ROWS = 40;

/** One wheel-up notch, as the pane sends it. */
const NOTCH = `[<64;60;20M`;

const program = Effect.gen(function* () {
  if (SESSION === undefined) {
    return yield* Effect.die(
      new Error("name the session to attach to — this reflows it, so be sure it is yours"),
    );
  }

  const rpc = yield* client.make;
  console.log(`\n  attaching to ${SESSION} at ${COLS}x${ROWS}\n`);

  interface Arrival {
    readonly at: number;
    readonly size: number;
  }
  const arrivals: Array<Arrival> = [];
  let sentAt = 0;
  let settledAt = 0;
  let opening = "";

  yield* Stream.runForEach(rpc.Attach({ session: SESSION, cols: COLS, rows: ROWS }), (chunk) =>
    Effect.sync(() => {
      const now = Date.now();
      if (sentAt === 0) {
        opening += chunk;
      } else {
        arrivals.push({ at: now - sentAt, size: chunk.length });
      }
      settledAt = now;
    }),
  ).pipe(
    // Let the opening redraw finish, send one notch, then watch for a second.
    Effect.raceFirst(
      Effect.gen(function* () {
        // Quiet for 400ms means the redraw is done and the number that follows
        // is the wheel's alone.
        // settledAt is written by the stream's fiber, not this one, which the
        // loop rule cannot see. Waiting on it is the point: it is how this
        // learns that the opening redraw has finished.
        // oxlint-disable-next-line no-unmodified-loop-condition
        while (settledAt === 0 || Date.now() - settledAt < 400) {
          yield* Effect.sleep("50 millis");
        }
        console.log("  session settled; sending one wheel notch\n");
        sentAt = Date.now();
        // A gesture, not a notch. A trackpad delivers events at the frame rate,
        // and each one the pane converts into at least one report — so this is
        // what half a second of scrolling actually asks of the program.
        for (let event = 0; event < 30; event += 1) {
          yield* rpc.Write({ session: SESSION, data: NOTCH });
          yield* Effect.sleep("16 millis");
        }
        yield* Effect.sleep("1500 millis");
      }),
    ),
    // Refusals are reported, not swallowed. An earlier version hid one and the
    // probe read as "the program ignored the wheel" when the truth was that it
    // had never attached — the session name was the shortened one the sidebar
    // shows rather than the one zmx knows.
    Effect.catchTag("AttachRefused", (error) =>
      Effect.sync(() => {
        console.log(`  refused: ${error.reason}\n`);
      }),
    ),
    Effect.catchCause(() => Effect.void),
  );

  // What the opening redraw said about the program, which is the thing the
  // pane's wheel handling depends on. If zmx does not replay these, the
  // emulator cannot know the program wants the mouse and neither can anything
  // else — and that is a different bug from a slow one.
  // The control character is the point of these patterns; a sequence that did
  // not start with ESC would match ordinary prose.
  // oxlint-disable no-control-regex
  const modes = [...opening.matchAll(/\u001B\[\?([\d;]+)([hl])/gu)].map((m) => `${m[1]}${m[2]}`);
  console.log(`  opening redraw       ${opening.length} chars`);
  console.log(`  private modes in it  ${modes.length > 0 ? modes.join(" ") : "none"}`);
  const alt = /\u001B\[\?(?:47|1047|1049)h/u.test(opening);
  const mouse = /\u001B\[\?(?:1000|1002|1003)h/u.test(opening);
  console.log(`  alternate screen     ${alt}`);
  // oxlint-enable no-control-regex
  console.log(`  mouse reporting      ${mouse}\n`);

  if (arrivals.length === 0) {
    console.log(
      mouse
        ? "  nothing came back, though the program asked for the mouse.\n"
        : "  nothing came back, and the program never asked for the mouse —\n" +
            "  so a wheel notch is not something it has any use for.\n",
    );
    return;
  }

  const bytes = arrivals.reduce((total, a) => total + a.size, 0);
  const first = arrivals[0];
  const last = arrivals.at(-1);
  const sizes = arrivals.map((a) => a.size).toSorted((a, b) => a - b);

  console.log(`  first byte back      ${first?.at}ms`);
  console.log(`  last byte back       ${last?.at}ms`);
  console.log(`  chunks               ${arrivals.length}`);
  console.log(`  bytes                ${bytes}`);
  console.log(
    `  chunk size  min/med/max  ${sizes[0]}/${sizes[Math.floor(sizes.length / 2)]}/${sizes.at(-1)}`,
  );
  const span = (last?.at ?? 0) - (first?.at ?? 0);
  console.log(
    `\n  30 notches over ~500ms produced ${arrivals.length} chunks in ${span}ms` +
      ` — ${(arrivals.length / Math.max(span / 1000, 0.001)).toFixed(0)} writes/sec\n` +
      `  every one is a term.write and a parse, whether or not the screen changed.\n`,
  );
}).pipe(Effect.provide(client.layer(url)), Effect.scoped);

const result = await Effect.runPromise(Effect.result(program));
if (Result.isFailure(result)) {
  console.error("\n  failed:", result.failure, "\n");
  process.exit(1);
}
process.exit(0);
