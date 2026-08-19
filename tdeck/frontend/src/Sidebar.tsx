import { Loader2, MessageSquarePlus, Monitor, Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import type { SessionSummary } from "./api";
import type { ThemeMode } from "./theme";

// The conversations, and which one you are reading.
//
// This is the honest, minimal version of the thing tdeck.md warns about: a list
// of chats is not the deck. The deck's job is attention across a fleet — which
// of eighteen workspaces needs you — and "sessions in a sidebar" is not that.
// What it does carry is the one signal that matters while an agent is working:
// a session that is busy says so, so a chat you are not reading is not silent.
//
// Workspaces, projects and PR status come next, from awp's own state.

const themes: { mode: ThemeMode; icon: typeof Sun; label: string }[] = [
  { mode: "light", icon: Sun, label: "light" },
  { mode: "dark", icon: Moon, label: "dark" },
  { mode: "system", icon: Monitor, label: "system" },
];

export function Sidebar({
  sessions,
  chosen,
  onChoose,
  onNew,
  opening,
  theme,
  onTheme,
}: {
  sessions: SessionSummary[];
  chosen: string;
  onChoose: (sessionId: string) => void;
  onNew: () => void;
  opening: boolean;
  theme: ThemeMode;
  onTheme: (mode: ThemeMode) => void;
}) {
  return (
    <aside className="flex h-full min-w-0 flex-col">
      <div className="flex items-center gap-2 px-3 py-2">
        <span className="text-sm font-semibold">tdeck</span>
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground ml-auto size-7"
          aria-label="new chat"
          title="new chat"
          disabled={opening}
          onClick={onNew}
        >
          {opening ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <MessageSquarePlus className="size-4" />
          )}
        </Button>
      </div>
      <Separator />

      <ScrollArea className="min-h-0 flex-1">
        <ul className="flex flex-col gap-0.5 p-2">
          {sessions.map((session) => {
            const active = session.sessionId === chosen;
            return (
              <li key={session.sessionId}>
                <button
                  onClick={() => onChoose(session.sessionId)}
                  className={[
                    "flex w-full min-w-0 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left",
                    active ? "bg-muted" : "hover:bg-muted/50",
                  ].join(" ")}
                >
                  <span className="flex min-w-0 items-center gap-1.5">
                    {session.busy && (
                      <Loader2 className="text-muted-foreground size-3 shrink-0 animate-spin" />
                    )}
                    <span className="truncate text-sm">{session.title}</span>
                  </span>
                  {/* The directory the agent is working in — the closest thing
                      to a workspace until awp's own state is wired in. */}
                  <span className="text-muted-foreground truncate text-sm">
                    {session.cwd.split("/").slice(-2).join("/")}
                  </span>
                </button>
              </li>
            );
          })}
          {sessions.length === 0 && (
            <li className="text-muted-foreground px-2 py-1.5 text-sm">
              no conversations
            </li>
          )}
        </ul>
      </ScrollArea>

      <Separator />
      <div className="flex items-center gap-1 p-2">
        {themes.map(({ mode, icon: Icon, label }) => (
          <Button
            key={mode}
            variant={theme === mode ? "secondary" : "ghost"}
            size="icon"
            className="size-7"
            aria-label={label}
            title={label}
            onClick={() => onTheme(mode)}
          >
            <Icon className="size-3.5" />
          </Button>
        ))}
      </div>
    </aside>
  );
}
