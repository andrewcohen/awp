import type { ChatConfigOption } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { AnimatePresence, motion } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { type Command, agentCommands, commandOf, completed } from "@awp-kit/protocol/commands";
import { heldBack, toolTitleOf, turningAt } from "@awp-kit/protocol/tools";
import { Composer } from "./Composer";
import { rememberDraft, rememberedDrafts } from "./remembered";
import { type Arriving, STILL, heavy, jelly, useArriving, useSpring } from "./springs";
import { useTurning } from "./turning";
import { Mcp } from "./Mcp";
import {
  type Asked,
  type Compacted,
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
  chatCancel,
  chatConfig,
  chatFork,
  chatFresh,
  chatSend,
  chatSet,
  watchChat,
} from "./daemon";
import { type Spot, spotIn, withQuote } from "./quote";
import { typeset } from "./typeset";
import { colors, glaze, lift, text } from "./tokens.stylex";

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
  status === "completed"
    ? "✓"
    : status === "failed"
      ? "✗"
      : // A call the turn ended underneath — see `settleHangingCalls` in
        // chat.ts. Not a cross, which would be a claim about the tool:
        // what is known is that whatever was watching it stopped.
        status === "cancelled"
        ? "⊘"
        : "…";

/** Whether a call is still going, which is every status but an ending. */
const going = (status: string): boolean =>
  status !== "completed" && status !== "failed" && status !== "cancelled";

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
  /**
   * What is in the box, restored for this checkout.
   *
   * Read once, in the initialiser: the panel is keyed by the workspace, so
   * a different checkout is a different component with its own first
   * render, and reading it in an effect would paint an empty box for a
   * frame before filling it.
   */
  const [draft, setDraft] = useState(() => rememberedDrafts()[`${project}/${workspace}`] ?? "");

  // Written on the way out rather than on every keystroke: a write per
  // character is a parse, a mutate and a stringify per character, for a
  // value nothing reads until this panel is gone. The ref is what lets the
  // unmount effect see the latest text without re-running as it changes —
  // and it is written in an effect rather than during render, which is
  // both the rule and the reason `react(refs)` exists.
  const typed = useRef(draft);
  useEffect(() => {
    typed.current = draft;
  }, [draft]);
  useEffect(
    () => () => {
      rememberDraft(`${project}/${workspace}`, typed.current);
    },
    [project, workspace],
  );
  const [config, setConfig] = useState<ReadonlyArray<ChatConfigOption>>([]);
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
   * The dock, and how much of the column it is standing on.
   *
   * ── the composer overlays the transcript now ───────────────────────────
   *
   * It used to be the last child of a flex column, which is the honest
   * layout and leaves nothing behind the glass to blur: a backdrop filter
   * over the page colour is the page colour. So the dock is positioned over
   * the scroller and the scroller is padded by exactly its height, which
   * puts the tail of the conversation *under* it rather than above it —
   * and the last few lines are what is softened while somebody types.
   *
   * Measured rather than written down. The dock is one to four rows tall
   * depending on the draft, the settings chips wrapping and whether a turn
   * is running, and a constant here is a constant that is wrong in three of
   * those four states — which reads as a transcript that cannot be scrolled
   * to the end.
   */
  const dock = useRef<HTMLDivElement>(null);
  const [under, setUnder] = useState(0);
  // The activity ledge carries the composer with it, so it moves like a
  // panel rather than like a row — see `heavy`.
  const ledgeSpring = useSpring(heavy);

  /**
   * Say something, and remember it before anybody echoes it back.
   *
   * Its own function because two things say: the send, and a command from the
   * menu that turns out to be the agent's own. A command chosen there is a
   * message like any other — see the note in `run`.
   */
  const deliver = useCallback(
    (words: string) => {
      // A name for the message, given here because only the sender can give
      // one. The reply that says how it was delivered arrives while the list
      // is still growing, so a position in the list would name a different
      // message by then.
      // A uuid rather than a counter, now that the daemon echoes the message
      // back: two clients on one conversation would both mint `mine-1`, and
      // the dedupe is by key — so one window's second message would silently
      // swallow the other's.
      const key = crypto.randomUUID();
      const working = held.running > 0;
      // Shown immediately rather than waiting for the daemon to echo it back.
      // A message that appears only once the agent has acknowledged it reads
      // as a send button that did nothing — and there is nothing to wait for
      // anyway: measured, the adapter never echoes a live user message back,
      // so this copy is the only record of it until the session is opened
      // again.
      //
      // `mine` and not `fold`, because it is not something the daemon said.
      // See the note there: dressing it up as an update is what put a steer
      // above the rest of a reply that was still arriving.
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
    },
    [held.running, project, workspace],
  );

  /**
   * Run whatever was chosen in the menu.
   *
   * ── the agent's are prompts, and this is where that is honoured ─────────
   *
   * Return on a highlighted row arrives here whatever the row is, so a branch
   * on `mine` is the whole of the difference commands.ts draws. Without it
   * every row ran the window's `/new`: picking `/bro` threw the conversation
   * away instead of sending it, which is the worst available outcome for a
   * key somebody pressed to say something.
   *
   * A command that takes arguments is *completed* rather than sent — there is
   * nothing to send yet, and the next thing to press is the first character
   * of an argument.
   *
   * The box is cleared first in every case. A command is not a message and
   * leaving it in the box would read as one that failed to send — and `/new`
   * replaces this component, so anything set after the call is set on a
   * component that is about to go.
   */
  const run = useCallback(
    (command: Command) => {
      setDraft("");
      if (!command.mine) {
        if (command.hint === undefined) {
          deliver(command.name);
        } else {
          setDraft(completed(command));
        }
        return;
      }
      if (command.name === "/mcp") {
        setAsking(true);
        return;
      }
      onFresh();
    },
    [onFresh, deliver],
  );

  useEffect(
    () =>
      watchChat(
        project,
        workspace,
        (update) => {
          setHeld((current) => fold(current, update));
        },
        // A resubscription replays the conversation from the start — see
        // `watchChat`. Emptied first, so what arrives rebuilds the panel
        // rather than doubling it.
        () => setHeld(nothing),
      ),
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

  // ── following the tail is a state, not a test made afterwards ───────────
  //
  // The panel used to scroll to the bottom on *every* change, which is the
  // wrong rule twice over: reading something four screens up was impossible
  // while the agent was answering, and that is exactly when somebody goes
  // back to check what they asked for.
  //
  // So `stuck` is whether the reader is at the tail, and it is written when
  // they scroll. It cannot be recomputed at the moment content arrives — by
  // then the distance already includes whatever just landed, so one message
  // longer than the leash would read as somebody having scrolled away.
  //
  // It starts true: a conversation opens on its most recent exchange.
  const stuck = useRef(true);

  /**
   * The bottom, exactly.
   *
   * `scrollTop = scrollHeight` rather than `scrollIntoView` on a sentinel,
   * which is what this had and is what left a gap under the last row: that
   * method aligns an element with the *padding* edge, so the column's own
   * 1.25rem of bottom padding stayed unscrolled — and a zero-height sentinel
   * in a flex column with a 1.1rem gap adds a second gap above itself. Two
   * invisible boxes to reason about, against an assignment that cannot be
   * wrong about where the bottom is.
   */
  const follow = useCallback(() => {
    const column = scroll.current;
    if (column === null) return;
    column.scrollTop = column.scrollHeight;
  }, []);

  /** Follow, but only if there is a reader at the tail to follow. */
  const followIfStuck = useCallback(() => {
    if (stuck.current) follow();
  }, [follow]);

  /**
   * Whether the reader is still at the tail, written on their own scrolling.
   *
   * A programmatic scroll fires this too and lands at the bottom, so it stays
   * true; content arriving fires nothing at all, which is what makes the
   * reading survive a long answer.
   */
  const watch = useCallback(() => {
    const column = scroll.current;
    if (column === null) return;
    stuck.current = column.scrollHeight - column.scrollTop - column.clientHeight <= LEASH;
  }, []);

  useEffect(() => {
    if (grown !== "0:0") followIfStuck();
  }, [grown, followIfStuck]);

  /**
   * Keep the scroller's bottom padding equal to the dock standing on it.
   *
   * A `ResizeObserver` and not a measurement per render: the dock's height is
   * changed by things this component does not re-render for — the textarea
   * growing a line, the settings chips wrapping at a narrower column, the
   * activity ledge's own height spring, which produces a height per frame.
   */
  useEffect(() => {
    const pane = dock.current;
    if (pane === null) return;
    const watching = new ResizeObserver(() => setUnder(pane.offsetHeight));
    watching.observe(pane);
    setUnder(pane.offsetHeight);
    return () => watching.disconnect();
  }, []);

  // Padding that grows pushes the tail under the dock, so a reader at the
  // bottom has to be taken back to it — the same rule as content arriving.
  useEffect(() => {
    // Nothing to follow to before the first measurement: the padding is zero,
    // so the tail is already where the dock is about to be.
    if (under > 0) followIfStuck();
  }, [under, followIfStuck]);

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
    // Sending is asking to be at the tail, wherever the reader had scrolled
    // to — the message about to appear is theirs, and the answer to it is
    // the thing they are now waiting for. Set rather than scrolled: the row
    // does not exist yet, and the effect that follows `grown` is what puts
    // the view there once it does.
    stuck.current = true;
    deliver(words);
  }, [draft, deliver, run]);

  return (
    <div {...stylex.props(styles.chat)} data-column-part="chat">
      <div ref={scroll} {...stylex.props(styles.scroll, space.under(under))} onScroll={watch}>
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
          <Transcript
            items={items}
            project={project}
            workspace={workspace}
            live={held.running > 0 ? held.turn : undefined}
          />
        )}

        {held.stopped !== undefined && (
          <p {...stylex.props(typeset.label, styles.stopped)}>the turn ended: {held.stopped}</p>
        )}
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

      {/* ── the dock ────────────────────────────────────────────────────────

          The activity ledge and the composer, over the transcript rather
          than under it. Positioned, so the last lines of the conversation
          pass behind the glass instead of stopping above it — which is the
          whole of what the blur is for, and the reason the scroller's bottom
          padding is this element's own measured height.

          It carries no fill of its own: the ledge and the composer are each
          their own pane of glass, so the composer looks the same drawn on
          its own in the style guide as it does here. */}
      <div ref={dock} {...stylex.props(styles.dock)}>
        {/* ── the activity is a ledge on the composer, not the tail of the
          transcript ────────────────────────────────────────────────────────

          It used to be the last thing in the scroller, which put a line that
          changes every few seconds inside the surface somebody is reading:
          every new activity re-laid the tail out and the follow-the-tail
          effect chased it, so a transcript being read three screens up was
          not still either. A word, not a spinner — the jobs panel's rule —
          and on the wire rather than inferred from silence, because silence
          is also what an agent that answered with nothing looks like.

          Pinned here it is always in the same place, next to the box the
          answer is being waited for in, and the transcript above it is a
          document rather than a thing in motion. What it says is what the
          agent is doing *right now*: the live turn's last unfinished call,
          by the field that carries intent. See `Working`.

          The strip's own arrival is a height spring, so the composer is
          moved once per turn rather than on every change of activity —
          which is the shifting this was reported for. */}
        <AnimatePresence initial={false}>
          {held.running > 0 && (
            <motion.div
              key="working"
              {...stylex.props(styles.ledge)}
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: "auto", opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={ledgeSpring}
              onUpdate={followIfStuck}
              onAnimationComplete={followIfStuck}
            >
              <Working doing={doing(items, held.turn)} />
            </motion.div>
          )}
        </AnimatePresence>

        {/* Everything below the transcript, and it is a component now — the
          style guide draws it, and a copy on that page would be a copy that
          drifts. What stayed here is what has a consequence: what a message
          does, what a command does, and what an option change tells the
          daemon. */}
        <Composer
          draft={draft}
          onDraft={setDraft}
          onSend={say}
          // ── the same button stops the agent while it is working ──────────
          //
          // `ChatCancel` and nothing else: the turn ends the way every turn
          // ends, on the update stream, so there is nothing to report here.
          // An idle conversation ignores it, which is why the button is only
          // a stop while `working`.
          onStop={() => {
            void chatCancel(project, workspace).catch(() => {
              // The stream is where a conversation that cannot be had says
              // so — the same channel every other failure in this panel uses.
            });
          }}
          working={held.running > 0}
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
    </div>
  );
};

