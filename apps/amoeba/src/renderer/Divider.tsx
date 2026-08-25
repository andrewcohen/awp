import * as stylex from "@stylexjs/stylex";
import { useRef, useState } from "react";
import { colors } from "./tokens.stylex";

// The grab line between two columns.
//
// One pixel of layout and nine pixels of target. The visible rule is what the
// eye wants and a nine-pixel hit area is what the hand wants, so the hit area
// is an absolutely positioned child overhanging both sides rather than a wider
// element — a divider that takes real width would push the columns around every
// time the affordance changed.

const GRAB = 9;

const styles = stylex.create({
  rule: {
    position: "relative",
    flex: "0 0 1px",
    backgroundColor: colors.border,
    cursor: "col-resize",
    // Pointer events are captured on this element, so a gesture that starts
    // here must not also be read as a scroll or a swipe.
    touchAction: "none",
  },
  held: { backgroundColor: colors.muted },
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
};

export function Divider({ value, onChange, invert = false, label }: Props) {
  // Where the pointer went down, and how wide the column was then. Read from
  // the origin rather than accumulated per move: summing deltas drifts once the
  // value clamps, so the column stops tracking the cursor and never catches up.
  const drag = useRef<{ readonly x: number; readonly from: number } | null>(null);
  const [active, setActive] = useState(false);

  const sign = invert ? -1 : 1;

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={Math.round(value)}
      tabIndex={0}
      {...stylex.props(styles.rule, active && styles.held)}
      onKeyDown={(event) => {
        // role="separator" with tabIndex and no keyboard handling is a lie told
        // to assistive technology. Arrows nudge, shift coarsens.
        const step = event.shiftKey ? 48 : 8;
        if (event.key === "ArrowLeft") {
          onChange(value - step * sign);
        } else if (event.key === "ArrowRight") {
          onChange(value + step * sign);
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
    </div>
  );
}
