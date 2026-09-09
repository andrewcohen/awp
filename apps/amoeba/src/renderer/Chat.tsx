import type { ChatConfigOption } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useCallback, useEffect, useRef, useState } from "react";
import { type Command, agentCommands, commandOf } from "./commands";
import { Composer } from "./Composer";
import { Mcp } from "./Mcp";
import {
  type Asked,
  type Conversation,
  type Item,
  type Ran,
  type Said,
  fold,
  grouped,
  mine,
  nothing,
  stalled,
  toolTitle,
  took,
  verb,
  waiting,
} from "./conversation";
import { Patch } from "./Fence";
import { Markdown } from "./Markdown";
import {
  chatAnswer,
  chatConfig,
  chatFork,
  chatFresh,
  chatSend,
  chatSet,
  watchChat,
} from "./daemon";
import { type Spot, spotIn, withQuote } from "./quote";
import { typeset } from "./typeset";
import { colors, text } from "./tokens.stylex";

// The agent as a conversation rather than as a picture of one.
//
// The pane draws whatever the program on the other end of a pty decided to
// paint. This draws records: a message, a tool call with a status that moves,
// a permission request with buttons on it. What that buys is everything the
// terminal cannot be asked — see chat.ts in the daemon, which carries the
// measurement.
//
// ── an update is a patch, not a row ────────────────────────────────────────
//
// One `cat` arrived as five updates sharing one id: pending with a generic
// title, then the command, then the output, then completed. So the reducer
// below merges by id, and a chunk of text appends to the message above it
// rather than starting a new one. Appending each update as its own row is the
// obvious first version and draws one tool call five times.

/** What a tool's status looks like at a glance. */
const mark = (status: string): string =>
  status === "completed" ? "✓" : status === "failed" ? "✗" : "…";

/**
 * The panel, and the thing that can be replaced wholesale.
 *
 * Forking changes which session this workspace's chat *is*, so everything
 * below — the folded transcript, the settings, the read of the config — is
 * about a conversation that no longer applies. Remounting says exactly that
 * and needs nothing else to say it: no effect that resets state, and no
 * dependency on a counter the effect never reads. Both of those were the first
 * version, and both are lint errors here for good reasons.
 */
export const Chat = ({
  project,
  workspace,
  focus,
}: {
  readonly project: string;
  readonly workspace: string;
  /**
   * Changes when the window has moved somewhere on purpose, which is when the
   * caret belongs in the box.
   *
   * There was no focus call here at all: every arrival at a chat needed a
   * click in the composer before a key did anything, which for the one face
   * that is nothing *but* typing is the whole of using it.
   */
  readonly focus?: string | undefined;
}) => {
  const [again, setAgain] = useState(0);
  const [forking, setForking] = useState(false);

  const fork = useCallback(() => {
    setForking(true);
    void chatFork(project, workspace)
      .then(() => setAgain((was) => was + 1))
      .catch(() => {
        // A conversation that cannot be forked says so where every other
        // chat failure does: on the stream the panel is already reading.
      })
      .finally(() => setForking(false));
  }, [project, workspace]);

  /**
   * `/new`: this workspace's chat starts again from nothing.
   *
   * The same remount as the fork, and for the same reason — everything the
   * panel holds is about a conversation that no longer applies, and a key is
   * the whole of saying so. Nothing is deleted: the daemon forgets which
   * session this workspace continues, and the transcript stays on disk.
   */
  const fresh = useCallback(() => {
    void chatFresh(project, workspace)
      .then(() => setAgain((was) => was + 1))
      .catch(() => {
        // Same channel as every other chat failure: the stream the panel is
        // already reading.
      });
  }, [project, workspace]);

  return (
    <Panel
      key={again}
      project={project}
      workspace={workspace}
      onFork={fork}
      forking={forking}
      onFresh={fresh}
      focus={focus}
    />
  );
};

