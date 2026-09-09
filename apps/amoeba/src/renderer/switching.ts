import type { Thread } from "@awp-kit/protocol";

// cmd+P: which thread, out of all of them.
//
// ── the first row is the one you were just in ─────────────────────────────
//
// Stated as the requirement and it is the whole design: "cmd p, enter flips you
// back to the last thread". So the order is not alphabetical and not newest —
// it is **recency of visiting**, and the row under the cursor when nothing has
// been typed is the *previous* thread rather than the current one.
//
//   visits   [ current, previous, … ]      what the window has been looking at
//   rows     [ previous, …, current, … ]   what the switcher offers
//              └─ cmd+P then Return, which is the gesture being paid for
//
// The current thread is not removed — it goes **last**, and last of everything
// rather than last of the visited. "Go to where I already am" is a thing
// somebody may still choose deliberately, so a missing row would read as a
// bug; but it is also the one thread nobody needs a switcher to reach, and any
// rule that can put it under the cursor makes Return a no-op that looks like a
// broken shortcut. With a single visit — a freshly opened window — that is
// exactly what "last among the visited" produced.
//
// Everything a thread has been *asked* about lives in the daemon. What is here
// is a property of this window and belongs in localStorage for the reason
// stated in remembered.ts: two windows on one machine should be able to have
// been looking at different work.

/** One thread as a row in the list. */
export interface Row {
  readonly id: string;
  readonly title: string;
  /** The projects it holds, which is what tells two similar titles apart. */
  readonly where: string;
  /** Where it sits in the visit history, for a row that says so. */
  readonly visited: boolean;
}

const where = (thread: Thread): string =>
  [...new Set(thread.members.map((one) => one.project))].join(" · ");

/**
 * Every thread, most recently visited first, with the current one demoted.
 *
 * @param visits  thread ids, newest first — `visits[0]` is where the window is
 *                now, so it is the one thing that must not be the default
 *                choice. Ids that no longer name a thread are ignored rather
 *                than filtered out of the record: a thread archived in another
 *                window is not a reason to forget the order of the rest.
 */
export const ordered = (
  threads: ReadonlyArray<Thread>,
  visits: ReadonlyArray<string>,
): ReadonlyArray<Row> => {
  const rows = new Map(
    threads.map((thread) => [
      thread.id,
      { id: thread.id, title: thread.title, where: where(thread), visited: false },
    ]),
  );
  const out: Array<Row> = [];
  const take = (id: string | undefined): void => {
    if (id === undefined) {
      return;
    }
    const row = rows.get(id);
    if (row !== undefined) {
      out.push({ ...row, visited: true });
      rows.delete(id);
    }
  };

  // The previous one, then the rest of the history, then everything never
  // opened in this window — in whatever order the daemon listed them, which is
  // newest first — and the current thread at the very end.
  const [current, ...earlier] = visits;
  for (const id of earlier) {
    take(id);
  }
  const here = current === undefined ? undefined : rows.get(current);
  if (here !== undefined) {
    rows.delete(current as string);
  }
  return [...out, ...rows.values(), ...(here === undefined ? [] : [{ ...here, visited: true }])];
};

/**
 * The rows worth showing for what has been typed.
 *
 * A substring, case-insensitively, against the title and the projects — not a
 * fuzzy match. A thread title is a sentence somebody wrote, so the letters
 * they remember of it are in it, in order; fuzzy matching a sentence mostly
 * finds every row that happens to contain the same letters somewhere.
 *
 * **The order is not rescored by the query.** Typing narrows the same list, so
 * the top row stays predictable — the alternative ranks by match quality and
 * moves the row under the cursor while somebody is still typing towards it.
 */
export const filtered = (rows: ReadonlyArray<Row>, typed: string): ReadonlyArray<Row> => {
  const said = typed.trim().toLowerCase();
  if (said === "") {
    return rows;
  }
  return rows.filter(
    (row) =>
      row.title.toLowerCase().includes(said) ||
      row.where.toLowerCase().includes(said) ||
      row.id.toLowerCase().includes(said),
  );
};

/** How many ids the visit history keeps. */
export const KEPT = 40;

/**
 * The history with `id` at the front, and no duplicate of it further down.
 *
 * Capped, because it is a record of what somebody has been doing rather than
 * an archive of it — and unbounded growth in a value read on every open is a
 * cost paid for rows nobody will scroll to.
 */
export const visitedWith = (visits: ReadonlyArray<string>, id: string): ReadonlyArray<string> => [
  id,
  ...visits.filter((one) => one !== id).slice(0, KEPT - 1),
];
