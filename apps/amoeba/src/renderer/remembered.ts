// What the window was looking at, across a reload.
//
// Editing the renderer reloads the page, and a pane that forgets its session
// every time means reattaching by hand after every change — which is most of
// what using this looks like while it is being built.
//
// localStorage rather than anything in the daemon. This is a property of the
// window and not of the work: two windows on one machine should be able to look
// at different sessions, and neither should tell the other what to show.

const KEY = "amoeba.session";

/**
 * Reads and writes are both wrapped.
 *
 * A webview can refuse storage outright depending on how the page was loaded,
 * and the accessor throws rather than returning nothing. Losing the selection
 * is a small thing; failing to render because of it is not.
 */
export const rememberedSession = (): string | undefined => {
  try {
    return globalThis.localStorage?.getItem(KEY) ?? undefined;
  } catch {
    return undefined;
  }
};

export const rememberSession = (session: string | undefined): void => {
  try {
    if (session === undefined) {
      globalThis.localStorage?.removeItem(KEY);
    } else {
      globalThis.localStorage?.setItem(KEY, session);
    }
  } catch {
    // Nothing to do and nothing worth saying. The window works without it.
  }
};
