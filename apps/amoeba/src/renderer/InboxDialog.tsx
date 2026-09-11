import type { Job } from "@awp-kit/jobs";
import { Dialog } from "@base-ui/react/dialog";
import { XIcon } from "@phosphor-icons/react/X";
import * as stylex from "@stylexjs/stylex";
import { Inbox } from "./Inbox";
import { useOverlay } from "./overlays";
import { typeset } from "./typeset";
import { colors, lift, space, timing } from "./tokens.stylex";

// The inbox, in a window of its own.
//
// ── why it is not a panel any more ─────────────────────────────────────────
//
// It was the second tab of the left column, which is 260px wide — and a pull
// request row carries a number, a title, a project, an author, a branch, a
// stack guide and up to three chips. All of that in a sidebar means the title,
// the one field that cannot be reconstructed from the others, is what
// truncates. The list is also the widest thing the daemon answers with: forty
// rows on this machine, sectioned, where the strip beside it holds a dozen.
//
// The accessory column was the other candidate and is the wrong one, for the
// reason `LeftColumn` used to state: that column is about the thing already on
// screen — this workspace's diff, a page beside it — and the inbox is about
// everywhere else. A permanent tab there would be the one panel with nothing
// to do with the session in the middle.
//
// So it is modal: opened on purpose, read at full width, and gone. Which is
// also what it is *for* — the rows are a list to pick from, and the pick lands
// in the two columns behind it.
//
// **Mounted only while open**, which `useInbox` already leans on: the hook asks
// the daemon on mount, so nothing fetches for an inbox nobody is looking at.
// The rows themselves live in atoms, so a second open shows the last answer at
// once and backfills it.

const styles = stylex.create({
  // Dimmed rather than blurred, the same choice `NewThread` makes: the window
  // behind is a terminal, and blurring legible text makes it look broken.
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.4)" },

  popup: {
    // Nothing pops — the window's mandate. Scale from just under, so it reads
    // as coming forward; the keyframe carries the centring translate because
    // the two transforms compose.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translate(-50%, -50%) scale(0.985)" },
      to: { opacity: 1, transform: "translate(-50%, -50%) scale(1)" },
    }),
    animationDuration: { default: timing.enter, "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: timing.spring,
    position: "fixed",
    // Centred, unlike the new-thread box. That one is a form a few lines tall,
    // which reads as low when it sits on the middle; this is a page of rows
    // and wants the window's whole height around it.
    top: "50%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    display: "flex",
    flexDirection: "column",
    // Large deliberately: the row is two lines of text plus chips, and the
    // point of moving it out of the sidebar was to stop the title truncating.
    // Not the whole window, though — a row's action sits at its right edge, so
    // every rem past what the titles need is distance between the thing read
    // and the thing pressed. 56rem holds the longest title on this machine.
    // Bounded on both axes so the list scrolls inside rather than the dialog
    // growing past the window.
    width: "min(56rem, calc(100vw - 6rem))",
    height: "min(44rem, calc(100vh - 6rem))",
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
  // A real heading rather than the hidden one `NewThread` carries. That box is
  // named by its own placeholder and its chips; a page of rows opened from a
  // menu item says which page it is.
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

export function InboxDialog({
  open,
  jobs,
  onClose,
  onOpen,
  onStarted,
}: {
  readonly open: boolean;
  /** Every job the window knows about, for a row's own progress. */
  readonly jobs: ReadonlyArray<Job>;
  readonly onClose: () => void;
  /** Go to a workspace's agent. The window owns the address — see App.tsx. */
  readonly onOpen: (project: string, workspace: string) => void;
  /** A review was started, so the threads and jobs App holds are out of date. */
  readonly onStarted: () => void;
}) {
  // Before the early return: a hook cannot be called conditionally, and the
  // condition is the argument. The web panel is another process drawing over
  // this one and cannot see a portal outside its subtree, so the component
  // that opened the modal is the one that has to say so.
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
        {/* `typeset.prose` here and not inside: a Base UI dialog is portalled
            to `document.body`, which is outside `#root` — where the window's
            font is set — so everything in it falls back to the engine's serif
            with every size still correct. Every portal in this window needs
            the line. */}
        <Dialog.Popup {...stylex.props(typeset.prose, styles.popup)}>
          <div {...stylex.props(styles.head)}>
            <Dialog.Title {...stylex.props(typeset.heading, styles.title)}>inbox</Dialog.Title>
            <button type="button" title="close" onClick={onClose} {...stylex.props(styles.close)}>
              <XIcon size={16} aria-hidden />
            </button>
          </div>
          <div {...stylex.props(styles.body)}>
            {/* Opening a row is the point of the list, so it closes on the way:
                the address it navigates to is drawn by the two columns this
                dialog is covering. Starting a review is not — that leaves a job
                running and a row that now says so. */}
            <Inbox
              jobs={jobs}
              onOpen={(project, workspace) => {
                onClose();
                onOpen(project, workspace);
              }}
              onStarted={onStarted}
            />
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