/**
 * The line that says the agent is going, and how long it has been.
 *
 * ── a still word is the same picture as a dead adapter ─────────────────
 *
 * `working…` sat there in one colour, not moving — which is exactly what
 * an agent whose adapter has died looks like, and the transcript above it
 * is not moving either while a model thinks. Reported as "the
 * thinking/working animation is super weak".
 *
 * Three things, and each says something the other two cannot:
 *
 *   the mark   the same braille the TUI turns, so the two faces agree
 *   the word   a band of light travelling across the glyphs. The one
 *              thing on screen that cannot be a still frame of anything
 *   the count  past ten seconds. What somebody is actually asking by then
 *              is "has this stalled", and no other row answers it
 *
 * Mounted only while a turn is in flight, which is what makes the count
 * honest with no state kept anywhere: the component's own life is the
 * turn. A steer starts a second turn inside the first and the count keeps
 * running, which is the truth — the agent has been working since it
 * started, not since the last thing said to it.
 */
export const Working = ({ doing }: { readonly doing?: string | undefined }) => {
  const turning = useTurning(true);
  const [since] = useState(() => Date.now());
  const [seconds, setSeconds] = useState(0);
  useEffect(() => {
    // A second is its own clock rather than a division of the frame
    // counter: the frames stop under reduced motion, and how long
    // something has taken is information rather than decoration.
    const timer = setInterval(() => setSeconds(Math.round((Date.now() - since) / 1000)), 1000);
    return () => clearInterval(timer);
  }, [since]);

  // `thinking` is the honest word for a turn with no call in flight, which
  // is a real and common state: the model is composing, and the row that
  // said `working` said nothing the spinner did not.
  const latest = doing === undefined || doing === "" ? "thinking" : doing;
  /**
   * What is actually drawn, which is the latest activity once it has held
   * still for a moment.
   *
   * ── a burst of calls is not four readings ────────────────────────────────
   *
   * An agent reading six files answers six tool calls inside a second, and
   * each one is a different sentence of a different length — so the pill rolled
   * six times and sprang to six widths in the time it takes to read one of
   * them. None of those was legible, and the movement is what made the line
   * hard to ignore rather than easy to glance at.
   *
   * Trailing, so a burst paints once, at the end, with the activity that is
   * still going. The cost is deliberate and is the other half of the feature:
   * a call that finishes inside {@link SETTLING} is never drawn at all, and a
   * reading nobody could have read is a reading not worth the movement.
   */
  const [say, setSay] = useState(latest);
  useEffect(() => {
    if (say === latest) return;
    const settle = setTimeout(() => setSay(latest), SETTLING);
    return () => clearTimeout(settle);
  }, [latest, say]);
  // The pill is as wide as its words, and the words change every few seconds.
  // See `styles.working` for why that has to be animated rather than jumped.
  const settling = useSpring();

  return (
    // No entrance of its own: the ledge it sits on animates its height, and
    // two animations on one thing is the fight `springs.ts` exists to stop.
    <motion.p
      {...stylex.props(styles.working)}
      // ── the width is animated, because it is the thing that changes ──────
      //
      // `read a file` and `Find who provides the worker pool` are a hundred
      // pixels apart, and the pill sizes to whichever it is holding. Snapped,
      // that is an edge jumping left and right beside the composer every time
      // the agent moves on — the same restlessness the ledge was pinned to
      // stop, arriving on the other axis.
      //
      // `layout="size"` and not `layout`: this sits in a dock anchored to the
      // bottom of the column, so a full layout animation would also animate
      // the position it is already being held at. Size is the only thing that
      // legitimately moves.
      layout="size"
      transition={settling}
    >
      <span {...stylex.props(styles.turningWord)} aria-hidden="true">
        {turningAt(turning)}
      </span>
      {/* ── it rolls, rather than shimmering ─────────────────────────────
          A gradient sweeping through a word is what every chat in the
          world does, and it is decoration: it says something is happening
          without saying what. Reported as "the shimmer is lame… dont just
          copy codex".

          This is a departures board. The line carries what the agent is
          doing *right now* — the live call's own purpose, which is the
          field that says intent — and each new activity rolls the old one
          up and out of the way. The movement is a consequence of the
          information changing, which is the only kind of movement that
          keeps being worth looking at.

          `mode="popLayout"` so the outgoing line leaves the flow at once
          and the incoming one does not wait for it — two lines of text
          sliding past each other in a strip one line tall. */}
      <span {...stylex.props(styles.rolling)}>
        <AnimatePresence initial={false} mode="popLayout">
          <motion.span
            key={say}
            {...stylex.props(styles.doing)}
            initial={{ y: "0.85em", opacity: 0 }}
            animate={{ y: 0, opacity: 1 }}
            exit={{ y: "-0.85em", opacity: 0 }}
            transition={jelly}
          >
            {say}
          </motion.span>
        </AnimatePresence>
      </span>
      {seconds >= WORTH_SAYING && (
        <span {...stylex.props(typeset.label, styles.since)}>{took(seconds)}</span>
      )}
    </motion.p>
  );
};

