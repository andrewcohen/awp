import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { Data, Effect } from "effect";

// Telling Claude Code that a workspace is a place it may work.
//
// ── the bug this exists for ────────────────────────────────────────────────
//
// An agent started in a directory Claude Code has not seen stops at a prompt:
//
//   Quick safety check: Is this a project you created or one you trust?
//   ❯ No, exit
//     Yes, I trust this folder
//
// Every workspace amoeba makes is a fresh directory, so every agent it started
// stopped there. That was quiet for a while — an agent sitting at a prompt
// looks like an agent thinking — and became an *exit* the moment `send` began
// delivering a real carriage return: the brief arrives, the Return lands on the
// highlighted option, and the highlighted option is "No, exit". Two bugs
// meeting, where the fix to one made the other decisive rather than silent.
//
// ── this is somebody else's file ───────────────────────────────────────────
//
// `~/.claude.json` is written by Claude Code, continuously, and holds a
// person's whole configuration. Four rules follow, and the Go implementation
// this is ported from had all four:
//
//   no file, no write     absence means their agent is something else —
//                         codex, cursor, aider — and littering their $HOME on
//                         their behalf is not this job's business
//   round-trip            read into a map, change two keys, write it back;
//                         anything else drops settings nobody here knows about
//   lock                  a read-modify-write against a file another process
//                         is writing needs one, or two writers interleave
//   atomic                temp plus rename, so a crash mid-write cannot leave
//                         a truncated `~/.claude.json`
//
// ── the lock is a file, not flock ──────────────────────────────────────────
//
// The Go version used `syscall.Flock`, which neither Bun nor Node exposes. An
// exclusive-create lockfile is the portable equivalent and is what everything
// else in this position does: `open(path, "wx")` fails when it is there, so
// the file's existence *is* the lock.
//
// It is weaker in one way worth stating: a process that dies holding it leaves
// it behind, where an advisory lock is released by the kernel. Hence the stale
// sweep — a lock older than the timeout is one nobody is waiting on any more,
// and refusing forever because of a crash last week would be worse than the
// race it is guarding.

/** Where Claude Code keeps its per-project settings. */
export const claudeConfigPath = (home: string = homedir()): string => join(home, ".claude.json");

/** How long to wait for another writer before giving up. The Go version's. */
const PATIENCE_MS = 2000;

/** How often to retry the lock while waiting. */
const RETRY_MS = 25;

/** A lock this old is left over from a process that died holding it. */
const STALE_MS = 30_000;

export class TrustError extends Data.TaggedError("TrustError")<{
  readonly reason: string;
  readonly cause?: unknown;
}> {}

/**
 * The two keys that turn the prompt off, and the shape of a fresh entry.
 *
 * The empty collections are not decoration — Claude Code reads them, and an
 * entry carrying only the two booleans is a shape it did not write. Taken from
 * the Go implementation, which took them from a real file.
 */
const freshEntry = (): Record<string, unknown> => ({
  allowedTools: [],
  mcpContextUris: [],
  mcpServers: {},
  enabledMcpjsonServers: [],
  disabledMcpjsonServers: [],
  projectOnboardingSeenCount: 0,
});

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

/**
 * Hold the lock, hand the `projects` map to `mutate`, write only if it changed.
 *
 * `mutate` answers whether it changed anything, so a no-op does not rewrite a
 * person's configuration file for nothing — which matters more than it sounds,
 * because every write is a chance to lose it.
 */
