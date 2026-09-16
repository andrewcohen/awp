import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { renameThread } from "../data/daemon";
import { colors } from "../design/tokens.stylex";

// Renaming a thread, in place, wherever its title is drawn.
//
// A thread's title is written once by a model, from the sentence somebody
// typed into the new-thread modal. It is frequently almost right, and there
// was no way to fix it — `ThreadRename` has been on the wire since threads
// landed and had no caller at all.
//
// ── double-click, and nothing else ─────────────────────────────────────────
//
// A rename is rare and the title is a thing people click on all day: the
// sidebar's heading folds its group, and the bar's title is inside the window
// drag region. So the gesture has to be one neither of those wants, which is
// the double click — the same one a file manager has used for this since
// before any of this existed.
//
// It is not the only way in: the sidebar row's ⋯ menu has `rename…` beside
// `archive…`, because a gesture with no visible affordance is a feature only
// somebody who already knows about it can use.
//
// ── the field replaces the control, rather than sitting inside it ──────────
//
// Both sites draw their title inside something interactive — a fold button, a
// drag region — and an input nested in either is an input whose clicks belong
// to its parent. So each caller swaps the whole control for this while it is
// editing, which is also what makes Escape unambiguous: there is nothing else
// on the row to give the key to.

export interface RenameProps {
  readonly thread: string;
  /** What it is called now, which is what the field opens holding. */
  readonly title: string;
  /** Settled or abandoned — either way the caller stops drawing this. */
  readonly onDone: () => void;
}

/**
 * The field itself.
 *
 * Commits on Enter and on blur, abandons on Escape. Blur committing is the
 * half worth arguing: a rename is a correction of one or two words, and
 * clicking away from a corrected title to lose the correction is the outcome
 * nobody wants. Escape is the way out, and it is the one every other composer
 * in this window already uses.
 */
export function Rename({ thread, title, onDone }: RenameProps) {
  const [draft, setDraft] = useState(title);
  const [sending, setSending] = useState(false);

  const commit = () => {
    const wanted = draft.trim();
    // Nothing to say, or nothing changed. Both are a cancel rather than a
    // write: a title emptied by accident is not a title somebody meant.
    if (sending || wanted === "" || wanted === title) {
      onDone();
      return;
    }
    setSending(true);
    // Nothing is re-read here: every write to a thread announces itself on the
    // store's own feed, so this window and any other are told by the daemon.
    // The reply is not the update for threads — that rule is about calls with
    // no feed, and this one has had a feed since `watchThreads` landed.
    renameThread(thread, wanted)
      .then(() => onDone())
      .catch(() => {
        // The bar says when the daemon is gone. Putting the old title back is
        // what closing does anyway — the list is re-read from the daemon.
        onDone();
      });
  };

  return (
    <input
      // The gesture that opened this was a double click, which selects a word;
      // opening with the whole title selected means typing replaces it and an
      // arrow key steps to an end. A correction usually wants the second.
      autoFocus
      data-nav-item
      value={draft}
      aria-label="the thread's name"
      onChange={(event) => setDraft(event.target.value)}
      onFocus={(event) => event.target.select()}
      onBlur={commit}
      onKeyDown={(event) => {
        // Both keys are stopped as well as prevented: this field is drawn
        // inside columns that answer Escape and Enter themselves, and a
        // rename that also closed a dialog behind it would be two acts.
        if (event.key === "Enter") {
          event.preventDefault();
          event.stopPropagation();
          commit();
        }
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onDone();
        }
      }}
      // The click that lands in the field must not reach what is underneath —
      // a fold button, a row that would select a different workspace.
      onClick={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
      {...stylex.props(styles.field)}
    />
  );
}

const styles = stylex.create({
  field: {
    flex: 1,
    minWidth: 0,
    padding: "0.05rem 0.2rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.accent,
    borderRadius: "0.2rem",
    color: colors.text,
    // Inherited, so the field reads at the size and weight of the title it
    // replaced. A rename that changes the type is a rename that jumps.
    font: "inherit",
    fontSize: "inherit",
    fontWeight: "inherit",
    outlineStyle: "none",
    // Never taller than the row it is in: both sites are fixed-height chrome,
    // and a field that grows one moves everything below it.
    lineHeight: 1.2,
    // The bar is a drag region; a field inside one cannot be clicked into.
    WebkitAppRegion: "no-drag",
  },
});
