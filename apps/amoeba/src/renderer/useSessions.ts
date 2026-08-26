import type { SessionInfo } from "@awp-kit/protocol";
import { useEffect, useRef, useState } from "react";
import { listSessions } from "./daemon";

// Every session the multiplexer knows about, and a way to ask again.
//
// ── why `reload` exists at all ─────────────────────────────────────────────
// This was a `useEffect` with an empty dependency list in App, so the list was
// taken once and never again. That is correct for as long as sessions only
// arrive from outside the window — and stopped being correct the moment awp
// could *make* one.
//
// What it looked like is worth writing down, because nothing about it read as a
// stale list. A thread created from the composer appeared in the sidebar
// immediately, because threads are re-read when one is made. Its workspace did
// not, because the session behind it had been started by a job fifteen seconds
// later and nothing had asked. So the thread sat there reading
//
//   Test 1234
//     nothing yet
//
// which is exactly what a thread whose creation *failed* looks like. The
// workspace, the bookmark and the session were all there on disk.
//
// ── not a stream, and not a poll ───────────────────────────────────────────
// The daemon has no session-change feed, and adding one to fix this would be
// building a mechanism for an event that already has one: a session appears
// because a *job* made it, and jobs already stream. So App re-lists when a job
// finishes. See the note there.
//
// A timer was the other option and is worse in both directions — it shows the
// state between the interesting moments, and it asks zmx a question several
// times a minute forever to catch something that happens twice a day.

export interface SessionsView {
  readonly sessions: ReadonlyArray<SessionInfo>;
  /** The daemon could not be reached. Absent once a listing has succeeded. */
  readonly failure: string | undefined;
  readonly reload: () => void;
}

/**
 * Ask the daemon, and report back through the setters.
 *
 * Outside the component for the same reason `useThreads` does it: inside, it
 * would be either a fresh function each render — which `exhaustive-deps`
 * refuses as an effect dependency — or a `useCallback`, which react-doctor
 * refuses as manual memoization in compiler-managed code.
 */
const load = (
  alive: { readonly current: boolean },
  setSessions: (found: ReadonlyArray<SessionInfo>) => void,
  setFailure: (reason: string | undefined) => void,
): void => {
  listSessions()
    .then((listed) => {
      if (alive.current) {
        setSessions(listed);
        setFailure(undefined);
      }
    })
    .catch((error: unknown) => {
      // A daemon that is not running is the ordinary case during development,
      // not an exception. The sidebar says so and gives the command.
      if (alive.current) {
        setFailure(error instanceof Error ? error.message : String(error));
      }
    });
};

export function useSessions(): SessionsView {
  const [sessions, setSessions] = useState<ReadonlyArray<SessionInfo>>([]);
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    load(alive, setSessions, setFailure);
    return () => {
      alive.current = false;
    };
  }, []);

  return { sessions, failure, reload: () => load(alive, setSessions, setFailure) };
}
