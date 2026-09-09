// A shell in the checkout's directory.
//
// The other half of the POC, and the reason it started: an
// `EmbeddedTerminalRenderable` is a terminal *inside a layout*, so the thread
// it belongs to can stay on screen beside it. `za` hands the whole terminal
// over to `zmx attach` and there is nowhere left to put anything.
//
// A plain `$SHELL`, not `zmx attach`. Attaching sizes the session to whoever is
// looking at it, and a POC that reflowed the terminal somebody is working in
// would be the exact hazard AGENTS.md spends a section on. The directory is
// asked for rather than composed — see `workspaceDir`.

import { useEffect, useRef, useState } from "react";
import { EmbeddedTerminalRenderable } from "@opentui/core";
import { extend, useKeyboard } from "@opentui/react";
import { isBack, isQuit } from "./keys";
import { said, workspaceDir } from "./daemon";
import { CHROME } from "./theme";
import type { Place } from "./Threads";

extend({ terminal: EmbeddedTerminalRenderable });

declare module "@opentui/react" {
  interface OpenTUIComponents {
    terminal: typeof EmbeddedTerminalRenderable;
  }
}

export const Term = ({
  place,
  onBack,
  onQuit,
}: {
  place: Place;
  onBack: () => void;
  onQuit: () => void;
}) => {
  const [dir, setDir] = useState<string>();
  const [failure, setFailure] = useState("");
  const terminal = useRef<EmbeddedTerminalRenderable | null>(null);
  const child = useRef<ReturnType<typeof Bun.spawn> | null>(null);

  useEffect(() => {
    let live = true;
    workspaceDir(place.project, place.workspace).then(
      (answer) => live && setDir(answer),
      (error: unknown) => live && setFailure(said(error)),
    );
    return () => {
      live = false;
    };
  }, [place.project, place.workspace]);

  useEffect(() => {
    const view = terminal.current;
    if (dir === undefined || view === null) return;
    const shell = process.env.SHELL ?? "/bin/sh";
    const spawned = Bun.spawn([shell], {
      cwd: dir,
      env: {
        ...process.env,
        TERM: "xterm-256color",
        COLORTERM: "truecolor",
        // Present and empty, never absent: a shell here is free to run zmx,
        // and a child that inherits the marker switches the *calling* client —
        // whatever session this process was launched from.
        ZMX_SESSION: "",
      },
      terminal: {
        cols: Math.max(1, view.width),
        rows: Math.max(1, view.height),
        data(_pty, bytes) {
          view.write(bytes);
        },
      },
    });
    child.current = spawned;
    view.focus();
    return () => {
      view.blur();
      spawned.kill();
      spawned.terminal?.close();
      child.current = null;
    };
    // `dir` only. A resize must not respawn the shell — the box tells the pty
    // its new size through `onTerminalResize`, which is what that callback is
    // for. Depending on the height here restarted somebody's shell every time
    // the window changed shape.
  }, [dir]);

  useKeyboard((key) => {
    // Read before the terminal sees them, and taken away from it: a global
    // handler runs first and `preventDefault` is what stops the pty receiving
    // the chord. Without that there is no way back out of a shell.
    if (isQuit(key)) {
      key.preventDefault();
      key.stopPropagation();
      onQuit();
      return;
    }
    if (isBack(key)) {
      key.preventDefault();
      key.stopPropagation();
      onBack();
    }
  });

  return (
    <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
      <text
        height={1}
        bg={CHROME.accent}
        fg={CHROME.base}
        content={` ${place.project}/${place.workspace} · ${dir ?? "…"} `}
      />
      {failure === "" ? undefined : <text fg={CHROME.warn} content={` ${failure}`} />}
      <terminal
        ref={terminal}
        flexGrow={1}
        width="100%"
        maxScrollback={10_000}
        onData={(bytes: Uint8Array) => child.current?.terminal?.write(bytes)}
        onTerminalResize={(cols: number, rows: number) =>
          child.current?.terminal?.resize(cols, rows)
        }
      />
      <text height={1} bg={CHROME.bar} fg={CHROME.muted} content=" ctrl-\ back · ctrl-q quit" />
    </box>
  );
};
