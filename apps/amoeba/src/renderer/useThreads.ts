import type { Thread } from "@awp-kit/protocol";
import { useCallback, useEffect, useRef, useState } from "react";
import { listThreads } from "./daemon";

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

export function useThreads(): Threads {
  const [threads, setThreads] = useState<ReadonlyArray<Thread>>([]);
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // The fetch itself is the reload, rather than a counter an effect watches.
  // A counter works, but it is a dependency the effect never reads — which is
  // both a lint error and a fair description of the problem with it.
  const reload = useCallback(() => {
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
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  return { threads, failure, reload };
}
