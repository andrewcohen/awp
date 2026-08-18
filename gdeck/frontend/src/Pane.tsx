import { useEffect, useRef, useState } from "react";
import type { Terminal } from "ghostty-web";
import { paneTheme } from "./palette";
import { mountPaneTerminal, resetPane, clearPaneSinks } from "./paneTerminal";
import * as Probe from "@bindings/probe";

// A pane with no process behind it: libghostty interpreting bytes this file
// hands it directly.
//
// Splitting this from the pty work is the point. When a live agent's output
// eventually looks wrong, the question is whether the emulator drew it wrong or
// the transport mangled it on the way in, and that question is much cheaper to
// answer if the emulator has already been shown correct against bytes chosen for
// the purpose. There is no process here, no IPC, and nothing asynchronous — if
// this frame is wrong, the emulator or the renderer is wrong.
// No fixed size any more: the shared terminal is sized by the window it sits
// in, and a static pane that forced 80x20 would resize the live one every time
// it was opened.

// Bytes chosen to fail loudly rather than to look nice.
//
// The last two lines are the ones that matter. 👩‍💻 is a ZWJ sequence stored
// across two wide cells: two graphemes of two columns each when measured apart,
// one grapheme of two columns when measured together, so anything summing
// per-cell widths reports 4 where the row is 2. That disagreement is #339 — the
// deck placed a pane's cursor by column of a *rendered string* while the
// emulator counted cells, and on a row holding one of these the cursor landed
// beside the text instead of on it. A webview terminal renders its own cells, so
// the translation that got this wrong does not exist here; this line is on
// screen to show that, not because it is expected to break.
//
// The combining-mark line is the same class one step down: 'é' as e + U+0301 is
// two codepoints occupying one cell, which a renderer measuring codepoints
// spreads across two.
//
// Both rows are padded so their `|` lands in the same column as a row of plain
// ASCII, because a column number in a comment is not something the eye can
// check. Three pipes in a vertical line is; one pipe out of line names its own
// row as the grapheme that measured wrong.
const script = [
  "\x1b[1mgdeck pane\x1b[0m — libghostty, no process behind it",
  "",
  "  \x1b[31mred\x1b[0m \x1b[32mgreen\x1b[0m \x1b[33myellow\x1b[0m \x1b[34mblue\x1b[0m \x1b[35mmagenta\x1b[0m \x1b[36mcyan\x1b[0m",
  "  \x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[0m \x1b[4munderline\x1b[0m \x1b[7minverse\x1b[0m \x1b[2mfaint\x1b[0m",
  "  \x1b[38;5;213m256-colour\x1b[0m \x1b[38;2;138;173;244mtruecolour\x1b[0m",
  "",
  "  ┌──────────┬──────────┐",
  "  │ box      │ ╭─ round │",
  "  └──────────┴──────────┘",
  "  ────────────────────────  ← one unbroken rule",
  "",
  "  ascii            | ← column 20",
  "  wide + ZWJ    👩‍💻 | ← two cells, two columns",
  "  combining      é | ← e + U+0301, one cell",
  "",
].join("\r\n");

type Status = { state: "starting" } | { state: "ok" } | { state: "failed"; error: string };

// What the emulator thinks it holds, as distinct from what the renderer drew.
//
// These are two different claims and the snapshot only carries the second. A box
// junction that looks broken on the canvas is either libghostty putting the
// wrong thing in the cell — which would be a fidelity bug shared with `awp
// deck`, because it is the same VT — or ghostty-web's canvas drawing the right
// cell badly, which is local to this surface and affects nothing the TUI does.
// Reporting the cells alongside the picture is what makes the two
// distinguishable without guessing from a 13px glyph.
//
// Each row is reported with the width the emulator assigned each cell, since a
// grapheme's cell footprint is the thing #339 turned on.
function describeBuffer(term: Terminal): string {
  const buffer = term.buffer.active;
  const lines: string[] = [];
  for (let y = 0; y < term.rows; y++) {
    const line = buffer.getLine(y);
    if (!line) {
      continue;
    }
    const text = line.translateToString(true);
    if (text === "") {
      continue;
    }
    const widths: number[] = [];
    for (let x = 0; x < line.length; x++) {
      const cell = line.getCell(x);
      if (cell && cell.getChars() !== "") {
        widths.push(cell.getWidth());
      }
    }
    lines.push(`${String(y).padStart(2)}: ${JSON.stringify(text)} widths=${widths.join("")}`);
  }
  return lines.join("\n");
}

// Hand Go a PNG of the pane's own canvas, so what the emulator drew can be
// looked at without capturing the screen it was drawn on. See Probe.Snapshot for
// why that distinction is the whole point.
//
// The two chained frames are not superstition: ghostty-web draws on a
// requestAnimationFrame, so a toDataURL issued in the same tick as write()
// serialises a canvas that is still blank.
async function snapshot(parent: HTMLElement, check: string): Promise<void> {
  await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
  const canvas = parent.querySelector("canvas");
  if (!canvas) {
    void Probe.Report(check, false, "the pane rendered no canvas to snapshot");
    return;
  }
  try {
    await Probe.Snapshot(check, canvas.toDataURL("image/png"));
  } catch (err: unknown) {
    void Probe.Report(check, false, err instanceof Error ? err.message : String(err));
  }
}

export function Pane({ fontFamily }: { fontFamily: string }) {
  const host = useRef<HTMLDivElement | null>(null);
  const [status, setStatus] = useState<Status>({ state: "starting" });

  useEffect(() => {
    const parent = host.current;
    if (!parent) {
      return;
    }

    // Borrowed, not built: the shared terminal, cleared and written to. See
    // paneTerminal — a Terminal per view is what put a live agent's bytes into
    // this pane's cells, which is the bug this file was supposed to rule out.
    const { term } = mountPaneTerminal(parent, fontFamily);
    resetPane();
    clearPaneSinks();
    try {
      term.write(script);
      setStatus({ state: "ok" });
      const cell = term.renderer?.getMetrics();
      void Probe.Report(
        "pane-render-static",
        true,
        `${term.cols}x${term.rows} cell=${cell?.width}x${cell?.height} baseline=${cell?.baseline}`,
      );
      void Probe.Report("pane-buffer-static", true, describeBuffer(term));
      void snapshot(parent, "pane-render-static");
    } catch (err: unknown) {
      const error = err instanceof Error ? err.message : String(err);
      setStatus({ state: "failed", error });
      void Probe.Report("pane-render-static", false, error);
    }

  }, [fontFamily]);

  return (
    <section>
      <div ref={host} />
      {status.state === "failed" && (
        <pre style={{ color: paneTheme.red, whiteSpace: "pre-wrap" }}>
          pane failed to render:{"\n"}
          {status.error}
        </pre>
      )}
    </section>
  );
}
