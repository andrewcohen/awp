import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PatchDiff } from "@pierre/diffs/react";
import {
  ChevronRight,
  CircleAlert,
  CornerDownLeft,
  Loader2,
  Paperclip,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import { api, type SessionSummary } from "./api";
import {
  apply,
  emptyConversation,
  type Conversation,
  type Tool,
  type Turn,
} from "./turns";
import { Markdown } from "./Markdown";

// The agent's conversation, driven by the agent rather than read from its logs.
//
// gdeck built this same view over the Claude Code transcript, which was a record
// and not a monitor: it gained a line after something finished, its thinking
// blocks were written empty, and rendering it meant shipping the whole
// conversation every time one line changed. This one is fed by the agent's own
// event stream, so thinking has content, a tool appears as it starts, and what
// crosses the wire is a token rather than a transcript.

// How many blocks are in the DOM at once.
//
// A long conversation runs to hundreds, some carrying syntax-highlighted diffs,
// and mounting all of them is what makes the view crawl — every append then
// re-reconciles the lot. Only the tail is rendered; the rest is a click away.
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
function WhenVisible({
  estimate,
  children,
}: {
  estimate: number;
  children: React.ReactNode;
}) {
  const [seen, setSeen] = useState(false);
  const slot = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (seen || !slot.current) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setSeen(true);
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

const ToolRow = memo(function ToolRow({ tool }: { tool: Tool }) {
  // A diff is the one result worth showing before it is asked for: it is what
  // the agent did, where everything else is what it looked at.
  if (tool.patch !== "") {
    const body = tool.patch.split("\n").slice(2);
    const added = body.filter((l) => l.startsWith("+")).length;
    const removed = body.filter((l) => l.startsWith("-")).length;
    // A diff is a citation inside a conversation, not the document. Left
    // unbounded a 200-line edit is a page of chat that has to be scrolled past
    // to reach what the agent said next — so it gets a fixed frame and scrolls
    // inside it, the way a quoted block does.
    return (
      <div className="border-border overflow-hidden rounded-lg border">
        <div className="text-muted-foreground bg-muted/40 flex items-center gap-2 border-b px-2.5 py-1.5 text-xs">
          <span className="truncate font-mono">{tool.file}</span>
          <span className="ml-auto shrink-0 font-mono">
            <span className="text-emerald-600 dark:text-emerald-400">
              +{added}
            </span>{" "}
            <span className="text-rose-600 dark:text-rose-400">−{removed}</span>
          </span>
        </div>
        <div className="max-h-64 overflow-auto text-xs">
          <WhenVisible estimate={Math.min(256, 24 + body.length * 18)}>
            <PatchDiff
              patch={tool.patch}
              options={diffOptions}
              renderCustomHeader={() => null}
            />
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
          {tool.name}
        </Badge>
        <span className="text-muted-foreground truncate font-mono text-xs">
          {tool.summary}
        </span>
        {tool.isError && (
          <CircleAlert className="text-destructive ml-auto size-3.5 shrink-0" />
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <Separator />
        <pre className="text-muted-foreground max-h-72 overflow-auto p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {tool.detail || "no output"}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
});

const Block = memo(function Block({ turn }: { turn: Turn }) {
  const mine = turn.kind === "user";
  return (
    // No content-visibility here: MessageScrollerItem already applies it, along
    // with an intrinsic size, and two mechanisms competing to guess one block's
    // height is how a scrollbar starts lurching.
    <div className="flex min-w-0 flex-col gap-2">
      <div className="text-muted-foreground flex items-baseline gap-2 text-xs">
        <span className="text-foreground/70 font-medium">
          {mine ? "you" : "agent"}
        </span>
      </div>

      <div
        className={
          mine
            ? "bg-muted/60 border-border w-fit max-w-full rounded-lg border px-3 py-2"
            : "flex max-w-full min-w-0 flex-col gap-2"
        }
      >
        {turn.thinking !== "" && (
          <Collapsible className="group/think">
            <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs">
              <ChevronRight className="size-3 transition-transform group-data-[state=open]/think:rotate-90" />
              thinking
            </CollapsibleTrigger>
            <CollapsibleContent>
              <p className="text-muted-foreground border-border mt-1 border-l pl-3 text-xs leading-relaxed whitespace-pre-wrap">
                {turn.thinking}
              </p>
            </CollapsibleContent>
          </Collapsible>
        )}
        {turn.tools.map((tool) => (
          <ToolRow key={tool.id} tool={tool} />
        ))}
        {turn.text !== "" &&
          (mine ? (
            <p className="text-sm leading-relaxed whitespace-pre-wrap">
              {turn.text}
            </p>
          ) : (
            <Markdown text={turn.text} />
          ))}
      </div>
    </div>
  );
});

// The composer owns its draft, so typing does not re-render the conversation.
function Composer({
  onSend,
  busy,
}: {
  onSend: (text: string) => void;
  busy: boolean;
}) {
  const [draft, setDraft] = useState("");
  const [attached, setAttached] = useState<string[]>([]);
  const box = useRef<HTMLTextAreaElement | null>(null);

  const send = () => {
    const text = draft.trim();
    if (text === "" && attached.length === 0) return;
    // Paths, not contents. The agent has a filesystem and a Read tool; sending
    // it a path is how a screenshot becomes something it can look at.
    const message = [...attached, text].filter(Boolean).join("\n");
    setDraft("");
    setAttached([]);
    onSend(message);
  };

  // Dropped files, from the OS onto this box. A browser hands over a File with
  // no path and the agent needs somewhere on disk to look, so the bytes go to
  // the server and what comes back is a path — see /upload.
  const onDrop = async (event: React.DragEvent) => {
    event.preventDefault();
    const files = [...event.dataTransfer.files];
    if (!files.length) return;
    const form = new FormData();
    for (const file of files) form.append("file", file);
    try {
      const { paths } = (await fetch("/upload", {
        method: "POST",
        body: form,
      }).then((r) => r.json())) as {
        paths: string[];
      };
      setAttached((have) => [
        ...have,
        ...paths.filter((p) => !have.includes(p)),
      ]);
      box.current?.focus();
    } catch (err) {
      console.error("upload failed", err);
    }
  };

  return (
    <div
      onDrop={(e) => void onDrop(e)}
      onDragOver={(e) => e.preventDefault()}
      className="border-border bg-background focus-within:ring-ring/40 relative rounded-xl border shadow-sm focus-within:ring-2"
    >
      <Textarea
        ref={box}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          // Enter sends, shift+enter breaks the line — the convention every chat
          // uses.
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
            <Badge
              key={path}
              variant="secondary"
              className="max-w-full gap-1 font-normal"
            >
              <Paperclip className="size-3 shrink-0" />
              <span className="truncate">{path.split("/").pop()}</span>
              <button
                className="text-muted-foreground hover:text-foreground ml-0.5"
                aria-label={`remove ${path}`}
                onClick={() =>
                  setAttached((have) => have.filter((p) => p !== path))
                }
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
        <Button
          onClick={send}
          disabled={draft.trim() === "" && attached.length === 0}
          size="sm"
        >
          {busy ? <Loader2 className="size-4 animate-spin" /> : "Send"}
        </Button>
      </div>
    </div>
  );
}

// The agent's own modes, not a notion of "auto" this client invented. A client
// that auto-approved by clicking its own buttons would only ever see the prompts
// the agent bothered to send, and would diverge from what the same setting means
// everywhere else in awp.
function ModeBar({
  session,
  modes,
}: {
  session: string;
  modes: SessionSummary["modes"];
}) {
  const [current, setCurrent] = useState(modes?.currentModeId ?? null);
  useEffect(() => setCurrent(modes?.currentModeId ?? null), [modes]);
  if (!modes?.availableModes.length) return null;

  return (
    <div className="flex flex-wrap items-center gap-1 text-xs">
      {modes.availableModes.map((mode) => (
        <Button
          key={mode.id}
          size="sm"
          variant={mode.id === current ? "secondary" : "ghost"}
          className="h-6 px-2 text-xs font-normal"
          onClick={() => {
            setCurrent(mode.id);
            void api.setMode(session, mode.id);
          }}
        >
          {mode.name}
        </Button>
      ))}
    </div>
  );
}

export function ChatView({ session }: { session: SessionSummary }) {
  const [chat, setChat] = useState<Conversation>(emptyConversation);
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    setChat(emptyConversation());
    setShowAll(false);
    // The backend replays what it has already shown before streaming what is
    // new, so switching sessions rebuilds a conversation rather than starting
    // from blank.
    return api.events(session.sessionId, (event) =>
      setChat((prev) => apply(prev, event)),
    );
  }, [session.sessionId]);

  const shown = useMemo(
    () => (showAll ? chat.turns : chat.turns.slice(-windowSize)),
    [chat.turns, showAll],
  );
  const hidden = Math.max(0, chat.turns.length - windowSize);

  const send = useCallback(
    (text: string) => {
      void api.say(session.sessionId, text);
    },
    [session.sessionId],
  );

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      {/* The scroller owns following the tail, and owns the part that is easy
          to get wrong: it only follows while the reader is already at the
          bottom, so a stream cannot yank someone out of the history they are
          reading. It also gives a "jump to end" button for when it has stopped
          following, which a hand-rolled version does not. */}
      <MessageScrollerProvider>
        <MessageScroller className="min-h-0 min-w-0 flex-1">
          <MessageScrollerViewport>
            <MessageScrollerContent className="mx-auto w-full max-w-[60rem] min-w-0 px-4 pb-2">
              {chat.error !== "" && (
                <p className="text-destructive text-sm">{chat.error}</p>
              )}
              {chat.turns.length === 0 && chat.error === "" && (
                <p className="text-muted-foreground text-sm">
                  nothing here yet — say something
                </p>
              )}
              {!showAll && hidden > 0 && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="self-start"
                  onClick={() => setShowAll(true)}
                >
                  show {hidden} earlier {hidden === 1 ? "turn" : "turns"}
                </Button>
              )}
              {shown.map((turn, i) => (
                <MessageScrollerItem key={chat.turns.length - shown.length + i}>
                  <Block turn={turn} />
                </MessageScrollerItem>
              ))}

              {chat.permission && (
                // The agent asking, rendered with the agent's own options rather
                // than a yes/no this client made up.
                <div className="border-border bg-muted/40 flex flex-col gap-2 rounded-lg border p-3">
                  <p className="text-sm">
                    the agent wants to{" "}
                    <span className="font-medium">{chat.permission.title}</span>
                  </p>
                  <div className="flex flex-wrap gap-2">
                    {chat.permission.options.map((option) => (
                      <Button
                        key={option.id}
                        size="sm"
                        variant="secondary"
                        onClick={() =>
                          void api.permit(session.sessionId, option.id)
                        }
                      >
                        {option.name}
                      </Button>
                    ))}
                  </div>
                </div>
              )}

              {chat.busy && !chat.permission && (
                <div className="text-muted-foreground flex items-center gap-2 text-xs">
                  <Loader2 className="size-3 animate-spin" />
                  working
                </div>
              )}
            </MessageScrollerContent>
          </MessageScrollerViewport>
          <MessageScrollerButton />
        </MessageScroller>
      </MessageScrollerProvider>

      <div className="mx-auto flex w-full max-w-[60rem] flex-col gap-2 px-4 pb-3">
        <div className="flex items-center justify-between gap-3">
          <ModeBar session={session.sessionId} modes={session.modes} />
          {chat.usage && (
            <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
              {Math.round((chat.usage.used / chat.usage.size) * 100)}% of
              context
              {chat.usage.cost !== undefined &&
                ` · $${chat.usage.cost.toFixed(3)}`}
            </span>
          )}
        </div>
        <Composer onSend={send} busy={chat.busy} />
      </div>
    </div>
  );
}
