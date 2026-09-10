import { AlertDialog } from "@base-ui/react/alert-dialog";
import { Menu } from "@base-ui/react/menu";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { threadLink } from "./address";
import { archiveThread, said } from "./daemon";
import { typeset } from "./typeset";
import { colors, lift, text, timing } from "./tokens.stylex";

// Putting a thread away, and taking its checkouts back with it.
//
// ── on the heading, not on a row ───────────────────────────────────────────
//
// A thread is the unit. It holds one checkout or four, in one project or two,
// and reclaiming half of them leaves a thread that is neither finished nor
// live. The row's own ⋯ used to offer thread membership and is gone: a menu
// nobody was using was in the way of the one thing anybody wanted from there.
//
// ── why it asks first ──────────────────────────────────────────────────────
//
// Archiving is two things at once, and only one of them can be taken back:
//
//   the flag      reversible. `ThreadArchive` with `archived: false` undoes it
//   the reclaim   permanent. A removed checkout does not come back, so an
//                 unarchive afterwards restores the row and not the work
//
// So the dialog says what is going, by name, before anything happens — an
// AlertDialog and not a Dialog, which is Base UI's distinction and the right
// one: an alert dialog has no dismiss-by-clicking-outside, and its default
// focus is the cancel.
//
// ── the bookmark is the checkbox, and it is off ────────────────────────────
//
// A bookmark is not part of a workspace. It is a name for a commit, kept in
// the repository, so it outlives the checkout being removed — keeping it is
// what keeps the work addressable by name, and deleting it can leave commits
// with nothing pointing at them for jj to collect later.
//
// Everywhere else in awp, forgetting takes nothing with it. This is the one
// place a person can ask for the opposite, so they have to ask.

