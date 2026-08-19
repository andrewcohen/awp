import { useCallback, useEffect, useState } from "react";
import { PanelRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { Boundary } from "./Boundary";
import { ChatView } from "./ChatView";
import { RightPanel } from "./RightPanel";
import { Sidebar } from "./Sidebar";
import { useTheme } from "./theme";
import { api, type SessionSummary } from "./api";

// Three panels: conversations, the chat, and a column for things that are about
// the workspace rather than the conversation.
//
// gdeck's version of this had tabs on the middle panel — terminal or chat —
// because the terminal was how you actually drove the agent and the chat was a
// read-only view of what it had done. tdeck drives the agent directly, so the
// tabs are gone and with them the whole reason the middle panel had to tell a
// pty what size it was.

// Layout is a preference about this window, and react-resizable-panels persists
// a whole layout under one key rather than a number per panel.
const layoutKey = "tdeck.layout";
const leftKey = "tdeck.left";
const panelOpenKey = "tdeck.panel";
const lastSessionKey = "tdeck.session";

export default function App() {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [chosen, setChosen] = useState<string>(
    () => localStorage.getItem(lastSessionKey) ?? "",
  );
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState("");
  const [theme, setTheme] = useTheme();
  const [panelOpen, setPanelOpen] = useState<boolean>(
    () => localStorage.getItem(panelOpenKey) === "1",
  );
  // Read once: the group owns the layout after mount, and feeding a changing
  // default back in would fight the drag.
  const [panelWidth] = useState<number>(
    () => Number(localStorage.getItem(layoutKey)) || 30,
  );
  const [leftWidth] = useState<number>(
    () => Number(localStorage.getItem(leftKey)) || 20,
  );

  useEffect(
    () => localStorage.setItem(panelOpenKey, panelOpen ? "1" : "0"),
    [panelOpen],
  );
  useEffect(() => {
    if (chosen) localStorage.setItem(lastSessionKey, chosen);
  }, [chosen]);

  const refresh = useCallback(async () => {
    try {
      const list = await api.sessions();
      setSessions(list);
      setError("");
      // A remembered session the backend no longer has — a restart with a
      // cleared state file, say — would leave the middle panel pointed at
      // nothing, so fall back to the first real one.
      setChosen((current) =>
        list.some((s) => s.sessionId === current)
          ? current
          : (list[0]?.sessionId ?? ""),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
    // Titles and busy flags change as agents work, and neither is worth a second
    // event stream. Three seconds is slower than the chat and fast enough for a
    // list you are glancing at.
    const timer = setInterval(() => void refresh(), 3000);
    return () => clearInterval(timer);
  }, [refresh]);

  const openChat = useCallback(async () => {
    setOpening(true);
    try {
      const session = await api.open();
      setSessions((have) => [...have, session]);
      setChosen(session.sessionId);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setOpening(false);
    }
  }, []);

  const session = sessions.find((s) => s.sessionId === chosen) ?? null;

  return (
    <div className="bg-background text-foreground relative flex h-screen flex-col overflow-hidden">
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
          // Persisted by hand rather than with the library's storage option: one
          // number per panel, read on mount and written when a drag ends.
          defaultLayout={
            panelOpen
              ? {
                  left: leftWidth,
                  main: 100 - leftWidth - panelWidth,
                  right: panelWidth,
                }
              : { left: leftWidth, main: 100 - leftWidth }
          }
          onLayoutChanged={(layout) => {
            if (Number.isFinite(layout.right))
              localStorage.setItem(layoutKey, String(layout.right));
            if (Number.isFinite(layout.left))
              localStorage.setItem(leftKey, String(layout.left));
          }}
          className="min-h-0 min-w-0 flex-1"
        >
          <ResizablePanel
            id="left"
            minSize="12"
            className="flex min-h-0 min-w-0 flex-col"
          >
            <Sidebar
              sessions={sessions}
              chosen={chosen}
              onChoose={setChosen}
              onNew={() => void openChat()}
              opening={opening}
              theme={theme}
              onTheme={setTheme}
            />
          </ResizablePanel>
          <ResizableHandle withHandle />

          <ResizablePanel
            id="main"
            minSize="30"
            className="flex min-h-0 min-w-0 flex-col"
          >
            <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden pt-3">
              {error !== "" && (
                <p className="text-destructive px-4 pb-2 text-sm">{error}</p>
              )}
              {/* Keyed on the session so a crash in one conversation does not
                  leave its error sitting over the next one clicked; picking
                  another gives a fresh boundary, which is also the recovery
                  path. */}
              {session ? (
                <Boundary key={session.sessionId}>
                  <ChatView session={session} />
                </Boundary>
              ) : (
                <p className="text-muted-foreground p-6 text-sm">
                  {error === "" ? "connecting…" : "no conversation"}
                </p>
              )}
            </div>
          </ResizablePanel>

          {panelOpen && (
            <>
              <ResizableHandle withHandle />
              <ResizablePanel
                id="right"
                minSize="18"
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
