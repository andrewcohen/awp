import { Clock, Duration, Effect, FileSystem, Stream } from "effect";

// Telling a client that a workspace's files changed.
//
// ── the ignore list is not tuning, it is the feature ───────────────────────
//
// Asking for the working copy snapshots it, and a snapshot writes to `.jj`. So
// a watcher that reported `.jj` would report the consequence of its own last
// report, and the panel would re-read the diff for ever:
//
//   file written  →  tick  →  client asks for the working copy
//        ↑                              ↓
//        └────────  .jj written  ←  jj snapshots
//
// That is a loop rather than a noisy watcher, and nothing downstream can damp
// it. `.git` is there for the same reason and `node_modules`, `dist` and the
// build directories because an install or a build is thousands of events about
// nothing a diff would show.
//
// ── why a tick and not a patch ─────────────────────────────────────────────
//
// The panel may be showing a commit, and a commit does not change because a
// file was written. Pushing a patch would put the choice of revision in the
// daemon, where it would be a second copy of a decision the client already
// makes — and the copy that drifts is the one nobody tests.

/**
 * Directory names whose contents are never worth a tick.
 *
 * Matched as a path *segment*, not as a prefix: a file called `.jjsomething`
 * is an ordinary file, and a directory called `dist` matters wherever it sits.
 */
const IGNORED = new Set([".jj", ".git", "node_modules", "dist", ".tsbuild", "target", ".venv"]);

/**
 * Is this path worth telling a client about?
 *
 * Exported because it is the part of this file that can be tested without a
 * file system: the loop above is a property of which paths are dropped, and a
 * test of the stream would prove nothing about it that a test of this does not.
 */
export const interesting = (path: string): boolean =>
  !path.split("/").some((segment) => IGNORED.has(segment));

/**
 * How long a burst is allowed to run before it is reported as one change.
 *
 * A single editor save is several events — write, rename, chmod — and an agent
 * writing six files is a few dozen. The panel re-reads a whole patch per tick,
 * so the cost of reporting each one is real and the value is nil: nobody can
 * see a diff redraw twice in 300ms.
 */
const SETTLE = Duration.millis(300);

/**
 * A tick each time the files under `dir` change.
 *
 * `debounce` and not `throttle`: a burst should be reported once *after* it
 * stops, not once at its start with the rest of the writes still landing. The
 * difference is visible — throttling reads the patch in the middle of an agent
 * writing four files and shows two of them.
 */
export const changesUnder = (
  dir: string,
): Stream.Stream<{ readonly at: number }, never, FileSystem.FileSystem> =>
  Stream.unwrap(
    Effect.gen(function* () {
      const files = yield* FileSystem.FileSystem;
      return files.watch(dir, { recursive: true }).pipe(
        Stream.filter((event) => interesting(event.path)),
        Stream.debounce(SETTLE),
        Stream.mapEffect(() => Clock.currentTimeMillis.pipe(Effect.map((at) => ({ at })))),
        // A watch that dies takes the subscription with it rather than the
        // daemon. The directory can be removed underneath this — a workspace
        // being torn down is exactly when it happens — and that is an ordinary
        // end to the stream, not a fault to report.
        Stream.catchCause(() => Stream.empty),
      );
    }),
  );
