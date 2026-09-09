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
import type { ChatConfigOption } from "@awp-kit/protocol";
import { chatAnswer, chatCancel, chatConfig, chatSend, said } from "./daemon";
import { grouped } from "./conversation";
import { Call, Calls, Message } from "./Items";
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

export const Chat = ({
  place,
  onBack,
  onQuit,
}: {
  place: Place;
  onBack: () => void;
  onQuit: () => void;
}) => {
  const { state, saidLocally } = useConversation(place.project, place.workspace);
  // The composer owns its text — a `textarea` takes no `value`, which is the
  // right way round for an editor with a cursor and a selection in it. What is
  // kept here is a mirror, for the footer and for what `esc` has to know.
  // `plainText` reads it, `clear()` empties it.
  const composer = useRef<TextareaRenderable | null>(null);
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState("");
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
    chatConfig(place.project, place.workspace).then(
      (answer) => {
        if (live) setConfig(answer);
      },
      () => {
        // Nothing. See above.
      },
    );
    return () => {
      live = false;
    };
  }, [place.project, place.workspace]);

  // One line until there is more than one line's worth in it, then as many as
  // six. A composer that is three rows tall before anybody types is three rows
  // of transcript nobody can see.
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
  const rows = Math.min(6, Math.max(1, wrap(draft, room).length));

  const ask = state.items
    .toReversed()
    // A settled question is not pending: its buttons are gone, and the answer
    // keys would otherwise reach for it and earn a refusal about somebody
    // else's click.
    .find(
      (item) => item.kind === "tool" && item.ask !== undefined && item.ask.answered === undefined,
    );
  const pending = ask?.kind === "tool" ? ask.ask : undefined;

  const send = (text: string) => {
    const message = text.trim();
    if (message === "") return;
    setDraft("");
    if (composer.current !== null) composer.current.clear();
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
      setDraft("");
      if (composer.current !== null) composer.current.clear();
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
      <text
        height={1}
        bg={CHROME.accent}
        fg={CHROME.base}
        content={` ${place.project}/${place.workspace} · ${working ? `${SPIN[tick % SPIN.length]} working` : "idle"}${
          state.asks > 0 ? ` · ${state.asks} asked` : ""
        } `}
      />

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
            was the obvious answer and cannot be used — see `wrapMarkdown`.

            Grouped, so a run of receipts is one block — see `grouped`, and
            note what it does *not* fold: a call that changed a file stands on
            its own, because the change is the thing worth reading. */}
        {grouped(state.items.slice(-200)).map((block, at) =>
          block.kind === "calls" ? (
            <Calls key={at} items={block.items} inner={inner} />
          ) : block.item.kind === "said" ? (
            <Message key={at} item={block.item} inner={inner} />
          ) : (
            <Call key={at} item={block.item} inner={inner} />
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
          onContentChange={() => setDraft(composer.current?.plainText ?? "")}
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
      <text
        height={1}
        bg={CHROME.bar}
        fg={notice === "" ? CHROME.muted : CHROME.text}
        wrapMode="none"
        content={
          notice === ""
            ? pending === undefined
              ? ` ${facts(config, state.used, state.size)}`
              : ` ctrl-y ${pending.options[0]?.label ?? "allow"} · ctrl-n ${
                  pending.options.at(-1)?.label ?? "deny"
                }`
            : ` ${notice}`
        }
      />
    </box>
  );
};
