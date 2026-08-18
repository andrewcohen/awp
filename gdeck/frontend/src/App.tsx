import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import { Pane } from "./Pane";
import { LivePane } from "./LivePane";
import { Artifact } from "./Artifact";
import { Boundary } from "./Boundary";
import { macchiato, paneFonts } from "./palette";
import * as Panes from "@bindings/panes";
import * as Probe from "@bindings/probe";

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
// Matches Mac.InvisibleTitleBarHeight in main.go; the traffic lights sit inside
// it. Stated in both places because the window is created in Go and the space is
// reserved in CSS, and there is no way for one to read the other.
const titleBarHeight = 52;

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
  const [chosen, setChosen] = useState<string>("");
  const [launchedFrom, setLaunchedFrom] = useState<string>("");
  const [font, setFont] = useState<string>(paneFonts[0].family);

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
    <div style={{ display: "flex", flexDirection: "column", height: "100vh" }}>
      {/* The window hides its title bar and insets the traffic lights, so the
          page owns the top edge — including the strip the lights sit in. Nothing
          reserved it, which is why content ran underneath them.

          Reserved once here rather than as top padding on each column: two
          paddings that have to agree about a number is how they stop agreeing.
          -webkit-app-region: drag gives the strip the one behaviour a title bar
          had that we still want. */}
      <div
        style={{
          height: titleBarHeight,
          flexShrink: 0,
          // @ts-expect-error -- WebkitAppRegion is a real WebKit property that
          // React's CSS typings do not carry.
          WebkitAppRegion: "drag",
        }}
      />
      <main style={{ display: "flex", flex: 1, minHeight: 0 }}>
      {/* One row per workspace, from zmx's live sessions rather than from
          deckdata. Which workspaces exist on disk and which of them want
          attention is deckdata's question and a different one — this list only
          has to name something live to attach to. See Panes.Workspaces. */}
      <nav
        style={{
          width: 260,
          flexShrink: 0,
          borderRight: `1px solid ${macchiato.black}`,
          overflowY: "auto",
          padding: "0.75rem 0 0",
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
              onClick={(e) => {
                setChosen(w.Agent);
                // Hand focus back immediately. The pane focuses itself on
                // mount, but a clicked button keeps focus afterwards, so
                // without this the first keystroke after every switch is eaten
                // by the sidebar.
                e.currentTarget.blur();
              }}
              // Only idle rows are disabled. A workspace with no live agent
              // has nothing to attach to, and a row that looks clickable and
              // does nothing is worse than one that says why.
              //
              // gdeck's own session is emphatically not that. Driving the
              // conversation that is building gdeck from inside gdeck is the
              // most informative test available, and forbidding it protected
              // nobody from anything: the reflow is cosmetic, it reverses on
              // close, and ZMX_SESSION is stripped, so this is a new client
              // rather than a hijack of the calling one. The row is marked so
              // it is not clicked by accident, not withheld.
              disabled={idle}
              title={
                isHost
                  ? "gdeck was launched from this workspace's agent — attaching reflows it"
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
                cursor: idle ? "not-allowed" : "pointer",
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
                  gdeck's own terminal — attaching reflows it, closing reflows back
                </span>
              )}
            </button>
          );
        })}
        <hr style={{ border: 0, borderTop: `1px solid ${macchiato.black}`, margin: "0.75rem 0" }} />
        {/* A face is chosen by looking at it, so they are listed rather than
            configured. Switching remounts the pane, which for a live one means
            a detach and re-attach — cheap, and the session survives it. */}
        <p style={{ color: macchiato.brightBlack, padding: "0 1rem", margin: "0 0 0.25rem" }}>font</p>
        {paneFonts.map((f) => (
          <button
            key={f.family}
            onClick={() => setFont(f.family)}
            style={{
              display: "block",
              width: "100%",
              textAlign: "left",
              padding: "0.15rem 1rem",
              border: 0,
              font: "inherit",
              fontFamily: f.family,
              cursor: "pointer",
              background: "transparent",
              color: f.family === font ? macchiato.yellow : macchiato.text,
            }}
          >
            {f.family === font ? "› " : "  "}
            {f.label}
            <span style={{ color: macchiato.brightBlack }}> {f.note}</span>
          </button>
        ))}
      </nav>

      <div
        style={{
          flex: 1,
          minWidth: 0,
          // The pane is the tallest thing gdeck has and it is sized to the box
          // it is given, so anything else sharing that column has to be counted
          // in the column's height rather than added under it. Laid out as a
          // flex column with the pane on `flex: 1`, an extra line below shrinks
          // the pane instead of overflowing the div and putting a scrollbar
          // beside a terminal that has nothing to scroll.
          display: "flex",
          flexDirection: "column",
          minHeight: 0,
          overflow: "hidden",
          padding: "0.75rem 1rem 1rem",
        }}
      >
        {/* Keyed on the view so a crash in one does not leave its error sitting
            over the next thing clicked — picking another row gives a fresh
            boundary, which is also the recovery path. */}
        <Boundary key={`${chosen}:${font}`}>
          {chosen === "" && <Pane fontFamily={font} />}
          {chosen === "artifact" && <Artifact />}
          {chosen !== "" && chosen !== "artifact" && <LivePane session={chosen} fontFamily={font} />}
        </Boundary>
        {/* Only where there is room for it. An attached pane gets the whole
            column: a line of build telemetry under someone's agent is the
            developer's note to themselves left on the user's screen. */}
        {(chosen === "" || chosen === "artifact") && (
          <p style={{ color: macchiato.brightBlack, margin: "0.75rem 0 0" }}>
            libghostty wasm instantiated in {status.ms}ms
            {chosen === "" && " · pick a workspace to attach"}
          </p>
        )}
      </div>
      </main>
    </div>
  );
}

export default App;
