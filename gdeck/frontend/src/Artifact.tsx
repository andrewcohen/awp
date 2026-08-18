import { useEffect, useRef, useState } from "react";
import { macchiato } from "./palette";
import * as Probe from "../bindings/github.com/andrewcohen/awp/gdeck/probe";

// The reason this surface exists: HTML an agent produced, rendered as HTML.
//
// A terminal can only transcribe such a thing. That is the whole argument for a
// webview, and it is worth proving with the smallest possible thing that is
// actually rendered rather than described — a heading, a table, an SVG — because
// the interesting question was never whether a browser can draw a table.
//
// The interesting question is what it is allowed to do while drawing it, and
// this file is the answer to that one.

// Agent HTML is untrusted, and the thing that makes it dangerous here is not the
// HTML — it is what sits next to it. A Wails webview has bindings on it: this
// page can call Panes.Send, which writes to a live agent's terminal, and
// Probe.Snapshot, which writes files. Markup that reached those would not need
// an exploit, only a <script> tag.
//
// So the frame is sandboxed with nothing added back. No allow-scripts, so
// nothing in it runs; no allow-same-origin, so it cannot reach the parent's
// globals even if something did. The two together are the point: allow-scripts
// plus allow-same-origin is documented as equivalent to no sandbox at all,
// because content granted both can simply remove the attribute from its own
// frame.
//
// srcdoc rather than a URL keeps it off the asset server, so there is no path
// for it to be fetched with the app's own origin later.
const SANDBOX = "";

// A CSP inside the document as well, because sandbox governs what the frame may
// do and this governs what it may load. Without it a report that "just" embeds
// a tracking pixel phones home from the developer's machine, quietly, every time
// it is opened.
const CSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:";

// Stand-in for what an agent would hand over: prose, a table, and an SVG chart —
// the three shapes a terminal handles worst. The script tag is not decoration.
// It is there so that a frame which ever stops being sandboxed says so on screen
// instead of silently gaining the ability to call Go.
const artifact = `<!doctype html>
<html>
  <head>
    <meta http-equiv="content-security-policy" content="${CSP}">
    <style>
      body { font: 13px/1.6 ui-sans-serif, system-ui, sans-serif; color: ${macchiato.text};
             background: ${macchiato.base}; margin: 0; padding: 1.25rem; }
      h1 { font-size: 1.1rem; margin: 0 0 .75rem; color: ${macchiato.text}; }
      table { border-collapse: collapse; margin: .5rem 0 1rem; }
      th, td { text-align: left; padding: .25rem .75rem .25rem 0; }
      th { color: ${macchiato.brightBlack}; font-weight: 500; }
      td.n { font-variant-numeric: tabular-nums; }
      .ok { color: ${macchiato.green}; }
      .bad { color: ${macchiato.red}; }
      /* Styled as the passing state, because that is the state the markup
         alone can produce. Only the script below can turn it into a failure,
         and it has to do the colour itself — CSS cannot say "if script ran". */
      #escaped { color: ${macchiato.green}; }
    </style>
  </head>
  <body>
    <h1>agent artifact</h1>
    <p>Rendered as HTML, not transcribed into cells.</p>
    <table>
      <tr><th>check</th><th>result</th></tr>
      <tr><td>wasm instantiate</td><td class="ok">pass</td></tr>
      <tr><td>grapheme widths</td><td class="ok">pass</td></tr>
      <tr><td>box junctions</td><td class="bad">renderer seam</td></tr>
    </table>
    <svg width="260" height="60" role="img" aria-label="latency sparkline">
      <polyline fill="none" stroke="${macchiato.blue}" stroke-width="2"
        points="0,50 30,44 60,47 90,20 120,32 150,12 180,26 210,18 240,22"/>
    </svg>
    <p id="escaped">scripts blocked — this frame cannot reach Go</p>
    <script>
      var line = document.getElementById('escaped');
      line.textContent = 'SANDBOX ESCAPED: this frame ran script and can reach the Wails bindings';
      line.style.color = '${macchiato.red}';
      line.style.fontWeight = '700';
    </script>
  </body>
</html>`;

export function Artifact() {
  const frame = useRef<HTMLIFrameElement | null>(null);
  const [escaped, setEscaped] = useState(false);

  useEffect(() => {
    // Ask the frame, rather than trusting the attribute. A sandbox that stopped
    // being applied is exactly the failure this check exists for, and reading
    // the attribute back would only confirm that the string is still the string.
    const id = setTimeout(() => {
      let reachable = false;
      try {
        reachable = frame.current?.contentWindow?.document !== undefined;
      } catch {
        // A cross-origin throw is the sandbox working: an opaque origin is what
        // withholding allow-same-origin buys.
        reachable = false;
      }
      setEscaped(reachable);
      void Probe.Report(
        "artifact-sandboxed",
        !reachable,
        reachable
          ? "the parent can reach into the frame, so the frame is same-origin and the bindings are exposed"
          : "opaque origin, scripts blocked",
      );
    }, 250);
    return () => clearTimeout(id);
  }, []);

  return (
    <section style={{ height: "100%", display: "flex", flexDirection: "column" }}>
      <iframe
        ref={frame}
        title="agent artifact"
        sandbox={SANDBOX}
        srcDoc={artifact}
        style={{ flex: 1, minHeight: 320, width: "100%", border: `1px solid ${macchiato.black}` }}
      />
      {escaped && (
        <p style={{ color: macchiato.red }}>
          the artifact frame is not isolated — do not render agent HTML here
        </p>
      )}
    </section>
  );
}
