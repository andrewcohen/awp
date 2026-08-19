import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button } from "@/components/ui/button";

// One pane throwing should cost you the pane, not the window.
//
// Without this, an exception anywhere below unmounts the entire React tree and
// the app goes black — no sidebar, no error, nothing to click, and no clue what
// happened unless someone thinks to read the dev server's log. That is how a
// missing binding method presented in gdeck: as "the whole UI blacked out".
//
// It is worth having beyond that one bug, because the faults this surface is
// most likely to hit arrive as exceptions from a dependency: a malformed event
// off the stream, a tool payload in a shape the renderer did not expect, a fetch
// that fails while the backend is restarting. All of those are recoverable if
// the frame around them survives.
type Props = { children: ReactNode };
type State = { error: Error | null };

export class Boundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Into the console as well as onto the screen, so a crash that happened
    // while nobody was looking is still on the record afterwards.
    console.error("ui crash", error, info.componentStack);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div className="p-4">
        <p className="mb-2 text-destructive">this view crashed</p>
        <pre className="mb-3 whitespace-pre-wrap text-sm text-muted-foreground">{error.message}</pre>
        <Button variant="secondary" size="sm" onClick={() => this.setState({ error: null })}>
          try again
        </Button>
      </div>
    );
  }
}
