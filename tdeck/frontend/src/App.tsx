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
import { History } from "./History";
import { Sidebar } from "./Sidebar";
import { useTheme } from "./theme";
import { api, type SessionSummary, type Workspace } from "./api";

// Three panels: conversations, the chat, and a column for things that are about
// the workspace rather than the conversation.
//
// gdeck's version of this had tabs on the middle panel — terminal or chat —
// because the terminal was how you actually drove the agent and the chat was a
// read-only view of what it had done. tdeck drives the agent directly, so the
// tabs are gone and with them the whole reason the middle panel had to tell a
// pty what size it was.

// What is on screen lives in the URL; how it is arranged lives in localStorage.
//
// The split is the useful one. Which conversation you are reading and which
// workspace the history panel is showing are facts about *this view* — they can
// be linked to, reopened tomorrow, or pasted to somebody so they land on the
// same chat. Panel widths and the theme are facts about this window and belong
// nowhere near a link.
//
// Paths rather than query strings, because these are things rather than
// filters:
//
//   /s/<sessionId>            a conversation
//   /w/<project>/<workspace>  a workspace, whether or not a chat is open on it
//
// A workspace is addressed by project and name rather than by its directory:
// awp already identifies it that way, and the alternative is an absolute path
// percent-encoded into a URL, which is unreadable and unguessable. The cost is
// that resolving one needs the workspace list, so the panel opens a moment
// after the page does.
//
// replaceState rather than pushState: clicking through six conversations should
// not put six entries in the back button, and back meaning "previous chat" is a
// promise the rest of the app does not keep.
type View = { session: string; project: string; workspace: string };

function readUrl(): View {
  const parts = location.pathname
    .split("/")
    .filter(Boolean)
    .map(decodeURIComponent);
  if (parts[0] === "s" && parts[1])
    return { session: parts[1], project: "", workspace: "" };
  if (parts[0] === "w" && parts[1] && parts[2]) {
    return { session: "", project: parts[1], workspace: parts[2] };
  }
  return { session: "", project: "", workspace: "" };
}

function writeUrl(view: View): void {
  const path = view.session
    ? `/s/${encodeURIComponent(view.session)}`
    : view.project
      ? `/w/${encodeURIComponent(view.project)}/${encodeURIComponent(view.workspace)}`
      : "/";
  if (path !== location.pathname) history.replaceState(null, "", path);
}

// Layout is a preference about this window, and react-resizable-panels persists
// a whole layout under one key rather than a number per panel.
const layoutKey = "tdeck.layout";
const leftKey = "tdeck.left";
// v2: the previous key holds a value nobody chose. An effect persisted the
// default on mount, so every reader has "0" stored whether they ever hid the
// panel or not, and there is no way to tell a preference from an artefact.
// Renaming discards the artefact.
const panelOpenKey = "tdeck.panel.v2";
const lastSessionKey = "tdeck.session";

