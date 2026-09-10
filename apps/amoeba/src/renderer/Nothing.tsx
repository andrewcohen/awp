// An empty panel, saying so like it meant to.
//
// ── an empty column is the state a panel is in most of the day ──────────
//
// Every panel's empty state was one line of muted text at the top left of
// several hundred pixels of nothing — `no workspace open`, `no open pull
// requests` — which reads as a panel that failed to load rather than as a
// panel with nothing to show. The accessory column is empty whenever
// nothing is selected, so this is not an edge case; it is the first thing
// somebody sees.
//
// Centred, with the mark that panel already uses beside its rows, and it
// springs in like everything else that appears. Nothing here is an offer:
// where there IS something to do about the emptiness the panel says so
// itself — the chat's `continue the terminal's conversation` is the worked
// example, and it stays in the chat.

import * as stylex from "@stylexjs/stylex";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useArriving } from "./springs";
import { colors, text } from "./tokens.stylex";
import { typeset } from "./typeset";

const styles = stylex.create({
  wrap: {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: "0.5rem",
    // Not the whole column: a message pinned to the exact middle of a tall
    // panel is a message somebody has to look for. A little above centre is
    // where the eye already is.
    minHeight: "min(18rem, 60%)",
    padding: "2rem 1rem",
    textAlign: "center",
    color: colors.muted,
  },
  /** The mark, at the size of a thing that is not asking to be pressed. */
  mark: { opacity: 0.45, fontSize: 22, lineHeight: 1 },
  say: { margin: 0, color: colors.muted },
  /** One more line, when there is something worth knowing about the gap. */
  hint: { margin: 0, maxWidth: "22rem", fontSize: text.small, opacity: 0.75 },
});

export const Nothing = ({
  mark,
  say,
  hint,
}: {
  /** The panel's own icon, muted — a picture of what is missing. */
  readonly mark?: ReactNode;
  readonly say: string;
  /** Why it is empty, when that is not obvious from the panel. */
  readonly hint?: string;
}) => {
  const arriving = useArriving();
  return (
    <motion.div {...arriving} {...stylex.props(styles.wrap)}>
      {mark !== undefined && (
        <span {...stylex.props(styles.mark)} aria-hidden="true">
          {mark}
        </span>
      )}
      <p {...stylex.props(typeset.control, styles.say)}>{say}</p>
      {hint !== undefined && <p {...stylex.props(typeset.label, styles.hint)}>{hint}</p>}
    </motion.div>
  );
};