const underLock = (
  path: string,
  mutate: (projects: Record<string, unknown>, at: string) => boolean,
  at: string,
): Effect.Effect<boolean, TrustError> =>
  Effect.promise(async () => {
    const fs = await import("node:fs/promises");
    const lock = `${path}.awp.lock`;

    const held = await (async (): Promise<boolean> => {
      const until = Date.now() + PATIENCE_MS;
      for (;;) {
        try {
          const handle = await fs.open(lock, "wx");
          await handle.close();
          return true;
        } catch {
          // Left behind by something that died holding it. Removing it is a
          // race with a live holder in principle and not in practice: nothing
          // holds this for thirty seconds.
          const age = await fs
            .stat(lock)
            .then((info) => Date.now() - info.mtimeMs)
            .catch(() => 0);
          if (age > STALE_MS) {
            await fs.rm(lock, { force: true });
            continue;
          }
          if (Date.now() > until) {
            return false;
          }
          await new Promise((done) => setTimeout(done, RETRY_MS));
        }
      }
    })();

    if (!held) {
      throw new Error(`another process is holding ${lock}`);
    }

    try {
      const raw = await fs.readFile(path, "utf8");
      const root: unknown = raw.trim() === "" ? {} : JSON.parse(raw);
      if (!isRecord(root)) {
        throw new Error("its top level is not an object");
      }
      const projects = isRecord(root["projects"]) ? root["projects"] : {};
      root["projects"] = projects;

      if (!mutate(projects, at)) {
        return false;
      }

      // Temp beside it rather than in /tmp: rename is only atomic within a
      // filesystem, and $HOME and /tmp are not always the same one.
      const temp = join(dirname(path), `.claude.json.awp.${process.pid}.tmp`);
      await fs.writeFile(temp, `${JSON.stringify(root, undefined, 2)}\n`, "utf8");
      await fs.rename(temp, path);
      return true;
    } finally {
      await fs.rm(lock, { force: true });
    }
  }).pipe(
    // `Effect.promise` puts a rejection on the defect channel, and every throw
    // above is a real failure a person should see rather than a crash: a
    // malformed config, a lock nobody released, a disk that is full.
    Effect.catchDefect((cause: unknown) =>
      Effect.fail(new TrustError({ reason: `cannot update ${path}: ${String(cause)}`, cause })),
    ),
  );

/**
 * Whether Claude Code is the agent on this machine at all.
 *
 * No file means it is not, and nothing below should run. Its own function
 * because both entry points need it and because "absent" is a real answer
 * rather than an error.
 */
const configExists = (path: string): Effect.Effect<boolean> =>
  Effect.promise(async () => {
    const fs = await import("node:fs/promises");
    return fs
      .access(path)
      .then(() => true)
      .catch(() => false);
  });

/**
 * Mark a workspace directory as trusted. Answers whether anything changed.
 *
 * Safe to run twice by construction — it sets two booleans to true — which is
 * what the jobs runner requires of every step. `false` means it was already
 * trusted, or there is no Claude Code config to write to.
 */
export const trustWorkspace = (
  dir: string,
  path: string = claudeConfigPath(),
): Effect.Effect<boolean, TrustError> =>
  Effect.gen(function* () {
    if (!(yield* configExists(path))) {
      return false;
    }
    return yield* underLock(
      path,
      (projects, at) => {
        const existing = isRecord(projects[at]) ? projects[at] : freshEntry();
        projects[at] = existing;
        if (existing["hasTrustDialogAccepted"] === true) {
          return false;
        }
        existing["hasTrustDialogAccepted"] = true;
        existing["hasCompletedProjectOnboarding"] = true;
        return true;
      },
      resolve(dir),
    );
  });

/**
 * Take the entry away again. Answers whether there was one.
 *
 * The undo half, so removing a workspace does not leave the file with an entry
 * per workspace ever created. Idempotent for the same reason every undo here
 * is: the runner re-enters them.
 */
export const untrustWorkspace = (
  dir: string,
  path: string = claudeConfigPath(),
): Effect.Effect<boolean, TrustError> =>
  Effect.gen(function* () {
    if (!(yield* configExists(path))) {
      return false;
    }
    return yield* underLock(
      path,
      (projects, at) => {
        if (!(at in projects)) {
          return false;
        }
        // eslint-disable-next-line @typescript-eslint/no-dynamic-delete
        delete projects[at];
        return true;
      },
      resolve(dir),
    );
  });
