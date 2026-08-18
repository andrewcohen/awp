import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { PatchDiff } from "@pierre/diffs/react";
import { ChevronRight, CircleAlert, CornerDownLeft } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import * as ChatBinding from "@bindings/chat";
import { sampleTurns } from "./sampleChat";

// The agent's conversation, from its transcript rather than from its screen.
//
// The pane shows what the agent drew — wrapped to a width, redrawn in place,
// everything above the viewport gone. The transcript is the record underneath,
// so the same session reads as turns: prose as prose, a diff as a diff, and a
// tool call as one line that opens rather than four hundred lines of output.

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

// How many turns are in the DOM at once.
//
// A transcript runs to hundreds of turns, some carrying syntax-highlighted
// diffs, and mounting all of them is what makes the view crawl — every append
// then re-reconciles the lot. Only the tail is rendered, which is the part
// anyone is reading; the rest is a click away.
const windowSize = 40;

// Diffs render unified and wrapped.
//
// Split needs two columns of code, and this column is one column of a window
// that also holds a sidebar — at that width the halves are too narrow to read
// and the rows clip. Wrapping rather than scrolling for the same reason: a
// horizontal scrollbar inside a message is a thing nobody finds.
const diffOptions = {
  diffStyle: "unified",
  overflow: "wrap",
  theme: { light: "github-light", dark: "github-dark" },
} as const;

