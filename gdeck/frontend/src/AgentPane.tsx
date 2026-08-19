import { useEffect, useState } from "react";
import { MessageSquare, SquareTerminal } from "lucide-react";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { LivePane } from "./LivePane";
import { focusPane } from "./paneTerminal";
import { Badge } from "@/components/ui/badge";
import * as Panes from "@bindings/panes";
import { ChatView } from "./ChatView";

// One session, two readings.
//
// The terminal is what the agent drew; the chat is what it said. Neither is a
// summary of the other — a TUI wraps and redraws and forgets, so the pane is the
// only place to see what the agent is doing *now*, and the transcript is the
// only place the last hour survives in a form that can be scrolled, expanded,
// and rendered as a diff.
//
// Tabs on the pane rather than a mode on the window, because the choice is about
// this workspace: it is reasonable to read one agent's transcript while watching
// another's terminal, and a global switch would make that impossible.
type Mode = "terminal" | "chat";

const modes: { id: Mode; label: string; icon: typeof MessageSquare }[] = [
  { id: "terminal", label: "terminal", icon: SquareTerminal },
  { id: "chat", label: "chat", icon: MessageSquare },
];

// Session names are project.workspace.kind — see zmx.SessionName — and the
// status store is keyed by workspace name.
function workspaceOf(session: string): { project: string; workspace: string } {
  const parts = session.split(".");
  return parts.length === 4
    ? { project: parts[1], workspace: parts[2] }
    : { project: "", workspace: "" };
}

const statusTone: Record<string, string> = {
  working: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  waiting: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  error: "bg-destructive/15 text-destructive",
};

export function AgentPane({ session, fontFamily }: { session: string; fontFamily: string }) {
  const [mode, setMode] = useState<Mode>("terminal");
  const [status, setStatus] = useState<{ Status: string; Prompt: string } | null>(null);

  useEffect(() => {
    const { project, workspace } = workspaceOf(session);
    if (workspace === "") {
      return;
    }
    // Polled, because the store is written by hooks in another process and
    // there is nothing to subscribe to from here. A second is well inside the
    // time it takes to notice a state change, and reads one small JSON file.
    const read = () =>
      Panes.Status(project, workspace).then(
        (s) => setStatus(s as { Status: string; Prompt: string }),
        () => setStatus(null),
      );
    void read();
    const timer = setInterval(read, 1000);
    return () => clearInterval(timer);
  }, [session]);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <Tabs value={mode} onValueChange={(v) => {
          const next = v as Mode;
          setMode(next);
          // The terminal does not unmount when the chat covers it, so nothing
          // re-runs a mount effect on the way back — focus has to be handed
          // over explicitly or the first keystroke goes to the tab strip.
          if (next === "terminal") {
            requestAnimationFrame(focusPane);
          }
        }}>
        <div className="flex items-center gap-3">
          <TabsList>
            {modes.map(({ id, label, icon: Icon }) => (
              <TabsTrigger
                key={id}
                value={id}
                // The terminal takes focus when it is shown; a trigger that
                // keeps focus would swallow the first keystroke after a switch.
                onMouseUp={(e) => e.currentTarget.blur()}
              >
                <Icon className="size-4" />
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
          {status?.Status && (
            <Badge
              variant="secondary"
              className={`shrink-0 font-normal ${statusTone[status.Status.toLowerCase()] ?? ""}`}
            >
              {status.Status}
            </Badge>
          )}
          <span className="text-muted-foreground min-w-0 truncate text-xs">
            {status?.Prompt || session}
          </span>
        </div>
      </Tabs>

      {/* The terminal stays mounted while the chat is over it, and stays
          *sized*, which is the harder half.

          Unmounting would detach the pty and re-attach on the way back, and
          attaching reflows the agent — the chat is a second reading of a
          session, not a second session. But hiding it with display:none is no
          better: the resize observer then measures a zero-size container, fit()
          derives a degenerate grid, and that size is sent to the pty, so
          opening the chat would reflow the agent to nothing.

          So both layers fill the same box and the terminal is only ever made
          invisible. visibility:hidden keeps its geometry, so it is still the
          size it will be when it comes back. */}
      <div className="relative flex min-h-0 flex-1 overflow-hidden">
        <div
          className={
            mode === "terminal"
              ? "absolute inset-0 flex min-w-0"
              : "invisible pointer-events-none absolute inset-0 flex min-w-0"
          }
        >
          <LivePane session={session} fontFamily={fontFamily} />
        </div>
        {mode === "chat" && (
          <div className="bg-background absolute inset-0 flex flex-col">
            <ChatView session={session} />
          </div>
        )}
      </div>
    </div>
  );
}
