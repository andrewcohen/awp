import * as stylex from "@stylexjs/stylex";
import { Component, type ErrorInfo, type ReactNode, useState } from "react";
import { colors, text } from "./tokens.stylex";

// What is on screen when a piece of the window throws.
//
// ── one boundary per column, not one per window ────────────────────────────
//
// A single boundary at the root is the same thing as no boundary: the whole
// application is replaced by a message, and whatever was being looked at is
// gone along with it. What made this worth building was a diff panel throwing
// on a bad option — the sidebar was fine, the terminal was fine, and all three
// went white.
//
// So the granularity is the part a person can carry on without. Each column
// wraps its own, and the pane wraps separately from the panels beside it:
//
//   sidebar   fails → the other two columns still work, and the address bar
//                     still names where you are
//   agent     fails → the terminal is the point of the window, but the diff
//                     and the jobs list can still be read
//   accessory fails → this is the common one. A panel is the newest code.
//
// ── the message is selectable, and that is the whole feature ───────────────
//
// A stack trace that cannot be copied is a stack trace that gets retyped from a
// photograph, or described in prose. `user-select: text` and a button that puts
// the message and the component stack on the clipboard, because the second is
// what says *which* component threw and it is never in the visible message.
//
// The console has all of this already — but the console is a different window
// in a different application, and in a build there is no obvious way to open
// one. The error is on screen; the copy of it should be too.

const styles = stylex.create({
  // Centred in whatever it was given, which is a column and not the window.
  // `height: 100%` rather than a viewport unit for the reason global.css gives.
  middle: {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: "0.6rem",
    height: "100%",
    minHeight: 0,
    padding: "1rem",
    overflowY: "auto",
  },
  what: { color: colors.warn, fontSize: text.body, textAlign: "center" },
  where: { color: colors.muted, fontSize: text.tiny },
  // The message itself. Selectable, wrapped, and scrolled inside its own box —
  // a stack trace is the widest text this window will ever hold, and letting it
  // set the column's width is how a horizontal scrollbar gets born.
  detail: {
    maxWidth: "100%",
    maxHeight: "40%",
    margin: 0,
    padding: "0.5rem",
    backgroundColor: colors.base,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.25rem",
    color: colors.text,
    fontSize: text.tiny,
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
    overflowY: "auto",
    userSelect: "text",
    cursor: "text",
  },
  row: { display: "flex", gap: "0.4rem" },
  button: {
    padding: "0.15rem 0.5rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    fontSize: text.tiny,
    cursor: "pointer",
  },
});

/** Everything worth copying, as one block of text. */
const report = (where: string, error: Error, stack: string | undefined): string =>
  [`awp — ${where}`, `${error.name}: ${error.message}`, error.stack ?? "", stack ?? ""]
    .filter((part) => part !== "")
    .join("\n\n");

function Fallen({
  where,
  error,
  stack,
  retry,
}: {
  readonly where: string;
  readonly error: Error;
  readonly stack: string | undefined;
  readonly retry: () => void;
}) {
  const [copied, setCopied] = useState(false);

  return (
    <div {...stylex.props(styles.middle)} role="alert">
      <div {...stylex.props(styles.what)}>{where} stopped working</div>
      <pre {...stylex.props(styles.detail)}>{report(where, error, stack)}</pre>
      <div {...stylex.props(styles.row)}>
        <button
          type="button"
          {...stylex.props(styles.button)}
          onClick={() => {
            // The clipboard can be refused — a webview loaded over a custom
            // protocol is not always a secure context — and a button that
            // silently does nothing is worse than one that says so. The text is
            // selectable either way, which is the fallback.
            navigator.clipboard
              ?.writeText(report(where, error, stack))
              .then(() => setCopied(true))
              .catch(() => setCopied(false));
          }}
        >
          {copied ? "copied" : "copy"}
        </button>
        <button type="button" {...stylex.props(styles.button)} onClick={retry}>
          try again
        </button>
      </div>
      <div {...stylex.props(styles.where)}>
        the rest of the window is still running — only this part stopped
      </div>
    </div>
  );
}

/**
 * Catch what a subtree throws, and keep the rest of the window.
 *
 * A class, and it has to be: `componentDidCatch` and
 * `getDerivedStateFromError` have no hook equivalent, which is the one place
 * React still requires one. Everything it renders is a function component.
 *
 * `retry` clears the error and re-renders the children. It fixes nothing on its
 * own — if the cause is still there it throws again immediately — but the
 * common case here is a panel that failed on data that has since changed, and
 * for that it is the difference between a button and a reload.
 */
export class Boundary extends Component<
  { readonly where: string; readonly children: ReactNode },
  { readonly error: Error | undefined; readonly stack: string | undefined }
> {
  override state: { error: Error | undefined; stack: string | undefined } = {
    error: undefined,
    stack: undefined,
  };

  static getDerivedStateFromError(error: Error) {
    return { error, stack: undefined };
  }

  override componentDidCatch(error: Error, info: ErrorInfo) {
    // The component stack arrives here and nowhere else — it is not on the
    // Error — so it is kept rather than looked up later. It is also the single
    // most useful line in the report, because it names the component that threw
    // rather than the frame the throw happened in.
    this.setState({ error, stack: info.componentStack ?? undefined });
  }

  override render() {
    const { error, stack } = this.state;
    if (error === undefined) {
      return this.props.children;
    }
    return (
      <Fallen
        where={this.props.where}
        error={error}
        stack={stack}
        retry={() => this.setState({ error: undefined, stack: undefined })}
      />
    );
  }
}
