import type { Page } from "@awp-kit/protocol";
import { useAtomSet, useAtomValue } from "@effect/atom-react";
import { useEffect } from "react";
import { pageAskedAtom, pagesAtom } from "./atoms";
import { watchPages } from "./daemon";
import { rememberPage } from "./remembered";

// The web panel's page, subscribed above the panel.
//
// ── the subscription cannot live in the panel ─────────────────────────────
//
// Every other stream this window reads is read by whatever draws it: the jobs
// panel subscribes to jobs, the chat to its conversation. This one cannot, and
// the reason is the whole of why the file exists — a page arrives because an
// *agent* asked for one, and the moment an agent has something to show is the
// moment somebody is reading the diff. Base UI unmounts a hidden tab, so a
// subscription owned by the web panel is not there precisely when it is needed,
// and the navigation would land nowhere at all.
//
// So {@link usePageWatch} is called once, by App, and the panel reads what it
// wrote. Two hooks rather than one for that reason: the writer's lifetime is
// the window's and the reader's is a tab's.
//
// ── the panel's key, and it is the thread ─────────────────────────────────
//
// A page belongs to a piece of work rather than to a checkout — see `Web.tsx` —
// so two workspaces of one thread share a page. A workspace no thread claims
// gets `""`, which is the same bucket the panel has always kept for one, and
// what arrives from the daemon for it is a `Page` with no `thread` field.
export const pageKey = (thread: string | undefined): string => thread ?? "";

/**
 * Keep this window's record of every thread's page up to date, for as long as
 * the window is open.
 *
 * Written into the atoms and into localStorage, both. The atom is what a panel
 * mounted now reads; the stored copy is what the *next* launch reads, because
 * the daemon replays nothing — a navigation is an event and one replayed a day
 * later would move somebody's page for them on startup.
 */
export const usePageWatch = (): void => {
  const setPages = useAtomSet(pagesAtom);
  const setAsked = useAtomSet(pageAskedAtom);

  useEffect(() => {
    const stop = watchPages((page: Page) => {
      const key = pageKey(page.thread);
      setPages((was) => ({ ...was, [key]: page.url }));
      rememberPage(page.thread, page.url);
      setAsked(page);
    });
    return stop;
    // Once, for the window's life. The setters are the registry's and are
    // stable, and this is the one subscription in the window with no subject to
    // key on: it carries pages for every thread, including ones nothing is
    // looking at.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
};

export interface UsePages {
  /** Every thread's page, by {@link pageKey}. Absent means none was ever set. */
  readonly pages: Record<string, string>;
  /** The last navigation asked for, whoever asked. Undefined until one is. */
  readonly asked: Page | undefined;
  /** Record a page this window navigated to itself. */
  readonly remember: (thread: string | undefined, url: string) => void;
}

/**
 * What the panel reads.
 *
 * `remember` is here as well as the stream, because the window navigates too:
 * typing an address and following a link both have to reach the same record,
 * and `PageOpen` is not the route for a link the page itself followed — nothing
 * asked for that, it happened.
 */
export const usePages = (): UsePages => {
  const pages = useAtomValue(pagesAtom);
  const asked = useAtomValue(pageAskedAtom);
  const setPages = useAtomSet(pagesAtom);

  return {
    pages,
    asked,
    remember: (thread, url) => {
      setPages((was) => ({ ...was, [pageKey(thread)]: url }));
      rememberPage(thread, url);
    },
  };
};
