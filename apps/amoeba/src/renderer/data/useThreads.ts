import type { Thread } from "@awp-kit/protocol";
import { useEffect, useRef, useState } from "react";
import { listThreads, onReconnect, said, watchThreads } from "./daemon";

// The threads: asked for once, and then watched.
//
// This is `useJobs`' shape now, and it was not. The argument for the old one
// was that a thread changes when a person changes it in this window, so the
// reply to the change is the update — and every exception to that had already
// been patched separately by the time there were three of them:
//
//   a create job    claims the workspace at its second-to-last step, minutes
//                   after the reply this window acted on. `progressKey`
//   a review        links the pull request from inside the job. `onStarted`
//   the reviewQueue join  adopts one by its head commit, on a read nobody made
//                   here. `useAdoptions`
//
// Three implementations of "read the threads again" is exactly the shape this
// codebase calls the copy that drifts, and none of them covers the fourth case
// at all: a second daemon on the same store, where the person changing the
// thread is in the other window.
//
// Nothing exposes a `reload` any more, and that is the measure of it: there is
// no longer a caller anywhere in the window whose job is to notice that the
// threads might have moved. The question is asked twice — at mount, and every
// time the socket comes back — which is the rule this file already followed for
// the reason a stream carries changes from *now*.

export interface Threads {
  readonly threads: ReadonlyArray<Thread>;
  /** Absent while it is working, which is not the same as "none". */
  readonly failure: string | undefined;
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
        setFailure(said(error));
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
    // And every change from now, whoever made it — this window, a job step in
    // the daemon, an agent's own tool, or a second instance on the same store.
    // The feed carries the whole list, so there is nothing to merge.
    const watching = watchThreads((found) => {
      if (alive.current) {
        setThreads(found);
        setFailure(undefined);
      }
    });
    return () => {
      alive.current = false;
      stop();
      watching();
    };
  }, []);

  return { threads, failure };
}