const Panel = ({
  project,
  workspace,
  onFork,
  forking,
  onFresh,
  focus,
}: {
  readonly project: string;
  readonly workspace: string;
  readonly onFork: () => void;
  readonly forking: boolean;
  /** `/new` — handled above this component, which is replaced by it. */
  readonly onFresh: () => void;
  /** See the note on `Chat`. */
  readonly focus?: string | undefined;
}) => {
  const [held, setHeld] = useState<Conversation>(nothing);
  const items = held.items;
  const [draft, setDraft] = useState("");
  const [config, setConfig] = useState<ReadonlyArray<ChatConfigOption>>([]);
  const bottom = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const scroll = useRef<HTMLDivElement>(null);
  /**
   * The highlighted phrase and where it is, or nothing.
   *
   * ── highlight, not hover ─────────────────────────────────────────────────
   *
   * The first version put a control on every row and revealed it on hover,
   * which was the wrong reading of the request — "hover or highlight anything
   * in agent chat and be able to reply to it" is about the *highlight*. A
   * hover control is a control per row whether or not anybody is pointing at
   * anything, and it says nothing about which part of the row is meant.
   *
   * This is the selection's own rectangle, so the control lands beside the
   * words that were highlighted.
   */
  const [spot, setSpot] = useState<Spot | undefined>(undefined);
  /** `/mcp` — a modal, which is why it is state and not a navigation. */
  const [asking, setAsking] = useState(false);

  /**
   * Run one of the window's own commands.
   *
   * The box is cleared first in both cases. A command is not a message and
   * leaving it in the box would read as one that failed to send — and `/new`
   * replaces this component, so anything set after the call is set on a
   * component that is about to go.
   */
  const run = useCallback(
    (command: Command) => {
      setDraft("");
      if (command.name === "/mcp") {
        setAsking(true);
        return;
      }
      onFresh();
    },
    [onFresh],
  );

  useEffect(
    () =>
      watchChat(project, workspace, (update) => {
        setHeld((current) => fold(current, update));
      }),
    [project, workspace],
  );

  useEffect(() => {
    // Asked once, when the panel opens. There is no call that answers "what
    // are my options" on the adapter either — they arrive with the session —
    // so the daemon holds them and this is a read of that.
    let gone = false;
    void chatConfig(project, workspace)
      .then((options) => {
        if (!gone) {
          setConfig(options);
        }
      })
      .catch(() => {
        // A conversation that could not be opened has no settings, and the
        // stream above is where that failure is already said out loud.
      });
    return () => {
      gone = true;
    };
  }, [project, workspace]);

  // How far the conversation has got, as one value.
  //
  // The count alone is not enough: an answer arrives as chunks appended to the
  // message already at the bottom, so the list stops growing while the text
  // still does — and the view would stall halfway through every reply.
  const tail = items.at(-1);
  const grown = `${String(items.length)}:${String(
    tail === undefined
      ? 0
      : tail.kind === "said"
        ? tail.text.length
        : tail.kind === "ran"
          ? tail.output.length
          : 0,
  )}`;

  useEffect(() => {
    // Follow the tail. `block: "end"` rather than a scrollTop assignment, so a
    // person who has scrolled up to read something is not yanked back by the
    // browser's own smooth behaviour fighting theirs.
    if (grown !== "0:0") {
      bottom.current?.scrollIntoView({ block: "end" });
    }
  }, [grown]);

  useEffect(() => {
    // ── settled gestures only ──────────────────────────────────────────────
    //
    // `selectionchange` fires continuously through a drag, and reading it
    // there would put a control under the pointer that is still selecting —
    // the same hazard as the diff panel's line selection, where a render
    // during a gesture ended the gesture. So the affordance appears on
    // `pointerup` and on `keyup` (shift+arrows are a selection too), and
    // `selectionchange` is used only to take it away once the selection has
    // collapsed.
    const settle = () => setSpot(spotIn(scroll.current));
    const collapsed = () => {
      const now = globalThis.getSelection?.();
      if (now === null || now === undefined || now.isCollapsed) {
        setSpot(undefined);
      }
    };
    globalThis.addEventListener("pointerup", settle);
    globalThis.addEventListener("keyup", settle);
    document.addEventListener("selectionchange", collapsed);
    return () => {
      globalThis.removeEventListener("pointerup", settle);
      globalThis.removeEventListener("keyup", settle);
      document.removeEventListener("selectionchange", collapsed);
    };
  }, []);

  /**
   * Quote a piece of the conversation into the composer.
   *
   * Into the box rather than out to the agent: a quoted reply *is* the next
   * turn and wants a sentence written after it, where the diff panel batches
   * comments because six remarks are one prompt.
   *
   * Focus moves to the box and the caret goes to the end, because the next
   * thing to happen is typing. `requestAnimationFrame` is not needed —
   * `setDraft` has not painted yet, so the value is set on the element by the
   * time the effect of typing matters, and `setSelectionRange` on the current
   * value would put the caret in the wrong place.
   */
  const quote = useCallback((words: string) => {
    setDraft((current) => withQuote(current, words));
    // The highlight has been spent. Left on screen it would keep the control
    // over a phrase already quoted, and a second press would quote it twice.
    globalThis.getSelection?.()?.removeAllRanges();
    setSpot(undefined);
    box.current?.focus();
  }, []);

  // The caret, on arrival. The composer is mounted by then — the effect runs
  // after the first paint — and a face whose whole purpose is typing should
  // not need a click before a key does anything.
  useEffect(() => {
    // The value is read rather than merely watched: nobody having asked means
    // nobody's focus should move, which is what makes this safe to leave on a
    // component a fixture also renders.
    if (focus === undefined) {
      return;
    }
    box.current?.focus();
  }, [focus]);

  const say = useCallback(() => {
    const words = draft.trim();
    if (words === "") {
      return;
    }
    // ── the window's commands are intercepted, and narrowly ──────────────
    //
    // Exact match on the whole draft: `/new` is a command and `/tmp/build.log
    // is missing` is a message about a path. Sent as text these would reach
    // the agent as a sentence *about* a command, and the agent would answer
    // it — which is the failure this catch prevents. See commands.ts.
    const command = commandOf(words);
    if (command !== undefined) {
      run(command);
      return;
    }
    setDraft("");
    // A name for the message, given here because only the sender can give one.
    // The reply that says how it was delivered arrives while the list is still
    // growing, so a position in the list would name a different message by
    // then.
    // A uuid rather than a counter, now that the daemon echoes the message
    // back: two clients on one conversation would both mint `mine-1`, and the
    // dedupe is by key — so one window's second message would silently swallow
    // the other's.
    const key = crypto.randomUUID();
    const working = held.running > 0;
    // Shown immediately rather than waiting for the daemon to echo it back. A
    // message that appears only once the agent has acknowledged it reads as a
    // send button that did nothing — and there is nothing to wait for anyway:
    // measured, the adapter never echoes a live user message back, so this
    // copy is the only record of it until the session is opened again.
    //
    // `mine` and not `fold`, because it is not something the daemon said. See
    // the note there: dressing it up as an update is what put a steer above
    // the rest of a reply that was still arriving.
    setHeld((current) => mine(current, words, key));
    void chatSend(project, workspace, words, key)
      .then((how) => {
        // Only a message that started a turn of its own while the agent was
        // already working has to wait for it. A steer is being read now, and
        // saying it is queued would be the opposite of the truth.
        if (how === "prompt" && working) {
          setHeld((current) => waiting(current, key));
        }
      })
      .catch(() => {
        // The stream is where a conversation that cannot be had says so.
      });
  }, [draft, held.running, project, workspace, run]);

  return (
    <div {...stylex.props(styles.chat)} data-column-part="chat">
      <div ref={scroll} {...stylex.props(styles.scroll)}>
        {items.length === 0 && held.running === 0 ? (
          // ── the empty state is where the fork belongs ────────────────────
          //
          // A chat with nothing in it, on a workspace whose agent has been
          // running in the terminal all along, is exactly the moment somebody
          // wants the conversation that is already happening. Offered here
          // rather than in the bar because it is an answer to what is on
          // screen; the bar would carry it on every conversation, including
          // the ones it would overwrite.
          <div {...stylex.props(styles.empty)}>
            <p {...stylex.props(typeset.label, styles.nothing)}>nothing said yet</p>
            <button
              type="button"
              data-nav-item
              {...stylex.props(typeset.label, styles.option)}
              title="copy the conversation the terminal is having and continue it here — the terminal's own is left alone"
              disabled={forking}
              onClick={onFork}
            >
              {forking ? "forking…" : "continue the terminal's conversation"}
            </button>
          </div>
        ) : (
          <Transcript items={items} project={project} workspace={workspace} />
        )}

        {/* A word, not a spinner. The jobs panel's rule holds here for the same
            reason: the word already says it is running, and a spinner beside a
            transcript that is itself moving is one animation too many.

            On the wire rather than inferred from silence, because silence is
            also what an agent that answered with nothing looks like. */}
        {held.running > 0 && <p {...stylex.props(styles.working)}>working…</p>}
        {held.stopped !== undefined && (
          <p {...stylex.props(typeset.label, styles.stopped)}>the turn ended: {held.stopped}</p>
        )}
        <div ref={bottom} />
      </div>

      {/* Beside the highlight, not in the flow: `position: fixed` at the
          range's own rectangle. In the flow it would move the text it is
          about — which is the thing a selection cannot survive. */}
      {spot !== undefined && <Quote spot={spot} onQuote={quote} />}

      {/* `/mcp`. A dialog rather than a panel in the accessory strip: it is a
          question asked once, about this conversation, and the answer is read
          and dismissed. It also announces itself as an overlay, which is what
          stops the web panel's native view being drawn over the top of it. */}
      {asking && <Mcp project={project} workspace={workspace} onClose={() => setAsking(false)} />}

      {/* Everything below the transcript, and it is a component now — the
          style guide draws it, and a copy on that page would be a copy that
          drifts. What stayed here is what has a consequence: what a message
          does, what a command does, and what an option change tells the
          daemon. */}
      <Composer
        draft={draft}
        onDraft={setDraft}
        onSend={say}
        onCommand={run}
        theirs={agentCommands(held.commands)}
        config={config}
        onSetOption={(option, value) => {
          // Painted before the daemon answers, and corrected by the answer. A
          // select that snaps back for a moment reads as a control that did
          // not take.
          setConfig((all) =>
            all.map((one) => (one.id === option ? { ...one, currentValue: value } : one)),
          );
          void chatSet(project, workspace, option, value)
            .then(setConfig)
            .catch(() => setConfig(config));
        }}
        usage={
          held.full === undefined
            ? undefined
            : { full: held.full, used: held.used, size: held.size }
        }
        onBox={(node) => {
          box.current = node;
        }}
      />
    </div>
  );
};

