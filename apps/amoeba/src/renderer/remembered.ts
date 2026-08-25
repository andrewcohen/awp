import { type Collapsed, bothOpen } from "./columns";

// What the window was looking at, across a reload.
//
// Editing the renderer reloads the page, and a window that forgets its session,
// its column widths and its appearance every time means putting all three back
// by hand after every change — which is most of what using this looks like
// while it is being built.
//
// localStorage rather than anything in the daemon. These are properties of the
// window and not of the work: two windows on one machine should be able to look
// at different sessions with different columns collapsed, and neither should
// tell the other what to show.

/**
 * Reads and writes are both wrapped, everywhere, and this is the only place
 * that has to say why.
 *
 * A webview can refuse storage outright depending on how the page was loaded,
 * and the accessor throws rather than returning nothing. Losing a preference is
 * a small thing; failing to render because of it is not.
 */
export const readStored = (key: string): string | undefined => {
  try {
    return globalThis.localStorage?.getItem(key) ?? undefined;
  } catch {
    return undefined;
  }
};

export const writeStored = (key: string, value: string | undefined): void => {
  try {
    if (value === undefined) {
      globalThis.localStorage?.removeItem(key);
    } else {
      globalThis.localStorage?.setItem(key, value);
    }
  } catch {
    // Nothing to do and nothing worth saying. The window works without it.
  }
};

const SESSION = "amoeba.session";
const COLLAPSED = "amoeba.collapsed";

export const rememberedSession = (): string | undefined => readStored(SESSION);

export const rememberSession = (session: string | undefined): void => {
  writeStored(SESSION, session);
};

/**
 * Stored as two letters rather than JSON.
 *
 * Not for size — for what happens when it is wrong. `JSON.parse` on whatever a
 * previous version of this app or a curious user left behind returns any shape
 * at all, and the checking that follows is longer than the writing. Two
 * characters have exactly four valid values and every other string is simply
 * not one.
 */
export const rememberedCollapsed = (): Collapsed => {
  const stored = readStored(COLLAPSED);
  if (stored?.length !== 2) {
    return bothOpen;
  }
  return { sidebar: stored[0] === "c", accessory: stored[1] === "c" };
};

export const rememberCollapsed = (collapsed: Collapsed): void => {
  writeStored(COLLAPSED, `${collapsed.sidebar ? "c" : "o"}${collapsed.accessory ? "c" : "o"}`);
};
