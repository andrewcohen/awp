import * as stylex from "@stylexjs/stylex";
import { useState } from "react";
import { said, startSession } from "./daemon";
import { colors, text } from "./tokens.stylex";

// What the agent column shows for a workspace with nothing running in it.
//
// ── the state that used to be invisible ─────────────────────────────────────
//
// This face did not exist, and what stood in for it was the *fixture* — the
// renderer's own test pattern, drawn because `Pane` had no session name to
// attach to. So a workspace whose agent had exited showed colour ramps and box
// drawing, which reads as a bug in the terminal rather than as an answer.
//
// Before that it could not be reached at all: the address resolved through a
// session, so a workspace with none resolved to nothing. See `placeAt`.
//
// ── and it is the one place with an act on it ───────────────────────────────
//
// Everything else about a dead workspace is a question — the chat, the diff,
// the pull request — and every one of them works without a terminal. The
// terminal is the only thing that is actually gone, so this is the only place
// that has anything to offer, and what it offers is `zmx run -d` through
// `SessionStart`. Idempotent in the daemon, so a second press is one session.

const styles = stylex.create({
  middle: {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: "0.6rem",
    width: "100%",
    height: "100%",
    minHeight: 0,
    padding: "1rem",
  },
  what: { color: colors.text, fontSize: text.body, textAlign: "center" },
  // The daemon's own sentence, never one composed here. zmx names what it
  // refused and why, and a message written in this file would say less.
  why: {
    maxWidth: "32rem",
    color: colors.warn,
    fontSize: text.small,
    textAlign: "center",
    userSelect: "text",
  },
  start: {
    padding: "0.25rem 0.7rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.text,
    font: "inherit",
    fontSize: text.small,
    cursor: "pointer",
  },
  busy: { color: colors.muted, cursor: "default" },
});

export function NoSession({
  project,
  workspace,
  onStarted,
}: {
  readonly project: string;
  readonly workspace: string;
  /** Re-read the sessions, so the pane can attach to the one just made. */
  readonly onStarted: () => void;
}) {
  const [starting, setStarting] = useState(false);
  const [refused, setRefused] = useState<string | undefined>();

  return (
    <div {...stylex.props(styles.middle)}>
      {/* The workspace is deliberately not named here. The header above this
          column says `<project>/<workspace>` — see `Title` — and the same
          pair twice in one column is duplication rather than information. */}
      <div {...stylex.props(styles.what)}>no terminal is running here</div>
      <button
        type="button"
        disabled={starting}
        // Reachable by ctrl+j/k, like every other control in this column. A
        // face whose only act needed a pointer would be the keyboard mandate
        // broken in the one place there is nothing else to press.
        data-nav-item
        {...stylex.props(styles.start, starting && styles.busy)}
        onClick={() => {
          setStarting(true);
          setRefused(undefined);
          startSession(project, workspace)
            .then(() => {
              // Left `starting` until the sessions come back, deliberately.
              // The button returning to "start the agent" a moment before the
              // pane appears reads as a press that did nothing.
              onStarted();
            })
            .catch((error: unknown) => {
              setStarting(false);
              setRefused(said(error));
            });
        }}
      >
        {/* No spinner. The word already says it is running — the jobs panel's
            rule, and it holds anywhere a person is watching one thing. */}
        {starting ? "starting…" : "start the agent"}
      </button>
      {refused !== undefined && <div {...stylex.props(styles.why)}>{refused}</div>}
    </div>
  );
}
