// The daemon, as a process.
//
// Separate from the window's main process on purpose, for now. A daemon that
// is a child of the window cannot outlive it, and the whole point of zmx owning
// the sessions is that closing a window is not the same as ending the work.
// Embedding it later is a decision about lifecycle, not about wiring — the
// layer in daemon.ts is the same either way.
//
// This may run from inside a zmx session, unlike the probes. The distinction is
// the one in AGENTS.md: spawning zmx as a child means stripping ZMX_SESSION,
// which childEnv does, and Attachment separately refuses to attach to the
// session this process is running in. Neither of those needs the daemon itself
// to stay out.

import {
  NodeChildProcessSpawner,
  NodeFileSystem,
  NodePath,
  NodeRuntime,
} from "@effect/platform-node-shared";
import { Effect, Layer } from "effect";
import { DAEMON_HOST, DAEMON_PORT, layer as daemonLayer } from "./daemon";
import { currentZmxSession } from "./zmx-session";

const banner = Effect.gen(function* () {
  const own = currentZmxSession();
  yield* Effect.log(`awp daemon listening on ws://${DAEMON_HOST}:${DAEMON_PORT}`);
  if (own !== undefined) {
    // Said out loud because the row will be disabled in the sidebar and the
    // reason is not guessable from the UI: a session takes its size from the
    // client looking at it, so attaching to this one would put the pane and the
    // terminal the daemon was launched from in a fight over one geometry.
    yield* Effect.log(`running inside ${own} — that session will not be attachable`);
  }
});

// ── a shutdown that finishes ───────────────────────────────────────────────
//
// The daemon did not exit on SIGTERM, and the way that presented was not a
// hang: it was **fourteen daemons running at once**, at 17–56MB each, and only
// one of them holding :5274. Every restart over two days had left a copy
// behind, and nothing about it was visible because the next daemon started
// perfectly well.
//
// The port is the tell. A process that could not bind would still be holding
// nothing and would exit — that was checked first, and it does:
//
//   second daemon, port taken   Failed to start server. Is port 5274 in use?
//                               exit code 1                    ← already right
//
// So the strays had *held* the port and let it go without dying. The cause is
// in the socket server's finaliser, and it is one line of somebody else's:
//
//   server.close(() => resume(Effect.void))
//
// `ws`'s `WebSocketServer.close` calls back only once every client connection
// has closed, and the connections handed to `run` are not among the ones it
// terminates first. With the window connected there is always one. So:
//
//   SIGTERM  →  finaliser runs  →  the listener closes, the port is freed
//            →  the callback never resumes  →  the process lives for ever
//
// Isolated before it was fixed, because "the daemon hangs" and "the library
// hangs" are different bugs in different files:
//
//   no client   scope closed after 310ms
//   one client  "the release runs here", and then nothing
//
// The repair is a deadline rather than a patch. Reaching into `ws` from here
// would mean patching a package to terminate connections the daemon cannot
// see, and the honest statement is simpler: a daemon that has been asked to
// stop and has not finished in {@link GRACE_MS} is going anyway. Sessions
// belong to zmx and outlive this process by design, so there is nothing here
// whose loss a longer wait would prevent.
//
// The timer is armed on the signal and never disarmed. It does not need to be:
// if the graceful path finishes first the process is gone, and the timer with
// it.
//
// **And in this daemon it never does.** Measured after the fix, and the second
// line is the one worth keeping:
//
//   with a client     exits after 2s, "leaving anyway"
//   with no client    exits after 2s, "leaving anyway"   ← something else too
//
// The isolated socket server closes in 310ms with no client, so the daemon is
// holding at least one more finaliser that does not complete — the watcher,
// the jobs runner and the pty layer are all candidates and none has been
// ruled out. The deadline makes it harmless rather than fixed, which is why
// it is written down here instead of being left as a clean-looking exit.

/** How long a shutdown may take before the process leaves without it. */
const GRACE_MS = 2000;

for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {
    setTimeout(() => {
      // Written on the way out, so a log that ends here says which of the two
      // shutdowns happened. A clean one never reaches this line.
      console.error(`awp daemon: ${signal} did not finish in ${GRACE_MS}ms — leaving anyway`);
      process.exit(0);
    }, GRACE_MS);
  });
}

// Building the layer is what starts the server; the effect then says so and
// waits. `Effect.never` rather than a loop — there is nothing to poll, and the
// process should exit only when it is told to.
NodeRuntime.runMain(
  Effect.gen(function* () {
    yield* banner;
    yield* Effect.never;
  }).pipe(
    Effect.provide(
      daemonLayer.pipe(
        // FileSystem and Path are the spawner's, not the daemon's — it resolves
        // an executable before running it. The R channel is what surfaced them:
        // they were never mentioned anywhere in this tree until the compiler
        // asked who was going to provide them.
        Layer.provide(NodeChildProcessSpawner.layer),
        Layer.provide(NodeFileSystem.layer),
        Layer.provide(NodePath.layer),
      ),
    ),
  ),
);
