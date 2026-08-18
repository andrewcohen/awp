import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import { Pane } from "./Pane";
import { LivePane } from "./LivePane";
import { Artifact } from "./Artifact";
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

// A sidebar row: a workspace, and the agent a click attaches to. Grouping and
// the agent-only rule live in Go — see Panes.Workspaces for why the list is not
// sessions.
type Workspace = {
  Project: string;
  Workspace: string;
  Agent: string;
  Cmd: string;
  Others: number;
};

function App() {
  const [status, setStatus] = useState<Status>({ state: "loading" });
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  // Opens on the artifact rather than the pane, for two reasons that happen to
  // agree. It is the thing a terminal cannot do, so it is what this surface is
  // for; and its check is whether untrusted agent HTML is isolated from the
  // Wails bindings, which is an invariant that should be re-asserted on every
  // launch rather than whenever someone happens to click the row.
  const [chosen, setChosen] = useState<string>("artifact");
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
    Panes.Workspaces().then(
      (rows) => {
        const list = rows as Workspace[];
        setWorkspaces(list);
        const attachable = list.filter((w) => w.Agent !== "").length;
        void Probe.Report(
          "workspaces-list",
          true,
          `${list.length} workspaces, ${attachable} with a live agent`,
        );
      },
      (err: unknown) =>
        void Probe.Report(
          "workspaces-list",
          false,
          err instanceof Error ? err.message : String(err),
        ),
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
        {/* The two views that need no session, listed with the ones that do so
            everything the POC answers is reachable in one run. */}
        {[
          { id: "", label: "static pane", note: "hardcoded bytes" },
          { id: "artifact", label: "artifact", note: "sandboxed agent HTML" },
        ].map((v) => (
          <button
            key={v.id}
            onClick={() => setChosen(v.id)}
            style={{
              display: "block",
              width: "100%",
              textAlign: "left",
              padding: "0.35rem 1rem",
              border: 0,
              font: "inherit",
              cursor: "pointer",
              background: v.id === chosen ? macchiato.black : "transparent",
              color: v.id === chosen ? macchiato.yellow : macchiato.text,
            }}
          >
            {v.label}
            <span style={{ display: "block", color: macchiato.brightBlack }}>{v.note}</span>
          </button>
        ))}
        <hr style={{ border: 0, borderTop: `1px solid ${macchiato.black}`, margin: "0.75rem 0" }} />
        {workspaces.length === 0 && (
          <p style={{ color: macchiato.brightBlack, padding: "0 1rem" }}>no awp workspaces running</p>
        )}
        {workspaces.map((w) => {
          const isHost = w.Agent !== "" && w.Agent === launchedFrom;
          const idle = w.Agent === "";
          return (
            <button
              key={`${w.Project}/${w.Workspace}`}
              onClick={() => setChosen(w.Agent)}
              // The session gdeck was launched from is the one row where a
              // stray click costs the developer their own terminal's layout.
              // Disabled rather than merely marked: nothing in the POC needs it,
              // and it is the row nearest the top of an alphabetical list.
              // Idle as well as host: a workspace with no live agent has
              // nothing to attach to, and a row that looks clickable and does
              // nothing is worse than one that says why.
              disabled={isHost || idle}
              title={
                isHost
                  ? "gdeck was launched from this workspace's agent"
                  : idle
                    ? "no live agent in this workspace"
                    : w.Agent
              }
              style={{
                display: "block",
                width: "100%",
                textAlign: "left",
                // The marked row keeps its indent by paying for the accent out
                // of the padding, so it does not sit a pixel off from the rest.
                padding: isHost ? "0.35rem 1rem 0.35rem calc(1rem - 2px)" : "0.35rem 1rem",
                border: 0,
                borderLeft: isHost ? `2px solid ${macchiato.yellow}` : undefined,
                font: "inherit",
                cursor: isHost || idle ? "not-allowed" : "pointer",
                background: w.Agent !== "" && w.Agent === chosen ? macchiato.black : "transparent",
                // Not dimmed. A row nobody may click still has to be findable —
                // dimming it to the same grey as every second line hid it in a
                // list of fifteen, which is the opposite of warning about it.
                // The accent and the note carry "you cannot click this"; the
                // name and command stay legible so it can be recognised.
                color:
                  w.Agent !== "" && w.Agent === chosen
                    ? macchiato.yellow
                    : idle
                      ? macchiato.brightBlack
                      : macchiato.text,
              }}
            >
              <span style={{ color: macchiato.cyan }}>{w.Project}</span> / {w.Workspace}
              <span style={{ display: "block", color: macchiato.brightBlack }}>
                {idle ? "no live agent" : w.Cmd || "—"}
                {w.Others > 0 ? ` · +${w.Others} other` : ""}
              </span>
              {isHost && (
                <span style={{ display: "block", color: macchiato.yellow }}>
                  gdeck's own terminal — attaching would reflow it
                </span>
              )}
            </button>
          );
        })}
      </nav>

      <div style={{ flex: 1, minWidth: 0, padding: "2.5rem 1rem 1rem" }}>
        {chosen === "" && <Pane />}
        {chosen === "artifact" && <Artifact />}
        {chosen !== "" && chosen !== "artifact" && <LivePane key={chosen} session={chosen} />}
        <p style={{ color: macchiato.brightBlack, marginTop: "0.75rem" }}>
          libghostty wasm instantiated in {status.ms}ms
          {chosen === "" && " · pick a session to attach"}
          {chosen !== "" && chosen !== "artifact" && ` · attached to ${chosen}`}
        </p>
      </div>
    </main>
  );
}

export default App;
