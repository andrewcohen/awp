// What counts as one press of a chord.
//
// Separate from the handlers so the rule is stated once and can be tested
// without a window — the keyboard is a mandate in this application and the part
// that goes wrong is never the letter.

/**
 * A fresh press of `code` with the window's modifier held.
 *
 * ── `event.repeat` is the half that is easy to leave out ──────────────────
 *
 * Holding a key repeats `keydown` about fifteen times a second. That was
 * invisible on the chords that only ever *open* something — `cmd+N` and `cmd+P`
 * both leave an already-open dialog alone, so the repeats land on a guard and
 * nothing moves — and plainly wrong on the ones that **toggle**, which flapped
 * open and shut under a held key and ended wherever the release happened to
 * land.
 *
 * So the guard belongs to every chord rather than to the ones that showed it:
 * the others were being saved by an unrelated guard, not by being right, and
 * the next toggle added would have had the bug again.
 *
 * ── `event.code`, not `event.key` ─────────────────────────────────────────
 *
 * `key` is layout-dependent and arrives upper-case whenever shift is down —
 * which is exactly what two of these chords are told apart by.
 *
 * ── shift is deliberately not read ────────────────────────────────────────
 *
 * `cmd+shift+R` is claimed and `cmd+shift+P` is deliberately left alone, so
 * whether shift belongs is the caller's rule rather than this one's.
 *
 * ── and `ctrl` is not the same modifier ───────────────────────────────────
 *
 * Either meta or control, because the window runs in a browser as well as in
 * Electron. `alt` is refused outright: an alt chord is a different binding, not
 * a sloppier version of this one.
 */
export const pressed = (event: KeyboardEvent, code: string): boolean =>
  event.code === code &&
  (event.metaKey || event.ctrlKey) &&
  !event.altKey &&
  // Deliberately last: it reads as the surprising clause, and it is.
  !event.repeat;
