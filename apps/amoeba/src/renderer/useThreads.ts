import type { Thread } from "@awp-kit/protocol";
import { useEffect, useRef, useState } from "react";
import { listThreads, onReconnect } from "./daemon";

// The threads, and a way to say they changed.
//
// Deliberately not the shape `useJobs` has. Jobs arrive on a stream because a
// job changes on its own — that is what a job *is* — and a list refreshed on a
// timer would show the state between the interesting moments and nothing else.
//
// A thread changes when a person changes it, in this window. So every mutation
// already has the new value in its reply, and the only thing missing is a way
// to say "read them again" after one — which is `reload`. Adding a stream here
// would be a second mechanism for something the reply already answered, and the
// two would disagree the first time one of them was slow.
//
// The exception is a workspace created by a *job*: the claim happens in the
// daemon, minutes later, and nothing here would hear it. `Sidebar` reloads when
// a job finishes, which is the one place the two systems have to meet.

export interface Threads {
  readonly threads: ReadonlyArray<Thread>;
  /** Absent while it is working, which is not the same as "none". */
  readonly failure: string | undefined;
  readonly reload: () => void;
}

/**
 * Ask the daemon, and report back through the setters.
 *
 * Outside the component on purpose. Inside, it would be either a fresh
 * function each render — which `exhaustive-deps` refuses as an effect
 * dependency — or a `useCallback`, which react-doctor refuses as manual
 * memoization in compiler-managed code. Out here it is neither: one function,
 * one identity, no memoization to argue about.
 */
const load = (
  alive: { readonly current: boolean },
  setThreads: (found: ReadonlyArray<Thread>) => void,
  setFailure: (reason: string | undefined) => void,
): void => {
  listThreads()
    .then((found) => {
      if (alive.current) {
        setThreads(found);
        setFailure(undefined);
      }
    })
    .catch((error: unknown) => {
      if (alive.current) {
        setFailure(String(error));
      }
    });
};

export function useThreads(): Threads {
  const [threads, setThreads] = useState<ReadonlyArray<Thread>>([]);
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    load(alive, setThreads, setFailure);
    // And again whenever the daemon comes back. A list is an answer, not a
    // feed: nothing arrives to say what changed while it was away, so a window
    // that survived a restart would go on showing the state from before it.
    const stop = onReconnect(() => load(alive, setThreads, setFailure));
    return () => {
      alive.current = false;
      stop();
    };
  }, []);

  return { threads, failure, reload: () => load(alive, setThreads, setFailure) };
}
