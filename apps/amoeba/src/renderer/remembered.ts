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
const SPLIT = "amoeba.split";
const SPLIT_OPEN = "amoeba.split.open";
const SIDE_BY_SIDE = "amoeba.diff.split";
const PAGE = "amoeba.page";
const PANELS = "amoeba.panels";
const LEFT = "amoeba.left";

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

const asMap = (raw: string | undefined): Record<string, string> => {
  if (raw === undefined) {
    return {};
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
      ? (parsed as Record<string, string>)
      : {};
  } catch {
    // Written by an older version, or by hand. A preference that cannot be
    // read is a preference nobody set.
    return {};
  }
};

/**
 * Which tab the left column is on: the work, or the inbox.
 *
 * The work by default, and it stays the default no matter how the inbox is
 * used: the column's job is to say what is running in this window, and the
 * inbox is a list of what is happening elsewhere. A window that opened on the
 * inbox would answer a question nobody had asked yet.
 */
export const rememberedLeft = (): string => (readStored(LEFT) === "inbox" ? "inbox" : "work");

export const rememberLeft = (tab: string): void => {
  writeStored(LEFT, tab === "inbox" ? "inbox" : "work");
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

// ── the diff panel's own boundary ──────────────────────────────────────────

/**
 * How tall the revision list is, in pixels. Zero means folded away.
 *
 * A number and not a fraction, because the thing being remembered is a
 * *boundary someone put somewhere*, and a fraction moves that boundary every
 * time the window is resized. The panel caps it at 60% of its own height, so a
 * value stored on a tall window cannot swamp a short one.
 *
 * 132 is about six rows: the working copy and the top of a stack, which is what
 * the list is for on an ordinary day. It replaced a fixed 30%, which on a short
 * window was two rows and on a tall one was mostly empty band.
 */
export const DEFAULT_SPLIT = 132;

export const rememberedSplit = (): number => {
  const height = Number(readStored(SPLIT));
  // Number("") is 0, which is a meaningful value here — folded — so the absent
  // case has to be told apart before the number is trusted. Number(undefined)
  // is NaN, and that is the one this leans on.
  if (!Number.isFinite(height) || height < 0) {
    return DEFAULT_SPLIT;
  }
  return Math.round(height);
};

export const rememberSplit = (height: number): void => {
  writeStored(SPLIT, String(Math.round(height)));
};

/**
 * How tall the list was the last time it was open.
 *
 * A second value, because the first one has to be able to hold zero: folded is
 * a state the panel restores on launch, so it cannot be inferred from a height
 * and the height cannot be inferred from it. Collapsing writes this one and
 * then writes zero to the other; expanding reads it back.
 *
 * Without it, expanding went to {@link DEFAULT_SPLIT} and threw away a
 * boundary somebody had put somewhere on purpose — which is the same thing
 * `DEFAULT_SPLIT`'s own note says a fraction would do on every window resize.
 */
export const rememberedOpenSplit = (): number => {
  const height = Number(readStored(SPLIT_OPEN));
  // Zero is not a legal value here, unlike in `rememberedSplit`: this is the
  // height to *open* to, and opening to nothing is not opening.
  if (!Number.isFinite(height) || height <= 0) {
    return DEFAULT_SPLIT;
  }
  return Math.round(height);
};

export const rememberOpenSplit = (height: number): void => {
  writeStored(SPLIT_OPEN, String(Math.round(height)));
};

/**
 * Whether the diff is drawn side by side rather than unified.
 *
 * **One setting for the window, not one per thread**, and it is the first
 * preference here that is. The open panel and the loaded page are properties of
 * a piece of *work* — which diff you are reading, which page you are reading
 * against it — so they are filed under a thread. Unified against split is a
 * reading habit: somebody who wants two columns wants them everywhere, and
 * having to say so again in each thread would be the setting failing to be a
 * setting.
 */
export const rememberedSideBySide = (): boolean => readStored(SIDE_BY_SIDE) === "yes";

export const rememberSideBySide = (on: boolean): void => {
  writeStored(SIDE_BY_SIDE, on ? "yes" : "no");
};

/**
 * The address the web panel was last showing, per thread.
 *
 * It was one per window, on the judgement that the page in that panel is
 * usually a dev server or a set of docs and follows the person rather than the
 * checkout. Use said otherwise: "I don't want to remember the same URL in each
 * thread". A thread is a piece of work, and the page beside it is part of that
 * work — the ticket, the preview, the failing build.
 *
 * Keyed by thread rather than by workspace for the same reason the panel
 * choice is: a thread holding two checkouts is one thing somebody is doing.
 */
export const rememberedPages = (): Record<string, string> => asMap(readStored(PAGE));

export const rememberPage = (thread: string | undefined, url: string | undefined): void => {
  if (thread === undefined) {
    // Nothing open, or a session no thread claims. Filing it under a
    // placeholder would hand this page to the next unclaimed session as though
    // somebody had chosen it there.
    return;
  }
  const was = asMap(readStored(PAGE));
  if (url === undefined || url === "") {
    // eslint-disable-next-line @typescript-eslint/no-dynamic-delete
    delete was[thread];
  } else {
    was[thread] = url;
  }
  writeStored(PAGE, JSON.stringify(was));
};

// ── which files of a patch have been looked at ─────────────────────────────

/**
 * Marked-as-viewed files, per workspace and revision.
 *
 * ── why this is window state and not the daemon's ──────────────────────────
 *
 * Viewed-ness is a property of *a person's pass through a patch*, not of the
 * patch. Nobody else needs to know, nothing acts on it, and it is worthless the
 * moment the revision changes. That makes it the same kind of thing as a column
 * width — which is the test this file applies — rather than the same kind of
 * thing as a comment, which is addressed to somebody and lives in sqlite.
 *
 * It can move to the daemon the day a second person looks at the same review.
 * Until then a migration and an RPC would be machinery for an audience of one.
 *
 * ── a key per patch, and paths separated by newlines ───────────────────────
 *
 * Not one JSON blob for everything. A patch is the unit that becomes worthless
 * at once, so it is the unit that gets thrown away at once — and a newline
 * split has no shape to validate, unlike a parse of whatever a previous version
 * of this app left behind.
 *
 * The keys do accumulate: one per revision ever reviewed, never collected. A
 * few hundred bytes each and localStorage holds megabytes, so the cost is
 * theoretical — but it is a leak, and it is written down here rather than
 * discovered later.
 */
const viewedKey = (project: string, workspace: string, revision: string): string =>
  `amoeba.viewed.${project}/${workspace}/${revision}`;

export const rememberedViewed = (
  project: string,
  workspace: string,
  revision: string,
): ReadonlyArray<string> => {
  const stored = readStored(viewedKey(project, workspace, revision));
  return stored === undefined || stored === "" ? [] : stored.split("\n");
};

export const rememberViewed = (
  project: string,
  workspace: string,
  revision: string,
  paths: ReadonlyArray<string>,
): void => {
  // Removed rather than stored empty, so unmarking the last file leaves nothing
  // behind. The absent and the empty case then read the same, which is what
  // they mean.
  writeStored(
    viewedKey(project, workspace, revision),
    paths.length === 0 ? undefined : paths.join("\n"),
  );
};

// ── which panel each thread had open ───────────────────────────────────────
//
// One panel choice for the whole window was wrong in the way that only shows
// up once there is more than one thread: the diff panel is what you want while
// reviewing one piece of work and the web panel is what you want while
// building another, and switching between them re-answered a question that had
// already been answered for each.
//
// Keyed by thread rather than by workspace, because the choice belongs to the
// *work*. A thread holding two checkouts of the same branch is one thing
// somebody is doing, and looking at the diff in one of them and the browser in
// the other is not a distinction anybody drew on purpose.
//
// A map rather than a key per thread. Threads are made and forgotten
// constantly, and a key per thread is a key per thread forever — this way the
// whole record is one entry that can be pruned or dropped.

/** Every thread's last panel, by thread id. Read once, at mount. */
export const rememberedPanels = (): Record<string, string> => asMap(readStored(PANELS));

export const rememberPanel = (thread: string | undefined, panel: string): void => {
  if (thread === undefined) {
    // Nothing open, or a session no thread claims. There is nowhere to file
    // the choice, and filing it under a placeholder would hand it to the next
    // unclaimed session as though it had been chosen for that one.
    return;
  }
  writeStored(PANELS, JSON.stringify({ ...asMap(readStored(PANELS)), [thread]: panel }));
};
