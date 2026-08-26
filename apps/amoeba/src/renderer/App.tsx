import type { SessionInfo } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { Accessory } from "./Accessory";
import { BottomBar, TopBar } from "./Bars";
import { Divider } from "./Divider";
import { NewThread, type NewThreadRequest } from "./NewThread";
import { Pane } from "./Pane";
import { Sidebar } from "./Sidebar";
import { type Collapsed, fitColumns } from "./columns";
import { projectsOf, threadOf } from "./workspaces";
import { listSessions } from "./daemon";
import {
  rememberCollapsed,
  rememberSession,
  rememberedCollapsed,
  rememberedSession,
} from "./remembered";
import { rendererFixture } from "./fixture";
import { themeFor, useAppearance, useColorScheme } from "./theme";
import { colors, text } from "./tokens.stylex";
import { useJobs } from "./useJobs";
import { useThreads } from "./useThreads";
import { useWindowWidth } from "./useWindowWidth";

// The window: two bars with three columns between them.
//
// The columns are the work — sidebar, agent, accessory. The bars are the window
// talking about itself, and they exist because everything in that category used
// to have to borrow space from a column that was already spoken for. See
// Bars.tsx for what each one carries and why the top one clears the traffic
// lights on behalf of all three columns.
//
// `height: 100%` and not `100vh`. The root is already pinned to the window in
// global.css, and vh units in a webview measure the visual viewport — which is
// a different number the moment anything insets the window, and a scrollbar's
// worth of overflow at the top level is precisely what is not allowed here.

const styles = stylex.create({
  window: {
    display: "flex",
    flexDirection: "column",
    height: "100%",
    backgroundColor: colors.base,
    color: colors.text,
    fontFamily: text.mono,
    fontSize: text.body,
  },
  // The one row that flexes. `minHeight: 0` so it can be shorter than its
  // content instead of pushing the bottom bar off the window — which is the
  // usual way a flex column grows a scrollbar it was told not to have.
  columns: { display: "flex", flex: 1, minHeight: 0 },
  column: {
    minWidth: 0,
    height: "100%",
    overflow: "hidden",
  },
  // Dynamic, because the width is state and not a design decision. StyleX
  // compiles this to one rule with a variable in it, so a drag re-renders
  // without minting a class per pixel.
  fixed: (width: number) => ({ flex: `0 0 ${width}px` }),
  agent: { flex: "1 1 auto" },
});

