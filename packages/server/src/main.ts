// The daemon, as a process.
//
// Separate from the Electrobun main process on purpose, for now. A daemon that
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
