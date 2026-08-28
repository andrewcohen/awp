import { isTerminal } from "@awp-kit/jobs";
import * as stylex from "@stylexjs/stylex";
import { useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { Accessory } from "./Accessory";
import { Boundary } from "./Boundary";
import { AgentBar, TopBar } from "./Bars";
import { Divider } from "./Divider";
import { NewThread, type NewThreadRequest } from "./NewThread";
import { Pane } from "./Pane";
import { Sidebar } from "./Sidebar";
import { addressFrom, addressOf, pathOf, sessionAt } from "./address";
import { type Collapsed, type Columns, FOLD_MS, fitColumns } from "./columns";
import {
  rememberCollapsed,
  rememberPlace,
  rememberWidths,
  rememberedCollapsed,
  rememberedWidths,
} from "./remembered";
import { rendererFixture } from "./fixture";
import { themeFor, useAppearance, useColorScheme } from "./theme";
import { colors, space, text } from "./tokens.stylex";
import { useColumnKeys } from "./navigation";
import { useJobs } from "./useJobs";
import { useConnection } from "./useConnection";
import { useSessions } from "./useSessions";
import { factsKey, useFacts } from "./useFacts";
import { useProjects } from "./useProjects";
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
    // The containing block for the corner strip, which is absolute and has to
    // measure from the window rather than from the viewport — the two agree
    // today and would not the moment anything insets this element.
    position: "relative",
    backgroundColor: colors.base,
    color: colors.text,
    // The window's default face, inherited by everything that does not ask
    // for otherwise — which is most of it. What still asks is the pane, and
    // the fields that hold an address: a slug, a bookmark, a revision, a path.
    // See the note on `text.mono`.
    fontFamily: text.ui,
    fontSize: text.body,
  },
  // The one row that flexes. `minHeight: 0` so it can be shorter than its
  // content instead of pushing the bottom bar off the window — which is the
  // usual way a flex column grows a scrollbar it was told not to have.
  columns: { display: "flex", flex: 1, minHeight: 0 },
  // Whatever is under the corner strip starts below it, and only that.
  //
  // The sidebar, ordinarily — which is the whole of "let the panes go all the
  // way to the top", since the agent and the panels then begin at zero. But
  // the strip never folds and the sidebar does, so once the sidebar is away
  // the strip is sitting over the *agent*, and the inset has to move with it:
  //
  //   open     strip 0..260   sidebar inset      agent at y=0
  //   folded   strip 0..152   agent inset        nothing behind the lights
  //
  // Measured both ways; without the second case the terminal's first two lines
  // are behind two buttons and the traffic lights.
  //
  // Padding rather than a spacer element, so the column's scroll container is
  // still the column.
  underStrip: { paddingBlockStart: space.titlebar },
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
  // A header and then the thing itself. The pane observes its own box, so it
  // simply gets a shorter one — nothing has to tell it the header is there.
  stacked: { display: "flex", flexDirection: "column" },

  // ── the fold, animated, and only the fold ────────────────────────────────
  //
  // Applied while `folding` is set and taken off again after, rather than left
  // on the column permanently. That is the whole of the design, and it is
  // because the same `flex-basis` is written by two very different things:
  //
  //   fold      one keypress, N → 0, and the eye needs to be told which
  //             column went where
  //   a drag    a value per pointermove, already tracking the cursor
  //   a resize  fitColumns squeezing a column to keep the agent readable
  //
  // A transition left on would make the last two lag. On a drag it is the
  // familiar failure — the divider trails the pointer and never catches up,
  // which is the same complaint the origin-relative arithmetic in Divider.tsx
  // exists to avoid. On a window resize it would animate a correction that is
  // supposed to be a fact about the window's size.
  //
  // Longhands, not the `transition` shorthand. StyleX drops some shorthands in
  // silence — see AGENTS.md on `border` and `background` — and this one has no
  // gate that would catch it: the layout is correct either way and only the
  // movement is missing, which reads as "the animation did not land" rather
  // than as a build problem.
  eased: (ms: number) => ({
    transitionProperty: "flex-basis",
    // Out fast, in gently. The column leaves at speed and arrives settling,
    // which is what makes a fold read as one object moving rather than as a
    // width being assigned.
    transitionTimingFunction: "cubic-bezier(0.32, 0.72, 0, 1)",
    transitionDuration: {
      default: `${ms}ms`,
      // Someone who has asked their system for less motion is not asking for a
      // faster version of it.
      "@media (prefers-reduced-motion: reduce)": "0s",
    },
  }),
});

