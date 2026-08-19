import { useEffect, useState } from "react";
import { History as HistoryIcon, PanelRightClose } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import { api, type PastSession, type SessionSummary } from "./api";

// Every conversation the agent has for this directory, whether tdeck started it
// or not.
//
// This is the part that makes tdeck a view onto work rather than a place work
// happens: the sessions listed here mostly came from a terminal, and picking one
// up replays it. `session/list` filters by cwd, which is why this belongs beside
// a workspace — a workspace is a directory, and its history is what ran there.
//
// The refusal case is not what this document originally claimed. Resuming a
// session that is live in another client does not fail; it attaches, and two
// writers share one conversation. So the button says what it is doing and there
// is a way to let go again — see /close.

function when(iso: string): string {
  if (!iso) return "";
  const at = new Date(iso);
  const days = Math.floor((Date.now() - at.getTime()) / 86_400_000);
  if (days === 0)
    return at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (days === 1) return "yesterday";
  if (days < 7) return `${days} days ago`;
  return at.toLocaleDateString();
}

export function History({
  cwd,
  openIds,
  onResumed,
  onChoose,
  onClose,
}: {
  cwd: string;
  openIds: string[];
  onResumed: (session: SessionSummary) => void;
  onChoose: (sessionId: string) => void;
  onClose: () => void;
}) {
  const [past, setPast] = useState<PastSession[] | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    setPast(null);
    setError("");
    api
      .history(cwd)
      .then(setPast)
      .catch((err: unknown) =>
        setError(err instanceof Error ? err.message : String(err)),
      );
  }, [cwd]);

  const resume = async (entry: PastSession) => {
    setBusy(entry.sessionId);
    setError("");
    try {
      onResumed(await api.resume(entry));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  const open = new Set(openIds);

  return (
    <aside className="flex h-full min-w-0 flex-col">
      <div className="flex items-center gap-2 px-3 py-2">
        <HistoryIcon className="text-muted-foreground size-4" />
        <span className="text-sm font-medium">history</span>
        <span className="text-muted-foreground ml-auto truncate text-sm">
          {cwd.split("/").slice(-2).join("/")}
        </span>
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground size-7 shrink-0"
          aria-label="hide panel"
          title="hide panel"
          onClick={onClose}
        >
          <PanelRightClose className="size-4" />
        </Button>
      </div>
      <Separator />

      {error !== "" && (
        <p className="text-destructive px-3 py-2 text-sm">{error}</p>
      )}

      <ScrollArea className="min-h-0 flex-1">
        <div className="flex flex-col gap-0.5 p-2">
          {past === null && (
            <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
              <Spinner />
              loading
            </div>
          )}

          {past?.length === 0 && (
            <Empty className="py-6">
              <EmptyHeader>
                <EmptyTitle>No past conversations</EmptyTitle>
                <EmptyDescription>
                  Nothing has run in this directory yet.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}

          {past?.map((entry) => {
            const here = open.has(entry.sessionId);
            return (
              <Item key={entry.sessionId} size="sm" className="rounded-md">
                <ItemContent className="min-w-0">
                  <ItemTitle className="truncate font-normal">
                    {entry.title}
                  </ItemTitle>
                  <ItemDescription>{when(entry.updatedAt)}</ItemDescription>
                </ItemContent>
                {busy === entry.sessionId ? (
                  <ItemMedia variant="icon">
                    <Spinner />
                  </ItemMedia>
                ) : (
                  <ItemActions>
                    <Button
                      size="sm"
                      variant={here ? "secondary" : "ghost"}
                      onClick={() =>
                        here ? onChoose(entry.sessionId) : void resume(entry)
                      }
                    >
                      {here ? "open" : "resume"}
                    </Button>
                  </ItemActions>
                )}
              </Item>
            );
          })}
        </div>
      </ScrollArea>
    </aside>
  );
}
