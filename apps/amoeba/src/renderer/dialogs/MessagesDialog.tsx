import type { Thread } from "@awp-kit/protocol";
import { Dialog } from "@base-ui/react/dialog";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { Messages } from "../panels/Messages";
import { useOverlay } from "../shell/overlays";
import { typeset } from "../design/typeset";
import { colors, lift, space, timing } from "../design/tokens.stylex";

// What the agents have said to each other, in a window of its own.
//
// Titled `inbox`, which the review queue held until it became `pull requests`.
// The dialog's title is the menu item's word rather than a second name for the
// same place — a control and the thing it opens disagreeing is how somebody
// ends up looking for two features.
//
// Modal for the reason the pull requests are: it is about everywhere else
// rather than about the workspace on screen, so a permanent panel in the
// accessory column would be the one panel with nothing to do with what is in
// the middle of the window. Opened on purpose, read, and gone.
//
// **The same size as the pull requests**, and the line length is handled a
// level down. It was narrower, on the argument that prose past about 40rem is
// a line the eye loses its place returning from — which is true of the line and
// not of the window. A narrow dialog enforces it by shrinking the headings and
// the routes as well, and leaves a page of short exchanges sitting in a column
// with the rest of the screen dimmed behind it. `Messages` caps the body at a
// character measure instead.
//
// **Mounted only while open**, which `useMessages` leans on: the hook asks the
// daemon on mount and subscribes, so nothing is fetched and no feed is held for
// a viewer nobody has open.

const styles = stylex.create({
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.4)" },

  popup: {
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translate(-50%, -50%) scale(0.985)" },
      to: { opacity: 1, transform: "translate(-50%, -50%) scale(1)" },
    }),
    animationDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: timing.spring,
    position: "fixed",
    top: "50%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    display: "flex",
    flexDirection: "column",
    // Larger than the pull requests rather than matched to them, and asked for
    // directly. The two lists are read differently: that one is scanned for a
    // row to pick, this one is read, and a page of prose in a box the size of a
    // form reads as a notification rather than as a record. The body keeps its
    // own character measure — see `Messages` — so the extra width goes to the
    // headings, the routes and the amount visible without scrolling.
    width: "min(73rem, calc(100vw - 4rem))",
    height: "min(57rem, calc(100vh - 4rem))",
    minHeight: 0,
    overflow: "hidden",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.5rem",
    color: colors.text,
    boxShadow: lift.high,
  },

  head: {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    flexShrink: 0,
    gap: "0.5rem",
    padding: `0.5rem 0.5rem 0.5rem ${space.gutter}`,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
  },
  title: { margin: 0, color: colors.text },
  close: {
    display: "flex",
    alignItems: "center",
    padding: "0.3rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    cursor: "pointer",
    ":hover": { color: colors.text, backgroundColor: colors.raised },
  },

  body: { flex: 1, minHeight: 0, display: "flex", flexDirection: "column" },
});

export function MessagesDialog({
  open,
  threads,
  onClose,
}: {
  readonly open: boolean;
  /** For the group headings: a message carries a thread id, not its title. */
  readonly threads: ReadonlyArray<Thread>;
  readonly onClose: () => void;
}) {
  // Before the early return — a hook cannot be called conditionally, and the
  // web panel is another process drawing over this one and cannot see a portal
  // outside its subtree. See `ReviewQueueDialog`.
  useOverlay(open);

  if (!open) {
    return null;
  }

  return (
    <Dialog.Root
      open
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop {...stylex.props(styles.backdrop)} />
        {/* `typeset.prose` here rather than inside: a Base UI dialog is
            portalled to `document.body`, outside the element the window's font
            is set on. Every portal in this window needs the line. */}
        <Dialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
          <div {...stylex.props(styles.head)}>
            <Dialog.Title {...stylex.props(typeset.heading, styles.title)}>inbox</Dialog.Title>
            <button type="button" title="close" onClick={onClose} {...stylex.props(styles.close)}>
              <XIcon size={16} aria-hidden />
            </button>
          </div>
          <div {...stylex.props(styles.body)}>
            <Messages threads={threads} />
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
