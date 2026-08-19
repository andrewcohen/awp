import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { PatchDiff } from "@pierre/diffs/react";
import { ChevronRight, CircleAlert, CornerDownLeft, Paperclip } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import * as ChatBinding from "@bindings/chat";
import { manyTurns, sampleTurns } from "./sampleChat";
import { Markdown } from "./Markdown";

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

// WhenVisible mounts its child the first time it comes near the viewport, and
// leaves it mounted after.
//
// A diff is a syntax-highlighted DOM tree, and a screenful of chat can hold
// several. Building them all up front is what makes scrolling stutter: the work
// is not the scroll, it is the layout of nodes nobody has looked at yet. Once
// built, a diff stays — unmounting on the way past would rebuild it every time
// the reader scrolled back, which is worse than holding it.
function WhenVisible({ estimate, children }: { estimate: number; children: React.ReactNode }) {
  const [seen, setSeen] = useState(false);
  const slot = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (seen || !slot.current) {
      return;
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setSeen(true);
        }
      },
      // A screen of margin, so a diff is ready by the time it is reached rather
      // than popping in under the cursor.
      { rootMargin: "600px 0px" },
    );
    io.observe(slot.current);
    return () => io.disconnect();
  }, [seen]);

  return (
    <div ref={slot} style={seen ? undefined : { minHeight: estimate }}>
      {seen ? children : null}
    </div>
  );
}

