import { useState } from "react";
import { MessageSquare, SquareTerminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { LivePane } from "./LivePane";
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

export function AgentPane({ session, fontFamily }: { session: string; fontFamily: string }) {
  const [mode, setMode] = useState<Mode>("terminal");

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex items-center gap-1">
        {modes.map(({ id, label, icon: Icon }) => (
          <Button
            key={id}
            size="sm"
            variant={mode === id ? "secondary" : "ghost"}
            onClick={(e) => {
              setMode(id);
              // The terminal takes focus when it mounts; a button that keeps it
              // would swallow the first keystroke after the switch.
              e.currentTarget.blur();
            }}
          >
            <Icon className="size-4" />
            {label}
          </Button>
        ))}
        <span className="text-muted-foreground ml-2 truncate text-xs">{session}</span>
      </div>

      {/* The terminal stays mounted while the chat is on top of it.
          Unmounting would detach the pty and re-attach on the way back, which
          costs a reflow of the agent each way — and the whole argument for the
          chat tab is that it is a second view of a session, not a second
          session. Hidden rather than conditional, so switching tabs is free. */}
      <div className={mode === "terminal" ? "flex min-h-0 flex-1" : "hidden"}>
        <LivePane session={session} fontFamily={fontFamily} />
      </div>
      {mode === "chat" && <ChatView session={session} />}
    </div>
  );
}
