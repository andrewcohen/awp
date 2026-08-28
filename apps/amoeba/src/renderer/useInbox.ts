import type { Inbox } from "@awp-kit/protocol";
import { useAtomSet, useAtomValue } from "@effect/atom-react";
import { useEffect } from "react";
import { inboxAtom, inboxFailureAtom, inboxReadingAtom } from "./atoms";
import { listInbox, onReconnect } from "./daemon";

// The inbox, held where a tab switch cannot destroy it.
//
// The fetching is unchanged — a call, not a subscription, because a pull request
// changes on somebody else's machine and there is nothing here to subscribe to.
// What changed is where the answer lives: in atoms, so that
//
//   opening the tab      shows the rows that were there, at once
//   the read that follows backfills them when it lands
//   a read still in flight when the tab is closed still lands, and the next
//   open shows its result rather than starting again
//
// See `atoms.ts` for why this is the thing `@effect/atom-react` was kept for.
//
// ── one read at a time ────────────────────────────────────────────────────
//
// The panel is mounted every time its tab is opened, so without a guard a person
// switching back and forth starts a read per switch — and each one takes a
// socket round trip and finishes out of order. The flag is module scope for the
// same reason the atoms are: it has to outlive the component that set it.

export interface UseInbox {
  /** Absent until the first answer, which is not the same as an empty inbox. */
  readonly inbox: Inbox | undefined;
  /** True while a read is in flight — including one over rows already shown. */
  readonly reading: boolean;
  /** The whole call failed — the daemon, not a project. See `Inbox.sources`. */
  readonly failure: string | undefined;
  /** Ask again. `refresh` goes past the daemon's cache to GitHub. */
  readonly reload: (refresh?: boolean) => void;
}

let inFlight = false;

/**
 * Ask the daemon, and write the answer where it will be found later.
 *
 * The setters are the registry's rather than a component's state, so nothing
 * here is guarded on the caller still being mounted: a read that finishes after
 * its panel was closed is exactly the read this is meant to keep.
 */
const load = (
  refresh: boolean,
  set: {
    readonly inbox: (found: Inbox) => void;
    readonly reading: (busy: boolean) => void;
    readonly failure: (reason: string | undefined) => void;
  },
): void => {
  if (inFlight) {
    return;
  }
  inFlight = true;
  set.reading(true);
  listInbox(refresh)
    .then((found) => {
      set.inbox(found);
      set.failure(undefined);
    })
    .catch((error: unknown) => {
      set.failure(String(error));
    })
    .finally(() => {
      inFlight = false;
      set.reading(false);
    });
};

export function useInbox(): UseInbox {
  const inbox = useAtomValue(inboxAtom);
  const reading = useAtomValue(inboxReadingAtom);
  const failure = useAtomValue(inboxFailureAtom);
  const set = {
    inbox: useAtomSet(inboxAtom),
    reading: useAtomSet(inboxReadingAtom),
    failure: useAtomSet(inboxFailureAtom),
  };

  useEffect(() => {
    // Asked on every mount, and the rows from last time are on screen while it
    // happens. Not a refresh: the daemon's own cache is what makes this cheap,
    // and going past it on every tab switch would undo that.
    load(false, set);
    // And again whenever the daemon comes back: a list is an answer, not a feed,
    // so nothing arrives to say what changed while it was away.
    const stop = onReconnect(() => load(false, set));
    return stop;
    // `set` is a fresh object each render holding stable setters, so it is
    // deliberately not a dependency — this runs once per mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { inbox, reading, failure, reload: (refresh = false) => load(refresh, set) };
}
