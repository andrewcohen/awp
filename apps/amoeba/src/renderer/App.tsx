import type { SessionInfo } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { Divider } from "./Divider";
import { debugTools } from "./debug";
import { Pane } from "./Pane";
import { Sidebar } from "./Sidebar";
import { fitColumns } from "./columns";
import { listSessions } from "./daemon";
import { rememberSession, rememberedSession } from "./remembered";
import { rendererFixture } from "./fixture";
import { themeFor, useAppearance, useColorScheme } from "./theme";
import { colors, space, text } from "./tokens.stylex";
import { useWindowWidth } from "./useWindowWidth";

// The three-column shape amoeba is built around: sidebar, agent, accessory.
//
// `height: 100%` and not `100vh`. The root is already pinned to the window in
// global.css, and vh units in a webview measure the visual viewport — which is
// a different number the moment anything insets the window, and a scrollbar's
// worth of overflow at the top level is precisely what is not allowed here.

const styles = stylex.create({
  window: {
    display: "flex",
    height: "100%",
    backgroundColor: colors.base,
    color: colors.text,
    fontFamily: text.mono,
    fontSize: text.body,
  },
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
  accessory: { display: "flex", flexDirection: "column", height: "100%" },
  tabs: {
    display: "flex",
    gap: "0.25rem",
    padding: `${space.titlebar} 0.5rem 0`,
  },
  tab: {
    padding: "0.2rem 0.5rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    color: colors.text,
    font: "inherit",
    cursor: "pointer",
  },
  tabOn: { backgroundColor: colors.border },
  tool: { flex: 1, minHeight: 0, overflowY: "auto" },
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
  const columns = fitColumns(width, want);

  const [sessions, setSessions] = useState<ReadonlyArray<SessionInfo>>([]);
  // Restored, not defaulted. Editing the renderer reloads the page, and a pane
  // that forgot its session every time would mean reattaching by hand after
  // every change.
  const [selected, setSelected] = useState<string | undefined>(rememberedSession);
  const [failure, setFailure] = useState<string | undefined>();
  const [activeTool, setTool] = useState<string>(debugTools[0]?.id ?? "");

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
      <aside {...stylex.props(styles.column, styles.fixed(columns.sidebar))}>
        <Sidebar
          sessions={sessions}
          selected={selected}
          onSelect={(session) => {
            setSelected(session.name);
            rememberSession(session.name);
          }}
          failure={failure}
        />
      </aside>

      <Divider
        label="sidebar width"
        value={columns.sidebar}
        onChange={(sidebar) => setWant((prev) => ({ ...prev, sidebar }))}
      />

      <main {...stylex.props(styles.column, styles.agent)}>
        <Pane session={selected} fixture={rendererFixture} scheme={scheme} />
      </main>

      <Divider
        label="accessory width"
        invert
        value={columns.accessory}
        onChange={(accessory) => setWant((prev) => ({ ...prev, accessory }))}
      />

      <aside {...stylex.props(styles.column, styles.fixed(columns.accessory))}>
        <div {...stylex.props(styles.accessory)}>
          {debugTools.length > 1 && (
            <div {...stylex.props(styles.tabs)}>
              {debugTools.map((tool) => (
                <button
                  key={tool.id}
                  type="button"
                  onClick={() => setTool(tool.id)}
                  {...stylex.props(styles.tab, tool.id === activeTool && styles.tabOn)}
                >
                  {tool.label}
                </button>
              ))}
            </div>
          )}
          <div {...stylex.props(styles.tool)}>
            {(debugTools.find((t) => t.id === activeTool) ?? debugTools[0])?.render()}
          </div>
        </div>
      </aside>
    </div>
  );
}
