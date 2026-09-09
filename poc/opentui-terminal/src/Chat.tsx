// The agent's conversation, for one checkout.
//
// The same `ChatOpen` the window uses. Everything below the fold is shared with
// the imperative first draft — `conversation.ts` and `lines.ts` did not change
// when this became React, which is the argument for having split them.
//
// What React took away is the part that kept breaking: a hand-managed pool of
// text rows, a scroll offset, a follow flag and a paint function. A `scrollbox`
// with `stickyScroll` is all four, and it handles the wheel.

import { useRef, useState } from "react";
import { useTerminalDimensions } from "@opentui/react";
import type { TextareaRenderable } from "@opentui/core";
import { useKeyboard } from "@opentui/react";
import { chatAnswer, chatSend, said } from "./daemon";
import { wrap } from "./lines";
import { isBack, isQuit } from "./keys";
import { CHROME, SPIN, SYNTAX } from "./theme";
import type { Place } from "./Threads";
import { useConversation, useSpinner } from "./useConversation";

/** A tool row is a receipt: what ran, and whether it worked. */
const mark = (status: string) =>
  status === "completed" ? "✓" : status === "failed" ? "✗" : status === "asking" ? "?" : "…";

/** One line of it, because the whole of a 4000-line grep is not a receipt. */
const receipt = (output: string): string | undefined => {
  const first = output.split("\n").find((line) => line.trim() !== "");
  return first === undefined ? undefined : first.slice(0, 100);
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

  // One line until there is more than one line's worth in it, then as many as
  // six. A composer that is three rows tall before anybody types is three rows
  // of transcript nobody can see.
  const { width } = useTerminalDimensions();
  const room = Math.max(20, width - 6);
  const rows = Math.min(6, Math.max(1, wrap(draft, room).length));

  const ask = state.items
    .toReversed()
    .find((item) => item.kind === "tool" && item.ask !== undefined);
  const pending = ask?.kind === "tool" ? ask.ask : undefined;

  const send = (text: string) => {
    const message = text.trim();
    if (message === "") return;
    setDraft("");
    if (composer.current !== null) composer.current.clear();
    saidLocally(message);
    setNotice("sending…");
    chatSend(place.project, place.workspace, message).then(
      // `steer` went into the turn already running; `prompt` started one. A
      // person cannot tell those apart from the screen, and they are the
      // difference between being read now and waiting.
      (delivery) =>
        setNotice(delivery === "steer" ? "steered into the running turn" : "sent as a new turn"),
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
      () => setNotice(`answered ${option.label}`),
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
    // Escape throws the draft away and never leaves the screen. They were one
    // key once, and the reflex that abandons a half-typed message everywhere
    // else abandoned the whole window here.
    if (key.name === "escape" || (key.ctrl && key.name === "c")) {
      key.preventDefault();
      setNotice(draft === "" ? "nothing to cancel · ctrl-\\ goes back" : "message cancelled");
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
            markdown — headings, lists, tables and fenced code, highlighted by
            the same tree-sitter the editor components use. */}
        {state.items.slice(-200).map((item, at) =>
          item.kind === "said" ? (
            <box key={at} flexDirection="row" paddingBottom={1}>
              <text
                width={4}
                fg={CHROME.muted}
                content={item.role === "user" ? "you " : item.role === "thought" ? "  ~ " : "··· "}
              />
              {item.role === "agent" ? (
                <markdown flexGrow={1} content={item.text.trimEnd()} syntaxStyle={SYNTAX} conceal />
              ) : (
                <text
                  flexGrow={1}
                  wrapMode="word"
                  fg={item.role === "user" ? CHROME.said : CHROME.muted}
                  content={item.text.trimEnd()}
                />
              )}
            </box>
          ) : (
            <box key={at} flexDirection="column" paddingBottom={1}>
              <text
                fg={CHROME.muted}
                content={`  ${mark(item.status)} ${item.title}${
                  item.subagent === undefined ? "" : ` · ${item.subagent}`
                }`}
              />
              {receipt(item.output) === undefined ? undefined : (
                <text fg={CHROME.muted} content={`      ${receipt(item.output)}`} />
              )}
              {item.ask === undefined ? undefined : (
                <text
                  fg={CHROME.ask}
                  content={`    asks: ${item.ask.options.map((one) => one.label).join("   ")}`}
                />
              )}
            </box>
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
          keyBindings={[
            { name: "return", action: "submit" },
            { name: "kpenter", action: "submit" },
            { name: "return", meta: true, action: "newline" },
            { name: "kpenter", meta: true, action: "newline" },
          ]}
        />
      </box>

      <text
        height={1}
        bg={CHROME.bar}
        fg={CHROME.muted}
        content={
          notice === ""
            ? ` enter send · alt-enter newline · esc cancel · ctrl-y allow · ctrl-\\ back · ctrl-q quit${
                pending === undefined
                  ? ""
                  : `  ·  asked: ${pending.options.map((one) => one.label).join(" / ")}`
              }`
            : ` ${notice}`
        }
      />
    </box>
  );
};
