import type { Chrome } from "@awp-kit/pane";
import { useRef, useState } from "react";

// The grab line between two columns.
//
// One pixel of layout and nine pixels of target. The visible rule is what the
// eye wants and a nine-pixel hit area is what the hand wants, so the hit area
// is an absolutely positioned child overhanging both sides rather than a wider
// element — a divider that takes real width would push the columns around every
// time the affordance changed.

const GRAB = 9;

type Props = {
  readonly value: number;
  readonly onChange: (next: number) => void;
  /** Dragging right makes the column narrower — true for a right-hand column. */
  readonly invert?: boolean;
  readonly chrome: Chrome;
  readonly label: string;
};

export function Divider({ value, onChange, invert = false, chrome, label }: Props) {
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
      style={{
        position: "relative",
        flex: "0 0 1px",
        background: active ? chrome.muted : chrome.border,
        cursor: "col-resize",
        // Pointer events are captured on this element, so a gesture that starts
        // here must not also be read as a scroll or a swipe.
        touchAction: "none",
      }}
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
      <div style={{ position: "absolute", inset: 0, left: -GRAB / 2, right: -GRAB / 2 }} />
    </div>
  );
}
