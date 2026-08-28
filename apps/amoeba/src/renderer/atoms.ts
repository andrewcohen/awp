import type { Inbox, PullRequest } from "@awp-kit/protocol";
import { Atom } from "effect/unstable/reactivity";

// State that outlives the component holding it.
//
// ── the day the dependency was for ────────────────────────────────────────
//
// `@effect/atom-react` has been in this tree, imported by nothing, with a note
// in AGENTS.md saying it is the answer when the window needs shared state and
// that reaching for something else on that day is the mistake the list exists to
// prevent. This is that day, and the reason is Base UI: a hidden tab is
// **unmounted**, so every panel's `useState` is destroyed by switching away from
// it. For the diff that is a feature — it re-reads the patch on the way back.
// For the inbox it is a list of forty-five pull requests, fetched over a socket,
// thrown away because somebody looked at the diff for a second.
//
// What that looked like: an empty panel with `reading…` in the corner, every
// single time the tab was opened, for a list the daemon already had in memory.
//
// ── why an atom and not a module-level `let` ──────────────────────────────
//
// A `let` would hold the value and tell nobody. What is needed is the value
// *plus* a subscription, so a fetch that finishes after its component unmounted
// still reaches whichever component is mounted now — which is the whole shape of
// `useSyncExternalStore`, and is what an atom is. The registry defaults when no
// provider is present, so there is no tree change and nothing to wire.
//
// These are deliberately plain state atoms rather than `Atom.make(effect)`. The
// fetching lives in `daemon.ts` behind promises — the seam this window keeps
// between Effect and React — and moving it into an atom would put a second
// runtime's worth of that decision here.

/**
 * The last inbox the daemon answered with, or nothing before the first.
 *
 * Kept whole rather than as rows: the sources and the login are part of the
 * answer, and a panel showing rows from one read beside a `read at` from another
 * would be lying about both.
 */
export const inboxAtom = Atom.make<Inbox | undefined>(undefined);

/** True while a read is in flight, so the panel can say so over the old rows. */
export const inboxReadingAtom = Atom.make(false);

/** The daemon-level failure, when the whole call failed. Not a per-project one. */
export const inboxFailureAtom = Atom.make<string | undefined>(undefined);

/**
 * Pull requests the window has read, by `<project>#<number>`.
 *
 * Keyed rather than one atom per panel, because the panel is remounted with a
 * *different* pull request whenever the selection moves — and what makes coming
 * back to one instant is that the last answer for it is still here.
 *
 * `null` is "gh says there is no such pull request", which is a different state
 * from "not asked yet" and reads differently in the panel.
 *
 * ── plain records, not Maps, and that is not a style choice ────────────────
 *
 * `Atom.make` is overloaded: it builds writable state from a value, and an
 * `AsyncResult` atom from an Effect, a Stream **or an Iterable**. A `Map` and a
 * `Set` are iterables, so they match the wrong overload first and the atom comes
 * back as `Atom<AsyncResult<…>>` — which fails to compile at the write, several
 * lines from the cause. A record is not iterable, so it lands on the constructor
 * that was meant. It is also what `Accessory` already keeps its per-thread panel
 * choice in.
 *
 * Nothing evicts these. A dozen pull requests is not a memory question, and the
 * whole point is that the answer is still here when somebody comes back to it.
 */
export const prsAtom = Atom.make<Record<string, PullRequest | null>>({});

/** Which of them have a read in flight, by the same key. */
export const prsReadingAtom = Atom.make<Record<string, true>>({});

/** The last failure per pull request, so a stale answer can be shown beside it. */
export const prsFailureAtom = Atom.make<Record<string, string>>({});

/** The key every one of those records uses. One function, so they agree. */
export const prKey = (project: string, number: number): string => `${project}#${number}`;
