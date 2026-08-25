import { chromeFor } from "@awp-kit/pane";
import type { SessionInfo } from "@awp-kit/protocol";
import { useEffect, useState } from "react";
import { Divider } from "./Divider";
import { debugTools } from "./debug";
import { Pane } from "./Pane";
import { Sidebar } from "./Sidebar";
import { fitColumns } from "./columns";
import { listSessions } from "./daemon";
import { rememberSession, rememberedSession } from "./remembered";
import { rendererFixture } from "./fixture";
import { useColorScheme } from "./useColorScheme";
import { useWindowWidth } from "./useWindowWidth";

// The three-column shape amoeba is built around: sidebar, agent, accessory.
//
// `height: 100%` and not `100vh`. The root is already pinned to the window in
// global.css, and vh units in a webview measure the visual viewport — which is
// a different number the moment anything insets the window, and a scrollbar's
// worth of overflow at the top level is precisely what is not allowed here.

const column = {
  minWidth: 0,
  height: "100%",
  overflow: "hidden",
} as const;

export function App() {
  const scheme = useColorScheme();
  const chrome = chromeFor(scheme);
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

  // Flex-shrink is zero on both sides. The arithmetic in fitColumns already
  // guarantees the layout fits, so letting flexbox shrink as well would mean
  // two rules deciding the same widths and the rendered result disagreeing with
  // the state that is supposed to describe it.
  return (
    <div
      style={{
        display: "flex",
        height: "100%",
        background: chrome.base,
        color: chrome.text,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        fontSize: 13,
      }}
    >
      <aside style={{ ...column, flex: `0 0 ${columns.sidebar}px` }}>
        <Sidebar
          sessions={sessions}
          selected={selected}
          onSelect={(session) => {
            setSelected(session.name);
            rememberSession(session.name);
          }}
          chrome={chrome}
          failure={failure}
        />
      </aside>

      <Divider
        label="sidebar width"
        chrome={chrome}
        value={columns.sidebar}
        onChange={(sidebar) => setWant((prev) => ({ ...prev, sidebar }))}
      />

      <main style={{ ...column, flex: "1 1 auto" }}>
        <Pane session={selected} fixture={rendererFixture} scheme={scheme} />
      </main>

      <Divider
        label="accessory width"
        chrome={chrome}
        invert
        value={columns.accessory}
        onChange={(accessory) => setWant((prev) => ({ ...prev, accessory }))}
      />

      <aside style={{ ...column, flex: `0 0 ${columns.accessory}px` }}>
        <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
          {debugTools.length > 1 && (
            <div style={{ display: "flex", gap: "0.25rem", padding: "2.5rem 0.5rem 0" }}>
              {debugTools.map((tool) => (
                <button
                  key={tool.id}
                  type="button"
                  onClick={() => setTool(tool.id)}
                  style={{
                    padding: "0.2rem 0.5rem",
                    background: tool.id === activeTool ? chrome.border : "transparent",
                    border: "none",
                    color: chrome.text,
                    font: "inherit",
                    cursor: "pointer",
                  }}
                >
                  {tool.label}
                </button>
              ))}
            </div>
          )}
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
            {(debugTools.find((t) => t.id === activeTool) ?? debugTools[0])?.render(chrome)}
          </div>
        </div>
      </aside>
    </div>
  );
}
