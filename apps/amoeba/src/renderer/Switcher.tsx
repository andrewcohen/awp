import { Dialog } from "@base-ui/react/dialog";
import type { Thread } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { useOverlay } from "./overlays";
import { type Row, filtered, ordered } from "./switching";
import { typeset } from "./typeset";
import { colors } from "./tokens.stylex";

// cmd+P: go to a thread.
//
// ── the first row is the previous thread, so Return flips back ─────────────
//
// Asked for that way — "cmd p, enter flips you back to the last thread" — and
// it is what makes this a *switcher* rather than a list. The ordering lives in
// `switcher.ts` because it is the part worth testing; this file is the box, the
// keys and the rows.
//
// ── a dialog, not a column ────────────────────────────────────────────────
//
// Base UI's, for what it ships rather than for how it looks: the focus trap,
// the restore on close, Escape, and making the rest of the window inert. The
// same three reasons the new-thread composer is one.
//
// ── the keys are the window's, and there are two sets ─────────────────────
//
//   up / down       what a person will press in a list, and what the arrows
//                   are for while a caret is in a text field
//   ctrl+j / ctrl+k the window's own movement chord, given up inside inputs
//                   everywhere else — see navigation.ts. Here the input *is*
//                   the whole dialog, so giving them up would leave this the
//                   one list in the app those keys do not move.
//
// Return opens the highlighted row, and typing narrows without rescoring: the
// row under the cursor must not move while somebody is typing towards it.

const styles = stylex.create({
  backdrop: { position: "fixed", inset: 0, backgroundColor: "rgba(0, 0, 0, 0.4)" },
  popup: {
    position: "fixed",
    // Higher than the other dialogs. A palette is read from the top down and a
    // long list has to have somewhere to go.
    top: "22%",
    left: "50%",
    transform: "translate(-50%, -50%)",
    display: "flex",
    flexDirection: "column",
    width: "min(38rem, calc(100vw - 6rem))",
    maxHeight: "min(30rem, calc(100vh - 8rem))",
    backgroundColor: colors.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.4rem",
    color: colors.text,
    boxShadow: "0 1rem 3rem rgba(0, 0, 0, 0.35)",
  },
  // The field is the dialog's own edge, so there is no box inside a box — the
  // same decision the new-thread composer's brief made.
  field: {
    width: "100%",
    minWidth: 0,
    padding: "0.7rem 0.9rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: colors.border,
    outlineStyle: "none",
    color: colors.text,
    font: "inherit",
  },
  list: { minHeight: 0, overflowY: "auto", overflowX: "hidden", padding: "0.3rem" },
  row: {
    display: "flex",
    alignItems: "baseline",
    gap: "0.6rem",
    width: "100%",
    textAlign: "start",
    padding: "0.35rem 0.6rem",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.text,
    font: "inherit",
    cursor: "pointer",
  },
  on: { backgroundColor: colors.border },
  // `flex: 1` with `minWidth: 0`. The pair — a long title must clip rather
  // than widen the dialog.
  title: {
    flex: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  where: { flexShrink: 0, color: colors.muted },
  /** `back` on the row Return would take. Said once, where it is true. */
  back: { flexShrink: 0, color: colors.muted },
  empty: { padding: "0.6rem 0.9rem", color: colors.muted },
});

export const Switcher = ({
  threads,
  visits,
  onPick,
  onClose,
}: {
  readonly threads: ReadonlyArray<Thread>;
  /** Thread ids, newest first. `visits[0]` is where the window is now. */
  readonly visits: ReadonlyArray<string>;
  readonly onPick: (row: Row) => void;
  readonly onClose: () => void;
}) => {
  // Announced rather than detected: the web panel is a native view drawn over
  // everything this window renders and cannot see a portal outside its own
  // subtree.
  useOverlay(true);

  const [typed, setTyped] = useState("");
  const [picked, setPicked] = useState(0);
  const rows = filtered(ordered(threads, visits), typed);
  const at = Math.min(picked, Math.max(0, rows.length - 1));

  const take = (row: Row | undefined): void => {
    if (row === undefined) {
      return;
    }
    onPick(row);
    onClose();
  };

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
        <Dialog.Popup
          // Never. The window puts the caret back in the agent column when
          // this closes, by either route — measured at `#/`, the default
          // restore left focus on nothing at all, and after a pick it would
          // hand the keyboard back to the thread just left.
          finalFocus={false}
          {...stylex.props(typeset.prose, styles.popup)}
        >
          {/* The accessible name. On screen the placeholder says what this is,
              and a heading would be a row repeating it — but a dialog with no
              name is one a screen reader announces as nothing at all. */}
          <Dialog.Title {...stylex.props(styles.back)} render={<span hidden />}>
            go to a thread
          </Dialog.Title>
          <input
            // Focus on mount, which is the moment the palette opened. A
            // callback ref rather than `autoFocus`, which react-doctor flags
            // because the attribute fires on any render it mounts in.
            ref={(node) => node?.focus()}
            value={typed}
            placeholder="go to a thread"
            aria-label="go to a thread"
            {...stylex.props(typeset.prose, styles.field)}
            onChange={(event) => {
              setTyped(event.target.value);
              // The list is re-filtered on every keystroke, so an index into
              // the old one names the wrong row. Reset rather than clamped:
              // the first match is what narrowing a list means.
              setPicked(0);
            }}
            onKeyDown={(event) => {
              const down =
                event.key === "ArrowDown" || (event.ctrlKey && event.key.toLowerCase() === "j");
              const up =
                event.key === "ArrowUp" || (event.ctrlKey && event.key.toLowerCase() === "k");
              if ((down || up) && rows.length > 0) {
                event.preventDefault();
                const step = down ? 1 : -1;
                setPicked((was) => (was + step + rows.length) % rows.length);
                return;
              }
              if (event.key === "Enter") {
                event.preventDefault();
                take(rows[at]);
              }
            }}
          />
          <div {...stylex.props(styles.list)}>
            {rows.length === 0 ? (
              <p {...stylex.props(typeset.label, styles.empty)}>
                {threads.length === 0 ? "no threads yet" : "nothing matches"}
              </p>
            ) : (
              rows.map((row, index) => (
                <button
                  key={row.id}
                  type="button"
                  aria-selected={index === at}
                  // A press must not move focus off the field, or the keys stop
                  // working the moment somebody uses the pointer once.
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => take(row)}
                  {...stylex.props(styles.row, index === at && styles.on)}
                >
                  <span {...stylex.props(styles.title)}>{row.title}</span>
                  <span {...stylex.props(typeset.label, styles.where)}>{row.where}</span>
                  {/* Only on the row Return would take, and only when nothing
                      has been typed — once a query has narrowed the list the
                      top row is a match rather than the way back. */}
                  {index === 0 && typed === "" && row.visited && (
                    <span {...stylex.props(typeset.label, styles.back)}>back</span>
                  )}
                </button>
              ))
            )}
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
};
