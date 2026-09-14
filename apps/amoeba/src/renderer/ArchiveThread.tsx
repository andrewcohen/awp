import { AlertDialog } from "@base-ui/react/alert-dialog";
import { Menu } from "@base-ui/react/menu";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useState } from "react";
import { AddProject } from "./AddProject";
import { threadLink } from "./address";
import { archiveThread, said } from "./daemon";
import { type Items, menuDanger, menuItem } from "./menus";
import { typeset } from "./typeset";
import { colors, layer, lift, text, timing } from "./tokens.stylex";

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

/**
 * A thread's menu: its items, and the dialogs two of them open.
 *
 * A hook rather than a component, because the two halves of a menu go in two
 * places — `RightClick` wraps the row, `More` sits inside it wherever the row
 * has room — and the dialogs belong at the end, outside both. A component
 * owning all three could only ever put them in one arrangement, and the
 * heading and the row do not share one.
 */
export function useThreadMenu({
  thread,
  onChanged,
}: {
  /**
   * The thread, or nothing where the row has none.
   *
   * Undefined is accepted rather than the caller branching, because a hook
   * cannot be called conditionally — and both callers have a case with no
   * thread: the sidebar's derived group for unclaimed workspaces, and a row
   * under a heading that already carries the menu. With no thread the items
   * are empty and the dialogs render nothing.
   */
  readonly thread: Thread | undefined;
  /** The thread list is out of date — something was archived or added to. */
  readonly onChanged: () => void;
}): {
  readonly items: Items;
  readonly onOpen: () => void;
  /** Rendered once by the caller, outside the row. */
  readonly dialogs: ReactNode;
} {
  const [asking, setAsking] = useState(false);
  const [adding, setAdding] = useState(false);
  const [bookmarks, setBookmarks] = useState(false);
  const [failure, setFailure] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  const go = () => {
    setBusy(true);
    setFailure(undefined);
    if (thread === undefined) return;
    archiveThread(thread.id, bookmarks)
      .then(() => {
        setAsking(false);
        // The job is what does the work; this only says the list should be
        // read again, so the thread leaves the sidebar now rather than when
        // the last step happens to finish.
        onChanged();
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

  const title = thread === undefined || thread.title === "" ? "this thread" : thread.title;

  const items: Items = () =>
    thread === undefined ? null : (
      <>
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
          {...stylex.props(menuItem)}
        >
          {copied === undefined ? "copy link" : copied ? "copied" : "the clipboard was refused"}
        </Menu.Item>

        {/* ── the second repository, from here ──────────────────────

    The cmd+N modal can name several projects at once, which is
    the way in when somebody already knows the work spans two.
    This is the way in for the case that actually happens: the
    api half turning out to be necessary an hour later. Both
    reach the same `ThreadStart`. */}
        <Menu.Item onClick={() => setAdding(true)} {...stylex.props(menuItem)}>
          add project to thread…
        </Menu.Item>

        {/* The ellipsis says there is more; the item's own ellipsis says
    it will ask first, which is the convention everywhere else a
    menu opens a dialog. */}
        <Menu.Item
          onClick={() => {
            setBookmarks(false);
            setFailure(undefined);
            setAsking(true);
          }}
          {...stylex.props(menuItem, menuDanger)}
        >
          archive…
        </Menu.Item>
      </>
    );

  const dialogs =
    thread === undefined ? null : (
      <>
        <AddProject thread={thread} open={adding} onOpenChange={setAdding} onStarted={onChanged} />

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

  return { items, onOpen: () => setCopied(undefined), dialogs };
}