/**
 * What the agent is doing this second, or nothing.
 *
 * The last call of the live turn that has not finished — last, because a
 * turn runs them in order and the newest is the one happening; of the live
 * turn, because a call left hanging by an older turn is not work in
 * progress, whatever its status says.
 */
const doing = (items: ReadonlyArray<Item>, turn: number): string | undefined => {
  for (let at = items.length - 1; at >= 0; at -= 1) {
    const item = items[at];
    // A compaction is not a tool call and takes no turn with it, but it is
    // the whole of what the agent is doing while it runs — and it is the one
    // wait in this window that can be half a minute with no call to show for
    // it. It is last in the list while it is happening, so the same walk
    // finds it.
    if (item?.kind === "compacted") {
      return item.status === "running" ? "compacting" : undefined;
    }
    if (item?.kind !== "ran" || item.turn !== turn) continue;
    if (going(item.status)) return toolTitleOf(item);
  }
  return undefined;
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
 * How long an activity has to hold still before the line draws it, in ms.
 *
 * Long enough to swallow a burst — six `Read`s answered inside a second —
 * and short enough that a call somebody is waiting on appears to arrive at
 * once. It is deliberately not one of the `timing` tokens: those are how long
 * a movement takes, and this is how long to wait before starting one.
 */
const SETTLING = 220;

/**
 * How far from the bottom still counts as reading the tail, in pixels.
 *
 * Comfortably more than the activity ledge is tall, because the moment it
 * starts opening it has already pushed the tail this far out of view — a
 * threshold under its height would decide "they have scrolled away" about
 * the very movement it exists to correct.
 */
const LEASH = 120;

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
  turning,
  streaming = false,
  arriving = STILL,
}: {
  readonly item: Item;
  readonly project: string;
  readonly workspace: string;
  /** The frame a live call turns on. See `Tool` — undefined is most rows. */
  readonly turning?: number | undefined;
  /** Whether this row is the answer currently arriving. */
  readonly streaming?: boolean;
  /**
   * How the row enters, which is usually not at all.
   *
   * Defaulted to still, because most renders of a row are not its first:
   * Base UI unmounts a hidden tab, so switching to the diff and back
   * remounts the whole transcript, and a list that replays its entrance
   * every time somebody glances at another panel is a list that never
   * settles. `Transcript` is the only thing that knows which rows are new.
   */
  readonly arriving?: Arriving;
}) => {
  if (item.kind === "said") {
    return <Message item={item} streaming={streaming} arriving={arriving} />;
  }
  if (item.kind === "ran") {
    return (
      <Tool
        item={item}
        project={project}
        workspace={workspace}
        turning={turning}
        arriving={arriving}
      />
    );
  }
  if (item.kind === "compacted") {
    return <Boundary item={item} arriving={arriving} turning={turning} />;
  }
  return <Permission item={item} project={project} workspace={workspace} arriving={arriving} />;
};

