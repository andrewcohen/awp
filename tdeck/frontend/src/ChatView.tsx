import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PatchDiff } from "@pierre/diffs/react";
import {
  ArrowUp,
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
import {
  Message,
  MessageContent,
  MessageHeader,
} from "@/components/ui/message";
import { Bubble, BubbleContent } from "@/components/ui/bubble";
import { Separator } from "@/components/ui/separator";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
        <div className="text-muted-foreground bg-muted/40 flex items-center gap-2 border-b px-2.5 py-1.5 text-sm">
          <span className="truncate font-mono">{tool.file}</span>
          <span className="ml-auto shrink-0 font-mono">
            <span className="text-emerald-600 dark:text-emerald-400">
              +{added}
            </span>{" "}
            <span className="text-rose-600 dark:text-rose-400">−{removed}</span>
          </span>
        </div>
        <div className="max-h-64 overflow-auto text-sm">
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
        <span className="text-muted-foreground truncate font-mono text-sm">
          {tool.summary}
        </span>
        {tool.isError && (
          <CircleAlert className="text-destructive ml-auto size-3.5 shrink-0" />
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <Separator />
        <pre className="text-muted-foreground max-h-72 overflow-auto p-2.5 font-mono text-sm leading-relaxed whitespace-pre-wrap">
          {tool.detail || "no output"}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
});

const Block = memo(function Block({ turn }: { turn: Turn }) {
  const mine = turn.kind === "user";

  // Message/Bubble rather than a hand-rolled div with a rounded border. The
  // alignment, the avatar gutter, the footer offset and the wrap behaviour are
  // all things the registry already decides consistently, and a bubble that is
  // nearly the same shape as everyone else's is worse than one that is exactly
  // the same shape.
  //
  // No content-visibility here: MessageScrollerItem already applies it, along
  // with an intrinsic size, and two mechanisms competing to guess one block's
  // height is how a scrollbar starts lurching.
  return (
    <Message align={mine ? "end" : "start"}>
      <MessageContent className="min-w-0">
        <MessageHeader className="text-muted-foreground">
          {mine ? "you" : "agent"}
        </MessageHeader>

        {mine ? (
          <Bubble align="end">
            <BubbleContent className="bg-muted whitespace-pre-wrap">
              {turn.text}
            </BubbleContent>
          </Bubble>
        ) : (
          <div className="flex max-w-full min-w-0 flex-col gap-2">
            {turn.thinking !== "" && (
              <Collapsible className="group/think">
                <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-sm">
                  <ChevronRight className="size-3.5 transition-transform group-data-[state=open]/think:rotate-90" />
                  thinking
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <p className="text-muted-foreground border-border mt-1 border-l pl-3 text-sm leading-relaxed whitespace-pre-wrap">
                    {turn.thinking}
                  </p>
                </CollapsibleContent>
              </Collapsible>
            )}
            {turn.tools.map((tool) => (
              <ToolRow key={tool.id} tool={tool} />
            ))}
            {turn.text !== "" && <Markdown text={turn.text} />}
          </div>
        )}
      </MessageContent>
    </Message>
  );
});

// Effort is not an ACP concept, so it goes the way a person would send it.
//
// The protocol has session modes and nothing for reasoning effort — but the
// agent advertises a /effort slash command, and a slash command is just a
// prompt. Sending one is honest: it is exactly what typing it would do, it
// stays correct if the levels change, and it does not invent a setting the
// agent does not have. What it costs is that the current level is not readable
// back, so this remembers what it last asked for rather than claiming to know.
const effortLevels = ["low", "medium", "high", "xhigh", "max"] as const;
const effortKey = "tdeck.effort";

// The composer owns its draft, so typing does not re-render the conversation.
function Composer({
  session,
  onSend,
  busy,
}: {
  session: SessionSummary;
  onSend: (text: string) => void;
  busy: boolean;
}) {
  const [draft, setDraft] = useState("");
  const [attached, setAttached] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);
  const [mode, setMode] = useState(session.modes?.currentModeId ?? "");
  const [effort, setEffort] = useState(
    () => localStorage.getItem(effortKey) ?? "",
  );
  const box = useRef<HTMLTextAreaElement | null>(null);
  const picker = useRef<HTMLInputElement | null>(null);

  useEffect(() => setMode(session.modes?.currentModeId ?? ""), [session.modes]);

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

  // A browser hands over a File with no path and the agent needs somewhere on
  // disk to look, so the bytes go to the server and what comes back is a path.
  const upload = async (files: File[]) => {
    if (!files.length) return;
    setUploading(true);
    const form = new FormData();
    for (const file of files) form.append("file", file);
    try {
      const { paths } = (await fetch("/upload", {
        method: "POST",
        body: form,
      }).then((r) => r.json())) as { paths: string[] };
      setAttached((have) => [
        ...have,
        ...paths.filter((p) => !have.includes(p)),
      ]);
      box.current?.focus();
    } catch (err) {
      console.error("upload failed", err);
    } finally {
      setUploading(false);
    }
  };

  return (
    <div
      onDrop={(e) => {
        e.preventDefault();
        void upload([...e.dataTransfer.files]);
      }}
      onDragOver={(e) => e.preventDefault()}
      className="border-border bg-card focus-within:border-ring/60 focus-within:ring-ring/20 flex flex-col rounded-2xl border shadow-sm transition focus-within:ring-4"
    >
      <Textarea
        ref={box}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onPaste={(e) => {
          // A pasted screenshot is the common case on a Mac, and it arrives as
          // a file on the clipboard rather than as text.
          const files = [...e.clipboardData.files];
          if (files.length) {
            e.preventDefault();
            void upload(files);
          }
        }}
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
        className="max-h-56 min-h-20 resize-none border-0 bg-transparent px-4 pt-3.5 text-base shadow-none focus-visible:ring-0"
      />

      {attached.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-4 pb-1">
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

      {/* One toolbar along the bottom of the box, the way every prompt input
          does it. Mode and effort were a row of six and five buttons — eleven
          controls permanently on screen to change a setting that changes maybe
          twice a day, crowding out the thing the box is actually for. Collapsed
          into pickers they state the current value in the space one button
          used. */}
      <div className="flex items-center gap-1 px-2 pb-2">
        <input
          ref={picker}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            void upload([...(e.target.files ?? [])]);
            e.target.value = "";
          }}
        />
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground size-8"
          aria-label="attach a file"
          title="attach a file"
          disabled={uploading}
          onClick={() => picker.current?.click()}
        >
          {uploading ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <Paperclip className="size-4" />
          )}
        </Button>

        {session.modes && session.modes.availableModes.length > 0 && (
          <Select
            value={mode}
            onValueChange={(next) => {
              if (typeof next !== "string") return;
              setMode(next);
              void api.setMode(session.sessionId, next);
            }}
          >
            <SelectTrigger
              size="sm"
              className="h-8 w-auto gap-1 border-0 shadow-none"
              aria-label="permission mode"
            >
              <SelectValue placeholder="mode" />
            </SelectTrigger>
            <SelectContent>
              {session.modes.availableModes.map((m) => (
                <SelectItem key={m.id} value={m.id}>
                  {m.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}

        <Select
          value={effort}
          onValueChange={(next) => {
            if (typeof next !== "string") return;
            setEffort(next);
            localStorage.setItem(effortKey, next);
            onSend(`/effort ${next}`);
          }}
        >
          <SelectTrigger
            size="sm"
            className="h-8 w-auto gap-1 border-0 shadow-none"
            aria-label="reasoning effort"
          >
            <SelectValue placeholder="effort" />
          </SelectTrigger>
          <SelectContent>
            {effortLevels.map((level) => (
              <SelectItem key={level} value={level}>
                {level}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <span className="text-muted-foreground ml-auto hidden text-sm sm:inline">
          <CornerDownLeft className="mr-1 inline size-3.5" />
          to send
        </span>
        <Button
          onClick={send}
          disabled={draft.trim() === "" && attached.length === 0}
          size="icon"
          className="size-8"
          aria-label="send"
        >
          {busy ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <ArrowUp className="size-4" />
          )}
        </Button>
      </div>
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
      {/* These live on the provider, not the root — and the peek is a number
          of pixels, though the documentation writes it as "64px". */}
      <MessageScrollerProvider
        // Reopen a conversation at the last thing you asked, not at the
        // absolute bottom. After a long agent turn those are pages apart, and
        // the bottom is the end of an answer whose question is off-screen.
        defaultScrollPosition="last-anchor"
        // Follow a streaming answer, but only while the reader is at the live
        // edge — scrolling up releases it.
        autoScroll
        // Keep a slice of the previous turn above a newly anchored one, so a new
        // turn reads as continuing a conversation rather than replacing it.
        scrollPreviousItemPeek={64}
      >
        <MessageScroller className="min-h-0 min-w-0 flex-1">
          <MessageScrollerViewport>
            <MessageScrollerContent
              className="mx-auto w-full max-w-[60rem] min-w-0 px-4 pb-2"
              // Announced to a screen reader while the agent is mid-answer.
              aria-busy={chat.busy}
            >
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
              {shown.map((turn, i) => {
                // Stable across prepends: an index into the whole conversation
                // rather than into the visible window, so pressing "show
                // earlier turns" does not renumber every row and cost the
                // scroller its position.
                const id = String(chat.turns.length - shown.length + i);
                return (
                  <MessageScrollerItem
                    key={id}
                    messageId={id}
                    // A turn begins where you asked for something. Anchoring
                    // agent replies too would treat each of the six tool calls
                    // inside one answer as a fresh place to scroll to.
                    scrollAnchor={turn.kind === "user"}
                  >
                    <Block turn={turn} />
                  </MessageScrollerItem>
                );
              })}

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
                <div className="text-muted-foreground flex items-center gap-2 text-sm">
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
        {chat.usage && (
          // Above the box and right-aligned, where it is readable without
          // competing with the controls inside it.
          <span className="text-muted-foreground self-end text-sm tabular-nums">
            {Math.round((chat.usage.used / chat.usage.size) * 100)}% of context
            {chat.usage.cost !== undefined &&
              ` · $${chat.usage.cost.toFixed(3)}`}
          </span>
        )}
        <Composer session={session} onSend={send} busy={chat.busy} />
      </div>
    </div>
  );
}
