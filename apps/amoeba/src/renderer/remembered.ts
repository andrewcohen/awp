import { type Collapsed, type Columns, bothOpen } from "./columns";

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

const COLLAPSED = "amoeba.collapsed";
const WIDTHS = "amoeba.widths";
const PLACE = "amoeba.place";
const LOOSE = "amoeba.loose";

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

/**
 * How wide the two side columns were asked to be.
 *
 * **What was asked for, not what was granted** — the same distinction App
 * already makes for the live value, and the reason this is stored separately
 * from anything `fitColumns` returns. A narrow window squeezes a column; if the
 * squeezed number were the one written down, widening the window back would
 * leave the column where the narrowest moment put it, permanently and across
 * every future launch.
 *
 * Two integers with a comma between them, for the reason the collapse flags are
 * two letters: a shape this small is cheaper to validate than to parse.
 */
export const DEFAULT_WIDTHS: Columns = { sidebar: 260, accessory: 280 };

export const rememberedWidths = (): Columns => {
  const parts = readStored(WIDTHS)?.split(",");
  if (parts?.length !== 2) {
    return DEFAULT_WIDTHS;
  }
  const sidebar = Number(parts[0]);
  const accessory = Number(parts[1]);
  // Number("") is 0 and Number("x") is NaN, so both are refused here rather
  // than reaching the layout as a column of zero width that nothing collapsed.
  //
  // Read separately rather than destructured off a `map`: an element of that is
  // `number | undefined`, and `Number.isFinite` is not declared as a type guard
  // in the standard lib, so the check does not narrow it away.
  if (!Number.isFinite(sidebar) || !Number.isFinite(accessory) || sidebar < 0 || accessory < 0) {
    return DEFAULT_WIDTHS;
  }
  return { sidebar: Math.round(sidebar), accessory: Math.round(accessory) };
};

export const rememberWidths = (columns: Columns): void => {
  writeStored(WIDTHS, `${Math.round(columns.sidebar)},${Math.round(columns.accessory)}`);
};

/**
 * Whether the "not in a thread" group is unfolded. Folded unless it says so.
 *
 * The default is the point rather than an implementation detail. On this
 * machine that group is thirty of thirty-seven rows — every workspace made
 * before threads existed — so an unfolded sidebar is mostly a list of work
 * nobody grouped, with the threads someone is actually running pushed off the
 * top. It is the *archive*, and an archive should be a heading you can open.
 */
export const rememberedLooseOpen = (): boolean => readStored(LOOSE) === "o";

export const rememberLooseOpen = (open: boolean): void => {
  writeStored(LOOSE, open ? "o" : "c");
};

// ── where the window was ───────────────────────────────────────────────────
//
// The address is the router's, and the history keeps it across a reload — a
// hash survives the page being replaced, which is most of the argument for
// routing at all. What a history does *not* survive is the application being
// quit and started again: a fresh window opens at `#/` with no entries behind
// it.
//
// So this is a mirror and not a store. It is written whenever the address
// changes, and read exactly once, at launch, and only when the hash is empty —
// which is the one moment the history has nothing to say. Anything more would
// be two things claiming to know where the window is.

export const rememberedPlace = (): string | undefined => {
  const stored = readStored(PLACE);
  // Anything not starting with a slash is not a path this app ever wrote, and
  // handing it to the router would be navigating to whatever it happens to
  // parse as.
  return stored?.startsWith("/") === true ? stored : undefined;
};

export const rememberPlace = (path: string): void => {
  writeStored(PLACE, path);
};