/**
 * A compaction, drawn as a line across the conversation.
 *
 * ── a boundary, because that is what it is ───────────────────────────────
 *
 * `/compact` used to arrive as three paragraphs — `Compacting...`,
 * `Compacting completed.` — indistinguishable from the agent talking, and
 * saying nothing about the one thing that mattered: the transcript above is
 * no longer what the agent can see. Reported as "it just said compacting
 * compacting compacting".
 *
 * A rule across the column is the only shape that says "everything above
 * this is different from everything below it", and it is the shape a person
 * already reads in a diff and in a log. The word sits in the middle of it,
 * turning while it runs, so a compaction of a long conversation is not a
 * still frame for the half minute it takes.
 *
 * No token counts: they are under the composer already, and the figure
 * corrects itself from the adapter's own `compact_boundary` — see the
 * contract's note. A second copy here would be the one that drifts.
 */
const Boundary = ({
  item,
  arriving,
  turning,
}: {
  readonly item: Compacted;
  readonly arriving: Arriving;
  readonly turning?: number | undefined;
}) => {
  const running = item.status === "running";
  const failed = item.status === "failed";
  return (
    <motion.div {...stylex.props(styles.item, styles.boundary)} {...arriving}>
      <span {...stylex.props(styles.rule)} aria-hidden="true" />
      <span {...stylex.props(typeset.label, styles.boundaryWord, failed && styles.boundaryFailed)}>
        {running && (
          <span {...stylex.props(styles.turningWord)} aria-hidden="true">
            {turningAt(turning)}
          </span>
        )}
        {running ? "compacting" : failed ? "compacting failed" : "compacted"}
      </span>
      {failed && item.reason !== "" && (
        <span {...stylex.props(typeset.label, styles.boundaryWhy)}>{item.reason}</span>
      )}
      <span {...stylex.props(styles.rule)} aria-hidden="true" />
    </motion.div>
  );
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

const Message = ({
  item,
  streaming = false,
  arriving,
}: {
  readonly item: Said;
  /** Whether this is the answer currently arriving. See `styles.caret`. */
  readonly streaming?: boolean;
  /** How the row enters — see `Transcript`, which decides whether it does. */
  readonly arriving: Arriving;
}) => {
  return (
    <motion.div
      {...arriving}
      {...stylex.props(
        styles.item,
        styles.said,
        item.role === "user" && styles.fromYou,
        item.role === "thought" && styles.thought,
      )}
    >
      {/* The chevron says `you`, so the label does not — see `fromYou`. The
          row is still there for a queued message, because "sent, and the
          agent is finishing something else first" is not something a
          prompt can say. */}
      <span
        {...stylex.props(
          typeset.control,
          styles.who,
          item.role === "user" && styles.mine,
          item.role === "user" && !item.queued && styles.noWho,
        )}
      >
        {item.role === "user" ? "" : item.role === "thought" ? "thinking" : "agent"}
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
      {/* ── where the ANSWER has got to, and only the answer ──────────────
          A paragraph that has stopped growing and one still growing are the
          same picture, and this is the difference — the terminal's own
          idiom, in a window that is mostly a terminal.

          `role === "agent"` is not belt and braces. The caller marks the
          last row of a live turn, and the moment somebody sends a message
          the last row is *theirs* — so the caret blinked after what you had
          just typed, over nothing arriving. Reported as "a random blinking
          orange cursor when i send a message", which is exactly what it
          was. */}
      {streaming && item.role === "agent" && (
        <span {...stylex.props(styles.caret)} aria-hidden="true" />
      )}
    </motion.div>
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
  live,
}: {
  readonly items: ReadonlyArray<Item>;
  readonly project: string;
  readonly workspace: string;
  /**
   * The turn in flight, when one is.
   *
   * A call turns while **its own** turn is running, which is not the same
   * as "it has not finished": an adapter that stopped talking, or a turn
   * cancelled under a call, leaves a row at `pending` for good. The style
   * guide passes nothing and its fixture sits perfectly still, which is
   * what a transcript of work that is over should do.
   */
  readonly live?: number | undefined;
}) => {
  // One clock for the panel — see `useTurning`. It is stopped whenever
  // nothing is in flight, so an idle conversation costs no timer at all.
  const turning = useTurning(live !== undefined);
  const arriving = useArriving();
  /**
   * Which rows were already here when this panel mounted.
   *
   * Everything in that set enters still. A remount is not an arrival —
   * Base UI unmounts a hidden tab, so a glance at the diff and back would
   * otherwise spring forty rows up from the bottom, which reads as the
   * conversation being reloaded rather than as one message landing.
   *
   * A snapshot in state rather than a growing set: nothing has to be added
   * to it, because Motion runs `initial` once per *element*, and a row
   * that has already mounted does not mount again when the list re-renders
   * around it.
   */
  const [known] = useState(() => new Set(items.map((one) => one.key)));
  const enters = (key: string): Arriving => (known.has(key) ? STILL : arriving);
  /** The frame, for a call of the live turn only. */
  const frameFor = (item: Ran): number | undefined =>
    live !== undefined && item.turn === live ? turning : undefined;
  return (
    <>
      {grouped(items).map((block) =>
        block.kind === "calls" ? (
          <Calls
            key={block.key}
            items={block.items}
            project={project}
            workspace={workspace}
            turning={block.items.some((one) => one.turn === live) ? turning : undefined}
            enters={enters}
          />
        ) : (
          <Row
            key={block.key}
            item={block.item}
            project={project}
            workspace={workspace}
            turning={block.item.kind === "ran" ? frameFor(block.item) : undefined}
            // The last row, and only while a turn is in flight: an answer
            // that has stopped mid-sentence is the case this exists for,
            // and a caret on a finished one would be a lie about it.
            streaming={live !== undefined && block.item === items.at(-1)}
            arriving={enters(block.key)}
          />
        ),
      )}
    </>
  );
};

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
  turning,
  enters,
}: {
  readonly items: ReadonlyArray<Ran>;
  readonly project: string;
  readonly workspace: string;
  /** The frame a live call turns on. */
  readonly turning?: number | undefined;
  /** Whether a given row is new, and therefore whether it springs in. */
  readonly enters?: ((key: string) => Arriving) | undefined;
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
        <Tool
          key={item.key}
          item={item}
          project={project}
          workspace={workspace}
          turning={turning}
          arriving={enters?.(item.key) ?? STILL}
        />
      ))}
    </div>
  );
};