/**
 * Past this, how long a tool call has taken is worth saying, in seconds.
 *
 * The same rule as the context figure and the status bar: an elapsed time on
 * every row is furniture, and what a person is looking for is the one call
 * that has been going for minutes.
 */
const WORTH_SAYING = 10;

/**
 * One item of a transcript, and the only piece of this panel the style guide
 * borrows.
 *
 * Exported for that alone. It takes a pair rather than reading one, so a
 * fixture can render every shape — a message, a tool call, a question — with
 * no daemon behind it: the buttons then refuse, which is the honest outcome
 * for a workspace that does not exist.
 */
export const Row = ({
  item,
  project,
  workspace,
}: {
  readonly item: Item;
  readonly project: string;
  readonly workspace: string;
}) => {
  if (item.kind === "said") {
    return <Message item={item} />;
  }
  if (item.kind === "ran") {
    return <Tool item={item} project={project} workspace={workspace} />;
  }
  return <Permission item={item} project={project} workspace={workspace} />;
};

/**
 * The control that quotes the highlighted phrase.
 *
 * ── it appears because something is highlighted ───────────────────────────
 *
 * Which is the whole correction over the first version: that one put a button
 * on every row and revealed it on hover, and a hover control neither waits to
 * be wanted nor says which part of a message it means. This one exists only
 * while there is a selection, and sits beside it.
 *
 * ── fixed, and clamped to the window ─────────────────────────────────────
 *
 * `position: fixed` at the range's viewport rectangle, so it does not push
 * the text it is about — a selection cannot survive its own words moving.
 * Clamped at the left edge because a phrase highlighted at the start of a
 * line would otherwise put the control off screen, and the window's rule is
 * that nothing grows a scrollbar sideways.
 *
 * ── the keyboard, honestly ───────────────────────────────────────────────
 *
 * It is a real button in the tree while it is shown, so Tab reaches it and
 * Return presses it. What this cannot grant is a way to *make* a selection
 * without a pointer: the transcript is not a focusable region, and caret
 * browsing is the browser's to offer. Said out loud rather than papered over
 * with a hover control nobody asked for.
 */
