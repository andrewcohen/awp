import { useEffect, useRef, useState } from "react";
import { Terminal, FitAddon } from "ghostty-web";
import { Events } from "@wailsio/runtime";
import { paneTheme } from "./palette";
import * as Panes from "../bindings/github.com/andrewcohen/awp/gdeck/panes";
import * as Probe from "../bindings/github.com/andrewcohen/awp/gdeck/probe";

// A real agent, attached over a pty, rendered by libghostty in the webview.
//
// This is the step that can kill the idea. Everything before it says the
// emulator is correct; none of it says a pane is usable, and the difference is
// latency — a terminal that is right and late is not a terminal anyone will
// work in. So the pane measures itself and reports numbers rather than a
// verdict, because "feels fine" is not a claim that survives being wrong.

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

export function LivePane({ session }: { session: string }) {
  const host = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState<string>("");
  const [exited, setExited] = useState(false);
  const timer = useRef(new echoTimer());

  useEffect(() => {
    const parent = host.current;
    if (!parent) {
      return;
    }

    const term = new Terminal({
      theme: paneTheme,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 13,
      scrollback: 10_000,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(parent);
    fit.fit();

    const offData = Events.On("pane:data", (event: { data: string }) => {
      timer.current.received();
      term.write(toBytes(event.data));
    });
    const offExit = Events.On("pane:exit", () => setExited(true));

    // The pty is opened at the size the emulator settled on rather than a
    // guessed one: a zmx session takes its shape from the client attached to
    // it, so opening at the wrong size means the program lays its first frame
    // out for a terminal that does not exist and reflows when corrected.
    Panes.Open(session, term.cols, term.rows).catch((err: unknown) =>
      setError(err instanceof Error ? err.message : String(err)),
    );

    term.onData((data: string) => {
      timer.current.sent();
      void Panes.Send(fromBytes(data));
    });
    term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
      void Panes.Resize(cols, rows);
    });
    fit.observeResize();

    return () => {
      // Report on the way out rather than on a timer: the sample set is only
      // interesting once someone has finished typing into it.
      void Probe.Report("pane-echo-latency", true, timer.current.summary());
      offData();
      offExit();
      fit.dispose();
      term.dispose();
      void Panes.Close();
    };
  }, [session]);

  return (
    <section style={{ height: "100%", display: "flex", flexDirection: "column" }}>
      <div ref={host} style={{ flex: 1, minHeight: 0 }} />
      {exited && <p style={{ color: paneTheme.brightBlack }}>{session} ended</p>}
      {error !== "" && <pre style={{ color: paneTheme.red, whiteSpace: "pre-wrap" }}>{error}</pre>}
    </section>
  );
}
