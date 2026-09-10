// Something happened that leaves nothing on screen.
//
// Most of what this app does says itself: a message appears, a row changes,
// a panel opens. Copying does not — the text is on a clipboard in another
// process and the screen is exactly as it was, which is indistinguishable
// from a gesture that did nothing.
//
// ── module scope, and it has to be ────────────────────────────────────────
//
// The copy happens in `clipboard.ts`, off a renderer event, outside React
// entirely. So the announcement cannot start life as component state: what is
// wanted is a value *plus* a subscription, which is the same argument the
// window's `atoms.ts` makes for the inbox.
//
// `notices.ts` and not `toast.ts`, because `Toast.tsx` is beside it and this
// filesystem is case-insensitive: the two would be one module, the import
// would resolve to the wrong file, and the hot reload of everything that
// imports it would fail — which AGENTS.md records as the cause of a webview
// nothing could close.
//
// One line, one at a time, and the newest wins. Two notices at once is a
// queue, and a queue is a thing that has to be read in order — which is not
// what a passing remark about the clipboard is.

const listeners = new Set<(said: string) => void>();

/** Say something, briefly, wherever the window is drawing notices. */
export const notify = (said: string): void => {
  for (const listener of listeners) listener(said);
};

/** Hear notices until the returned function is called. */
export const onNotice = (listener: (said: string) => void): (() => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
