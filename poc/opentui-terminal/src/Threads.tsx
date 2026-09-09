// The list the POC opens on.
//
// Threads, not workspaces and not sessions. A thread is the only record that a
// set of checkouts are one piece of work, and it is the one name in the system
// a person wrote rather than derived — so it is what a list should be of.
//
//   ▸ paginate the tabular exports          ← the thread, in somebody's words
//       awp/tabular-exports    agent         ← its checkouts, one per row
//       beta/tabular-exports
//
// Enter opens the agent's conversation for a row; `s` opens a terminal in that
// row's directory. Both are per checkout rather than per thread, because a
// thread with two checkouts has two agents and two directories, and there is
// no sensible way to pick one for somebody.

import { useEffect, useState } from "react";
import type { Thread } from "@awp-kit/protocol";
import { useKeyboard } from "@opentui/react";
import { isDown, isEnter, isQuit, isUp } from "./keys";
import { CHROME } from "./theme";
import { logTui, said, threads } from "./daemon";

export type Place = {
  readonly project: string;
  readonly workspace: string;
  readonly title: string;
};

type Row =
  | { readonly kind: "thread"; readonly thread: Thread }
  | { readonly kind: "member"; readonly thread: Thread; readonly place: Place };

/** Threads flattened to rows, because a list is rows and only members are openable. */
const rowsOf = (all: ReadonlyArray<Thread>): ReadonlyArray<Row> =>
  all.flatMap((thread) => [
    { kind: "thread" as const, thread },
    ...thread.members.map((member) => ({
      kind: "member" as const,
      thread,
      place: { project: member.project, workspace: member.workspace, title: thread.title },
    })),
  ]);

export const Threads = ({
  at,
  onMove,
  onOpen,
  onTerminal,
  onQuit,
}: {
  at: number;
  onMove: (at: number) => void;
  onOpen: (place: Place) => void;
  onTerminal: (place: Place) => void;
  onQuit: () => void;
}) => {
  const [all, setAll] = useState<ReadonlyArray<Thread>>([]);
  const [failure, setFailure] = useState("");

  useEffect(() => {
    let live = true;
    logTui("threads: asking");
    threads().then(
      (answer) => {
        logTui(`threads: ${answer.length} back, live=${live}`);
        if (!live) return;
        setAll(answer.filter((thread) => thread.archivedAt === undefined));
      },
      (error: unknown) => {
        logTui(`threads: failed ${said(error)}`);
        if (live) setFailure(said(error));
      },
    );
    return () => {
      live = false;
    };
  }, []);

  const rows = rowsOf(all);
  // The selection only ever lands on something openable, so `j` and `k` skip
  // the headings rather than stopping on a row where enter would do nothing.
  const openable = rows.flatMap((row, index) => (row.kind === "member" ? [index] : []));
  const selected = openable.includes(at) ? at : openable[0];

  const step = (by: number) => {
    const where = selected === undefined ? -1 : openable.indexOf(selected);
    const next = openable[Math.min(openable.length - 1, Math.max(0, where + by))];
    if (next !== undefined) onMove(next);
  };

  useKeyboard((key) => {
    if (isQuit(key) || key.name === "escape" || key.name === "q") {
      key.preventDefault();
      onQuit();
      return;
    }
    if (isDown(key)) {
      key.preventDefault();
      step(1);
      return;
    }
    if (isUp(key)) {
      key.preventDefault();
      step(-1);
      return;
    }
    const row = selected === undefined ? undefined : rows[selected];
    if (row?.kind !== "member") return;
    if (isEnter(key)) {
      key.preventDefault();
      onOpen(row.place);
      return;
    }
    if (key.name === "s" && !key.ctrl) {
      key.preventDefault();
      onTerminal(row.place);
    }
  });

  return (
    <box flexGrow={1} flexDirection="column" backgroundColor={CHROME.base}>
      <text
        height={1}
        bg={CHROME.accent}
        fg={CHROME.base}
        content={` awp · ${all.length} threads `}
      />
      <box flexGrow={1} flexDirection="column" paddingLeft={1} paddingTop={1}>
        {failure === "" ? undefined : <text fg={CHROME.warn} content={failure} />}
        {rows.map((row, index) =>
          row.kind === "thread" ? (
            <text
              key={`t-${row.thread.id}`}
              fg={CHROME.text}
              content={`▸ ${row.thread.title}${
                row.thread.prs.length === 0
                  ? ""
                  : `  #${row.thread.prs.map((pr) => pr.number).join(" #")}`
              }`}
            />
          ) : (
            <text
              key={`m-${row.thread.id}-${row.place.project}-${row.place.workspace}`}
              fg={index === selected ? CHROME.accent : CHROME.muted}
              content={`${index === selected ? "  ❯ " : "    "}${row.place.project}/${row.place.workspace}`}
            />
          ),
        )}
        {rows.length === 0 && failure === "" ? (
          <text fg={CHROME.muted} content="no threads" />
        ) : undefined}
      </box>
      <text
        height={1}
        bg={CHROME.bar}
        fg={CHROME.muted}
        content=" enter agent · s terminal · j/k move · q quit"
      />
    </box>
  );
};