export default function App() {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  // The URL wins over the remembered session: someone who followed a link meant
  // that link, not wherever they were last time.
  const [chosen, setChosen] = useState<string>(
    () => readUrl().session || (localStorage.getItem(lastSessionKey) ?? ""),
  );
  // A workspace named by the URL, held until the workspace list arrives and can
  // say which directory it means.
  const [wanted] = useState<View>(readUrl);
  const [opening, setOpening] = useState(false);
  // A directory being looked at without a conversation open on it — the case
  // where a terminal agent already owns the workspace.
  const [inspecting, setInspecting] = useState("");
  const [error, setError] = useState("");
  const [theme, setTheme] = useTheme();
  // Open unless explicitly hidden. It was opt-in while the panel was a
  // placeholder reading "the workspace diff goes here"; now it holds the
  // directory's conversations, which is the only route to work a terminal
  // started — and the only place the resume and attach controls live. A useful
  // panel behind a toggle nobody presses is a feature nobody has.
  const [panelOpen, setPanelOpenState] = useState<boolean>(
    () =>
      readUrl().project !== "" || localStorage.getItem(panelOpenKey) !== "0",
  );

  // Written here rather than in an effect on the value, so only an actual
  // choice is stored. Persisting from an effect records the default too, which
  // makes "I have never touched this" indistinguishable from "I want it off" —
  // and then changing the default changes nothing for anyone.
  const setPanelOpen = useCallback((open: boolean) => {
    localStorage.setItem(panelOpenKey, open ? "1" : "0");
    setPanelOpenState(open);
  }, []);
  // Read once: the group owns the layout after mount, and feeding a changing
  // default back in would fight the drag.
  const [panelWidth] = useState<number>(
    () => Number(localStorage.getItem(layoutKey)) || 30,
  );
  const [leftWidth] = useState<number>(
    () => Number(localStorage.getItem(leftKey)) || 20,
  );

  useEffect(() => {
    if (chosen) localStorage.setItem(lastSessionKey, chosen);
  }, [chosen]);

  const refresh = useCallback(async () => {
    try {
      // Both together: the workspace rows carry which session is open on them,
      // so fetching them apart would show a workspace as unopened for a beat
      // after its chat appeared.
      const [list, spaces] = await Promise.all([
        api.sessions(),
        api.workspaces(),
      ]);
      setSessions(list);
      setWorkspaces(spaces);
      setError("");
      // A session the daemon does not have — a stale link, or a daemon that has
      // been restarted since — falls back to the first real one, and the URL is
      // rewritten to match.
      //
      // Known limitation: that silently discards the link rather than offering
      // to resume what it named. Resuming would need the conversation's cwd,
      // which the link does not carry, and guessing it is worse than landing
      // somewhere usable. A link that carried both could do better.
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

  const openChat = useCallback(
    async (cwd?: string) => {
      setOpening(true);
      try {
        const session = await api.open(cwd);
        setSessions((have) => [...have, session]);
        setChosen(session.sessionId);
        void refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setOpening(false);
      }
    },
    [refresh],
  );

  const session = sessions.find((s) => s.sessionId === chosen) ?? null;

  // A /w/<project>/<workspace> link, resolved once the list can say which
  // directory it means. Runs until it matches: the list arrives a moment after
  // the page, and a link to a workspace that does not exist simply never
  // resolves rather than erroring at someone.
  useEffect(() => {
    if (!wanted.project || inspecting) return;
    const match = workspaces.find(
      (w) => w.project === wanted.project && w.name === wanted.workspace,
    );
    if (!match) return;
    if (match.sessionId) setChosen(match.sessionId);
    else setInspecting(match.path);
  }, [workspaces, wanted, inspecting]);

  // The address bar follows the view. A conversation names itself; a workspace
  // being inspected without one is named by the workspace it belongs to.
  useEffect(() => {
    const shown = inspecting
      ? workspaces.find((w) => w.path === inspecting)
      : workspaces.find((w) => w.sessionId === chosen);
    writeUrl(
      chosen && !inspecting
        ? { session: chosen, project: "", workspace: "" }
        : shown
          ? { session: "", project: shown.project, workspace: shown.name }
          : { session: chosen, project: "", workspace: "" },
    );
  }, [chosen, inspecting, workspaces]);

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
              workspaces={workspaces}
              sessions={sessions}
              chosen={chosen}
              onChoose={(id) => {
                setInspecting("");
                setChosen(id);
              }}
              // Clicking a workspace with no conversation opens one *in that
              // directory*, which is the whole join between awp and tdeck: the
              // agent starts where the work is.
              onOpenWorkspace={(workspace) => void openChat(workspace.path)}
              onInspect={(workspace) => {
                setInspecting(workspace.path);
                setPanelOpen(true);
              }}
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
                  <ChatView
                    session={session}
                    // A setting changed inside the chat is a fact about the
                    // session, so it goes back to the list the sidebar reads —
                    // rather than being held twice and drifting.
                    onSessionChanged={(updated) =>
                      setSessions((have) =>
                        have.map((s) =>
                          s.sessionId === updated.sessionId ? updated : s,
                        ),
                      )
                    }
                  />
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
                <Boundary key={`history:${inspecting || session?.cwd || ""}`}>
                  {inspecting || session ? (
                    // The right panel is the history of wherever the current
                    // conversation is working. It was a placeholder waiting for
                    // "the workspace diff"; past conversations turn out to be
                    // the thing you reach for far more often, and they are the
                    // only way to get at work the terminal started.
                    <History
                      cwd={inspecting || session!.cwd}
                      openIds={sessions.map((s) => s.sessionId)}
                      onChoose={setChosen}
                      onClose={() => {
                        setPanelOpen(false);
                        setInspecting("");
                      }}
                      onResumed={(resumed) => {
                        setSessions((have) =>
                          have.some((s) => s.sessionId === resumed.sessionId)
                            ? have
                            : [...have, resumed],
                        );
                        setChosen(resumed.sessionId);
                        setInspecting("");
                        void refresh();
                      }}
                    />
                  ) : (
                    <p className="text-muted-foreground p-4 text-sm">
                      no conversation selected
                    </p>
                  )}
                </Boundary>
              </ResizablePanel>
            </>
          )}
        </ResizablePanelGroup>
      </main>
    </div>
  );
}
