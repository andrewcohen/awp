// Whether it is safe to touch zmx from here.
//
// `zmx attach` branches on ZMX_SESSION: from inside a session it tells the
// daemon to switch the *calling* client rather than making a new one, which
// steals the terminal the caller was launched from.
//
// Stripping the marker from a child's environment stops that hijack, and it is
// tempting to treat it as sufficient. It is not. A stripped child still opens a
// **new client** against the daemon, and a session takes its size from the
// client looking at it — so attaching still reflows whatever is attached, and
// still redraws. Running that from inside a live session disturbs the session
// doing the running. Measured the hard way: a probe that had stripped the
// marker correctly still emitted a clear-screen into the terminal it was
// probing from.
//
// So there are two different rules, and conflating them is the mistake:
//
//   - Spawning zmx as a child: strip ZMX_SESSION. Always.
//   - Testing or probing against a REAL zmx: refuse to run inside a session.
//     Not strip — refuse. There is no environment edit that makes it safe.

/** The session this process is running inside, if any. */
export const currentZmxSession = (): string | undefined => process.env.ZMX_SESSION;

/** Whether this process is running inside a zmx session. */
export const insideZmxSession = (): boolean => currentZmxSession() !== undefined;

/**
 * Markers that describe the session *this* process is running inside.
 *
 * Cleared as a family rather than named one by one, and that is the whole
 * lesson of the bug that added them: transcript saving was off in every agent
 * amoeba started, because `CLAUDE_CODE_CHILD_SESSION` reached it from whatever
 * launched the daemon. Nobody had heard of that variable. A list of the five
 * known today would be a list that is wrong the next time one is added, and
 * the failure would look like this one — silent, and about something else.
 *
 * What was actually being inherited, measured on the running daemon:
 *
 *   CLAUDE_CODE_CHILD_SESSION      transcript saving off
 *   CLAUDE_CODE_SESSION_ID         the parent's session
 *   CLAUDE_CODE_MESSAGING_SOCKET   the parent's IPC
 *   CLAUDE_CODE_MESSAGING_TOKEN
 *   CLAUDECODE · CLAUDE_PID · CLAUDE_JOB_DIR
 *
 * The transcript is the symptom that got noticed. A fresh agent holding
 * another session's messaging socket is the one that would have been harder to
 * explain.
 *
 * Deliberately *not* everything beginning with `CLAUDE`. `CLAUDE_EFFORT` and
 * anything else a person put in their own shell profile is theirs and is meant
 * to be inherited; what goes is what a running session wrote about itself.
 */
const describesTheParentSession = (key: string): boolean =>
  key.startsWith("CLAUDE_CODE_") ||
  key === "CLAUDECODE" ||
  key === "CLAUDE_PID" ||
  key === "CLAUDE_JOB_DIR";

/**
 * The environment a child we spawn must be given: this one, with every marker
 * that describes a session we are *inside* emptied rather than removed.
 *
 * ── two markers, one rule ──────────────────────────────────────────────────
 *
 *   ZMX_SESSION      makes `zmx attach` hijack its caller
 *   CLAUDE_CODE_*    makes a fresh agent believe it is a continuation of the
 *                    session that started the daemon
 *
 * Both are a parent describing itself, and neither is true of the child. This
 * was `childEnv` and handled only the first, which is why the second went
 * unnoticed for as long as it did — the seam was right and its name said it
 * was about one thing.
 *
 * ── why emptied and not removed ────────────────────────────────────────────
 * Leaving it out does not take it away. bun-pty hands the pairs to a Rust
 * `Command`, which **inherits the parent environment** and applies what it is
 * given on top; without an `env_clear()` there is no way to express a removal
 * by omission. The same is true of Node's `child_process` only when `env` is
 * absent, which is the difference that made this look correct.
 *
 * The first version of this function omitted the key and was believed to strip
 * it for weeks. It did not. A child spawned through bun-pty saw the marker
 * intact, so `zmx attach <name>` resolved ZMX_SESSION and switched the calling
 * client instead — which is the exact hijack this exists to prevent, aimed at
 * whatever session the daemon happens to be running in.
 *
 * Setting it empty is expressible through any of these APIs, and empty is what
 * a marker is tested for: an empty ZMX_SESSION is not a session name, and an
 * empty `CLAUDE_CODE_CHILD_SESSION` is falsy.
 *
 * **A test of this function cannot catch a failure of it**, because the failure
 * is in the spawner. `probe/child-env.ts` spawns a shell and prints what the
 * child actually received, which is the only way to know.
 */
export const childEnv = (
  base: Record<string, string | undefined> = process.env,
): Record<string, string> => {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(base)) {
    if (key !== "ZMX_SESSION" && !describesTheParentSession(key) && value !== undefined) {
      env[key] = value;
    }
  }
  // Present and empty, all of them. Absent would be a request the spawner is
  // free to ignore, and bun-pty's does.
  env.ZMX_SESSION = "";
  for (const key of Object.keys(base)) {
    if (describesTheParentSession(key)) {
      env[key] = "";
    }
  }
  return env;
};

export class InsideZmxSessionError extends Error {
  readonly session: string;

  constructor(session: string) {
    super(
      [
        `refusing to touch a real zmx from inside session ${session}.`,
        "",
        "Attaching opens a new client, and a session takes its size from the",
        "client looking at it — so this would reflow and redraw the session you",
        "are sitting in. Stripping ZMX_SESSION prevents the hijack, not this.",
        "",
        "Run it from a plain terminal outside zmx.",
      ].join("\n"),
    );
    this.name = "InsideZmxSessionError";
    this.session = session;
  }
}

/**
 * Guard for anything that talks to a real zmx. Throws inside a session.
 *
 * The Go tree paired its equivalent with a reflective test asserting that every
 * real-zmx test called it, because the guard is only as good as nobody
 * forgetting. That test belongs here too, once there is more than one caller.
 */
export const requireOutsideZmxSession = (): void => {
  const session = currentZmxSession();
  if (session !== undefined) {
    throw new InsideZmxSessionError(session);
  }
};
