import type { Chrome } from "@awp-kit/pane";
import type { ReactNode } from "react";
import { Meter } from "./Meter";

// The debug tools, and where they live.
//
// A collection rather than one panel, because the meter will not be the last of
// these: the questions that needed answering today were about wheel arithmetic
// and frame times, and tomorrow's will be about attach timings, protocol
// traffic, or whatever the next "it feels wrong" turns out to mean.
//
// They live in the accessory column because that column is for whatever is
// beside the work — a diff, a webview, a shell — and an instrument is exactly
// that. Nothing here is behind a flag: a debug tool nobody can find is a debug
// tool nobody uses, and the cost of this one is a timer at 4Hz.

export interface DebugTool {
  readonly id: string;
  /** Short enough for a tab. */
  readonly label: string;
  readonly render: (chrome: Chrome) => ReactNode;
}

export const debugTools: ReadonlyArray<DebugTool> = [
  {
    id: "meter",
    label: "meter",
    render: (chrome) => <Meter chrome={chrome} />,
  },
];