const Tool = ({
  item,
  project,
  workspace,
  turning,
  arriving,
}: {
  readonly item: Ran;
  readonly project: string;
  readonly workspace: string;
  /** How the row enters. See `Transcript`. */
  readonly arriving: Arriving;
  /**
   * The frame this call is turning on, or nothing.
   *
   * Nothing for every row that is not the live one — which is not the same
   * as every row that is finished. A call whose terminal status never
   * arrived, because the turn was cancelled under it or the adapter stopped
   * talking, sits at `pending` for the life of the conversation: turning on
   * "not finished" left a row from this morning spinning under one from
   * now. Reported as exactly that.
   */
  readonly turning?: number | undefined;
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
  // `toolTitleOf` first: a Bash call whose command has not streamed in yet is
  // titled `Terminal` by the adapter, which names nothing and repeats the
  // verb. See its note — the real command lands on the same id a moment
  // later.
  const said = toolTitle(item.subagent === undefined ? toolTitleOf(item) : `a ${item.subagent}`);
  // Openable for anything held back, not only for output. A twelve-line
  // heredoc drawn as its first line with no way to see the other eleven is a
  // row that has quietly lost the command — and so is a row drawn as what
  // the call is *for*, which is what `heldBack` answers.
  const behind = heldBack(item);
  const holds = item.output !== "" || said.more > 0 || behind;

  return (
    <motion.div
      {...arriving}
      // ── no sweep ──────────────────────────────────────────────────────
      //
      // The running row used to carry a slow band of light across itself, on
      // the argument that a turning mark is one cell and cannot catch an eye
      // reading three rows up. What it does in practice is move a gradient
      // under text somebody is trying to read, forever, in the one column
      // they are reading — and it kept going on rows whose call had long
      // since stopped, because the fold's `turning` is about the turn rather
      // than about the call. Both complaints, in order: stop it when the
      // tool is not running, then "actually just remove that".
      //
      // The mark still turns, which is the reading that was asked for and
      // the one the TUI agrees with.
      {...stylex.props(styles.item, styles.ran)}
    >
      {/* ── the running mark turns, and only the running one ──────────────
          `…` is what a call in flight had, and three dots are also what a
          truncated anything looks like — so the one row somebody is waiting
          on was the quietest thing in the column.

          `turning` is undefined for every row but the live one, and the
          same braille the TUI turns: a spinning notch in one face and a
          braille dot in the other is two vocabularies for one state. See
          `TURNING`. */}
      <span
        {...stylex.props(
          typeset.address,
          styles.status,
          item.status === "failed" && styles.failed,
          turning !== undefined && styles.turning,
        )}
      >
        {going(item.status) ? turningAt(turning) : mark(item.status)}
      </span>
      <div {...stylex.props(styles.grow)}>
        <button
          type="button"
          data-nav-item
          disabled={!holds}
          // ── no tooltip ────────────────────────────────────────────────────
          //
          // It carried the whole title — for a Bash call that is the command
          // in full, heredoc and all. The trade every other address in this
          // window makes is that a tooltip costs no pixels until it is asked
          // for, and here it is not asked for: the pointer crosses these rows
          // on its way to the composer, so forty lines of somebody's script
          // appeared over the transcript on the way past. Reported as "remove
          // the tool use hover tooltip that has the full tool log its
          // distracting".
          //
          // Nothing is lost that cannot be reached: a row with anything held
          // back is a disclosure, and pressing it shows the command in full
          // under the title — deliberately, not incidentally. See `holds`.
          onClick={() => setOpen((was) => !was)}
          {...stylex.props(styles.command)}
        >
          {/* Nothing at all for a command, which is most rows — and no
              empty span either, or the gap it leaves is the column that
              was just taken out. */}
          {/* Nothing at all for a command, which is most rows — and no
              empty span either, or the gap it leaves is the column that was
              just taken out.

              Nothing for a spawn either: the subject beside it already
              reads `a code-reviewer`, and the label made that
              `code-reviewer a code-reviewer`. A label is only worth a word
              when it says something the row does not. */}
          {item.subagent === undefined && verb(item) !== "" && (
            <span {...stylex.props(typeset.label, styles.verb)}>{verb(item)}</span>
          )}
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
        {open && (said.more > 0 || behind) && (
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
    </motion.div>
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
  arriving,
}: {
  readonly item: Asked;
  readonly project: string;
  readonly workspace: string;
  /** How the row enters. See `Transcript`. */
  readonly arriving: Arriving;
}) => (
  <motion.div {...arriving} {...stylex.props(styles.item, styles.asked)}>
    <p {...stylex.props(typeset.address, styles.question)}>{item.title}</p>
    <Answering item={item} project={project} workspace={workspace} />
  </motion.div>
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

/**
 * The scroller's bottom padding, which is the dock's height.
 *
 * A dynamic style because the value is measured at runtime, and because an
 * ordinary constant inside `stylex.create` is a build error about theming
 * rules — the trap this file's own notes record three times.
 */
const space = stylex.create({
  under: (px: number) => ({ paddingBottom: `${String(px)}px` }),
});

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
    // The dock's containing block. Everything else in this column is in the
    // flow; the dock is the one thing drawn *over* the transcript.
    position: "relative",
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
    // A longhand, because the bottom is not a constant: `space.under` sets it
    // to the dock's measured height so the transcript can be scrolled clear
    // of the glass standing on it. Two declarations rather than the shorthand
    // plus an override — StyleX resolves a shorthand against a longhand by
    // nulling, and which of the two wins is a question not worth having.
    paddingTop: "1.25rem",
    paddingInline: "1.25rem",
    display: "flex",
    flexDirection: "column",
    // Between turns, not between lines. A message and the tool call it caused
    // are one thought and want to sit together; two turns want air.
    gap: "1.1rem",
  },
  /**
   * The dock: the activity ledge and the composer, over the transcript.
   *
   * Absolute rather than the last child of the column, and that is the whole
   * of the glass working — a backdrop filter blurs what is *painted behind*
   * the element, and a sibling in a flex column has nothing behind it but the
   * page. Here the conversation itself runs underneath.
   *
   * No background: each pane inside it carries its own, so the composer is
   * the same object drawn on its own in the style guide.
   */
  dock: {
    position: "absolute",
    insetInline: 0,
    bottom: 0,
    display: "flex",
    flexDirection: "column",
  },
  nothing: { color: colors.muted },
  /** The empty state, which is a sentence and an offer rather than a sentence. */
  empty: { display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "0.6rem" },
  item: {
    // ── the entrance is Motion's now, not a keyframe ────────────────────
    //
    // Nothing pops — the mandate is unchanged — but a CSS keyframe replays
    // on every mount, and a hidden Base UI tab unmounts this whole list.
    // So the row's arrival is a spring the transcript hands it, and only
    // for rows that are actually new. See `springs.ts` and `enters`.
    //
    // Kept as an entry because it is where a row's own geometry lives, and
    // because two animations on one element is the fight this removed.
    position: "relative",
  },
  said: { display: "flex", flexDirection: "column", gap: "0.15rem" },
  /**
   * What *you* said, as a surface rather than as more prose.
   *
   * ── a transcript of one voice reads as a document ──────────────────────
   *
   * Every row was ink on the same ground, so a conversation looked like an
   * essay with the speaker's name in the margin — and the thing somebody
   * scans a transcript for is the shape of the exchange: what I asked, what
   * it did, what it answered. Reported as "super flat and lame".
   *
   * The agent keeps the page, because it is the long half and a fill under
   * four paragraphs of markdown is a box, not a message. Yours gets the
   * surface: short, and there are few of them, which is exactly when a
   * fill reads as an utterance rather than as furniture.
   *
   * The edge is `border`, not the accent. The accent is spent on four
   * things and "who is talking" is not one of them — see AGENTS.md.
   */
  /**
   * What *you* said, drawn as the prompt it was typed at.
   *
   * ── a bubble is somebody else's application ────────────────────────────
   *
   * The first attempt was a rounded fill, and it was reported back as
   * "what am i imessage 2007" — which is right, and the deeper problem is
   * that a chat bubble is a borrowed idiom. It says "this is a messaging
   * app" about a window whose whole subject is terminals.
   *
   * A prompt says the same thing — this half is yours, that half is the
   * machine's — in this application's own vocabulary, and it is the mark
   * every person using this reads a hundred times a day in the pane two
   * columns over. It costs one character and no fill, no radius and no
   * shadow.
   *
   * It also replaces the `you` label rather than joining it: a chevron and
   * the word `you` on the line above it are two marks for one fact.
   */
  fromYou: {
    position: "relative",
    paddingInlineStart: "1.1rem",
    // Space before the prompt, because a prompt is where an exchange
    // starts: what somebody scans a transcript for is the boundary between
    // "what I asked" and "what it did", and that boundary is this line.
    // Not after it — the answer belongs to the question.
    marginBlockStart: { default: "0.7rem", ":first-child": 0 },
    "::before": {
      content: '"❯"',
      position: "absolute",
      insetInlineStart: 0,
      insetBlockStart: 0,
      fontFamily: text.mono,
      color: colors.accent,
      // The prose line-height, or the mark sits a line above the words it
      // belongs to: an absolutely positioned glyph takes its own line box,
      // and this column reads at 1.7. Measured — the first version put the
      // chevron level with the top of the box and the sentence a third of
      // a line below it, which reads as two rows rather than a prompt.
      lineHeight: 1.7,
      // Not quite the text's own weight: the mark is an address, and an
      // address at full strength competes with the sentence beside it.
      opacity: 0.85,
    },
  },
  thought: { opacity: 0.7 },
  who: {
    color: colors.muted,
  },
  mine: { color: colors.accent },
  /** No label at all: the prompt mark has already said whose words these are. */
  noWho: { display: "none" },
  /** Quiet, and beside the name rather than under it — it qualifies "you". */
  queued: {
    marginInlineStart: "0.4rem",
    fontWeight: text.regular,
    color: colors.muted,
  },
  words: {
    // A `<p>` carries a 1em margin from the UA, which this column does not
    // want: the block already sits in a flex column with its own gap, and
    // the margin put the prompt mark a whole line above the sentence it
    // belongs to. Measured — the row began 16px above its own text.
    margin: 0,
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
  /**
   * A run of calls, as one object.
   *
   * It was a 2px rule and nothing else, which put the run at the same depth
   * as the prose around it — so a dozen receipts read as a dozen more
   * sentences. A quiet card says "this is one thing, and it is not what you
   * came to read", which is exactly what the grouping already decided.
   *
   * `page` rather than `surface`: the run sits *under* the conversation,
   * not on it, and the darker ground is what says so in the dark theme
   * without a border doing the work.
   */
  calls: {
    display: "flex",
    flexDirection: "column",
    gap: "0.1rem",
    padding: "0.3rem 0.45rem",
    backgroundColor: colors.page,
    borderRadius: "0.5rem",
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
  /**
   * The running mark, which turns because the glyph changes — not because
   * anything here rotates. So it needs no animation and no reduced-motion
   * branch: the clock that feeds it does not run at all under one.
   *
   * The accent, and this is the fifth place the window spends it. It earns
   * it on the same rule as the other four: at most one row in a transcript
   * is running, so it marks a deviation rather than a baseline.
   */
  turning: { color: colors.accent },
  /**
   * The caret at the tail of an answer still arriving.
   *
   * The transcript already moves while text streams, so this is not there
   * to say "something is happening" — it says **where**, which is the one
   * thing a growing paragraph does not: an answer that pauses mid-sentence
   * and an answer that has finished are the same picture without it.
   *
   * A block, and the terminal's own idiom, in a window that is mostly a
   * terminal. It blinks on two steps rather than fading, because a fading
   * caret is a caret somebody keeps checking.
   */
  caret: {
    display: "inline-block",
    width: "0.5em",
    height: "1em",
    marginInlineStart: "0.15em",
    verticalAlign: "text-bottom",
    backgroundColor: colors.accent,
    animationName: stylex.keyframes({ "0%, 49%": { opacity: 1 }, "50%, 100%": { opacity: 0 } }),
    animationDuration: { default: "1.06s", "@media (prefers-reduced-motion: reduce)": "0s" },
    animationTimingFunction: "steps(1, end)",
    animationIterationCount: {
      default: "infinite",
      "@media (prefers-reduced-motion: reduce)": "1",
    },
  },
  verb: {
    flexShrink: 0,
    // ── no column, because most rows have nothing to put in it ──────────
    //
    // It was 4rem and right aligned, so a run of rows shared an edge and
    // the eye had something to run down. That was right while every row
    // carried a word. `toolLabel` suppresses the baseline now — a command
    // says it is one by being one — so the column was empty on most rows
    // and the edge it aligned had nothing sitting on it.
    //
    // The mark to its left is the edge that survives, and it is on every
    // row rather than on half of them.
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
  /**
   * The activity, as a pill rather than a bar.
   *
   * Asked for as "maybe should not be full width maybe just take the space it
   * takes and rounded". `inline-flex` is what makes it shrink to its words —
   * the strip above is a flex row starting at the leading edge, so this floats
   * over the transcript at whatever width the sentence needs and the rest of
   * the line is the conversation showing through.
   *
   * It is the glass now, and the blur is worth more here than it was on the
   * band: what is behind this is three or four words' worth of transcript
   * rather than the whole column, so the softening reads as depth instead of
   * as a second surface.
   *
   * The margins rather than padding on the strip: vertical padding on a box
   * animating to `height: 0` leaves a gap that never closes, and `margin: 0`
   * in the first place because a `<p>` carries 1em from the UA.
   */
  working: {
    display: "inline-flex",
    alignItems: "baseline",
    gap: "0.45rem",
    fontFamily: text.ui,
    fontSize: text.small,
    color: colors.muted,
    // ── it hugs its words, wherever it is drawn ──────────────────────────
    //
    // `inline-flex` alone is not enough: a flex item's display is blockified,
    // so inside the strip — and inside the style guide's own specimen cell —
    // it becomes `flex` and stretches to the cross axis. Measured at 939px in
    // a 976px cell, which is the full-width bar this was meant to stop being.
    // `align-self` is the property that actually answers it.
    alignSelf: "flex-start",
    // ── as wide as its words, and never wider than the column ────────────
    //
    // It carried a 420px ceiling for a while, which clipped a long purpose to
    // keep the pill short. What that cost is the half of the sentence that
    // says what the agent is doing, on the one line whose whole job is to say
    // it — so the cap is the column, and a sentence that will not fit there
    // still clips: `rolling` is a one-line window with `overflow: hidden` and
    // `doing` ends in an ellipsis.
    maxWidth: "100%",
    // Close to the composer: this is a label *about* the box below it, and a
    // gap the size of the transcript's own makes it read as the last row of
    // the conversation instead.
    margin: "0.1rem 0 0.3rem",
    padding: "0.3rem 0.7rem",
    // Fully round, so it reads as a marker on the page rather than as a small
    // panel — every other rounded thing in this window is a control.
    borderRadius: "999px",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: `color-mix(in oklab, ${colors.border} 70%, transparent)`,
    backgroundColor: colors.glass,
    backdropFilter: glaze.pane,
    // `low`, not `mid`: this is a label that appears for the length of a turn,
    // and the card below it is the thing that should look nearest.
    boxShadow: lift.low,
  },
  /**
   * The strip the activity sits on: pinned above the composer, clipped, and
   * the thing whose height animates.
   *
   * `overflow: hidden` is what makes a height spring possible at all, and
   * the padding is inline-only for the same reason — vertical padding on a
   * box animating to `height: 0` leaves a gap that never closes.
   */
  ledge: {
    flexShrink: 0,
    overflow: "hidden",
    paddingInline: "1.25rem",
    // ── the strip carries no fill; the pill inside it does ────────────────
    //
    // It was a full-width band of glass, which drew a second horizontal
    // register above the composer and made the dock two stacked slabs. What
    // the line actually is is one short sentence about what is happening
    // right now, and a bar the width of the column is a container claiming
    // more of the screen than its contents ever use.
    //
    // So this element is only the clipping box the height spring needs, and
    // `align-items: flex-start` is what lets the pill be as wide as its
    // words. See `working`.
    display: "flex",
    alignItems: "flex-start",
  },
  /**
   * The one-line window the activity rolls through.
   *
   * `overflow: hidden` is the whole mechanism: the outgoing line leaves
   * upward and the incoming one arrives from below, and neither is ever
   * seen outside the strip. A fixed height rather than a min, because the
   * two are briefly both in the flow and the line must not grow by one.
   */
  rolling: {
    position: "relative",
    display: "inline-flex",
    alignItems: "center",
    height: "1.25em",
    // The one part that gives when the pill is at its ceiling. `minWidth: 0`
    // is the half that matters: a flex item will not shrink below its content
    // without it, so the sentence would push the elapsed count out instead of
    // clipping.
    flexShrink: 1,
    minWidth: 0,
    overflow: "hidden",
  },
  /** The activity itself. Clipped rather than wrapped: this is one line. */
  doing: {
    display: "block",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    color: colors.text,
  },
  /**
   * A compaction: a rule across the column with a word in the middle of it.
   *
   * The one row in a transcript that is about the transcript rather than in
   * it, so it is the only one drawn edge to edge.
   */
  boundary: {
    display: "flex",
    alignItems: "center",
    gap: "0.6rem",
    marginBlock: "0.4rem",
  },
  /** Half the line, either side of the word. */
  rule: { flex: 1, minWidth: "1rem", height: 1, backgroundColor: colors.border },
  boundaryWord: {
    display: "inline-flex",
    alignItems: "center",
    gap: "0.4rem",
    color: colors.muted,
    // Its own claim on the eye is small — a compaction is worth noticing once
    // and never looking for again.
    letterSpacing: "0.02em",
  },
  boundaryFailed: { color: colors.warn },
  /** Why it failed, in the adapter's own sentence. */
  boundaryWhy: { color: colors.muted, opacity: 0.85 },
  /** The turning frame, in the accent, leading the line. */
  turningWord: { color: colors.accent, fontFamily: text.mono },
  /** How long the turn has been going. Tabular, so it does not jitter. */
  // Never clipped: it is four characters, and a half-drawn duration is worse
  // than none. The activity is what gives — see `rolling`.
  since: { flexShrink: 0, fontVariantNumeric: "tabular-nums", opacity: 0.75 },
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
    // The one control in the transcript that stops the agent until somebody
    // presses it, and it did not acknowledge the press. A button that
    // answers under the finger is the difference between "did that work"
    // and knowing it did — and this is the press people make while reading
    // something else.
    boxShadow: lift.low,
    transitionProperty: "transform, box-shadow, background-color, border-color",
    transitionDuration: { default: "130ms", "@media (prefers-reduced-motion: reduce)": "0s" },
    transitionTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
    ":hover": { transform: "translateY(-1px)", boxShadow: lift.mid },
    ":active": { transform: "scale(0.96) translateY(0)", boxShadow: "none" },
  },
  allow: { borderColor: colors.live, color: colors.live },
  reject: { borderColor: colors.warn, color: colors.warn },
  warn: { color: colors.warn },
});
