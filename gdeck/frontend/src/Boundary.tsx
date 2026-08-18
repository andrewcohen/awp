import { Component, type ErrorInfo, type ReactNode } from "react";
import { macchiato } from "./palette";
import * as Probe from "@bindings/probe";

// One pane throwing should cost you the pane, not the window.
//
// Without this, an exception anywhere below unmounts the entire React tree and
// the app goes black — no sidebar, no error, nothing to click, and no clue what
// happened unless someone thinks to read the dev server's log. That is how a
// missing binding method presented: as "the whole UI blacked out".
//
// It is worth having beyond that one bug, because the faults this surface is
// most likely to hit are exactly the ones that arrive as exceptions from a
// dependency: a Go method that has not been regenerated yet, a wasm terminal
// that was disposed and rebuilt, a binding call that fails while the backend is
// restarting. All of those are recoverable if the frame around them survives.
type Props = { children: ReactNode };
type State = { error: Error | null };

export class Boundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Into the log as well as onto the screen, so a crash that happened while
    // nobody was looking is still on the record afterwards.
    void Probe.Report("ui-crash", false, `${error.message}\n${info.componentStack ?? ""}`);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (!error) {
      return this.props.children;
    }
    return (
      <div style={{ padding: "1rem" }}>
        <p style={{ color: macchiato.red, margin: "0 0 0.5rem" }}>this view crashed</p>
        <pre style={{ color: macchiato.text, whiteSpace: "pre-wrap", margin: "0 0 0.75rem" }}>
          {error.message}
        </pre>
        <button
          onClick={() => this.setState({ error: null })}
          style={{
            font: "inherit",
            color: macchiato.text,
            background: macchiato.black,
            border: 0,
            padding: "0.3rem 0.8rem",
            cursor: "pointer",
          }}
        >
          try again
        </button>
      </div>
    );
  }
}
