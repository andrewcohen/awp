import { useMemo } from "react";
import { MessageSquarePlus, Monitor, Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import {
  Item,
  ItemContent,
  ItemDescription,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import { Badge } from "@/components/ui/badge";
import type { SessionSummary, Workspace } from "./api";
import type { ThemeMode } from "./theme";

// awp's workspaces, and the conversations open on them.
//
// tdeck.md's warning was that a list of chats is not the deck: the deck's job is
// attention across a fleet — which of eighteen workspaces needs you — and
// "sessions in a sidebar" is not that. So the fleet is the primary structure
// here and conversations hang off it, rather than the other way round.
//
// This is still only half the job. The rows say what each agent is doing, which
// is what awp's own state already knows; they do not yet sort or group by what
// needs *you*. That is the interesting problem, and having the data on screen
// does not solve it.

// awp's status vocabulary, passed through from the store rather than remapped.
// A second spelling of these would be a second answer to "what is this agent
// doing", and the two would disagree eventually.
const statusColor: Record<string, string> = {
  working: "bg-emerald-500",
  waiting: "bg-amber-500",
  error: "bg-rose-500",
  starting: "bg-sky-500",
  idle: "bg-muted-foreground/40",
  exited: "bg-muted-foreground/25",
};

function StatusDot({ status }: { status: string }) {
  return (
    <span
      title={status}
      aria-label={status}
      className={`size-2 rounded-full ${statusColor[status] ?? statusColor.idle}`}
    />
  );
}

const themes: { mode: ThemeMode; icon: typeof Sun; label: string }[] = [
  { mode: "light", icon: Sun, label: "light" },
  { mode: "dark", icon: Moon, label: "dark" },
  { mode: "system", icon: Monitor, label: "system" },
];

export function Sidebar({
  workspaces,
  sessions,
  chosen,
  onChoose,
  onOpenWorkspace,
  onInspect,
  onNew,
  opening,
  theme,
  onTheme,
}: {
  workspaces: Workspace[];
  sessions: SessionSummary[];
  chosen: string;
  onChoose: (sessionId: string) => void;
  onOpenWorkspace: (workspace: Workspace) => void;
  // A workspace whose agent is already running in a terminal. Clicking shows
  // what is there rather than starting a second agent in the same checkout.
  onInspect: (workspace: Workspace) => void;
  onNew: () => void;
  opening: boolean;
  theme: ThemeMode;
  onTheme: (mode: ThemeMode) => void;
}) {
  // Grouped by project, because a workspace name alone is ambiguous — half of
  // them are called "default".
  const projects = useMemo(() => {
    const groups = new Map<string, Workspace[]>();
    for (const workspace of workspaces) {
      const group = groups.get(workspace.project);
      if (group) group.push(workspace);
      else groups.set(workspace.project, [workspace]);
    }
    return [...groups.entries()];
  }, [workspaces]);

  // Conversations with no workspace behind them — a scratch chat, or one opened
  // in a directory awp does not know about. Without this they would vanish from
  // the sidebar entirely once workspaces became the organising structure.
  const loose = useMemo(() => {
    const known = new Set(workspaces.map((w) => w.sessionId).filter(Boolean));
    return sessions.filter((s) => !known.has(s.sessionId));
  }, [workspaces, sessions]);

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
          {opening ? <Spinner /> : <MessageSquarePlus className="size-4" />}
        </Button>
      </div>
      <Separator />

      <ScrollArea className="min-h-0 flex-1">
        <div className="flex flex-col gap-3 p-2">
          {projects.map(([project, rows]) => (
            <div key={project} className="flex flex-col gap-0.5">
              <h2 className="text-muted-foreground px-2 py-1 text-sm font-medium">
                {project}
              </h2>
              <ul className="flex flex-col gap-0.5">
                {rows.map((workspace) => {
                  const active =
                    workspace.sessionId !== undefined &&
                    workspace.sessionId === chosen;
                  return (
                    <li key={`${workspace.projectPath}/${workspace.name}`}>
                      <Item
                        size="sm"
                        className={[
                          "w-full rounded-md text-left",
                          active ? "bg-muted" : "hover:bg-muted/50",
                        ].join(" ")}
                        render={
                          <button
                            onClick={() => {
                              if (workspace.sessionId)
                                return onChoose(workspace.sessionId);
                              // An agent is already working here. Opening a
                              // chat would put a second one in the same
                              // checkout, so this shows the directory's
                              // conversations instead and lets the choice be
                              // deliberate.
                              if (workspace.terminalAgent)
                                return onInspect(workspace);
                              onOpenWorkspace(workspace);
                            }}
                          />
                        }
                      >
                        <ItemMedia variant="icon">
                          <StatusDot status={workspace.status} />
                        </ItemMedia>
                        <ItemContent className="min-w-0">
                          <ItemTitle className="truncate font-normal">
                            {workspace.displayName}
                          </ItemTitle>
                          {/* What it is stuck on, when it is stuck on you. This
                              is the row's most useful line whenever it exists,
                              so it displaces the bookmark rather than sitting
                              beside it. */}
                          {workspace.waitingOn ? (
                            <ItemDescription className="text-amber-600 truncate dark:text-amber-400">
                              needs you: {workspace.waitingOn}
                            </ItemDescription>
                          ) : (
                            (workspace.bookmark !== "" ||
                              workspace.prNumber > 0) && (
                              <ItemDescription className="truncate">
                                {workspace.prNumber > 0 &&
                                  `#${workspace.prNumber} `}
                                {workspace.bookmark}
                              </ItemDescription>
                            )
                          )}
                        </ItemContent>
                        {/* Two different facts, deliberately not merged. The
                            dot is what the agent is doing; these say who is
                            holding it. */}
                        {workspace.terminalAgent && (
                          <Badge
                            variant="secondary"
                            className="shrink-0 font-normal"
                            title="an agent is running here in a terminal"
                          >
                            term
                          </Badge>
                        )}
                        {workspace.sessionId !== undefined && (
                          <ItemMedia variant="icon">
                            <span className="bg-primary/60 size-1.5 rounded-full" />
                          </ItemMedia>
                        )}
                      </Item>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}

          {loose.length > 0 && (
            <div className="flex flex-col gap-0.5">
              <h2 className="text-muted-foreground px-2 py-1 text-sm font-medium">
                other
              </h2>
              <ul className="flex flex-col gap-0.5">
                {loose.map((session) => (
                  <li key={session.sessionId}>
                    <Item
                      size="sm"
                      className={[
                        "w-full rounded-md text-left",
                        session.sessionId === chosen
                          ? "bg-muted"
                          : "hover:bg-muted/50",
                      ].join(" ")}
                      render={
                        <button onClick={() => onChoose(session.sessionId)} />
                      }
                    >
                      {session.busy && (
                        <ItemMedia variant="icon">
                          <Spinner className="text-muted-foreground size-3" />
                        </ItemMedia>
                      )}
                      <ItemContent className="min-w-0">
                        <ItemTitle className="truncate font-normal">
                          {session.title}
                        </ItemTitle>
                        <ItemDescription className="truncate">
                          {session.cwd.split("/").slice(-2).join("/")}
                        </ItemDescription>
                      </ItemContent>
                    </Item>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {projects.length === 0 && loose.length === 0 && (
            <Empty className="py-6">
              <EmptyHeader>
                <EmptyTitle>Nothing to show</EmptyTitle>
                <EmptyDescription>
                  No awp workspaces and no conversations yet.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </div>
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
