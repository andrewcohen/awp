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
 * The environment a zmx child must be given: this one, with the marker that
 * makes `zmx attach` hijack its caller emptied rather than removed.
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
 * Setting it empty is expressible through any of these APIs, and an empty
 * ZMX_SESSION is not a session name. `probe/child-env.ts` checks what a child
 * actually receives, because nothing short of spawning one can.
 */
export const zmxChildEnv = (
  base: Record<string, string | undefined> = process.env,
): Record<string, string> => {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(base)) {
    if (key !== "ZMX_SESSION" && value !== undefined) {
      env[key] = value;
    }
  }
  // Present and empty. Absent would be a request the spawner is free to ignore,
  // and bun-pty's does.
  env.ZMX_SESSION = "";
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