function ToolRow({ tool }: { tool: ChatTool }) {
  // A diff is the one result worth showing before it is asked for: it is what
  // the agent did, where everything else is what it looked at.
  if (tool.Patch !== "") {
    const body = tool.Patch.split("\n").slice(2);
    const added = body.filter((l) => l.startsWith("+")).length;
    const removed = body.filter((l) => l.startsWith("-")).length;
    // A diff is a citation inside a conversation, not the document. Left
    // unbounded a 200-line edit is a page of chat that has to be scrolled past
    // to reach what the agent said next — so it gets a fixed frame and scrolls
    // inside it, the way a quoted block does.
    return (
      <div className="border-border overflow-hidden rounded-lg border">
        <div className="text-muted-foreground bg-muted/40 flex items-center gap-2 border-b px-2.5 py-1.5 text-xs">
          <span className="truncate font-mono">{tool.File}</span>
          <span className="ml-auto shrink-0 font-mono">
            <span className="text-emerald-600 dark:text-emerald-400">+{added}</span>{" "}
            <span className="text-rose-600 dark:text-rose-400">−{removed}</span>
          </span>
        </div>
        <div className="max-h-64 overflow-auto text-xs">
          <WhenVisible estimate={Math.min(256, 24 + body.length * 18)}>
            <PatchDiff patch={tool.Patch} options={diffOptions} renderCustomHeader={() => null} />
          </WhenVisible>
        </div>
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
        <span className="text-muted-foreground truncate font-mono text-xs">{tool.Summary}</span>
        {tool.IsError && <CircleAlert className="text-destructive ml-auto size-3.5 shrink-0" />}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <Separator />
        <pre className="text-muted-foreground max-h-72 overflow-auto p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap">
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
    // content-visibility lets the engine skip layout and paint for blocks that
    // are scrolled out of view, which is most of them. The intrinsic size is a
    // guess at a block's height so the scrollbar does not lurch as real heights
    // replace it.
    <div
      className="flex min-w-0 flex-col gap-2"
      style={{ contentVisibility: "auto", containIntrinsicSize: "auto 180px" }}
    >
      <div className="text-muted-foreground flex items-baseline gap-2 text-xs">
        <span className="text-foreground/70 font-medium">{mine ? "you" : "agent"}</span>
        {when && <span>{when}</span>}
      </div>

      <div
        className={
          mine
            ? "bg-muted/60 border-border w-fit max-w-full rounded-lg border px-3 py-2"
            : "flex max-w-full min-w-0 flex-col gap-2"
        }
      >
        {block.turns.map((turn, i) => (
          <div key={i} className="flex min-w-0 flex-col gap-2">
            {turn.Text !== "" &&
              (mine ? (
                <p className="text-sm leading-relaxed whitespace-pre-wrap">{turn.Text}</p>
              ) : (
                <Markdown text={turn.Text} />
              ))}
            {turn.Thinking !== "" && (
              <Collapsible className="group/think">
                <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs">
                  <ChevronRight className="size-3 transition-transform group-data-[state=open]/think:rotate-90" />
                  thinking
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <p className="text-muted-foreground border-border mt-1 border-l pl-3 text-xs leading-relaxed whitespace-pre-wrap">
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
  const [attached, setAttached] = useState<string[]>([]);
  const box = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    // Go forwards the paths and says which element they landed on; anything
    // dropped elsewhere belongs to another view.
    const off = Events.On("files:dropped", (event: { data: { paths: string[]; target: string } }) => {
      const { paths, target } = event.data ?? { paths: [], target: "" };
      if (target !== "chat-drop" || !paths?.length) {
        return;
      }
      setAttached((have) => [...have, ...paths.filter((p) => !have.includes(p))]);
      box.current?.focus();
    });
    return off;
  }, []);

  const send = () => {
    const text = draft.trim();
    if (text === "" && attached.length === 0) {
      return;
    }
    // Paths, not contents. The agent has a filesystem and a Read tool; sending
    // it a path is how a screenshot becomes something it can look at, and the
    // alternative — base64 through a line editor — is not a thing a terminal
    // can carry.
    const message = [...attached, text].filter(Boolean).join("\n");
    setDraft("");
    setAttached([]);
    onSend(message);
  };

  return (
    <div
      // The drop target. Marked with the attribute Wails looks for, and given
      // an id so the Go side can say which element a drop landed on.
      id="chat-drop"
      data-file-drop-target
      className="border-border bg-background focus-within:ring-ring/40 relative rounded-xl border shadow-sm focus-within:ring-2"
    >
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
        className="max-h-40 min-h-16 resize-none border-0 bg-transparent pr-24 text-sm shadow-none focus-visible:ring-0"
      />
      {attached.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-3 pt-2.5">
          {attached.map((path) => (
            <Badge key={path} variant="secondary" className="max-w-full gap-1 font-normal">
              <Paperclip className="size-3 shrink-0" />
              <span className="truncate">{path.split("/").pop()}</span>
              <button
                className="text-muted-foreground hover:text-foreground ml-0.5"
                aria-label={`remove ${path}`}
                onClick={() => setAttached((have) => have.filter((p) => p !== path))}
              >
                ×
              </button>
            </Badge>
          ))}
        </div>
      )}
      <div className="absolute right-2 bottom-2 flex items-center gap-2">
        <span className="text-muted-foreground hidden text-xs sm:inline">
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

  // What this view already has. A ref rather than state: it is a position in a
  // stream, not something rendered, and making it state would re-run the loader
  // that sets it.
  const have = useRef(0);

  const load = useCallback(() => {
    if (session === "") {
      const want = Number(new URLSearchParams(location.search).get("mock")) || 0;
      setTurns((want > 1 ? manyTurns(want) : sampleTurns) as ChatTurn[]);
      return;
    }
    // Only what is new crosses the bridge. Asking for the whole conversation
    // once a second is what made this slow — a long transcript is megabytes of
    // JSON, and almost all of it was already on screen.
    ChatBinding.Turns(session, have.current).then(
      (since) => {
        const fresh = (since.Turns as ChatTurn[]) ?? [];
        if (since.Reset) {
          have.current = since.Total;
          setTurns(fresh);
        } else if (fresh.length > 0) {
          have.current = since.Total;
          setTurns((prev) => [...prev, ...fresh]);
        }
        // A message shows locally the moment it is sent, and is dropped once it
        // comes back in the transcript — which is the only place it becomes
        // real. Matching on text rather than an id because the transcript has
        // no idea this client wrote it.
        if (fresh.length > 0) {
          setPending((waiting) =>
            waiting.filter((text) => !fresh.some((t) => t.Kind === "user" && t.Text.includes(text))),
          );
        }
        setError("");
      },
      (err: unknown) => setError(err instanceof Error ? err.message : String(err)),
    );
  }, [session]);

  useEffect(() => {
    have.current = 0;
    setTurns([]);
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
        <div className="mx-auto flex w-full max-w-[60rem] min-w-0 flex-col gap-6 px-4 pb-2">
          {error !== "" && <p className="text-destructive text-sm">{error}</p>}
          {turns.length === 0 && error === "" && (
            <p className="text-muted-foreground text-sm">no transcript for this session yet</p>
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
              <div className="text-muted-foreground text-xs">
                <span className="text-foreground/70 font-medium">you</span> · sending
              </div>
              <div className="bg-muted/60 border-border w-fit max-w-full rounded-lg border px-3 py-2">
                <p className="text-sm leading-relaxed whitespace-pre-wrap">{text}</p>
              </div>
            </div>
          ))}
          <div ref={bottom} />
        </div>
      </ScrollArea>
      <div className="mx-auto w-full max-w-[60rem] px-4">
        <Composer onSend={send} />
      </div>
    </div>
  );
}
