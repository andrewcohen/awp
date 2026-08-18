import { useEffect, useState } from "react";
import { init } from "ghostty-web";
import { Pane } from "./Pane";
import { AgentPane } from "./AgentPane";
import { Artifact } from "./Artifact";
import { Boundary } from "./Boundary";
import { ChatView } from "./ChatView";
import { Sidebar, type Workspace } from "./Sidebar";
import { RightPanel } from "./RightPanel";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { Button } from "@/components/ui/button";
import { PanelRight } from "lucide-react";
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
// The panel's width is a preference about this window, and react-resizable-panels
// persists a whole layout under one key rather than a number per panel.
const layoutKey = "gdeck.layout";
const panelOpenKey = "gdeck.panel";
const leftKey = "gdeck.left";

function App() {
  const [status, setStatus] = useState<Status>({ state: "loading" });
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [chosen, setChosen] = useState<string>(
    () => localStorage.getItem(lastViewKey) ?? "artifact",
  );
  const [font] = useState<string>(() => localStorage.getItem(lastFontKey) ?? paneFonts[0].family);
  const [theme, setTheme] = useTheme();
  const [panelOpen, setPanelOpen] = useState<boolean>(
    () => localStorage.getItem(panelOpenKey) === "1",
  );
  // Read once: the group owns the layout after mount, and feeding a changing
  // default back in would fight the drag.
  const [panelWidth] = useState<number>(() => Number(localStorage.getItem(layoutKey)) || 30);
  const [leftWidth] = useState<number>(() => Number(localStorage.getItem(leftKey)) || 20);

  useEffect(() => localStorage.setItem(panelOpenKey, panelOpen ? "1" : "0"), [panelOpen]);

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
    // ?mock=1 renders the chat against a fixture, so its layout can be looked
    // at in a browser where the Go bindings do not exist.
    if (new URLSearchParams(location.search).has("mock")) {
      return <ChatView session="" />;
    }
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
    <div className="bg-background text-foreground relative flex h-screen flex-col overflow-hidden">
      <div
        className="shrink-0"
        style={{
          height: titleBarHeight,
          // @ts-expect-error -- a real WebKit property React's CSS typings omit.
          WebkitAppRegion: "drag",
        }}
      />
      {!panelOpen && (
        <Button
          variant="ghost"
          size="icon"
          aria-label="show panel"
          title="show panel"
          className="text-muted-foreground absolute top-1 right-2 z-10"
          onClick={() => setPanelOpen(true)}
        >
          <PanelRight className="size-4" />
        </Button>
      )}
      <main className="flex min-h-0 flex-1 overflow-hidden">
        <ResizablePanelGroup
          orientation="horizontal"
          // Persisted by hand rather than with the library's storage option:
          // one number, read on mount and written when a drag ends. Sizes are
          // strings here because this version reads a bare number as pixels.
          defaultLayout={
            panelOpen
              ? { left: leftWidth, main: 100 - leftWidth - panelWidth, right: panelWidth }
              : { left: leftWidth, main: 100 - leftWidth }
          }
          onLayoutChanged={(layout) => {
            // Written on drag end rather than during: the callback fires per
            // frame while dragging, and a terminal is on the other side of it.
            if (Number.isFinite(layout.right)) {
              localStorage.setItem(layoutKey, String(layout.right));
            }
            if (Number.isFinite(layout.left)) {
              localStorage.setItem(leftKey, String(layout.left));
            }
          }}
          className="min-h-0 min-w-0 flex-1"
        >
          {/* The workspace list is a panel like the others now. It was a fixed
              288px column, which is the wrong width twice: too wide on a laptop
              where the pane wants every pixel, too narrow on a monitor where
              project and workspace names are what you are scanning. */}
          <ResizablePanel id="left" minSize="12" className="flex min-h-0 min-w-0 flex-col">
            <Sidebar
              workspaces={workspaces}
              chosen={chosen}
              onChoose={setChosen}
              theme={theme}
              onTheme={setTheme}
            />
          </ResizablePanel>
          <ResizableHandle withHandle />

          <ResizablePanel id="main" minSize="30" className="flex min-h-0 min-w-0 flex-col">
            <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-3">
              {/* Keyed on the view so a crash in one does not leave its error
                  sitting over the next thing clicked; picking another row gives
                  a fresh boundary, which is also the recovery path. */}
              <Boundary key={`${chosen}:${font}`}>{body()}</Boundary>
            </div>
          </ResizablePanel>

          {panelOpen && (
            <>
              <ResizableHandle withHandle />
              <ResizablePanel
                id="right"
                minSize="18"
                // Dragging this handle resizes the pane on the left, which
                // resizes the pty, which reflows the agent. Nothing to prevent —
                // it is what resizing a terminal means — but it is not free.
                className="flex min-h-0 min-w-0 flex-col"
              >
                <Boundary key="right-panel">
                  <RightPanel onClose={() => setPanelOpen(false)} />
                </Boundary>
              </ResizablePanel>
            </>
          )}
        </ResizablePanelGroup>
      </main>
    </div>
  );
}

export default App;
