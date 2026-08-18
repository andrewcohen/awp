import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Monitor, Moon, Sun } from "lucide-react";
import type { ThemeMode } from "./theme";

// One row per workspace, from zmx's live sessions rather than from deckdata.
// Which workspaces exist on disk and which of them want attention is deckdata's
// question and a different one — this list only has to name something live to
// attach to. See Panes.Workspaces.
export type Workspace = {
  Project: string;
  Workspace: string;
  Agent: string;
  Cmd: string;
  Others: number;
};

type Props = {
  workspaces: Workspace[];
  chosen: string;
  onChoose: (id: string) => void;
  theme: ThemeMode;
  onTheme: (mode: ThemeMode) => void;
};

// Follow the OS, or override it. Icons rather than a label because the row is
// three choices wide and "system" is the only one whose name is not obvious
// from its picture.
const themes: { mode: ThemeMode; icon: typeof Sun; label: string }[] = [
  { mode: "system", icon: Monitor, label: "follow the system" },
  { mode: "light", icon: Sun, label: "light" },
  { mode: "dark", icon: Moon, label: "dark" },
];

const views = [
  { id: "", label: "static pane", note: "hardcoded bytes" },
  { id: "artifact", label: "artifact", note: "sandboxed agent HTML" },
];

export function Sidebar({
  workspaces,
  chosen,
  onChoose,
  theme,
  onTheme,
}: Props) {
  // Clicking a row is asking to work in that workspace, so the pane takes the
  // keyboard on mount — but a clicked button keeps focus, and would take it
  // straight back. Releasing it here is the other half of that.
  const choose = (id: string) => (event: React.MouseEvent<HTMLButtonElement>) => {
    onChoose(id);
    event.currentTarget.blur();
  };

  return (
    <nav className="border-border flex h-full min-w-0 flex-col border-r">
      <ScrollArea className="flex-1">
        <div className="flex flex-col gap-0.5 p-2">
          {views.map((v) => (
            <Button
              key={v.id}
              variant={v.id === chosen ? "secondary" : "ghost"}
              onClick={choose(v.id)}
              className="h-auto w-full flex-col items-start gap-0 px-3 py-2 text-left"
            >
              <span className="text-sm">{v.label}</span>
              <span className="text-muted-foreground text-xs">{v.note}</span>
            </Button>
          ))}

          <Separator className="my-2" />

          {/* Attaching is not looking. A zmx session takes its size from the
              client attached to it, so clicking a row reflows that agent to
              this pane's shape and reflows it back on close. Said before the
              click, because after it the damage is already on someone's
              screen. */}
          <p className="text-muted-foreground px-3 pb-1 text-xs">
            attaching resizes the session to this pane
          </p>

          {workspaces.length === 0 && (
            <p className="text-muted-foreground px-3 py-2 text-sm">no awp workspaces running</p>
          )}

          {workspaces.map((w) => {
            const idle = w.Agent === "";
            return (
              <Button
                key={`${w.Project}/${w.Workspace}`}
                variant={w.Agent !== "" && w.Agent === chosen ? "secondary" : "ghost"}
                // Idle rows only. A workspace with no live agent has nothing to
                // attach to, and a row that looks clickable and does nothing is
                // worse than one that says why.
                disabled={idle}
                onClick={choose(w.Agent)}
                className="h-auto w-full flex-col items-start gap-0 px-3 py-2 text-left"
              >
                <span className="flex w-full items-center gap-2">
                  <span className="text-primary truncate text-xs">{w.Project}</span>
                  <span className="truncate text-sm">{w.Workspace}</span>
                  {w.Others > 0 && (
                    <Badge variant="outline" className="ml-auto shrink-0">
                      +{w.Others}
                    </Badge>
                  )}
                </span>
                <span className="text-muted-foreground w-full truncate text-xs">
                  {idle ? "no live agent" : w.Cmd || "—"}
                </span>
              </Button>
            );
          })}
        </div>
      </ScrollArea>

      <Separator />

      <div className="flex items-center gap-1 px-2 pt-2">
        {themes.map(({ mode, icon: Icon, label }) => (
          <Button
            key={mode}
            variant={theme === mode ? "secondary" : "ghost"}
            size="icon"
            title={label}
            aria-label={label}
            onClick={() => onTheme(mode)}
          >
            <Icon className="size-4" />
          </Button>
        ))}
      </div>

    </nav>
  );
}