export function App() {
  const appearance = useAppearance();
  const scheme = useColorScheme();
  const width = useWindowWidth();

  // What was asked for, not what was granted. fitColumns squeezes a column when
  // the window cannot hold it, and storing the squeezed result would make that
  // permanent: widening the window back would leave the column where the
  // narrowest moment put it. See columns.ts.
  const [want, setWant] = useState({ sidebar: 260, accessory: 280 });
  // Which columns are folded away, restored the same way the session is. A
  // window that reopened both every time would undo the one choice the user
  // makes to get them out of the way.
  const [collapsed, setCollapsed] = useState<Collapsed>(rememberedCollapsed);
  const columns = fitColumns(width, want, collapsed);

  const fold = (which: keyof Collapsed) => () => {
    setCollapsed((prev) => {
      const next = { ...prev, [which]: !prev[which] };
      rememberCollapsed(next);
      return next;
    });
  };

  const [sessions, setSessions] = useState<ReadonlyArray<SessionInfo>>([]);
  // Restored, not defaulted. Editing the renderer reloads the page, and a pane
  // that forgot its session every time would mean reattaching by hand after
  // every change.
  const [selected, setSelected] = useState<string | undefined>(rememberedSession);
  const [failure, setFailure] = useState<string | undefined>();
  const { jobs } = useJobs();

  // Threads live here rather than in the sidebar, because the sidebar is no
  // longer the only thing that changes them: cmd+N starts one from anywhere in
  // the window. Two owners of the same list is two lists.
  const { threads, reload: reloadThreads } = useThreads();

  // What is open, and therefore what a new thread would default to. Computed
  // before the shortcut below, which reads it.
  const open = sessions.find((session) => session.name === selected);

  // The modal, as a request rather than a boolean. It carries what the window
  // knew at the moment it was opened — which project was on screen, and which
  // workspace — so the form can read them once at mount instead of tracking a
  // selection that may move underneath it.
  const [newThread, setNewThread] = useState<NewThreadRequest | undefined>();

  useEffect(() => {
    let cancelled = false;
    listSessions()
      .then((listed) => {
        if (cancelled) {
          return;
        }
        setSessions(listed);
        setFailure(undefined);
        // A remembered session may have ended, been killed, or become
        // unattachable since the window last ran. Attaching to it would fail
        // with a refusal the user did not ask for, so it is dropped instead.
        setSelected((current) => {
          if (current === undefined) {
            return current;
          }
          const still = listed.find((session) => session.name === current);
          if (still !== undefined && still.refusal === undefined) {
            return current;
          }
          rememberSession(undefined);
          return undefined;
        });
      })
      .catch((error: unknown) => {
        // A daemon that is not running is the ordinary case during development,
        // not an exception. The sidebar says so and tells you the command.
        if (!cancelled) {
          setFailure(error instanceof Error ? error.message : String(error));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // cmd+N from anywhere in the window, and cmd+shift+N to start from the
  // workspace on screen rather than from the project's main line.
  //
  // ── on `window`, and in the CAPTURE phase ────────────────────────────────
  // The capture flag is the whole reason this works, and it was not obvious:
  // the emulator installs its own keydown handler on the pane's host and calls
  // `stopPropagation` for every key it consumes, cmd+N included. A bubble-phase
  // listener on `window` therefore never hears the chord while the pane has
  // focus — measured, not guessed:
  //
  //   capture at window   MetaLeft, ShiftLeft, KeyN
  //   bubble  at window   MetaLeft, ShiftLeft          ← KeyN never arrives
  //
  // Capture is also the right *meaning* rather than merely the thing that
  // works. An application shortcut has to be decided before the terminal
  // claims the key, or cmd+N with an agent focused is typed into the agent.
  //
  // `preventDefault` because the webview would otherwise hand cmd+N to the
  // host as "new window".
  //
  // `event.code` and not `event.key`: with a non-US layout `key` is whatever
  // the letter N maps to, and the shortcut is the physical key. It also side-
  // steps `key` arriving upper-case whenever shift is down, which is exactly
  // the chord this has to tell apart.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.code !== "KeyN" || !(event.metaKey || event.ctrlKey) || event.altKey) {
        return;
      }
      event.preventDefault();
      // Already open: leave it alone. Re-opening would throw away whatever had
      // been typed into it, which is the opposite of what pressing the shortcut
      // again means.
      setNewThread(
        (current) =>
          current ?? {
            project: open?.identity?.project,
            parent: threadOf(threads, open),
            fromParent: event.shiftKey,
          },
      );
    };
    window.addEventListener("keydown", onKey, { capture: true });
    return () => window.removeEventListener("keydown", onKey, { capture: true });
  }, [open, threads]);

  // The appearance theme rides the outermost element rather than <html>. The
  // variables it sets are inherited, so everything below sees them, and putting
  // them here keeps the override inside React's tree — where it can be reasoned
  // about — instead of in a mutation of the document.
  //
  // Flex-shrink is zero on both columns. The arithmetic in fitColumns already
  // guarantees the layout fits, so letting flexbox shrink as well would mean
  // two rules deciding the same widths and the rendered result disagreeing with
  // the state that is supposed to describe it.
  return (
    <div {...stylex.props(themeFor(appearance), styles.window)}>
      <TopBar session={open} connected={failure === undefined} />

      <div {...stylex.props(styles.columns)}>
        <aside {...stylex.props(styles.column, styles.fixed(columns.sidebar))}>
          <Sidebar
            sessions={sessions}
            selected={selected}
            onSelect={(session) => {
              setSelected(session.name);
              rememberSession(session.name);
            }}
            threads={threads}
            failure={failure}
            onNew={() =>
              setNewThread({
                project: open?.identity?.project,
                parent: threadOf(threads, open),
                fromParent: false,
              })
            }
          />
        </aside>

        <Divider
          label="sidebar width"
          value={columns.sidebar}
          onChange={(sidebar) => setWant((prev) => ({ ...prev, sidebar }))}
          collapsed={collapsed.sidebar}
          onToggle={fold("sidebar")}
        />

        <main {...stylex.props(styles.column, styles.agent)}>
          <Pane session={selected} fixture={rendererFixture} scheme={scheme} />
        </main>

        <Divider
          label="accessory width"
          invert
          value={columns.accessory}
          onChange={(accessory) => setWant((prev) => ({ ...prev, accessory }))}
          collapsed={collapsed.accessory}
          onToggle={fold("accessory")}
        />

        <aside {...stylex.props(styles.column, styles.fixed(columns.accessory))}>
          <Accessory />
        </aside>
      </div>

      <BottomBar jobs={jobs} session={open} />

      {/* Outside the columns, because it is the window's and not a column's.
          It renders nothing at all while shut — see NewThread.tsx. */}
      <NewThread
        request={newThread}
        projects={projectsOf(sessions)}
        threads={threads}
        onClose={() => setNewThread(undefined)}
        onStarted={reloadThreads}
      />
    </div>
  );
}
