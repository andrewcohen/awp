import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import { Pane } from "./Pane";
import { AgentPane } from "./AgentPane";
import { Artifact } from "./Artifact";
import { Boundary } from "./Boundary";
import { Sidebar, type Workspace } from "./Sidebar";
import { paneFonts } from "./palette";
import { useTheme } from "./theme";
import * as Panes from "@bindings/panes";
import * as Probe from "@bindings/probe";

// The POC's first question, asked on screen rather than in a test: does
// libghostty's wasm instantiate inside a Wails webview at all?
//
// WKWebView is particular about WebAssembly served over a custom URL scheme,
// which is how Wails serves frontend assets. ghostty-web sidesteps the scheme
// entirely — its bundle carries the wasm inline as a base64 data: URL and
// compiles it from an ArrayBuffer — so the expected result is a pass. But
// "expected to pass" and "passes" are different claims, and everything after
// this step assumes the second one.
//
// Everything else waits on it: init() loads the module the Terminal class
// constructs against, so rendering a pane first would fail for a reason that has
// nothing to do with the pane.
type Status =
  | { state: "loading" }
  | { state: "ok"; ms: number }
  | { state: "failed"; error: string };

// Matches Mac.InvisibleTitleBarHeight in main.go. The window hides its title bar
// and insets the traffic lights, so the page owns the top edge including the
// strip they sit in — reserved once here rather than as padding on each column,
// because two paddings that have to agree about a number eventually do not.
const titleBarHeight = 38;

// What to reopen on launch. A POC gets relaunched constantly, and landing on the
// artifact view every time means re-picking the workspace before any question
// can be asked again. Kept in localStorage rather than in Go: it describes this
// window's last view, not anything about the workspace, and a preferences file
// under ~/.awp for a throwaway surface is one someone has to clean up later.
const lastViewKey = "gdeck.view";
const lastFontKey = "gdeck.font";

function App() {
  const [status, setStatus] = useState<Status>({ state: "loading" });
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [chosen, setChosen] = useState<string>(
    () => localStorage.getItem(lastViewKey) ?? "artifact",
  );
  const [font, setFont] = useState<string>(
    () => localStorage.getItem(lastFontKey) ?? paneFonts[0].family,
  );
  const [theme, setTheme] = useTheme();

  useEffect(() => localStorage.setItem(lastViewKey, chosen), [chosen]);
  useEffect(() => localStorage.setItem(lastFontKey, font), [font]);

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
    Panes.Workspaces().then(
      (rows) => {
        const list = rows as Workspace[];
        setWorkspaces(list);
        void Probe.Report(
          "workspaces-list",
          true,
          `${list.length} workspaces, ${list.filter((w) => w.Agent !== "").length} with a live agent`,
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

  // A remembered session that is no longer running would leave the pane trying
  // to attach to nothing, so it falls back once the list is known.
  useEffect(() => {
    if (chosen === "" || chosen === "artifact" || workspaces.length === 0) {
      return;
    }
    if (!workspaces.some((w) => w.Agent === chosen)) {
      setChosen("artifact");
    }
  }, [workspaces, chosen]);

  const body = () => {
    if (status.state === "loading") {
      return <p className="text-muted-foreground p-6">instantiating libghostty wasm…</p>;
    }
    if (status.state === "failed") {
      return (
        <pre className="text-destructive p-6 whitespace-pre-wrap">
          libghostty wasm failed to instantiate:{"\n"}
          {status.error}
        </pre>
      );
    }
    if (chosen === "") {
      return <Pane fontFamily={font} />;
    }
    if (chosen === "artifact") {
      return <Artifact />;
    }
    return <AgentPane session={chosen} fontFamily={font} />;
  };

  return (
    <div className="bg-background text-foreground flex h-screen flex-col">
      <div
        className="shrink-0"
        style={{
          height: titleBarHeight,
          // @ts-expect-error -- a real WebKit property React's CSS typings omit.
          WebkitAppRegion: "drag",
        }}
      />
      <main className="flex min-h-0 flex-1">
        <Sidebar
          workspaces={workspaces}
          chosen={chosen}
          onChoose={setChosen}
          theme={theme}
          onTheme={setTheme}
          font={font}
          onFont={setFont}
        />
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-3">
          {/* Keyed on the view so a crash in one does not leave its error
              sitting over the next thing clicked; picking another row gives a
              fresh boundary, which is also the recovery path. */}
          <Boundary key={`${chosen}:${font}`}>{body()}</Boundary>
        </div>
      </main>
    </div>
  );
}

export default App;