const Quote = ({
  spot,
  onQuote,
}: {
  readonly spot: Spot;
  readonly onQuote: (text: string) => void;
}) => (
  <button
    type="button"
    data-nav-item
    {...stylex.props(styles.quote)}
    style={{
      // Dynamic because it follows a selection, which no static rule can
      // know. Above the phrase, centred on it, and never left of the column.
      left: Math.max(8, spot.left + spot.width / 2 - QUOTE_WIDTH / 2),
      top: Math.max(8, spot.top - QUOTE_HEIGHT - 6),
    }}
    title="quote what you highlighted into a reply"
    onClick={() => onQuote(spot.text)}
  >
    quote
  </button>
);

/** The control's own size, so it can be centred on the phrase it is about. */
const QUOTE_WIDTH = 62;

/** Its height, so it can sit above the phrase rather than over it. */
const QUOTE_HEIGHT = 24;

const Message = ({ item }: { readonly item: Said }) => {
  return (
    <div {...stylex.props(styles.item, styles.said, item.role === "thought" && styles.thought)}>
      <span {...stylex.props(typeset.control, styles.who, item.role === "user" && styles.mine)}>
        {item.role === "user" ? "you" : item.role === "thought" ? "thinking" : "agent"}
        {/* ── a steer says that it is waiting ───────────────────────────────
          Measured: a message sent mid-turn is answered only once the turn it
          interrupted has ended. Drawn as an ordinary message it reads as a
          question the agent ignored — and then, when the answer does come,
          as an answer to the wrong thing. */}
        {item.queued && (
          <span
            {...stylex.props(typeset.label, styles.queued)}
            title="sent — the agent is finishing what it was doing and will answer this next"
          >
            queued
          </span>
        )}
      </span>
      {/* ── markdown for what the agent wrote, and not for what you wrote ──

          An agent answers in markdown — headings, lists, fenced code — and
          drawn as text that is most of the reply showing its own syntax.
          `Markdown.tsx` already exists for the PR panel and is reused whole:
          it builds React elements rather than HTML, so there is no sanitiser
          to get right.

          Your own message is drawn as text on purpose. It is exactly what you
          typed, and rendering it would mean a message containing `# ` silently
          becoming a heading — which is a window editing what somebody said. */}
      {item.role === "agent" ? (
        <Markdown reading>{item.text}</Markdown>
      ) : (
        <p {...stylex.props(typeset.prose, styles.words, styles.reading)}>{item.text}</p>
      )}
    </div>
  );
};