function ToolRow({ tool }: { tool: ChatTool }) {
  // A diff is the one result worth showing before it is asked for: it is what
  // the agent did, where everything else is what it looked at.
  if (tool.Patch !== "") {
    return (
      <div className="border-border overflow-hidden rounded-lg border text-xs">
        <PatchDiff patch={tool.Patch} options={diffOptions} />
      </div>
    );
  }
  return (
    <Collapsible className="group/tool border-border rounded-lg border">
      <CollapsibleTrigger className="hover:bg-muted/40 flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left">
        <ChevronRight className="text-muted-foreground size-3.5 shrink-0 transition-transform group-data-[state=open]/tool:rotate-90" />
        <Badge variant="secondary" className="shrink-0 font-normal">
          {tool.Name}
        </Badge>
        <span className="text-muted-foreground truncate font-mono text-[11px]">{tool.Summary}</span>
        {tool.IsError && <CircleAlert className="text-destructive ml-auto size-3.5 shrink-0" />}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <Separator />
        <pre className="text-muted-foreground max-h-72 overflow-auto p-2.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
          {tool.Detail || "no output"}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

// Consecutive turns from the same speaker are one block.
//
// A single answer is often several transcript lines — text, then a tool, then
// more text — and labelling each one "agent" turns a paragraph into a stack of
// headed boxes. The transcript's line breaks are an artefact of how the agent
// streamed, not of how it spoke.
type Block = { kind: string; at: string; turns: ChatTurn[] };

function group(turns: ChatTurn[]): Block[] {
  const blocks: Block[] = [];
  for (const turn of turns) {
    const last = blocks[blocks.length - 1];
    if (last && last.kind === turn.Kind) {
      last.turns.push(turn);
    } else {
      blocks.push({ kind: turn.Kind, at: turn.At, turns: [turn] });
    }
  }
  return blocks;
}

const Block = memo(function Block({ block }: { block: Block }) {
  const mine = block.kind === "user";
  const when = block.at
    ? new Date(block.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : "";

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="text-muted-foreground flex items-baseline gap-2 text-[11px]">
        <span className="text-foreground/70 font-medium">{mine ? "you" : "agent"}</span>
        {when && <span>{when}</span>}
      </div>

      <div
        className={
          mine
            ? "bg-muted/60 border-border w-fit max-w-[46rem] rounded-lg border px-3 py-2"
            : "flex max-w-[52rem] min-w-0 flex-col gap-2"
        }
      >
        {block.turns.map((turn, i) => (
          <div key={i} className="flex min-w-0 flex-col gap-2">
            {turn.Text !== "" && (
              <p className="text-[13px] leading-relaxed whitespace-pre-wrap">{turn.Text}</p>
            )}
            {turn.Thinking !== "" && (
              <Collapsible className="group/think">
                <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-[11px]">
                  <ChevronRight className="size-3 transition-transform group-data-[state=open]/think:rotate-90" />
                  thinking
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <p className="text-muted-foreground border-border mt-1 border-l pl-3 text-[11px] leading-relaxed whitespace-pre-wrap">
                    {turn.Thinking}
                  </p>
                </CollapsibleContent>
              </Collapsible>
            )}
            {(turn.Tools ?? []).map((tool, j) => (
              <ToolRow key={`${tool.Name}-${j}`} tool={tool} />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
});

// The composer owns its draft, so typing does not re-render the conversation.
function Composer({ onSend }: { onSend: (text: string) => void }) {
  const [draft, setDraft] = useState("");
  const box = useRef<HTMLTextAreaElement | null>(null);

  const send = () => {
    const text = draft.trim();
    if (text !== "") {
      setDraft("");
      onSend(text);
    }
  };

  return (
    <div className="border-border bg-background focus-within:ring-ring/40 relative max-w-[52rem] rounded-xl border shadow-sm focus-within:ring-2">
      <Textarea
        ref={box}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          // Enter sends, shift+enter breaks the line — the convention every chat
          // uses, and the opposite of the terminal tab, where enter is whatever
          // the program says it is.
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            send();
          }
        }}
        rows={2}
        placeholder="message the agent"
        className="max-h-40 min-h-16 resize-none border-0 bg-transparent pr-24 text-[13px] shadow-none focus-visible:ring-0"
      />
      <div className="absolute right-2 bottom-2 flex items-center gap-2">
        <span className="text-muted-foreground hidden text-[11px] sm:inline">
          <CornerDownLeft className="mr-1 inline size-3" />
          to send
        </span>
        <Button onClick={send} disabled={draft.trim() === ""} size="sm">
          Send
        </Button>
      </div>
    </div>
  );
}

export function ChatView({ session }: { session: string }) {
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [pending, setPending] = useState<string[]>([]);
  const [showAll, setShowAll] = useState(false);
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement | null>(null);
  const mock = session === "";

  const load = useCallback(() => {
    if (session === "") {
      setTurns(sampleTurns as ChatTurn[]);
      return;
    }
    ChatBinding.Turns(session).then(
      (rows) => {
        const next = (rows as ChatTurn[]) ?? [];
        setTurns((prev) => {
          // Only replace when something actually changed. The transcript is
          // stat'd every second while an agent works, and swapping in an equal
          // array re-renders every block for nothing.
          const same =
            prev.length === next.length &&
            prev[prev.length - 1]?.At === next[next.length - 1]?.At &&
            prev[prev.length - 1]?.Text === next[next.length - 1]?.Text;
          return same ? prev : next;
        });
        // A message shows locally the moment it is sent, and is dropped once it
        // comes back in the transcript — which is the only place it becomes
        // real. Matching on text rather than an id because the transcript has
        // no idea this client wrote it.
        setPending((waiting) =>
          waiting.filter((text) => !next.some((t) => t.Kind === "user" && t.Text.includes(text))),
        );
        setError("");
      },
      (err: unknown) => setError(err instanceof Error ? err.message : String(err)),
    );
  }, [session]);

  useEffect(() => {
    load();
    if (mock) {
      return;
    }
    const off = Events.On("chat:changed", load);
    void ChatBinding.Follow(session).catch(() => {
      // A session with no transcript still renders; it just will not update.
    });
    return () => {
      off();
      ChatBinding.Unfollow();
    };
  }, [session, load, mock]);

  const blocks = useMemo(() => {
    const all = group(turns);
    return showAll ? all : all.slice(-windowSize);
  }, [turns, showAll]);

  const hidden = useMemo(() => Math.max(0, group(turns).length - windowSize), [turns]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [blocks, pending]);

  const send = useCallback(
    (text: string) => {
      setPending((waiting) => [...waiting, text]);
      ChatBinding.Say(session, text).catch((err: unknown) => {
        setPending((waiting) => waiting.filter((t) => t !== text));
        setError(err instanceof Error ? err.message : String(err));
      });
    },
    [session],
  );

  // min-w-0 throughout: this column and the terminal share one box, so content
  // wide enough to stretch the flex container stretches the box the pane
  // measures itself against — and the pane sends its size to the pty.
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <ScrollArea className="min-h-0 min-w-0 flex-1">
        <div className="flex min-w-0 flex-col gap-6 px-1 pr-4 pb-2">
          {error !== "" && <p className="text-destructive text-[13px]">{error}</p>}
          {turns.length === 0 && error === "" && (
            <p className="text-muted-foreground text-[13px]">no transcript for this session yet</p>
          )}
          {!showAll && hidden > 0 && (
            <Button variant="ghost" size="sm" className="self-start" onClick={() => setShowAll(true)}>
              show {hidden} earlier {hidden === 1 ? "block" : "blocks"}
            </Button>
          )}
          {blocks.map((block, i) => (
            <Block key={`${block.at}-${i}`} block={block} />
          ))}
          {pending.map((text) => (
            // Shown dimmed until the transcript confirms it: the agent has been
            // told, and the record has not caught up.
            <div key={text} className="flex min-w-0 flex-col gap-2 opacity-60">
              <div className="text-muted-foreground text-[11px]">
                <span className="text-foreground/70 font-medium">you</span> · sending
              </div>
              <div className="bg-muted/60 border-border w-fit max-w-[46rem] rounded-lg border px-3 py-2">
                <p className="text-[13px] leading-relaxed whitespace-pre-wrap">{text}</p>
              </div>
            </div>
          ))}
          <div ref={bottom} />
        </div>
      </ScrollArea>
      <Composer onSend={send} />
    </div>
  );
}
