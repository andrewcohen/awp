import { CaretLeftIcon } from "@phosphor-icons/react/CaretLeft";
import { CaretRightIcon } from "@phosphor-icons/react/CaretRight";
import * as stylex from "@stylexjs/stylex";
import { useRef, useState } from "react";
import { DIVIDER } from "./columns";
import { colors, space } from "./tokens.stylex";

// The grab line between two columns, and the way its column folds away.
//
// One pixel of layout and nine pixels of target. The visible rule is what the
// eye wants and a nine-pixel hit area is what the hand wants, so the hit area
// is an absolutely positioned child overhanging both sides rather than a wider
// element — a divider that takes real width would push the columns around every
// time the affordance changed.
//
// The collapse control lives here rather than in the column it collapses, and
// that is the whole reason this component owns it: a button inside the sidebar
// is a button that disappears along with the sidebar, leaving no way back. The
// divider is the one part of a folded column that is still on screen.
//
//   expanded    │  ‹  appears on hover — the control is not the point of the UI
//   collapsed   ▐ ›  always visible — it is now the only way back

const GRAB = 9;

const styles = stylex.create({
  // The width arrives as a runtime argument rather than being read from the
  // import at compile time.
  //
  // StyleX resolves every identifier used inside a `create` value itself, and
  // insists it come from a `.stylex.ts` file. Interpolating DIVIDER into a
  // static style is therefore not a shorter version of this — it is a build
  // error naming a theming rule that has nothing to do with the case. A
  // dynamic style takes its value at runtime and asks no such question.
  //
  // DIVIDER stays in columns.ts because it is a term in the layout arithmetic
  // before it is a measurement in a stylesheet, and the two must be the same
  // number or the widths the layout computes are not the widths on screen.
  rule: (width: number) => ({
    position: "relative",
    flex: `0 0 ${width}px`,
    backgroundColor: colors.border,
    cursor: "col-resize",
    // Pointer events are captured on this element, so a gesture that starts
    // here must not also be read as a scroll or a swipe.
    touchAction: "none",
  }),
  held: { backgroundColor: colors.muted },
  handle: {
    position: "absolute",
    insetBlockStart: space.titlebar,
    insetInlineStart: "50%",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "0.85rem",
    height: "1.6rem",
    padding: 0,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.25rem",
    backgroundColor: colors.base,
    color: colors.muted,
    // A pointer over the handle should say what the handle does, not what the
    // rule underneath it does.
    cursor: "pointer",
    opacity: 0,
    // Near the titlebar rather than centred. Centred puts a control in the
    // middle of the terminal's edge, where the eye is actually working.
    transitionProperty: "opacity",
    transitionDuration: "120ms",
  },
  shown: { opacity: 1 },
  // Centred on a one-pixel rule means half the handle hangs over each column,
  // which is right in the middle of the window and wrong at its edge: a folded
  // sidebar puts its divider at x=0, and half a control is off-screen. So a
  // folded column's handle steps aside, into the agent column, which is the
  // only neighbour it still has.
  placed: (nudge: number) => ({ transform: `translateX(calc(-50% + ${nudge}px))` }),
  grab: {
    position: "absolute",
    insetBlock: 0,
    insetInlineStart: -GRAB / 2,
    insetInlineEnd: -GRAB / 2,
  },
});

type Props = {
  readonly value: number;
  readonly onChange: (next: number) => void;
  /** Dragging right makes the column narrower — true for a right-hand column. */
  readonly invert?: boolean;
  readonly label: string;
  readonly collapsed: boolean;
  readonly onToggle: () => void;
};

export function Divider({ value, onChange, invert = false, label, collapsed, onToggle }: Props) {
  // Where the pointer went down, and how wide the column was then. Read from
  // the origin rather than accumulated per move: summing deltas drifts once the
  // value clamps, so the column stops tracking the cursor and never catches up.
  const drag = useRef<{ readonly x: number; readonly from: number } | null>(null);
  const [active, setActive] = useState(false);
  const [hovered, setHovered] = useState(false);

  const sign = invert ? -1 : 1;

  // Which way the caret points is which way the column will go. For the
  // accessory the whole gesture is mirrored, so the arrow is too.
  const folding = collapsed !== invert;
  const Caret = folding ? CaretRightIcon : CaretLeftIcon;
  const nudge = collapsed ? (invert ? -9 : 9) : 0;

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={Math.round(value)}
      tabIndex={0}
      {...stylex.props(styles.rule(DIVIDER), active && styles.held)}
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      onKeyDown={(event) => {
        // role="separator" with tabIndex and no keyboard handling is a lie told
        // to assistive technology. Arrows nudge, shift coarsens, Enter folds.
        if (event.key === "Enter" || event.key === " ") {
          onToggle();
        } else if (event.key === "ArrowLeft") {
          onChange(value - (event.shiftKey ? 48 : 8) * sign);
        } else if (event.key === "ArrowRight") {
          onChange(value + (event.shiftKey ? 48 : 8) * sign);
        } else {
          return;
        }
        event.preventDefault();
      }}
      onPointerDown={(event) => {
        // Capture, so the drag survives the cursor crossing the terminal canvas.
        // Without it the pane swallows the move events and the column sticks
        // wherever the pointer left the nine pixels.
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = { x: event.clientX, from: value };
        setActive(true);
      }}
      onPointerMove={(event) => {
        const start = drag.current;
        if (start === null) {
          return;
        }
        onChange(start.from + (event.clientX - start.x) * sign);
      }}
      onPointerUp={(event) => {
        event.currentTarget.releasePointerCapture(event.pointerId);
        drag.current = null;
        setActive(false);
      }}
      onPointerCancel={() => {
        drag.current = null;
        setActive(false);
      }}
    >
      <div {...stylex.props(styles.grab)} />
      <button
        type="button"
        aria-label={`${collapsed ? "show" : "hide"} ${label.replace(" width", "")}`}
        onClick={onToggle}
        // The handle sits inside the separator's hit area, so without this a
        // press on it also starts a drag and the column follows the cursor
        // while it is being folded.
        onPointerDown={(event) => event.stopPropagation()}
        {...stylex.props(
          styles.handle,
          styles.placed(nudge),
          (collapsed || hovered) && styles.shown,
        )}
      >
        <Caret size={11} weight="bold" aria-hidden />
      </button>
    </div>
  );
}
