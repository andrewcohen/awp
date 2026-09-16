import { AlertDialog } from "@base-ui/react/alert-dialog";
import { Menu } from "@base-ui/react/menu";
import type { ThreadMember } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useState } from "react";
import { reclaimWorkspace, said } from "../data/daemon";
import { type Items, menuDanger, menuItem } from "./menus";
import { typeset } from "../design/typeset";
import { colors, layer, lift, text, timing } from "../design/tokens.stylex";

// Taking one checkout back, and leaving the thread alone.
//
// ── the row's ⋯ is back, for a reason it did not have before ───────────────
//
// It was removed once, and `ArchiveThread.tsx` still records why: it offered
// thread membership, nobody used it, and it was in the way of the one thing
// anybody wanted from a row. That was right at the time and the world changed
// underneath it — a thread held one workspace then, so "the row" and "the
// thread" were the same object and a menu on each was two menus for one thing.
//
// A thread across three repositories is three rows, and reclaiming one of them
// is a real and ordinary thing to want: the api half is merged, the frontend
// half is not, and 588MB of `node_modules` is sitting in a checkout nobody is
// going to open again. There was no way to ask for that short of archiving the
// whole thread, which takes the work still in progress with it.
//
// ── it is not `ThreadDetach` ──────────────────────────────────────────────
//
//   detach    the claim is released, the checkout stays. How a workspace moves
//             between threads, and it takes nothing with it
//   reclaim   the sessions are killed, the workspace is forgotten and the
//             directory is removed. The thread stays, one member shorter
//
// Both exist and they are not near-synonyms, which is why this one asks first
// and that one does not.
//
// ── one row, and the dialog names it ──────────────────────────────────────
//
// The thread's own archive dialog lists what is going because it is usually
// several. Here it is one, so the name goes in the sentence rather than in a
// list of one — a bulleted list with a single bullet reads as a list that
// failed to load.

const styles = stylex.create({
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.45)" },
  popup: {
    // Nothing pops. Same keyframe as every other dialog here, and it carries
    // the centring translate because the two transforms compose.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translate(-50%, -50%) scale(0.97)" },
      to: { opacity: 1, transform: "translate(-50%, -50%) scale(1)" },
    }),
    animationDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: timing.spring,
    position: "fixed",
    top: "50%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    zIndex: layer.modal,
    display: "flex",
    flexDirection: "column",
    gap: "0.75rem",
    width: "min(30rem, calc(100vw - 3rem))",
    padding: "1rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.5rem",
    color: colors.text,
    boxShadow: lift.high,
  },
  title: { margin: 0 },
  said: { margin: 0, color: colors.muted, fontSize: text.small },
  /** The checkout, by name. An address, so the mono face. */
  which: { color: colors.text },
  keep: { color: colors.muted },
  choice: {
    display: "flex",
    alignItems: "center",
    gap: "0.4rem",
    fontSize: text.small,
    cursor: "pointer",
  },
  box: { margin: 0, accentColor: colors.warn, cursor: "pointer" },
  warn: { color: colors.warn, fontSize: text.small },
  buttons: { display: "flex", gap: "0.5rem", justifyContent: "flex-end" },
  button: {
    padding: "0.35rem 0.75rem",
    backgroundColor: colors.raised,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.3rem",
    color: colors.text,
    cursor: "pointer",
  },
  danger: { backgroundColor: colors.warn, borderColor: colors.warn, color: colors.base },
  failure: { color: colors.warn, fontSize: text.small },
});

/**
 * One checkout's menu: its items, and the dialog the one item opens.
 *
 * A hook for the same reason `useThreadMenu` is one — the right-click area and
 * the ⋯ go in two places in a row, and the dialog belongs outside both.
 */
export function useWorkspaceMenu({
  thread,
  member,
  onChanged,
}: {
  /**
   * The thread holding it, or nothing.
   *
   * Absent for a workspace no thread has claimed — most checkouts on a real
   * machine — and for a row that stands in for its whole thread, which carries
   * the thread's menu instead. With none the items are empty: reclaiming is
   * expressed as a *thread* losing a member, so there is nothing to ask for
   * where there is no thread.
   */
  readonly thread: string | undefined;
  readonly member: ThreadMember | undefined;
  /** The thread list is out of date — a member is going. */
  readonly onChanged: () => void;
}): { readonly items: Items; readonly dialogs: ReactNode } {
  const [asking, setAsking] = useState(false);
  const [bookmarks, setBookmarks] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  const go = () => {
    if (thread === undefined || member === undefined) return;
    setBusy(true);
    setFailure(undefined);
    reclaimWorkspace(thread, member, bookmarks)
      .then(() => {
        setAsking(false);
        // The job does the work; this only says the list should be read again,
        // so the row goes now rather than when the last step happens to
        // finish.
        onChanged();
      })
      .catch((error: unknown) => setFailure(said(error)))
      .finally(() => setBusy(false));
  };

  const where = member === undefined ? "" : `${member.project}/${member.workspace}`;

  const items: Items = () =>
    thread === undefined || member === undefined ? null : (
      <Menu.Item
        onClick={() => {
          setBookmarks(false);
          setFailure(undefined);
          setAsking(true);
        }}
        {...stylex.props(menuItem, menuDanger)}
      >
        archive this checkout…
      </Menu.Item>
    );

  const dialogs =
    thread === undefined || member === undefined ? null : (
      <AlertDialog.Root open={asking} onOpenChange={setAsking}>
        <AlertDialog.Portal>
          <AlertDialog.Backdrop {...stylex.props(styles.backdrop)} />
          <AlertDialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
            <AlertDialog.Title {...stylex.props(typeset.heading, styles.title)}>
              Archive this checkout?
            </AlertDialog.Title>

            <AlertDialog.Description {...stylex.props(styles.said)}>
              <span {...stylex.props(typeset.address, styles.which)}>{where}</span> is removed from
              disk and its sessions are killed. This cannot be undone. The thread and its other
              checkouts are left alone.
            </AlertDialog.Description>

            <label {...stylex.props(styles.choice)}>
              <input
                type="checkbox"
                checked={bookmarks}
                onChange={(event) => setBookmarks(event.target.checked)}
                {...stylex.props(styles.box)}
              />
              delete its bookmark too
            </label>
            {/* Said only when it is being asked for. A warning that is always
                on screen is one nobody reads by the third time. */}
            <p {...stylex.props(bookmarks ? styles.warn : styles.keep, styles.said)}>
              {bookmarks
                ? "A bookmark is a name for a commit, not part of the checkout — deleting it can leave commits nothing points at."
                : "The bookmark stays, so the commits are still there under their name."}
            </p>

            {failure !== undefined && <div {...stylex.props(styles.failure)}>{failure}</div>}

            <div {...stylex.props(styles.buttons)}>
              <AlertDialog.Close {...stylex.props(typeset.label, styles.button)}>
                cancel
              </AlertDialog.Close>
              <button
                type="button"
                disabled={busy}
                onClick={go}
                {...stylex.props(typeset.label, styles.button, styles.danger)}
              >
                {busy ? "archiving…" : "archive"}
              </button>
            </div>
          </AlertDialog.Popup>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    );

  return { items, dialogs };
}
