// The agent's conversation, for one checkout.
//
// The same `ChatOpen` the window uses. Everything below the fold is shared with
// the imperative first draft — `conversation.ts` and `lines.ts` did not change
// when this became React, which is the argument for having split them.
//
// What React took away is the part that kept breaking: a hand-managed pool of
// text rows, a scroll offset, a follow flag and a paint function. A `scrollbox`
// with `stickyScroll` is all four, and it handles the wheel.

import { useEffect, useRef, useState } from "react";
import { useTerminalDimensions } from "@opentui/react";
import type { TextareaRenderable } from "@opentui/core";
import { useKeyboard } from "@opentui/react";
import type { ChatConfigOption, McpStatus } from "@awp-kit/protocol";
import {
  type Command,
  agentCommands,
  commandOf,
  completed,
  matching,
} from "@awp-kit/protocol/commands";
import {
  chatAnswer,
  chatCancel,
  chatConfig,
  chatFresh,
  chatSend,
  mcpStatus,
  onReconnect,
  said,
} from "./daemon";
import { grouped } from "./conversation";
import { Boundary, Call, Calls, Message } from "./Items";
import { wrap } from "./lines";
import { isBack, isQuit } from "./keys";
import { CHROME, SPIN } from "./theme";
import type { Place } from "./Threads";
import { useConversation, useSpinner } from "./useConversation";

/**
 * How a setting reads in the status row.
 *
 * The value's own name, and the setting's name in front of it when the value
 * would not say which setting it belongs to — `Manual` and `Opus` name
 * themselves, `On` and `Off` do not. The same rule as the window's chips, and
 * a second copy of six lines rather than an import: the window's lives in a
 * file that imports StyleX, which is not loadable here.
 */
const settingOf = (option: ChatConfigOption): string => {
  const now =
    option.values.find((value) => value.value === option.currentValue)?.name ?? option.currentValue;
  return now === "On" || now === "Off" || now === "Default"
    ? `${option.name.toLowerCase()}: ${now.toLowerCase()}`
    : now;
};

/**
 * What the status row says when nothing has gone wrong.
 *
 * The same read-only facts the window draws under its composer — mode, model,
 * effort, fast mode, and how full the context is. Read-only because this
 * client has no select to change one with, and a figure somebody cannot act on
 * is still the figure they decide `/new` on.
 */
const facts = (
  config: ReadonlyArray<ChatConfigOption>,
  used: number | undefined,
  size: number | undefined,
): string => {
  const parts = config.map(settingOf);
  // No floor under the figure, deliberately, and the window's note says why:
  // the decision it informs — carry on here, or start again — is made before
  // the context is a problem.
  if (used !== undefined && size !== undefined && size > 0) {
    parts.push(`${String(Math.round((used / size) * 100))}% context`);
  }
  return parts.join(" · ");
};

/**
 * The screen, and the one thing above it that survives `/new`.
 *
 * Starting again is a remount: everything the panel holds is about a
 * conversation that is no longer this workspace's, and a key is the whole of
 * saying so — the same shape the window uses, and the reason `useConversation`
 * deliberately has no reset in it.
 *
 * A refusal has to outlive the remount, though, which is why it is held here
 * and handed down: the component that would have drawn it is the component
 * being replaced.
 */
export const Chat = (props: { place: Place; onBack: () => void; onQuit: () => void }) => {
  const [again, setAgain] = useState(0);
  const [refusal, setRefusal] = useState("");
  return (
    <Panel
      key={again}
      {...props}
      refusal={refusal}
      onFresh={() => {
        chatFresh(props.place.project, props.place.workspace).then(
          () => {
            setRefusal("");
            setAgain((was) => was + 1);
          },
          (error: unknown) => setRefusal(said(error)),
        );
      }}
    />
  );
};

/** How many command rows the menu draws before it stops and says how many are left. */
const CAP = 6;

