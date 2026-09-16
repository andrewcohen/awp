import { useSyncExternalStore } from "react";
import { watchConnection } from "./daemon";

// Whether the daemon is there, from the socket rather than from a guess.
//
// The window used to infer it: `connected={failure === undefined}`, where
// `failure` was whatever the last session listing did. That answers "did a
// request work", which is not the same question — it is stale from the moment
// the request returns, and it says nothing at all about a connection that
// dropped between one listing and the next. With the client reconnecting, that
// gap is now the ordinary case rather than a rare one.
//
// `useSyncExternalStore` and not `useState` + `useEffect`, for the reason the
// colour scheme uses it too: the second reads a frame late, so the bar would
// announce a daemon that had already gone, and briefly the reverse on launch.

export function useConnection(): boolean {
  return useSyncExternalStore(
    // The subscribe function is called with React's own callback, which is
    // stable — but `watchConnection` delivers the current value immediately as
    // well, and that is a *notification* React will answer by calling the
    // getter. Harmless, and worth knowing it happens.
    (onChange) => watchConnection(() => onChange()),
    () => connected,
  );
}

// Kept beside the hook rather than in daemon.ts: the module there owns the
// truth and the subscription, and this is only the snapshot React needs to be
// handed synchronously.
let connected = false;
watchConnection((value) => {
  connected = value;
});
