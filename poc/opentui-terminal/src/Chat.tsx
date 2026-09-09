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
import type { TextareaRenderable } from "@opentui/core";
import { useKeyboard } from "@opentui/react";
import { chatAnswer, chatSend, said } from "./daemon";
import { linesOf } from "./lines";
import { isBack, isQuit } from "./keys";
import { CHROME, SPIN } from "./theme";
import type { Place } from "./Threads";
import { useConversation, useSpinner } from "./useConversation";

const COLOUR = {
  user: CHROME.accent,
  agent: CHROME.text,
  thought: CHROME.muted,
  tool: CHROME.muted,
  ask: CHROME.ask,
  gutter: CHROME.base,
} as const;

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

  const lines = linesOf(state.items.slice(-400), 100);

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
        {lines.map((line, at) => (
          <text key={at} fg={COLOUR[line.role]} content={line.text === "" ? " " : line.text} />
        ))}
        {/* A blank row, then the mark hard against the left edge of the view —
            it is the window saying something, not the agent, so it does not
            line up with anybody's message. */}
        {working ? <text content=" " /> : undefined}
        {working ? (
          <text fg={CHROME.muted} content={`${SPIN[tick % SPIN.length]} thinking`} />
        ) : undefined}
      </scrollbox>

      {/* A clear row above the composer, so the newest line is not jammed
          against a control. Text touching a control reads as text cut off. */}
      <text height={1} content=" " />

      <box
        height={3}
        flexDirection="row"
        paddingLeft={1}
        paddingRight={1}
        backgroundColor={CHROME.raised}
      >
        <text width={4} fg={CHROME.accent} content="you " />
        <textarea
          ref={composer}
          flexGrow={1}
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
