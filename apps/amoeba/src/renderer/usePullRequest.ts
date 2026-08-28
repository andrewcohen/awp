import type { PullRequest } from "@awp-kit/protocol";
import { useAtomSet, useAtomValue } from "@effect/atom-react";
import { useEffect } from "react";
import { prKey, prsAtom, prsFailureAtom, prsReadingAtom } from "./atoms";
import { onReconnect, readPullRequest } from "./daemon";

// One pull request, held where a tab switch cannot destroy it.
//
// The same treatment `useInbox` got, and the same cause: Base UI unmounts a
// hidden tab, so switching to the diff and back destroyed the answer and the
// panel came back empty while it re-asked. What was on screen for that moment
// was nothing at all — a title, a description and a conversation replaced by the
// word `reading…`, for a pull request the daemon had cached.
//
// **Stale beats empty**, which is the rule this exists to enforce: while a read
// is in flight the previous answer stays on screen and the header says
// `refreshing`. The only blank state left is the first read of a pull request
// nobody has looked at yet, where there is genuinely nothing to show.
//
// Keyed by `<project>#<number>` because the panel is remounted with a *different*
// pull request whenever the selection moves — see `prsAtom`.

export interface UsePullRequest {
  /** The last answer. Absent before the first; `null` if gh has no such PR. */
  readonly pr: PullRequest | null | undefined;
  /** True while a read is in flight, over whatever is already on screen. */
  readonly reading: boolean;
  readonly failure: string | undefined;
  /** Ask again; `refresh` goes past the daemon's cache to GitHub. */
  readonly reload: (refresh?: boolean) => void;
}

interface Setters {
  readonly prs: (
    update: (was: Record<string, PullRequest | null>) => Record<string, PullRequest | null>,
  ) => void;
  readonly reading: (update: (was: Record<string, true>) => Record<string, true>) => void;
  readonly failure: (update: (was: Record<string, string>) => Record<string, string>) => void;
}

/**
 * Ask the daemon, and write the answer where it will be found later.
 *
 * Nothing is guarded on the caller still being mounted: writing to the registry
 * after the panel closed is the behaviour, not a leak — the next open shows the
 * result rather than starting again.
 */
const load = (
  project: string | undefined,
  number: number | undefined,
  refresh: boolean,
  set: Setters,
  inFlight: Record<string, true>,
): void => {
  if (project === undefined || number === undefined) {
    return;
  }
  const key = prKey(project, number);
  // One read at a time per pull request. Without it, switching tabs three times
  // starts three reads that finish out of order.
  if (inFlight[key] === true) {
    return;
  }
  set.reading((was) => ({ ...was, [key]: true }));
  readPullRequest(project, number, refresh)
    .then((found) => {
      set.prs((was) => ({ ...was, [key]: found ?? null }));
      set.failure((was) => {
        const { [key]: _gone, ...rest } = was;
        return rest;
      });
    })
    .catch((error: unknown) => {
      // The previous answer is deliberately left in place: a failed refresh over
      // a pull request already on screen should say so beside it, not blank it.
      set.failure((was) => ({ ...was, [key]: String(error) }));
    })
    .finally(() => {
      set.reading((was) => {
        const { [key]: _gone, ...rest } = was;
        return rest;
      });
    });
};

export function usePullRequest(
  project: string | undefined,
  number: number | undefined,
): UsePullRequest {
  const prs = useAtomValue(prsAtom);
  const inFlight = useAtomValue(prsReadingAtom);
  const failures = useAtomValue(prsFailureAtom);
  const setPrs = useAtomSet(prsAtom);
  const setReading = useAtomSet(prsReadingAtom);
  const setFailure = useAtomSet(prsFailureAtom);

  const key = project === undefined || number === undefined ? undefined : prKey(project, number);
  const set: Setters = {
    // `useAtomSet` takes a value; the updater shape is this file's, so every
    // write is a read-modify-write of the map rather than a replacement that
    // would drop the other pull requests.
    prs: (update) => setPrs((was) => update(was)),
    reading: (update) => setReading((was) => update(was)),
    failure: (update) => setFailure((was) => update(was)),
  };

  useEffect(() => {
    load(project, number, false, set, inFlight);
    const stop = onReconnect(() => load(project, number, false, set, inFlight));
    return stop;
    // Keyed on the pair: a different pull request is a different read. `set` and
    // `inFlight` are deliberately not dependencies — the first holds stable
    // setters, and the second is read for a guard rather than watched.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, number]);

  return {
    pr: key === undefined ? undefined : prs[key],
    reading: key !== undefined && inFlight[key] === true,
    failure: key === undefined ? undefined : failures[key],
    reload: (refresh = false) => load(project, number, refresh, set, inFlight),
  };
}
