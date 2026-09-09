#!/usr/bin/env bun
// Does a message actually wrap, and does a fence keep its shape?
//
// A render check with no daemon in it: the same pieces the chat draws, given
// text this file chose, at a width this file chose. It exists because the
// question "did that wrap" cannot be answered from a live transcript — what is
// on screen is whatever the agent last said, and the interesting cases are the
// ones it happens not to have produced.

import { createCliRenderer } from "@opentui/core";
import { createRoot } from "@opentui/react";
import { segments } from "../lines";
import { CHROME, SYNTAX } from "../theme";

const MESSAGE = [
  "A paragraph long enough to need breaking, which is the whole point of the",
  "check: markdown would join these lines back up and then clip the result at",
  "the column, so prose is drawn as text instead.",
  "",
  "```ts",
  'const fence = "kept exactly as it is, because the breaks here are content";',
  "```",
  "",
  "And a closing line after the fence.",
].join("\n");

const width = Number(process.argv[2] ?? 50);

const App = () => (
  <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
    <box width={width} flexDirection="row">
      <text width={4} fg={CHROME.muted} content="··· " />
      <box width={width - 4} flexDirection="column">
        {segments(MESSAGE).map((part, index) =>
          part.kind === "code" ? (
            <code
              key={index}
              width={width - 4}
              content={part.text}
              filetype={part.language === "" ? "text" : part.language}
              syntaxStyle={SYNTAX}
            />
          ) : (
            <text
              key={index}
              width={width - 4}
              wrapMode="word"
              fg={CHROME.text}
              content={part.text}
            />
          ),
        )}
      </box>
    </box>
  </box>
);

const renderer = await createCliRenderer({ exitOnCtrlC: false, targetFps: 10 });
createRoot(renderer).render(<App />);
renderer.start();