const styles = stylex.create({
  trigger: {
    flexShrink: 0,
    padding: "0 0.25rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.muted,
    font: "inherit",
    fontSize: text.small,
    lineHeight: 1,
    cursor: "pointer",
    // Never `display: none` — an element outside the layout cannot be focused,
    // and hover-only means the feature does not exist without a pointer.
    opacity: 0,
    ":focus-visible": { opacity: 1 },
  },
  shown: { opacity: 1 },
  positioner: { zIndex: 20 },
  menu: {
    // Portalled, so the family is stated rather than inherited.
    minWidth: "10rem",
    padding: "0.25rem",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.35rem",
    color: colors.text,
    boxShadow: lift.high,
  },
  item: {
    display: "flex",
    alignItems: "center",
    padding: "0.3rem 0.5rem",
    borderRadius: "0.25rem",
    cursor: "pointer",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    outline: "none",
  },

  backdrop: {
    position: "fixed",
    inset: 0,
    backgroundColor: "rgba(0, 0, 0, 0.45)",
  },
  popup: {
    // ── it arrives, rather than being there ─────────────────────────────
    //
    // Nothing pops: the window's mandate, and a dialog is the largest
    // thing in it that appears. Scale from just under, so it reads as
    // coming forward rather than as growing — and the transform is
    // composed with the centring translate, which is why the keyframe
    // carries both.
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
    zIndex: 30,
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
  /** What is going, by name. Addresses, so the mono face. */
  list: {
    display: "flex",
    flexDirection: "column",
    gap: "0.15rem",
    maxHeight: "10rem",
    overflowY: "auto",
  },
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

export function ArchiveThread({
  thread,
  shown,
  onArchived,
}: {
  readonly thread: Thread;
  /** The heading is hovered. Focus reveals the trigger on its own. */
  readonly shown: boolean;
  readonly onArchived: () => void;
}) {
  const [asking, setAsking] = useState(false);
  const [bookmarks, setBookmarks] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  const go = () => {
    setBusy(true);
    setFailure(undefined);
    archiveThread(thread.id, bookmarks)
      .then(() => {
        setAsking(false);
        // The job is what does the work; this only says the list should be
        // read again, so the thread leaves the sidebar now rather than when
        // the last step happens to finish.
        onArchived();
      })
      .catch((error: unknown) => {
        setFailure(said(error));
      })
      .finally(() => setBusy(false));
  };

  /**
   * Whether the last copy landed, or nothing has been pressed.
   *
   * Three states rather than a boolean: `undefined` is the resting label, and
   * a refusal has to be able to say so — see the note on the item.
   */
  const [copied, setCopied] = useState<boolean | undefined>(undefined);

  const title = thread.title === "" ? "this thread" : thread.title;

  return (
    <>
      <Menu.Root
        onOpenChange={(open) => {
          if (open) {
            // A menu reopened an hour later saying `copied` would be
            // reporting a press nobody remembers making.
            setCopied(undefined);
          }
        }}
      >
        <Menu.Trigger
          aria-label={`more for ${title}`}
          title="more"
          {...stylex.props(styles.trigger, shown && styles.shown)}
        >
          ⋯
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner sideOffset={4} align="end" {...stylex.props(styles.positioner)}>
            <Menu.Popup {...stylex.props(typeset.label, styles.menu)}>
              {/* The ellipsis says there is more; the item's own ellipsis says
                  it will ask first, which is the convention everywhere else a
                  menu opens a dialog. */}
              {/* ── the link is to the THREAD, not to a checkout ──────────

                  A thread survives its checkouts being renamed, added and
                  removed, so a link naming one of them goes stale the first
                  time somebody reorganises the work. `/t/<id>` resolves to
                  whichever checkout the thread holds first — see the `thread`
                  variant in address.ts.

                  `closeOnClick={false}`, and that is the whole of the
                  feedback: the clipboard can be refused — a renderer served
                  over a custom protocol is not always a secure context — and
                  a menu that closed on a copy that did not happen would be a
                  control that silently did nothing. So the item stays and
                  says which it was. */}
              <Menu.Item
                closeOnClick={false}
                onClick={() => {
                  navigator.clipboard
                    ?.writeText(threadLink(thread.id))
                    .then(() => setCopied(true))
                    .catch(() => setCopied(false));
                }}
                {...stylex.props(styles.item)}
              >
                {copied === undefined
                  ? "copy link"
                  : copied
                    ? "copied"
                    : "the clipboard was refused"}
              </Menu.Item>
              <Menu.Item
                onClick={() => {
                  setBookmarks(false);
                  setFailure(undefined);
                  setAsking(true);
                }}
                {...stylex.props(styles.item)}
              >
                archive…
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <AlertDialog.Root open={asking} onOpenChange={setAsking}>
        <AlertDialog.Portal>
          <AlertDialog.Backdrop {...stylex.props(styles.backdrop)} />
          <AlertDialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
            <AlertDialog.Title {...stylex.props(typeset.heading, styles.title)}>
              Archive {title}?
            </AlertDialog.Title>

            <AlertDialog.Description {...stylex.props(styles.said)}>
              {thread.members.length === 0
                ? "It holds no workspaces, so this only puts it away."
                : `Its ${thread.members.length === 1 ? "checkout is" : `${thread.members.length} checkouts are`} removed from disk and their sessions are killed. This cannot be undone.`}
            </AlertDialog.Description>

            {thread.members.length > 0 && (
              <div {...stylex.props(typeset.address, styles.list)}>
                {thread.members.map((member) => (
                  <span key={`${member.project}/${member.workspace}`}>
                    {member.project}/{member.workspace}
                  </span>
                ))}
              </div>
            )}

            {thread.members.length > 0 && (
              <>
                <label {...stylex.props(styles.choice)}>
                  <input
                    type="checkbox"
                    checked={bookmarks}
                    onChange={(event) => setBookmarks(event.target.checked)}
                    {...stylex.props(styles.box)}
                  />
                  delete their bookmarks too
                </label>
                {/* Said only when it is being asked for. A warning that is
                    always on screen is a warning nobody reads by the third
                    time. */}
                <p {...stylex.props(bookmarks ? styles.warn : styles.keep, styles.said)}>
                  {bookmarks
                    ? "A bookmark is a name for a commit, not part of the checkout — deleting it can leave commits nothing points at."
                    : "The bookmarks stay, so the commits are still there under their names."}
                </p>
              </>
            )}

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
    </>
  );
}