/**
 * A conversation, drawn.
 *
 * Exported for the style guide, and that is not a nicety: it draws the
 * grouping as well as the rows, and only `Row` was exported before — so the
 * page showed a run of tool calls as a run of paragraphs while the panel drew
 * it as one block. A page that lies about the thing it exists to let somebody
 * criticise is worse than no page.
 */
export const Transcript = ({
  items,
  project,
  workspace,
}: {
  readonly items: ReadonlyArray<Item>;
  readonly project: string;
  readonly workspace: string;
}) => (
  <>
    {grouped(items).map((block) =>
      block.kind === "calls" ? (
        <Calls key={block.key} items={block.items} project={project} workspace={workspace} />
      ) : (
        <Row key={block.key} item={block.item} project={project} workspace={workspace} />
      ),
    )}
  </>
);

/**
 * A run of tool calls, as one block.
 *
 * ── the tail is the part worth drawing ────────────────────────────────────
 *
 * A dozen calls between two sentences is ordinary, and the ones a person is
 * looking at are the recent ones — the earlier ones are how the agent got
 * here, which is worth having and not worth the height. So a long run draws
 * its last few and offers the rest, and the offer says how many so the number
 * is the reason to press it.
 *
 * Left-ruled rather than boxed. A border on four sides makes a panel out of
 * something that is a passage in a document; a rule down the side says "this
 * belongs together" and takes eight pixels to do it.
 */
const SHOWN = 4;

const Calls = ({
  items,
  project,
  workspace,
}: {
  readonly items: ReadonlyArray<Ran>;
  readonly project: string;
  readonly workspace: string;
}) => {
  const [all, setAll] = useState(false);
  // A question used to be excepted here, and it is excepted a step earlier
  // now: `grouped` never puts one in a block, along with anything that
  // changed a file. A guard that can no longer fire is one nothing tests.
  const hidden = all ? 0 : Math.max(0, items.length - SHOWN);
  const drawn = hidden === 0 ? items : items.slice(hidden);

  return (
    <div {...stylex.props(styles.calls)}>
      {hidden > 0 && (
        <button
          type="button"
          data-nav-item
          onClick={() => setAll(true)}
          {...stylex.props(typeset.label, styles.earlier)}
        >
          {`${String(hidden)} earlier ${hidden === 1 ? "call" : "calls"}`}
        </button>
      )}
      {drawn.map((item) => (
        <Tool key={item.key} item={item} project={project} workspace={workspace} />
      ))}
    </div>
  );
};

const Tool = ({
  item,
  project,
  workspace,
}: {
  readonly item: Ran;
  readonly project: string;
  readonly workspace: string;
}) => {
  // Shut by default, and open once for anything that went wrong.
  //
  // Output is usually long and usually uninteresting — the row already says
  // what ran and whether it worked. What a person opens it for is the case
  // where it did not, so that case opens itself.
  const [open, setOpen] = useState(item.status === "failed");

  // What the row draws, and what it keeps back. See `toolTitle`: the important
  // half of a title is its basename or its first line, and the rest either
  // clips or is counted.
  const said = toolTitle(item.subagent === undefined ? item.title : `a ${item.subagent}`);
  // Openable for anything held back, not only for output. A twelve-line
  // heredoc drawn as its first line with no way to see the other eleven is a
  // row that has quietly lost the command.
  const holds = item.output !== "" || said.more > 0;

  return (
    <div {...stylex.props(styles.item, styles.ran)}>
      <span
        {...stylex.props(typeset.address, styles.status, item.status === "failed" && styles.failed)}
      >
        {mark(item.status)}
      </span>
      <div {...stylex.props(styles.grow)}>
        <button
          type="button"
          data-nav-item
          disabled={!holds}
          // The whole of it, for the one that was clipped. A tooltip costs no
          // pixels until it is asked for, which is the trade every address in
          // this window makes.
          title={item.title}
          onClick={() => setOpen((was) => !was)}
          {...stylex.props(styles.command)}
        >
          <span {...stylex.props(typeset.label, styles.verb)}>{verb(item)}</span>
          {/* ── a delegated call, labelled ────────────────────────────────
              A spawn used to read as `ran  Task`, which says neither that
              work was handed off nor to what. There is no subagent update
              kind in ACP — a subagent's own messages never arrive — so this
              is one tool call named honestly rather than a tree. */}
          <span {...stylex.props(styles.subject)}>
            {said.lead !== "" && (
              <span {...stylex.props(typeset.address, styles.lead)}>{said.lead}</span>
            )}
            <span {...stylex.props(typeset.address, styles.what)}>{said.name}</span>
          </span>
          {said.more > 0 && (
            <span {...stylex.props(typeset.label, styles.aside)}>
              {`+${String(said.more)} line${said.more === 1 ? "" : "s"}`}
            </span>
          )}
          {item.elapsed !== undefined && item.elapsed >= WORTH_SAYING && (
            <span {...stylex.props(typeset.label, styles.aside)}>{took(item.elapsed)}</span>
          )}
        </button>
        {/* Why a spawn is sitting still. A subagent waiting out a rate limit
            and a subagent doing slow work are the same picture without this,
            and only one of them is worth waiting for. */}
        {stalled(item) !== undefined && (
          <p {...stylex.props(typeset.label, styles.retry)}>{stalled(item)}</p>
        )}
        {/* The question about this call, on this call. See the note on `ask`:
            a separate row was a second copy of the command already above it. */}
        {item.ask !== undefined && (
          <Answering item={item.ask} project={project} workspace={workspace} />
        )}
        {/* The command in full, once, and only when the row could not hold
            it. Above the output because it is what produced it. */}
        {open && said.more > 0 && (
          <pre {...stylex.props(typeset.address, styles.whole)}>{item.title}</pre>
        )}
        {/* ── what it changed, drawn as the change ──────────────────────
            Not behind the disclosure, unlike the output: an edit's output is
            empty, so the patch *is* the row's content and a shut row would be
            a title and a tick over the one thing worth reading. The same
            renderer a ```diff in a message goes through, and the same one the
            diff panel uses — a change described and a change made must not
            read as two different things.

            One block per patch, because the adapter reports an edit per hunk:
            a `MultiEdit` of three places in one file arrives as three. */}
        {item.diffs.map((diff) => (
          <div key={diff.path + diff.patch} {...stylex.props(styles.changed)}>
            <Patch source={diff.patch} />
          </div>
        ))}
        {open && item.output !== "" && (
          <pre {...stylex.props(typeset.address, styles.output)}>{item.output}</pre>
        )}
      </div>
    </div>
  );
};

