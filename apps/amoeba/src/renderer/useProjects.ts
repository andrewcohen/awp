import type { Project } from "@awp-kit/protocol";
import { useEffect, useRef, useState } from "react";
import { listProjects, onReconnect } from "./daemon";

// The projects, and a way to say they changed.
//
// The same shape as `useThreads`, and for the same reason: a project changes
// when a person imports or forgets one, in this window, so the reply to the
// change is the update and there is nothing to stream.
//
// It replaced deriving the list from the session listing. That derivation was
// not wrong so much as backwards — it made a project exist because something
// was running in it, so the picker was empty in exactly the case someone opened
// it for: a repository with no work started in it yet. The daemon still folds
// the sessions in, so nothing that used to appear has stopped appearing.

export interface Projects {
  readonly projects: ReadonlyArray<Project>;
  readonly failure: string | undefined;
  readonly reload: () => void;
}

/** Outside the component, for the reason `useThreads` states at length. */
const load = (
  alive: { readonly current: boolean },
  setProjects: (found: ReadonlyArray<Project>) => void,
  setFailure: (reason: string | undefined) => void,
): void => {
  listProjects()
    .then((found) => {
      if (alive.current) {
        setProjects(found);
        setFailure(undefined);
      }
    })
    .catch((error: unknown) => {
      if (alive.current) {
        setFailure(String(error));
      }
    });
};

export function useProjects(): Projects {
  const [projects, setProjects] = useState<ReadonlyArray<Project>>([]);
  const [failure, setFailure] = useState<string | undefined>();
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    load(alive, setProjects, setFailure);
    const stop = onReconnect(() => load(alive, setProjects, setFailure));
    return () => {
      alive.current = false;
      stop();
    };
  }, []);

  return { projects, failure, reload: () => load(alive, setProjects, setFailure) };
}
