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
import { logTui, onReconnect, said, threads, watchFacts } from "./daemon";

export type Place = {
  readonly project: string;
  readonly workspace: string;
  readonly title: string;
};

type Row =
  | { readonly kind: "thread"; readonly thread: Thread }
  | { readonly kind: "member"; readonly thread: Thread; readonly place: Place };

/**
 * When a thread was last worked in, as a number to sort by.
 *
 * ── the record has no such field, and it should not ────────────────────
 *
 * A `Thread` carries `createdAt` and nothing else about time, which is
 * right: a thread is a claim somebody made, and the moment it was made does
 * not change. What *does* change is the work — so the reading comes from the
 * workspaces it holds, which is where activity actually happens.
 *
 * `lastActiveAt` is written by the agent's own hooks into
 * `~/.awp/workspace-state.json` and reaches here as a fact. A thread with
 * two checkouts is as recent as its most recent one; a thread whose
 * workspaces have never been stamped falls back to when it was created,
 * which puts an untouched thread where it was before rather than at the
 * bottom.
 */
const activeAt = (thread: Thread, when: ReadonlyMap<string, number>): number =>
  Math.max(
    thread.createdAt.getTime(),
    ...thread.members.map((member) => when.get(`${member.project}/${member.workspace}`) ?? 0),
  );

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
  /** Per `project/workspace`, when its agent was last doing something. */
  const [when, setWhen] = useState<ReadonlyMap<string, number>>(new Map());

  useEffect(
    () =>
      watchFacts((facts) => {
        setWhen(
          new Map(
            facts.flatMap((one) =>
              one.lastActiveAt === undefined
                ? []
                : [[`${one.project}/${one.workspace}`, one.lastActiveAt.getTime()] as const],
            ),
          ),
        );
      }),
    [],
  );

  useEffect(() => {
    let live = true;
    const ask = () => {
      logTui("threads: asking");
      threads().then(
        (answer) => {
          logTui(`threads: ${answer.length} back, live=${live}`);
          if (!live) return;
          setFailure("");
          setAll(answer.filter((thread) => thread.archivedAt === undefined));
        },
        (error: unknown) => {
          logTui(`threads: failed ${said(error)}`);
          if (live) setFailure(said(error));
        },
      );
    };
    ask();
    // ── and again when the daemon comes back ──────────────────────────
    //
    // This is a call, not a feed: asked once at mount and never again, so a
    // daemon restart left the list showing what it had before — or nothing
    // at all, permanently, when the mount happened during the outage. The
    // socket reconnects on its own and that is not the same thing.
    const stop = onReconnect(ask);
    return () => {
      live = false;
      stop();
    };
  }, []);

  // Most recently worked in at the top. `ThreadList` answers newest-created
  // first, which is the same order for a week and then never again: the
  // thread somebody is in today is the one they started in June.
  const rows = rowsOf(all.toSorted((a, b) => activeAt(b, when) - activeAt(a, when)));
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
      {/* A bar is a box: a `text` paints its background under its own
          characters and nowhere else, so `bg` on one is a coloured phrase
          rather than a bar that reaches both edges. Measured. */}
      <box height={1} backgroundColor={CHROME.accent}>
        <text fg={CHROME.base} wrapMode="none" content={` awp · ${all.length} threads `} />
      </box>
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
      <box height={1} backgroundColor={CHROME.bar}>
        <text
          fg={CHROME.muted}
          wrapMode="none"
          content=" enter agent · s terminal · j/k move · q quit"
        />
      </box>
    </box>
  );
};
