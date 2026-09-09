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
import { segments, wrap } from "./lines";
import { isBack, isQuit } from "./keys";
import { CHROME, SPIN, SYNTAX } from "./theme";
import type { Place } from "./Threads";
import { useConversation, useSpinner } from "./useConversation";

/**
 * A tool row is a receipt: what ran, and whether it worked.
 *
 * Wrapped rather than clipped, but bounded — a title here is whatever command
 * was run, and some of them are a forty-line script. Wrapping one of those
 * unbounded gives a single tool call the whole transcript.
 */
const clip = (text: string, most: number) =>
  text.length > most ? `${text.slice(0, most - 1)}…` : text;

/**
 * One line, whatever it takes.
 *
 * A title is not a title: it is the command, and a `python3 - <<'PY'` carries
 * its whole script in it, newlines and all. Clipping counts characters and a
 * multi-line string was still multi-line after being clipped — which is why a
 * "one line" row was drawing nine. So the first line is taken *before* the
 * width is applied, and an ellipsis says the rest is there.
 */
const oneLine = (text: string, most: number): string => {
  const [first = "", ...rest] = text.split("\n");
  const trimmed = first.trimEnd();
  return clip(rest.length > 0 ? `${trimmed} …` : trimmed, most);
};

const mark = (status: string) =>
  status === "completed" ? "✓" : status === "failed" ? "✗" : status === "asking" ? "?" : "…";

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
            drawn in two pieces: prose as text, which wraps, and a fence as
            code, highlighted by tree-sitter and left unwrapped because the
            line breaks in a fence are the content. The `markdown` renderable
            was the obvious answer and cannot be used — see `wrapMarkdown`. */}
        {state.items.slice(-200).map((item, at) =>
          item.kind === "said" ? (
            <box key={at} width={inner} flexDirection="row" paddingBottom={1}>
              <text
                width={4}
                fg={CHROME.muted}
                content={item.role === "user" ? "you " : item.role === "thought" ? "  ~ " : "··· "}
              />
              {item.role === "agent" ? (
                <box width={inner - 4} flexDirection="column">
                  {segments(item.text.trimEnd()).map((part, index) =>
                    part.kind === "code" ? (
                      <code
                        key={index}
                        width={inner - 4}
                        content={part.text}
                        filetype={part.language === "" ? "text" : part.language}
                        syntaxStyle={SYNTAX}
                      />
                    ) : (
                      <text
                        key={index}
                        width={inner - 4}
                        wrapMode="word"
                        fg={CHROME.text}
                        content={part.text}
                      />
                    ),
                  )}
                </box>
              ) : (
                <text
                  width={inner - 4}
                  wrapMode="word"
                  fg={item.role === "user" ? CHROME.said : CHROME.muted}
                  content={item.text.trimEnd()}
                />
              )}
            </box>
          ) : (
            // ── a tool call is one line ──────────────────────────────────
            //
            // Truncated rather than wrapped, which is the opposite of the rule
            // everywhere else here and is the point: a transcript is read for
            // what was said, and a tool call is a receipt beside it. One
            // `bun -e` script wrapped over nine rows buries the sentence it
            // was run for. The output line went with it, for the same reason.
            //
            // A question is the exception: it is the one tool row that wants
            // something from a person, so it keeps a line of its own.
            <box key={at} width={inner} flexDirection="column" paddingBottom={1}>
              <text
                width={inner}
                wrapMode="none"
                fg={CHROME.muted}
                content={`  ${mark(item.status)} ${(item.subagent ?? item.toolKind ?? "tool").padEnd(7)} ${oneLine(
                  item.title,
                  Math.max(12, inner - 13),
                )}`}
              />
              {item.ask === undefined ? undefined : (
                <text
                  width={inner}
                  wrapMode="word"
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

      {/* ── the footer has to fit ────────────────────────────────────────
          Every chord it could name does not: at 70 columns the old line ran
          past the edge and took `ctrl-q quit` with it, which is the one thing
          somebody stuck needs to be able to read. So it says the four that
          are always true, and swaps in the answer keys only while something
          is being asked. */}
      <text
        height={1}
        bg={CHROME.bar}
        fg={notice === "" ? CHROME.muted : CHROME.text}
        content={
          notice === ""
            ? pending === undefined
              ? " ⏎ send · ⇧⏎ or ctrl-o newline · esc cancel · ctrl-\\ back · ctrl-q quit"
              : ` ctrl-y ${pending.options[0]?.label ?? "allow"} · ctrl-n ${
                  pending.options.at(-1)?.label ?? "deny"
                } · ctrl-\\ back`
            : ` ${notice}`
        }
      />
    </box>
  );
};