export function App() {
  const appearance = useAppearance();
  const scheme = useColorScheme();
  const width = useWindowWidth();

  // What was asked for, not what was granted. fitColumns squeezes a column when
  // the window cannot hold it, and storing the squeezed result would make that
  // permanent: widening the window back would leave the column where the
  // narrowest moment put it — and now that the request is written down, it
  // would do so across every future launch as well. See columns.ts.
  //
  // Restored, and written back on every change. A drag writes on each move —
  // localStorage is synchronous, so this is a real cost and a small one: the
  // gesture already re-renders three columns, and a string of nine characters
  // is not what will be slow about that. Debouncing it would buy nothing and
  // would introduce a window in which the stored value and the screen disagree.
  const [want, setWant] = useState<Columns>(rememberedWidths);

  const resize = (change: (prev: Columns) => Columns) => {
    setWant((prev) => {
      const next = change(prev);
      rememberWidths(next);
      return next;
    });
  };
  // Which columns are folded away, restored the same way the session is. A
  // window that reopened both every time would undo the one choice the user
  // makes to get them out of the way.
  const [collapsed, setCollapsed] = useState<Collapsed>(rememberedCollapsed);
  const columns = fitColumns(width, want, collapsed);

  // True only for the length of a fold, and the reason is in `styles.eased`:
  // the same width is written by a drag and by a window resize, and neither of
  // those may be animated.
  const [folding, setFolding] = useState(false);

  const fold = (which: keyof Collapsed) => () => {
    setFolding(true);
    setCollapsed((prev) => {
      const next = { ...prev, [which]: !prev[which] };
      rememberCollapsed(next);
      return next;
    });
  };

  // A timer rather than `transitionend`, and the difference matters: the event
  // does not fire when there is no transition to end, which is exactly the
  // `prefers-reduced-motion` case — so the flag would be raised and never
  // lowered, and the next drag would be the animated one.
  //
  // Folding again mid-animation restarts the timer, because the effect is keyed
  // on `folding` going false and back true. A second fold before the first has
  // finished is one continuous movement, not two.
  useEffect(() => {
    if (!folding) {
      return;
    }
    const timer = setTimeout(() => setFolding(false), FOLD_MS);
    return () => clearTimeout(timer);
  }, [folding]);

  // ctrl+h/l between the columns, ctrl+j/k within one. See navigation.ts.
  useColumnKeys(collapsed);

  const { sessions, failure, reload: reloadSessions } = useSessions();
  const connected = useConnection();
  const { jobs } = useJobs();

  // Where the window is, off the router rather than out of state.
  //
  // `strict: false` because this component is the *root* — it is above every
  // match, so there is no single route whose params could be typed here. What
  // comes back is whatever the current match holds, and `addressFrom` tells the
  // three shapes apart by which keys are present.
  const navigate = useNavigate();
  const address = addressFrom(useParams({ strict: false }));

  // Mirrored to localStorage, for the one case a history cannot cover: the
  // application being quit and launched again, which starts at `#/` with no
  // entries behind it. Read once, at launch, in main.tsx.
  //
  // Keyed on the path and not on `address`, which is a fresh object every
  // render and would therefore run this on every one of them.
  const place = pathOf(address);
  useEffect(() => {
    rememberPlace(place);
  }, [place]);

  // Threads live here rather than in the sidebar, because the sidebar is no
  // longer the only thing that changes them: cmd+N starts one from anywhere in
  // the window. Two owners of the same list is two lists.
  const { threads, reload: reloadThreads } = useThreads();
  const { projects, reload: reloadProjects } = useProjects();
  const facts = useFacts();

  // What is open: the session the address names, if it is here and can be
  // attached to. Derived, never written back — see `sessionAt`.
  const open = sessionAt(address, sessions);

  // The modal, as a request rather than a boolean. It carries what the window
  // knew at the moment it was opened — which project was on screen, and which
  // workspace — so the form can read them once at mount instead of tracking a
  // selection that may move underneath it.
  const [newThread, setNewThread] = useState<NewThreadRequest | undefined>();

  // ── why a job finishing re-reads the sessions ────────────────────────────
  //
  // Because a job is what makes one. Before this, the list was taken once at
  // mount, which was right for as long as sessions only arrived from outside
  // the window — and wrong the moment awp could create one.
  //
  // The failure had no tell. A thread appeared in the sidebar immediately, and
  // its workspace did not, so it read "nothing yet" — which is precisely what a
  // thread whose creation *failed* looks like, while the workspace, bookmark
  // and session were all on disk.
  //
  // Counted rather than compared: `finished` changes exactly once per job that
  // stops, which is exactly when there might be a new session to see. The
  // threads are re-read at the same moment because the claim that puts a
  // workspace under its thread is the job's second-to-last step.
  const finished = jobs.filter((job) => isTerminal(job.status)).length;
  useEffect(() => {
    reloadSessions();
    reloadThreads();
    // `reloadSessions` and `reloadThreads` are stable — see the note in
    // useSessions on why `load` lives outside the component.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [finished]);

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
            workspace: open?.identity?.workspace,
            fromWorkspace: event.shiftKey,
          },
      );
    };
    window.addEventListener("keydown", onKey, { capture: true });
    return () => window.removeEventListener("keydown", onKey, { capture: true });
  }, [open]);

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
      {/* A corner, not a row. It is absolutely positioned over the sidebar's
          width, so the agent and the panels begin at the top of the window —
          see `styles.top` in Bars.tsx. */}
      <TopBar width={columns.sidebar} collapsed={collapsed} onFold={(which) => fold(which)()} />

      <div {...stylex.props(styles.columns)}>
        <aside
          data-column="sidebar"
          // ── a folded column is not merely narrow ──────────────────────────
          //
          // It stays mounted so the fold can be animated, which leaves every
          // control in it focusable, in the accessibility tree, and reachable
          // by ctrl+j/k — at zero width, off the edge of the window. The
          // accessory column made that visible rather than theoretical: folded,
          // the page offered both "show the panels" and "hide the panels", and
          // one of them was 16 pixels past the right edge.
          //
          // `inert` is the whole repair: it takes the subtree out of focus, out
          // of hit testing and out of the accessibility tree without removing
          // it from the layout the animation is driving.
          inert={collapsed.sidebar}
          {...stylex.props(
            styles.column,
            styles.underStrip,
            styles.fixed(columns.sidebar),
            folding && styles.eased(FOLD_MS),
          )}
        >
          <Boundary where="the sidebar">
            <Sidebar
              sessions={sessions}
              facts={facts}
              selected={open?.name}
              onSelect={(session) => {
                void navigate({ to: pathOf(addressOf(session)) });
              }}
              threads={threads}
              onThreadsChanged={reloadThreads}
              failure={failure}
              onNew={() =>
                setNewThread({
                  project: open?.identity?.project,
                  workspace: open?.identity?.workspace,
                  fromWorkspace: false,
                })
              }
            />
          </Boundary>
        </aside>

        <Divider
          label="sidebar width"
          value={columns.sidebar}
          onChange={(sidebar) => resize((prev) => ({ ...prev, sidebar }))}
          onToggle={fold("sidebar")}
        />

        <main
          data-column="agent"
          {...stylex.props(
            styles.column,
            styles.agent,
            styles.stacked,
            collapsed.sidebar && styles.underStrip,
          )}
        >
          <AgentBar
            jobs={jobs}
            session={open}
            facts={
              open?.identity === undefined
                ? undefined
                : facts.get(factsKey(open.identity.project, open.identity.workspace))
            }
            connected={connected}
            collapsed={collapsed}
            onFold={(which) => fold(which)()}
          />
          <Boundary where="the terminal">
            <Pane session={open?.name} fixture={rendererFixture} scheme={scheme} />
          </Boundary>
        </main>

        <Divider
          label="accessory width"
          invert
          value={columns.accessory}
          onChange={(accessory) => resize((prev) => ({ ...prev, accessory }))}
          onToggle={fold("accessory")}
        />

        <aside
          data-column="accessory"
          // See the sidebar's. Folded, its tab strip is still a tab strip.
          inert={collapsed.accessory}
          {...stylex.props(
            styles.column,
            styles.fixed(columns.accessory),
            folding && styles.eased(FOLD_MS),
          )}
        >
          {/* The open session's directory, because the diff panel diffs the
              workspace on screen. `startDir` and not the identity's workspace
              name: a workspace name is not a path, and jj resolves `@` from a
              directory. Nothing open is a state the panel says out loud. */}
          {/* One boundary per column, so a panel that throws does not take the
              terminal with it. The accessory column is where this earns its
              keep: it holds the newest code, and a bad option handed to the
              diff renderer used to white out the whole window. */}
          <Boundary where="the accessory panel">
            <Accessory
              onFold={fold("accessory")}
              dir={open?.startDir}
              project={open?.identity?.project}
              workspace={open?.identity?.workspace}
              scheme={scheme}
            />
          </Boundary>
        </aside>
      </div>

      {/* Outside the columns, because it is the window's and not a column's.
          It renders nothing at all while shut — see NewThread.tsx. */}
      <NewThread
        request={newThread}
        projects={projects}
        onProjects={reloadProjects}
        onClose={() => setNewThread(undefined)}
        onStarted={reloadThreads}
      />
    </div>
  );
}