/** As much of a sentence as there is room for, and an ellipsis where it stopped. */
const cut = (text: string, room: number): string =>
  room <= 1 ? "" : text.length <= room ? text : `${text.slice(0, room - 1)}…`;

/**
 * The command menu, above the box.
 *
 * Above rather than below because it is a list of things the *box* can
 * become, and the eye is already at the box.
 *
 * Its own component so the render probe can draw it: the interesting states
 * — 57 skills, a highlight walked past the sixth row — are ones a live agent
 * happens not to be in, and a probe that rebuilt the rows itself would be
 * testing its own reconstruction.
 */
export const Menu = ({
  commands,
  at,
  width,
}: {
  commands: ReadonlyArray<Command>;
  /** Which row is highlighted, as an index into the whole list. */
  at: number;
  /** The screen's, so a description can be cut rather than overrun. */
  width: number;
}) => {
  if (commands.length === 0) return undefined;
  // Six rows, then a count. Unfiltered this is every skill the agent has
  // discovered — 57 on this machine — and a menu that tall is the transcript
  // gone, to say what one more letter would narrow to three rows. The window
  // scrolls its own at the same six; here the highlight is what scrolls, so
  // walking down past the sixth brings the seventh into view.
  const from = Math.max(0, Math.min(at - CAP + 1, commands.length - CAP));
  const shown = commands.slice(from, from + CAP);
  const rest = commands.length - from - shown.length;
  // One column for the names, so the descriptions line up and the eye has an
  // edge to run down. Measured off what is *shown* rather than off the whole
  // list: a menu narrowed to `/new` should not keep a gutter the width of the
  // longest skill on the machine.
  const naming = shown.reduce(
    (most, one) =>
      Math.max(most, one.name.length + (one.hint === undefined ? 0 : one.hint.length + 1)),
    0,
  );
  return (
    // ── it takes room, it does not float ─────────────────────────────────
    //
    // `flexShrink={0}`, and it is the whole of the menu appearing *above*
    // the composer rather than over it. Every child of a column shrinks by
    // default, so a menu with no height of its own was squeezed to two rows
    // and its remaining rows painted outside their parent — on top of the
    // composer, in the order they happened to be drawn. Measured: with six
    // commands the box drew two, then the box below it, then a third
    // command over that.
    <box flexDirection="column" flexShrink={0} height={shown.length + (rest > 0 ? 1 : 0)}>
      {shown.map((command, index) => (
        /* Each row is a box so the highlight is a full-width band: a `text`
           with a `bg` paints under its own characters and nowhere else,
           which reads as a coloured phrase rather than a selected row. */
        <box
          key={command.name}
          height={1}
          flexDirection="row"
          paddingLeft={1}
          paddingRight={1}
          backgroundColor={from + index === at ? CHROME.bar : CHROME.base}
        >
          <text
            width={naming}
            wrapMode="none"
            fg={from + index === at ? CHROME.text : CHROME.muted}
            content={`${command.name}${command.hint === undefined ? "" : ` ${command.hint}`}`}
          />
          {/* Cut, not wrapped and not left to overflow. A flex child here
              will happily draw past the row and the `awp` mark is painted on
              top of it — measured, and it reads as a description with three
              letters of nonsense in the middle of it. */}
          <text
            flexGrow={1}
            wrapMode="none"
            fg={CHROME.muted}
            content={cut(`  ${command.said}`, width - 2 - naming - (command.mine ? 4 : 0))}
          />
          {/* This client's two are marked, not the agent's dozens. An agent
              here advertises 57 and this client has two, so marking the
              majority would be marking the baseline — what somebody needs to
              know is which rows do NOT reach their agent. */}
          {command.mine ? <text wrapMode="none" fg={CHROME.muted} content=" awp" /> : undefined}
        </box>
      ))}
      {rest <= 0 ? undefined : (
        <box height={1} paddingLeft={1}>
          <text fg={CHROME.muted} wrapMode="none" content={`… ${String(rest)} more`} />
        </box>
      )}
    </box>
  );
};

