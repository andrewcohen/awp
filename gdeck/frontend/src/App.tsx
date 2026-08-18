import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import * as Probe from "../bindings/github.com/andrewcohen/awp/gdeck/probe";

// The POC's first question, asked on screen rather than in a test: does
// libghostty's wasm instantiate inside a Wails webview at all?
//
// WKWebView is particular about WebAssembly served over a custom URL scheme,
// which is how Wails serves frontend assets — the wrong MIME type is enough to
// break streaming compilation, and a 400KB wasm binary is not what that path
// usually carries. ghostty-web sidesteps the scheme entirely: its bundle
// carries the wasm inline as a base64 data: URL and compiles it from an
// ArrayBuffer via WebAssembly.compile, with ./ghostty-vt.wasm and
// /ghostty-vt.wasm only as fallbacks. So the expected result here is a pass —
// but "expected to pass" and "passes" are different claims, and everything
// after this step assumes the second one.
type Status =
  | { state: "loading" }
  | { state: "ok"; ms: number }
  | { state: "failed"; error: string };

function App() {
  const [status, setStatus] = useState<Status>({ state: "loading" });

  useEffect(() => {
    const started = performance.now();
    init().then(
      () => {
        const ms = Math.round(performance.now() - started);
        setStatus({ state: "ok", ms });
        void Probe.Report("wasm-instantiate", true, `${ms}ms`);
      },
      (err: unknown) => {
        const error = err instanceof Error ? err.message : String(err);
        setStatus({ state: "failed", error });
        void Probe.Report("wasm-instantiate", false, error);
      },
    );
  }, []);

  return (
    <main style={{ padding: "2rem" }}>
      <h1 style={{ font: "inherit", fontWeight: 700, marginBottom: "1rem" }}>gdeck</h1>
      {status.state === "loading" && <p>instantiating libghostty wasm…</p>}
      {status.state === "ok" && (
        <p style={{ color: "#a6da95" }}>libghostty wasm instantiated in {status.ms}ms</p>
      )}
      {status.state === "failed" && (
        <pre style={{ color: "#ed8796", whiteSpace: "pre-wrap" }}>
          libghostty wasm failed to instantiate:{"\n"}
          {status.error}
        </pre>
      )}
    </main>
  );
}

export default App;