/**
 * A question with nothing on screen to attach it to.
 *
 * The ordinary case is `Answering` inside the tool row — the adapter emits the
 * tool call before it asks. This is what is left: a question about a call this
 * window was never told about, which is still a question and still has an
 * agent waiting on it.
 */
const Permission = ({
  item,
  project,
  workspace,
}: {
  readonly item: Asked;
  readonly project: string;
  readonly workspace: string;
}) => (
  <div {...stylex.props(styles.item, styles.asked)}>
    <p {...stylex.props(typeset.address, styles.question)}>{item.title}</p>
    <Answering item={item} project={project} workspace={workspace} />
  </div>
);

/** What a person may answer, and what they answered. */
const Answering = ({
  item,
  project,
  workspace,
}: {
  readonly item: Asked;
  readonly project: string;
  readonly workspace: string;
}) => {
  // Answered once. The daemon forgets the request as soon as it is replied to,
  // so a second press is a refusal from the other end — and a row that still
  // looks pressable after it has been answered is one somebody will press.
  //
  // Two sources, and the second is the point of it: this window's own press,
  // and `item.answered`, which is what the daemon says when the answer came
  // from somewhere else — another window, or the TUI. Local first, because a
  // press should not wait for a round trip to draw.
  const [answered, setAnswered] = useState<string | undefined>(undefined);
  const said = answered ?? item.answered;

  return (
    <>
      {said === undefined ? (
        <div {...stylex.props(styles.options)}>
          {item.options.map((option) => (
            <button
              key={option.id}
              type="button"
              data-nav-item
              {...stylex.props(
                typeset.label,
                styles.option,
                option.kind.startsWith("allow") && styles.allow,
                option.kind.startsWith("reject") && styles.reject,
              )}
              onClick={() => {
                setAnswered(option.name);
                void chatAnswer(project, workspace, item.key, option.id);
              }}
            >
              {option.name}
            </button>
          ))}
        </div>
      ) : (
        <p {...stylex.props(typeset.label, styles.answered)}>{said}</p>
      )}
    </>
  );
};