const Panel = ({
  place,
  onBack,
  onQuit,
  onFresh,
  refusal,
}: {
  place: Place;
  onBack: () => void;
  onQuit: () => void;
  onFresh: () => void;
  refusal: string;
}) => {
  const { state, saidLocally } = useConversation(place.project, place.workspace);
  // The composer owns its text — a `textarea` takes no `value`, which is the
  // right way round for an editor with a cursor and a selection in it. What is
  // kept here is a mirror, for the footer and for what `esc` has to know.
  // `plainText` reads it, `clear()` empties it.
  const composer = useRef<TextareaRenderable | null>(null);
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState("");
  /**
   * Which row of the command menu is highlighted.
   *
   * Kept rather than derived, because the arrows move it — and reset on every
   * keystroke in the box, since an index into a list that has just been
   * filtered names a row in the wrong list.
   */
  const [picked, setPicked] = useState(0);
  /** `/mcp` — what the daemon handed this conversation, once it has been asked. */
  const [mcp, setMcp] = useState<McpStatus | undefined>(undefined);
  const working = state.running > 0;
  const tick = useSpinner(working);

  // ── the session's own facts, asked for once ─────────────────────────────
  //
  // A call rather than a field on the stream, which is the contract's choice
  // and the right one here too: nothing in this list changes unless somebody
  // changes it, and this client offers no way to. A refusal is silence — the
  // row simply has fewer things in it, which is what a conversation the daemon
  // could not open looks like anyway, and the transcript says that better.
  const [config, setConfig] = useState<ReadonlyArray<ChatConfigOption>>([]);
  useEffect(() => {
    let live = true;
    const ask = () => {
      chatConfig(place.project, place.workspace).then(
        (answer) => {
          if (live) setConfig(answer);
        },
        () => {
          // Nothing. See above.
        },
      );
    };
    ask();
    // A call rather than a feed, so nothing brings it back on its own: after
    // a daemon restart the status row had lost the model and the mode and
    // there was no way to get them back short of leaving the screen.
    const stop = onReconnect(ask);
    return () => {
      live = false;
      stop();
    };
  }, [place.project, place.workspace]);

  // ── two lines at rest, six at most ──────────────────────────────────────
  //
  // One was the first answer and it is too tight: a box the height of the
  // text in it has nowhere for the caret to go, so a message being edited
  // scrolls inside a single row and the line above what somebody is typing is
  // the transcript. Two is a box that looks like somewhere to write.
  //
  // Six is still the ceiling: past that the composer is eating the
  // conversation it is about.
  const { width } = useTerminalDimensions();
  const room = Math.max(20, width - 6);
  // ── every block is given a width, and none is left to infer one ──────────
  //
  // A flex child will not shrink below its content unless it is told it may,
  // which is the same rule the renderer's own AGENTS.md records for the web:
  // `flex: 1` **with** `minWidth: 0`, and either alone is the bug. Here it
  // showed up twice at once — an agent's paragraph ran off the right, and the
  // tool rows collapsed to the width of the four-cell label beside it, which
  // is where the 4 came from.
  const inner = Math.max(24, width - 2);
  const rows = Math.min(6, Math.max(2, wrap(draft, room).length));

  const ask = state.items
    .toReversed()
    // A settled question is not pending: its buttons are gone, and the answer
    // keys would otherwise reach for it and earn a refusal about somebody
    // else's click.
    .find(
      (item) => item.kind === "tool" && item.ask !== undefined && item.ask.answered === undefined,
    );
  const pending = ask?.kind === "tool" ? ask.ask : undefined;

  // ── the command menu ────────────────────────────────────────────────────
  //
  // Two sets, and the rule for which is which is in the contract package
  // rather than here: `/new` and `/mcp` are things this client does, and
  // everything else the agent advertises is a prompt the adapter passes
  // through. A face deciding that for itself would be the second
  // implementation, and the copy that drifts is the one nobody tests.
  //
  // The list is only ever open while the draft is a bare `/word`, so a
  // message about `/tmp/build.log` puts nothing over the transcript.
  const menu = matching(draft, agentCommands(state.commands));
  /** The highlighted row, clamped: the list is re-filtered on every keystroke. */
  const highlight = Math.min(picked, Math.max(0, menu.length - 1));

  /** Empty the box, and the mirror of it this component keeps. */
  const empty = () => {
    setDraft("");
    setPicked(0);
    if (composer.current !== null) composer.current.clear();
  };

  /** Put something in the box, with the caret after it. */
  const fill = (text: string) => {
    setDraft(text);
    setPicked(0);
    if (composer.current !== null) {
      // `clear` then `insertText` rather than `setText`, because the caret
      // has to end up after what was just written — a completion somebody
      // then types into is the whole reason Tab fills the box instead of
      // running the command.
      composer.current.clear();
      composer.current.insertText(text);
    }
  };

  /**
   * What the status row says, in the order the row is worth reading in.
   *
   * A refusal first — this client's own, then the one `/new` earned on a
   * component that no longer exists — and then whatever the keys are about
   * to do, which is only worth a line while it is not obvious. Everything
   * under that is the session's facts, which is the ordinary state.
   */
  const told = [
    notice,
    refusal,
    menu.length === 0 ? "" : "tab to complete · return to run · esc to leave it",
    mcp === undefined ? "" : "esc to put this away",
  ].find((one) => one !== "");

  const send = (text: string) => {
    const message = text.trim();
    if (message === "") return;
    // ── this client's commands are intercepted, and narrowly ─────────────
    //
    // Exact match on the whole draft. `/new` is a command and
    // `/tmp/build.log is missing` is a message about a path — a prefix match
    // would eat the second.
    const command = commandOf(message);
    if (command !== undefined) {
      run(command);
      return;
    }
    empty();
    // One name for two copies of the same message: the one drawn here on the
    // keypress, and the daemon's echo — which exists so the *window* sees what
    // was typed here. A uuid, because a counter in two clients collides.
    const key = crypto.randomUUID();
    saidLocally(message, key);
    // Nothing is said about it going well. `sending…`, then `sent as a new
    // turn` or `steered into the running turn`, was three reports of a thing
    // the transcript shows: the message is on screen, and the agent either
    // answers it or does not. A row that narrates every success is a row
    // whose one useful line — a refusal — arrives somewhere the eye has
    // already learned to skip.
    chatSend(place.project, place.workspace, message, key).then(
      () => setNotice(""),
      (error: unknown) => setNotice(said(error)),
    );
  };

  /**
   * Run whatever was chosen in the menu.
   *
   * The branch on `mine` is the whole of the difference the two sets draw:
   * this client's two are acts, and the agent's are messages. Sent as text
   * `/new` would reach the agent as a sentence *about* a command and be
   * answered rather than run; run, `/bro` would throw away the conversation
   * it was meant to be typed into.
   *
   * A command that takes arguments is completed rather than sent. There is
   * nothing to send yet, and the next thing to press is an argument.
   */
  const run = (command: Command) => {
    if (!command.mine) {
      if (command.hint === undefined) {
        send(command.name);
      } else {
        fill(completed(command));
      }
      return;
    }
    empty();
    if (command.name === "/mcp") {
      setNotice("");
      mcpStatus(place.project, place.workspace).then(setMcp, (error: unknown) =>
        setNotice(said(error)),
      );
      return;
    }
    // `/new`. The panel above this one remounts, so nothing set after this
    // call is set on a component that will draw again.
    onFresh();
  };

  const answer = (which: "first" | "last") => {
    if (pending === undefined) {
      setNotice("nothing is being asked");
      return;
    }
    const option = which === "first" ? pending.options[0] : pending.options.at(-1);
    if (option === undefined) return;
    chatAnswer(place.project, place.workspace, pending.request, option.id).then(
      // The row itself changes — the buttons go and what was chosen stays, on
      // whichever client answered. Saying it again here is a second copy of
      // something already on screen.
      () => setNotice(""),
      (error: unknown) => setNotice(said(error)),
    );
  };

  useKeyboard((key) => {
    if (isQuit(key)) {
      key.preventDefault();
      onQuit();
      return;
    }
    if (isBack(key)) {
      key.preventDefault();
      onBack();
      return;
    }
    // ── the menu's keys, and only while it is open ───────────────────────
    //
    // A global handler runs before the focused renderable's and
    // `preventDefault` stops it, so these are taken from the textarea for as
    // long as the box holds a bare `/word` and handed straight back after.
    // The caret never leaves the box — a menu that took focus would take the
    // typing with it.
    if (menu.length > 0) {
      if (key.name === "down" || key.name === "up") {
        key.preventDefault();
        setPicked((was) => (was + (key.name === "down" ? 1 : -1) + menu.length) % menu.length);
        return;
      }
      if (key.name === "tab") {
        // Completion, not selection: Tab fills the box and leaves the next
        // gesture — return — to run it, so a wrong pick can still be edited
        // or abandoned.
        key.preventDefault();
        const one = menu[highlight];
        if (one !== undefined) fill(completed(one));
        return;
      }
      if (key.name === "return" || key.name === "kpenter" || key.name === "linefeed") {
        key.preventDefault();
        const one = menu[highlight];
        if (one !== undefined) run(one);
        return;
      }
      if (key.name === "escape") {
        // The draft is what the menu is open on, so emptying it is what
        // closes the menu — and it takes precedence over stopping the agent,
        // because a slash typed mid-turn is somebody reaching for a command
        // rather than for the interrupt.
        key.preventDefault();
        empty();
        return;
      }
    }
    // What `/mcp` answered is dismissed before anything else escape means.
    // It is a reading, not a mode: the next thing somebody does is type.
    if (mcp !== undefined && (key.name === "escape" || (key.ctrl && key.name === "c"))) {
      key.preventDefault();
      setMcp(undefined);
      return;
    }
    // ── escape stops the agent, and only then the draft ──────────────────
    //
    // Two meanings, in that order, which is the order the terminal habit
    // already has: while something is running, the key that interrupts it is
    // the one everybody reaches for. The draft is what is left when nothing
    // is running, and it is thrown away silently — emptying a box somebody is
    // looking at needs no announcement.
    //
    // A turn first even with a draft typed: somebody who has been writing a
    // steer while the agent works and then presses escape means the agent,
    // not their own half-sentence. Pressing it again clears that.
    //
    // Never leaves the screen. That was one key once, and the reflex which
    // abandons a message everywhere else abandoned the whole session here.
    if (key.name === "escape" || (key.ctrl && key.name === "c")) {
      key.preventDefault();
      if (working) {
        chatCancel(place.project, place.workspace).then(
          () => setNotice(""),
          (error: unknown) => setNotice(said(error)),
        );
        return;
      }
      setNotice("");
      empty();
      return;
    }
    if (key.ctrl && key.name === "y") {
      key.preventDefault();
      answer("first");
      return;
    }
    if (key.ctrl && key.name === "n") {
      key.preventDefault();
      answer("last");
    }
  });

  return (
    <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
      {/* ── a bar is a box, not a text ──────────────────────────────────
          A `text` paints its background under its own characters and nowhere
          else, so `bg` on one is a coloured phrase rather than a bar — width
          100% does not change it, measured. What fills a row edge to edge is
          a box's `backgroundColor`, with the words inside it. */}
      <box height={1} backgroundColor={CHROME.accent}>
        <text
          fg={CHROME.base}
          wrapMode="none"
          content={` ${place.project}/${place.workspace} · ${working ? `${SPIN[tick % SPIN.length]} working` : "idle"}${
            state.asks > 0 ? ` · ${state.asks} asked` : ""
          } `}
        />
      </box>

      <scrollbox
        flexGrow={1}
        stickyScroll
        stickyStart="bottom"
        paddingLeft={1}
        paddingRight={1}
        scrollbarOptions={{ visible: false }}
      >
        {/* ── one block per item, not one line per line ──────────────────────
            The first draft flattened everything to strings and painted a row
            each, which is why the speaker and the sentence were the same
            colour: a `text` has one foreground. Rendering the item lets the
            label be dim and the words be bright, and lets an agent's answer be
            drawn in two pieces: prose as text, which wraps, and a fence as
            code, highlighted by tree-sitter and left unwrapped because the
            line breaks in a fence are the content. The `markdown` renderable
            was the obvious answer and was measured not to wrap — a finding
            that has since expired, and `wrapMarkdown`'s note says so.

            Grouped, so a run of receipts is one block — see `grouped`, and
            note what it does *not* fold: a call that changed a file stands on
            its own, because the change is the thing worth reading. */}
        {grouped(state.items.slice(-200), working ? state.turn : undefined).map((block, at) =>
          block.kind === "calls" ? (
            // The turning frame only where something is turning: a finished
            // run keeps its ticks and crosses, and passing the tick to it
            // would be a re-render a second for a receipt.
            <Calls
              key={at}
              items={block.items}
              inner={inner}
              live={block.live}
              tick={block.live ? tick : undefined}
            />
          ) : block.item.kind === "said" ? (
            <Message
              key={at}
              item={block.item}
              inner={inner}
              streaming={working && block.item.turn === state.turn}
            />
          ) : block.item.kind === "compacted" ? (
            <Boundary key={at} item={block.item} inner={inner} tick={working ? tick : undefined} />
          ) : (
            <Call key={at} item={block.item} inner={inner} tick={working ? tick : undefined} />
          ),
        )}
        {/* A blank row, then the mark hard against the left edge of the view —
            it is the window speaking, not the agent, so it lines up with
            nobody's message. */}
        {working ? <text content=" " /> : undefined}
        {working ? (
          <text fg={CHROME.muted} content={`${SPIN[tick % SPIN.length]} thinking`} />
        ) : undefined}
      </scrollbox>

      {/* ── what /mcp answered ────────────────────────────────────────────
          Above the composer, where the menu it was chosen from was, and
          dismissed with escape. Two fields carry it: the directory, which is
          the whole of the server's scope — no tool takes a workspace
          argument, so that path is *why* a conversation cannot reach another
          checkout — and the daemon it was spawned against, because a second
          instance's agents reaching the instance somebody is working in is a
          real failure with nothing else on screen to show it.

          What is not said is whether the agent's own client accepted the
          handshake. Nothing in ACP reports it, so this is what was handed
          over and it says so in those words rather than drawing a tick that
          would be a guess. */}
      {mcp === undefined ? undefined : (
        // `flexShrink={0}` and a height, for the reason the menu carries the
        // same pair: a column's children shrink by default, and a box with
        // neither draws its first rows and paints the rest over whatever is
        // below it.
        <box flexDirection="column" flexShrink={0} height={4} paddingLeft={1} paddingRight={1}>
          <text
            fg={CHROME.text}
            wrapMode="none"
            content={`mcp · ${mcp.name} — handed over on open`}
          />
          <text fg={CHROME.muted} wrapMode="none" content={`  cwd    ${mcp.cwd}`} />
          <text fg={CHROME.muted} wrapMode="none" content={`  daemon ${mcp.url}`} />
          <text
            fg={CHROME.muted}
            wrapMode="none"
            content={`  tools  ${mcp.tools.map((tool) => tool.name).join(" · ")}`}
          />
        </box>
      )}

      <Menu commands={menu} at={highlight} width={width} />

      {/* A clear row above the composer, so the newest line is not jammed
          against a control. Text touching a control reads as text cut off. */}
      <text height={1} content=" " />

      <box
        height={rows}
        flexDirection="row"
        paddingLeft={1}
        paddingRight={1}
        backgroundColor={CHROME.raised}
      >
        <text width={2} fg={CHROME.accent} content="> " />
        <textarea
          ref={composer}
          flexGrow={1}
          height={rows}
          focused
          onContentChange={() => {
            setDraft(composer.current?.plainText ?? "");
            // The list is re-filtered on every keystroke, so an index into
            // the old one names the wrong row. Reset rather than clamped:
            // the first match is what somebody narrowing a list means.
            setPicked(0);
          }}
          onSubmit={() => send(composer.current?.plainText ?? "")}
          placeholder="say something to the agent"
          // Wrapped, not scrolled sideways: a message is prose, and prose that
          // runs off the right of a one-line box cannot be read back before
          // it is sent.
          wrapMode="word"
          backgroundColor={CHROME.raised}
          textColor={CHROME.text}
          // ── enter sends; something-enter makes a line ──────────────────
          //
          // Four of them, because which one a terminal actually delivers is
          // not this application's to decide:
          //
          //   cmd+enter     never arrives. Reported as "going fullscreen" —
          //                 that is the terminal's own binding, and it is
          //                 taken before any program sees the key
          //   shift+enter   the chat convention, and it needs the kitty
          //                 keyboard protocol to be distinguishable at all
          //   alt+enter     ESC then CR, which every terminal sends
          //   ctrl+o        the one that needs nothing: a plain control byte
          //
          // `super` stays bound for a terminal that does pass cmd through.
          keyBindings={[
            { name: "return", action: "submit" },
            { name: "kpenter", action: "submit" },
            { name: "return", shift: true, action: "newline" },
            { name: "kpenter", shift: true, action: "newline" },
            { name: "return", meta: true, action: "newline" },
            { name: "kpenter", meta: true, action: "newline" },
            { name: "return", super: true, action: "newline" },
            { name: "kpenter", super: true, action: "newline" },
            { name: "o", ctrl: true, action: "newline" },
          ]}
        />
      </box>

      {/* And one below, for the same reason: the status row is a control
          strip, and a composer hard against it reads as one box with a
          coloured bottom edge rather than two things with different jobs. */}
      <text height={1} content=" " />

      {/* ── the status row ──────────────────────────────────────────────
          What the window draws under its composer, and for the same reason:
          these are facts about the *session*, and they read in the right
          order down here — what you are about to say, then who is about to
          answer it. Read-only, because this client has no control to change
          one with; the figures are still the ones somebody decides `/new` on.

          It replaced a row of chords. Those were four things that are always
          true, which is what teaches an eye to skip a bar — and the one that
          somebody stuck really needs, `ctrl-\`, is in the header the moment a
          terminal is attached and in the README either way.

          A notice takes the row, and only a **failure** is one: a refusal
          from the daemon, or a key that did nothing. Reporting the successes
          as well — `sending…`, `sent as a new turn`, `answered Allow Once` —
          was three announcements of things the transcript already shows, and
          it teaches the eye to skip the row that the one refusal lands in.
          The answer keys are the exception, because they are an offer rather
          than a report. */}
      <box height={1} backgroundColor={CHROME.bar}>
        <text
          fg={told === undefined ? CHROME.muted : CHROME.text}
          wrapMode="none"
          content={
            told === undefined
              ? pending === undefined
                ? ` ${facts(config, state.used, state.size)}`
                : ` ctrl-y ${pending.options[0]?.label ?? "allow"} · ctrl-n ${
                    pending.options.at(-1)?.label ?? "deny"
                  }`
              : ` ${told}`
          }
        />
      </box>
    </box>
  );
};
