import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { PatchDiff } from "@pierre/diffs/react";
import { ChevronRight, CircleAlert, Send } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import * as ChatBinding from "@bindings/chat";

// The agent's conversation, from its transcript rather than from its screen.
//
// This is the half of the surface a terminal cannot do. The pane shows what the
// agent drew — wrapped to a width, redrawn in place, with everything above the
// viewport gone. The transcript is the record underneath, so the same session
// can be read as turns: prose as prose, a diff as a diff, and a tool call as one
// line that opens rather than four hundred lines of output nobody asked for.
//
// It is a second view of one session, not a second session. Sending from here
// goes to the same agent the terminal tab is attached to, so the two tabs are
// two readings of one conversation.

type ChatTool = {
  Name: string;
  Summary: string;
  Detail: string;
  IsError: boolean;
  File: string;
  Patch: string;
};

type ChatTurn = {
  Kind: string;
  At: string;
  Text: string;
  Thinking: string;
  Tools: ChatTool[];
};

function Collapsible({
  label,
  tone,
  children,
}: {
  label: React.ReactNode;
  tone?: "muted" | "danger";
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border-border rounded-md border">
      <button
        onClick={() => setOpen((v) => !v)}
        className="hover:bg-muted/50 flex w-full items-center gap-2 px-2 py-1 text-left text-xs"
      >
        <ChevronRight className={`size-3 shrink-0 transition-transform ${open ? "rotate-90" : ""}`} />
        <span className={tone === "danger" ? "text-destructive" : "text-muted-foreground"}>
          {label}
        </span>
      </button>
      {open && <div className="border-border border-t p-2">{children}</div>}
    </div>
  );
}

function Tool({ tool }: { tool: ChatTool }) {
  // A diff is the one tool result worth showing before it is asked for: it is
  // what the agent did, where everything else is what it looked at.
  if (tool.Patch !== "") {
    return (
      <div className="border-border overflow-hidden rounded-md border">
        <div className="text-muted-foreground bg-muted/40 flex items-center gap-2 px-2 py-1 text-xs">
          <Badge variant="outline">{tool.Name}</Badge>
          <span className="truncate">{tool.File}</span>
        </div>
        <div className="overflow-x-auto text-xs">
          <PatchDiff patch={tool.Patch} />
        </div>
      </div>
    );
  }
  return (
    <Collapsible
      tone={tool.IsError ? "danger" : "muted"}
      label={
        <span className="flex min-w-0 items-center gap-2">
          <Badge variant="outline" className="shrink-0">
            {tool.Name}
          </Badge>
          <span className="truncate font-mono">{tool.Summary}</span>
          {tool.IsError && <CircleAlert className="size-3 shrink-0" />}
        </span>
      }
    >
      <pre className="max-h-80 overflow-auto text-xs whitespace-pre-wrap">
        {tool.Detail || "no output"}
      </pre>
    </Collapsible>
  );
}

function Turn({ turn }: { turn: ChatTurn }) {
  const mine = turn.Kind === "user";
  return (
    <div className="flex flex-col gap-2">
      <div className="text-muted-foreground text-xs">{mine ? "you" : "agent"}</div>
      {turn.Text !== "" && (
        <div
          className={
            mine
              ? "bg-muted rounded-md px-3 py-2 text-sm whitespace-pre-wrap"
              : "text-sm whitespace-pre-wrap"
          }
        >
          {turn.Text}
        </div>
      )}
      {turn.Thinking !== "" && (
        // Collapsed by default: useful when an agent is stuck, noise otherwise.
        <Collapsible label="thinking">
          <p className="text-muted-foreground text-xs whitespace-pre-wrap">{turn.Thinking}</p>
        </Collapsible>
      )}
      {turn.Tools.map((tool, i) => (
        <Tool key={`${tool.Name}-${i}`} tool={tool} />
      ))}
    </div>
  );
}

export function ChatView({ session }: { session: string }) {
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const bottom = useRef<HTMLDivElement | null>(null);

  const load = useCallback(() => {
    ChatBinding.Turns(session).then(
      (rows) => {
        setTurns(rows as ChatTurn[]);
        setError("");
      },
      (err: unknown) => setError(err instanceof Error ? err.message : String(err)),
    );
  }, [session]);

  useEffect(() => {
    load();
    // The transcript is appended to constantly while an agent works, so Go
    // reports that it changed and this re-reads. Pushing parsed turns on every
    // write would send the whole conversation dozens of times a minute.
    const off = Events.On("chat:changed", load);
    void ChatBinding.Follow(session).catch(() => {
      // A session with no transcript still renders; it just will not update.
    });
    return () => {
      off();
      ChatBinding.Unfollow();
    };
  }, [session, load]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [turns]);

  const send = () => {
    const text = draft.trim();
    if (text === "") {
      return;
    }
    setDraft("");
    ChatBinding.Say(session, text).catch((err: unknown) =>
      setError(err instanceof Error ? err.message : String(err)),
    );
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <ScrollArea className="min-h-0 flex-1">
        <div className="flex flex-col gap-6 p-1 pr-3">
          {error !== "" && <p className="text-destructive text-sm">{error}</p>}
          {turns.length === 0 && error === "" && (
            <p className="text-muted-foreground text-sm">no transcript for this session yet</p>
          )}
          {turns.map((turn, i) => (
            <Turn key={`${turn.At}-${i}`} turn={turn} />
          ))}
          <div ref={bottom} />
        </div>
      </ScrollArea>

      <div className="flex items-end gap-2">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends, shift+enter breaks the line — the convention every
            // chat uses, and the opposite of the terminal tab, where enter is
            // whatever the program says it is.
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          rows={2}
          placeholder="send to the agent…"
          className="border-border bg-background focus-visible:ring-ring min-h-0 flex-1 resize-none rounded-md border px-3 py-2 text-sm focus-visible:ring-1 focus-visible:outline-none"
        />
        <Button onClick={send} disabled={draft.trim() === ""} size="icon" aria-label="send">
          <Send className="size-4" />
        </Button>
      </div>
    </div>
  );
}
