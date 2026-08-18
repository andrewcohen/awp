import { useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { paneTheme } from "./palette";
import { mountPaneTerminal, resetPane, setPaneSinks, clearPaneSinks } from "./paneTerminal";
import * as Panes from "@bindings/panes";
import * as Probe from "@bindings/probe";

// A real agent, attached over a pty, rendered by libghostty in the webview.
//
// This is the step that can kill the idea. Everything before it says the
// emulator is correct; none of it says a pane is usable, and the difference is
// latency — a terminal that is right and late is not a terminal anyone will
// work in. So the pane measures itself and reports numbers rather than a
// verdict, because "feels fine" is not a claim that survives being wrong.
//
// The Terminal is borrowed, not built — see paneTerminal for what building one
// per view did to the wasm state they share.

// A pty is bytes, and JSON is text, so both directions go base64. The pane's
// output cannot be decoded as UTF-8 on the way through: a 64KB read can split a
// multi-byte sequence across two events, and the emulator is the thing that
// knows how to hold half of one.
function toBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function fromBytes(data: string): string {
  const bytes = new TextEncoder().encode(data);
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

// echoTimer measures the round trip a person actually feels: key pressed here,
// bytes back from the program that echoed it.
//
// It is a queue rather than a single timestamp because typing outruns the round
// trip — hold a key down and several are in flight at once, and matching the
// newest keystroke to the next arriving byte would report the latency of the
// wrong one. Chunks that arrive with nothing outstanding are the program talking
// on its own and are not samples.
class echoTimer {
  private pending: number[] = [];
  readonly samples: number[] = [];

  sent(): void {
    this.pending.push(performance.now());
  }

  received(): void {
    const start = this.pending.shift();
    if (start !== undefined) {
      this.samples.push(performance.now() - start);
    }
  }

  // Reported as a spread rather than a mean: a pane whose median is 8ms and
  // whose worst case is 300ms is a pane that stutters, and the mean hides
  // exactly that.
  summary(): string {
    if (this.samples.length === 0) {
      return "no keystrokes measured";
    }
    const sorted = [...this.samples].sort((a, b) => a - b);
    const at = (q: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))];
    const ms = (v: number) => `${v.toFixed(1)}ms`;
    return `n=${sorted.length} p50=${ms(at(0.5))} p90=${ms(at(0.9))} max=${ms(sorted[sorted.length - 1])}`;
  }
}

export function LivePane({ session, fontFamily }: { session: string; fontFamily: string }) {
  const container = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState<string>("");
  const timer = useRef(new echoTimer());

  useEffect(() => {
    const parent = container.current;
    if (!parent) {
      return;
    }

    const { term } = mountPaneTerminal(parent, fontFamily);
    resetPane();
    timer.current = new echoTimer();

    // Clicking a workspace is asking to work in it, so the keyboard should
    // already be there. Without this the pane renders, looks attached, and
    // swallows the first thing typed at it — the one interaction where "it
    // looks ready but isn't" costs you a keystroke you meant for the agent.
    term.focus();

    setPaneSinks(
      (data) => {
        timer.current.sent();
        void Panes.Send(fromBytes(data));
      },
      (cols, rows) => void Panes.Resize(cols, rows),
    );

    // The wheel, on a full-screen program, belongs to the program.
    //
    // An alternate-screen program has no terminal scrollback — measured here as
    // buffer=alternate, length == rows — so there is nothing for the wheel to
    // move through and the agent owns its own transcript. What it does have is
    // mouse reporting, and a wheel notch is a mouse event: that is how scrolling
    // an agent works in Ghostty, and ghostty-web never does it. Its handler goes
    // straight from "alternate screen" to synthesising arrow keys, which walks
    // the input history instead and looks like the pane typing to itself.
    //
    // So: ask the program. hasMouseTracking() is the terminal reporting that the
    // program asked for mouse events, and the pty is already ours to write to,
    // so the notch goes down as an SGR report and the program scrolls itself.
    term.attachCustomWheelEventHandler((event: WheelEvent) => {
      if (term.buffer.active.type !== "alternate") {
        return false;
      }
      if (!term.wasmTerm?.hasMouseTracking()) {
        return true;
      }
      const cell = term.renderer?.getMetrics();
      const box = (event.currentTarget as HTMLElement | null)?.getBoundingClientRect();
      const col =
        cell && box
          ? Math.min(term.cols, Math.max(1, Math.floor((event.clientX - box.left) / cell.width) + 1))
          : 1;
      const row =
        cell && box
          ? Math.min(term.rows, Math.max(1, Math.floor((event.clientY - box.top) / cell.height) + 1))
          : 1;
      const lines = Math.min(
        10,
        Math.max(1, Math.round(Math.abs(event.deltaY) / (cell?.height ?? 20))),
      );
      const button = event.deltaY > 0 ? 65 : 64;
      let report = "";
      for (let i = 0; i < lines; i++) {
        report += `\x1b[<${button};${col};${row}M`;
      }
      void Panes.Send(fromBytes(report));
      return true;
    });

    const offData = Events.On("pane:data", (event: { data: string }) => {
      timer.current.received();
      term.write(toBytes(event.data));
    });

    // The pty is opened at the size the emulator settled on rather than a
    // guessed one: a zmx session takes its shape from the client attached to it,
    // so opening at the wrong size means the program lays its first frame out
    // for a terminal that does not exist and reflows when corrected.
    //
    // No history prefill. It was written to give the wheel something to reach,
    // and the measurement said the agent is on the alternate screen — which has
    // no scrollback at all, so the history landed in a buffer the alt screen
    // hides. Scrolling works now because the wheel reaches the program instead.
    const opening = performance.now();
    void Panes.Open(session, term.cols, term.rows)
      .then(() =>
        Probe.Report(
          "pane-open-cost",
          true,
          `${Math.round(performance.now() - opening)}ms buffer=${term.buffer.active.type} ` +
            `rows=${term.rows} length=${term.buffer.active.length}`,
        ),
      )
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));

    // Clicking a sidebar button leaves focus on the button, so the next
    // keystroke goes to the browser rather than the agent. Taking it back when
    // the window is re-entered means alt-tabbing away and back lands you where
    // you were, which is what a terminal does.
    const refocus = () => term.focus();
    window.addEventListener("focus", refocus);

    return () => {
      void Probe.Report("pane-echo-latency", true, timer.current.summary());
      window.removeEventListener("focus", refocus);
      offData();
      clearPaneSinks();
      void Panes.Close();
    };
  }, [session, fontFamily]);

  return (
    // h-full *and* w-full: this section is a flex item inside an absolutely
    // positioned layer, so without both it sizes to its content and the
    // terminal's container stops tracking the window — which reads as a pane
    // that will not reflow.
    <section className="flex h-full w-full min-w-0 flex-1 flex-col">
      <div
        ref={container}
        className="flex-1 overflow-hidden rounded-md"
        // Painted in the terminal's own background, not the chrome's.
        //
        // A terminal is a grid of whole cells, so the container is almost never
        // an exact multiple of the cell width — and ghostty-web also reserves
        // room for a scrollbar. The remainder is bare container, which reads as
        // a gutter down the right edge only because it was a different colour
        // from the pane it borders. Matching it makes the leftover invisible
        // without pretending the grid can be fractional.
        style={{ minHeight: 0, backgroundColor: paneTheme.background }}
      />
      {error !== "" && <pre style={{ color: paneTheme.red, whiteSpace: "pre-wrap" }}>{error}</pre>}
    </section>
  );
}
