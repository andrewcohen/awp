import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import { Pane } from "./Pane";
import { LivePane } from "./LivePane";
import { macchiato } from "./palette";
import * as Panes from "../bindings/github.com/andrewcohen/awp/gdeck/panes";
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
//
// Everything else waits on it. init() loads the module the Terminal class
// constructs against, so rendering a pane before it resolves would fail for a
// reason that has nothing to do with the pane.
type Status =
  | { state: "loading" }
  | { state: "ok"; ms: number }
  | { state: "failed"; error: string };

// A zmx session, as much of one as picking a pane needs. The generated bindings
// carry the full type; this is the shape the list reads.
type Session = { Name: string; Cmd: string; Ended: boolean; Clients: number };

function App() {
  const [status, setStatus] = useState<Status>({ state: "loading" });
  const [sessions, setSessions] = useState<Session[]>([]);
  const [chosen, setChosen] = useState<string>("");
  const [launchedFrom, setLaunchedFrom] = useState<string>("");

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

  useEffect(() => {
    void Panes.LaunchedFrom().then(setLaunchedFrom);
    Panes.Sessions().then(
      (rows) => {
        const live = (rows as Session[]).filter((s) => !s.Ended);
        setSessions(live);
        void Probe.Report("sessions-list", true, `${live.length} live of ${rows.length}`);
      },
      (err: unknown) =>
        void Probe.Report("sessions-list", false, err instanceof Error ? err.message : String(err)),
    );
  }, []);

  if (status.state === "loading") {
    return <main style={{ padding: "1.5rem" }}>instantiating libghostty wasm…</main>;
  }
  if (status.state === "failed") {
    return (
      <main style={{ padding: "1.5rem" }}>
        <pre style={{ color: macchiato.red, whiteSpace: "pre-wrap" }}>
          libghostty wasm failed to instantiate:{"\n"}
          {status.error}
        </pre>
      </main>
    );
  }

  return (
    <main style={{ display: "flex", height: "100vh" }}>
      {/* The dumbest sidebar that can pick a pane: zmx's own session list, not
          the deck's rows. Which workspaces exist and which of them have an agent
          waiting is deckdata's question and a different one — this list only has
          to name something real to attach to. */}
      <nav
        style={{
          width: 260,
          flexShrink: 0,
          borderRight: `1px solid ${macchiato.black}`,
          overflowY: "auto",
          padding: "2.5rem 0 0",
        }}
      >
        {/* Attaching is not looking. A zmx session takes its size from the
            client attached to it, so clicking a row reflows that agent to this
            pane's shape — and reflows it back on close. Said before the click,
            because after it the damage is already on someone's screen. */}
        <p style={{ color: macchiato.yellow, padding: "0 1rem 0.75rem", margin: 0 }}>
          attaching resizes the session to this pane
        </p>
        {sessions.length === 0 && (
          <p style={{ color: macchiato.brightBlack, padding: "0 1rem" }}>no live zmx sessions</p>
        )}
        {sessions.map((s) => {
          const isHost = s.Name === launchedFrom;
          return (
            <button
              key={s.Name}
              onClick={() => setChosen(s.Name)}
              // The session gdeck was launched from is the one row where a
              // stray click costs the developer their own terminal's layout.
              // Disabled rather than merely marked: nothing in the POC needs it,
              // and it is the row nearest the top of an alphabetical list.
              disabled={isHost}
              title={isHost ? "gdeck was launched from this session" : s.Name}
              style={{
                display: "block",
                width: "100%",
                textAlign: "left",
                padding: "0.35rem 1rem",
                border: 0,
                font: "inherit",
                cursor: isHost ? "not-allowed" : "pointer",
                background: s.Name === chosen ? macchiato.black : "transparent",
                color: isHost
                  ? macchiato.brightBlack
                  : s.Name === chosen
                    ? macchiato.yellow
                    : macchiato.text,
              }}
            >
              {s.Name}
              <span style={{ display: "block", color: macchiato.brightBlack }}>
                {isHost ? "gdeck's own terminal" : s.Cmd || "—"}
                {s.Clients > 0 ? ` · ${s.Clients} client${s.Clients === 1 ? "" : "s"}` : ""}
              </span>
            </button>
          );
        })}
      </nav>

      <div style={{ flex: 1, minWidth: 0, padding: "2.5rem 1rem 1rem" }}>
        {chosen === "" ? <Pane /> : <LivePane key={chosen} session={chosen} />}
        <p style={{ color: macchiato.brightBlack, marginTop: "0.75rem" }}>
          libghostty wasm instantiated in {status.ms}ms
          {chosen === "" ? " · pick a session to attach" : ` · attached to ${chosen}`}
        </p>
      </div>
    </main>
  );
}

export default App;