const styles = stylex.create({
  chat: {
    display: "flex",
    flexDirection: "column",
    flex: 1,
    minHeight: 0,
    // A page, not a panel. This is the one surface in the window somebody
    // reads for minutes rather than glances at, and it is white in the light
    // theme and the deepest grey in the dark one — see `page` in the tokens.
    backgroundColor: colors.page,
  },
  scroll: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    // ── selectable, against the window's default ──────────────────────────
    //
    // `body { user-select: none }` in global.css, because a drag on empty
    // chrome should move the window rather than select it. This is not empty
    // chrome: it is the one surface here that is prose, read for minutes, and
    // quoted from — and quoting *is* selecting, so the selection half of the
    // quote control was unreachable until this line existed.
    //
    // Measured before it did: a real drag across a message left
    // `getSelection().toString()` empty and `user-select` computed to `none`.
    // Copying what an agent said did not work either, which is the larger of
    // the two things this fixes.
    //
    // `Boundary` does the same for the same reason — a stack trace nobody can
    // select is one that gets retyped from a photograph.
    userSelect: "text",
    // The window's rule: a horizontal scrollbar is a layout that was allowed
    // to be wider than the column holding it. Output wraps instead.
    overflowX: "hidden",
    // Room, and more of it than a panel gets. This column is the one somebody
    // reads for minutes at a time rather than glances at, and text pressed
    // against a column edge is what makes a transcript tiring — the gutter is
    // what the eye returns to at the start of every line.
    paddingBlock: "1.25rem",
    paddingInline: "1.25rem",
    display: "flex",
    flexDirection: "column",
    // Between turns, not between lines. A message and the tool call it caused
    // are one thought and want to sit together; two turns want air.
    gap: "1.1rem",
  },
  nothing: { color: colors.muted },
  /** The empty state, which is a sentence and an offer rather than a sentence. */
  empty: { display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "0.6rem" },
  item: {
    // Nothing pops. Every item in this list appears while somebody is looking
    // at the list, which is exactly the case the mandate is about.
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translateY(4px)" },
      to: { opacity: 1, transform: "none" },
    }),
    // The window's duration and curve, written out rather than read from
    // `columns.ts`. An identifier inside `stylex.create` is resolved by StyleX
    // and must come from a `.stylex.ts` file — interpolating an ordinary
    // constant is a build error about theming rules, which is not what is
    // wrong. A dynamic style would take it at runtime, and a dynamic style per
    // item in a list is a class per item.
    animationDuration: { default: "260ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  },
  said: { display: "flex", flexDirection: "column", gap: "0.15rem" },
  thought: { opacity: 0.7 },
  who: {
    color: colors.muted,
  },
  mine: { color: colors.accent },
  /** Quiet, and beside the name rather than under it — it qualifies "you". */
  queued: {
    marginInlineStart: "0.4rem",
    fontWeight: text.regular,
    color: colors.muted,
  },
  words: {
    color: colors.text,
    whiteSpace: "pre-wrap",
    // A flex child will not shrink below its content, and a long unbroken
    // token is content. Both of these, or one URL widens the column.
    minWidth: 0,
    overflowWrap: "anywhere",
  },
  /**
   * What somebody typed, at the size they will read the answer in.
   *
   * The same step and the same line height `Markdown`'s `reading` uses — see
   * the note there. Two sizes in one transcript would read as two documents.
   */
  reading: { fontSize: text.lead, lineHeight: 1.7 },
  /**
   * The block a run of tool calls lives in.
   *
   * A rule down the left and the rows tight against each other, so a dozen
   * calls read as one passage rather than as a dozen paragraphs. The gap
   * between rows is deliberately smaller than the gap between items in the
   * transcript: inside a block, the rows are a list.
   */
  calls: {
    display: "flex",
    flexDirection: "column",
    gap: "0.15rem",
    paddingInlineStart: "0.6rem",
    borderInlineStartWidth: 2,
    borderInlineStartStyle: "solid",
    borderInlineStartColor: colors.border,
  },
  /** `9 earlier calls` — a count, because the count is the reason to press it. */
  earlier: {
    alignSelf: "flex-start",
    padding: "0.1rem 0.3rem",
    marginInlineStart: "-0.3rem",
    backgroundColor: { default: "transparent", ":hover": colors.raised },
    borderStyle: "none",
    borderRadius: "0.2rem",
    color: colors.muted,
    font: "inherit",
    cursor: "pointer",
  },
  ran: { display: "flex", gap: "0.5rem", alignItems: "flex-start" },
  status: {
    color: colors.muted,
    paddingTop: "0.1rem",
  },
  grow: { flex: 1, minWidth: 0 },
  // A row, and a button, because its output folds. Reset the UA's own button
  // look by long-hand: StyleX drops `border` and `background` shorthands in
  // silence, which leaves macOS's 2px outset bevel around every tool call.
  command: {
    display: "flex",
    gap: "0.4rem",
    alignItems: "baseline",
    width: "100%",
    textAlign: "start",
    padding: 0,
    borderStyle: "none",
    backgroundColor: "transparent",
    cursor: { default: "pointer", ":disabled": "default" },
  },
  /**
   * What sort of thing it was — ui, because it is a word and not an address.
   *
   * A fixed width, so the subjects line up down the block. `read`, `edited`
   * and `searched` are three different widths, and a column of subjects that
   * each start somewhere else is the specific thing that made a run of these
   * hard to scan: the eye has no edge to run down.
   */
  verb: {
    flexShrink: 0,
    // Wide enough for `spawned`, the longest of the seven, and **right
    // aligned**: that puts a clean edge on both sides of the column, where
    // left-aligning it leaves a ragged gap after every short verb. What made
    // a run of these hard to scan is that the subjects each started somewhere
    // else — the eye had no edge to run down.
    width: "4rem",
    textAlign: "end",
    color: colors.muted,
  },
  /** The meta at the end — an elapsed time, a line count. */
  aside: {
    flexShrink: 0,
    color: colors.muted,
  },
  // ── the subject, on one line ────────────────────────────────────────────
  //
  // One row per call, whatever it was passed. It wrapped before, mid-word,
  // and a screenful of tool calls was an unreadable block — the row is a
  // scannable index into the transcript, and an index whose entries are three
  // lines long is not one. What is clipped is on the tooltip and, for a
  // multi-line command, behind the disclosure.
  subject: {
    display: "flex",
    flex: 1,
    minWidth: 0,
    alignItems: "baseline",
    // `nowrap` here as well: the flex line must not break between the two
    // halves of a path.
    whiteSpace: "nowrap",
    overflow: "hidden",
  },
  /** A path's directories: muted, and the part allowed to disappear first. */
  lead: {
    // A large factor, not merely `1`. Shrink is shared in proportion to base
    // size, so two shrinkable children both give ground and a path ends up
    // clipped at *both* ends; this makes the directories absorb nearly all of
    // it and the basename give way only once they have gone.
    flexShrink: 999,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.muted,
  },
  /** What it actually did — mono, because it is a command. */
  what: {
    // Shrinks last, not never. `flexShrink: 0` was the first answer and it is
    // wrong for the case with no `lead` at all — a long command is entirely
    // this span, so an unshrinkable one ran past the right edge of the panel
    // with no ellipsis and no scrollbar to say so. Measured at 820px: the row
    // reached 1560px.
    flexShrink: 1,
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
  },
  failed: { color: colors.warn },
  /**
   * Present, and out of the way until it is wanted.
   *
   * `opacity`, never `display: none` — an element outside the layout cannot
   * be tabbed to, and hover-only means the feature does not exist without a
   * pointer. The same shape `MoveToThread` uses.
   */
  /**
   * The control, beside the highlight.
   *
   * Raised and outlined, because it is floating over text rather than sitting
   * in a row — and it appears while somebody is looking at the words it is
   * over, which is exactly the case the window's animation mandate is about.
   */
  quote: {
    position: "fixed",
    zIndex: 5,
    width: `${String(QUOTE_WIDTH)}px`,
    height: `${String(QUOTE_HEIGHT)}px`,
    fontFamily: text.ui,
    fontSize: text.small,
    fontWeight: text.medium,
    borderStyle: "solid",
    borderWidth: 1,
    borderColor: colors.border,
    borderRadius: "0.3rem",
    backgroundColor: colors.raised,
    color: colors.text,
    cursor: "pointer",
    animationName: stylex.keyframes({
      from: { opacity: 0, transform: "translateY(3px)" },
      to: { opacity: 1, transform: "none" },
    }),
    animationDuration: { default: "160ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  },
  /** A sentence about a stall, not a state — see the note on the row. */
  retry: {
    marginTop: "0.15rem",
    color: colors.waiting,
  },
  answered: { color: colors.muted },
  working: {
    fontFamily: text.ui,
    fontSize: text.small,
    color: colors.muted,
    // It appears and disappears, so it moves. The mandate is the window's, and
    // a line that blinks into a transcript somebody is reading is the case it
    // exists for.
    animationName: stylex.keyframes({
      from: { opacity: 0 },
      to: { opacity: 1 },
    }),
    animationDuration: { default: "260ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
  },
  stopped: { color: colors.muted },
  /** The command in full, when the row showed one line of it. */
  whole: {
    color: colors.text,
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
    marginTop: "0.25rem",
  },
  // A patch under a tool row. Bounded, because a whole-file rewrite is a
  // legitimate edit and a transcript is not the diff panel: past this it
  // scrolls in its own box, which is the rule every wide thing in this window
  // follows.
  changed: {
    marginTop: "0.25rem",
    maxHeight: "20rem",
    overflowY: "auto",
  },
  output: {
    color: colors.muted,
    whiteSpace: "pre-wrap",
    overflowWrap: "anywhere",
    marginTop: "0.25rem",
    maxHeight: "12rem",
    overflowY: "auto",
  },
  asked: {
    borderStyle: "solid",
    borderWidth: 1,
    borderColor: colors.accent,
    borderRadius: "0.375rem",
    padding: "0.6rem",
    display: "flex",
    flexDirection: "column",
    gap: "0.5rem",
    backgroundColor: colors.surface,
  },
  question: {
    color: colors.text,
    overflowWrap: "anywhere",
  },
  options: { display: "flex", gap: "0.4rem", flexWrap: "wrap" },
  option: {
    padding: "0.25rem 0.6rem",
    borderRadius: "0.25rem",
    borderStyle: "solid",
    borderWidth: 1,
    borderColor: colors.border,
    backgroundColor: colors.raised,
    color: colors.text,
    cursor: "pointer",
  },
  allow: { borderColor: colors.live, color: colors.live },
  reject: { borderColor: colors.warn, color: colors.warn },
  warn: { color: colors.warn },
});
