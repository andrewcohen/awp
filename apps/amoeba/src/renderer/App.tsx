import { chromeFor } from "@awp-kit/pane";
import { Pane } from "./Pane";
import { rendererFixture } from "./fixture";
import { useColorScheme } from "./useColorScheme";

// The three-column shape amoeba is built around: sidebar, agent, accessory.
//
// Not resizable yet, and deliberately unstyled — this unit exists to answer
// whether the emulator renders correctly, and chrome would only make a
// rendering fault ambiguous. The columns are here anyway so the pane is sized
// by a real layout rather than by the whole window: a terminal that is right at
// one size and wrong at another is worth finding early.
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
      <aside
        style={{
          ...column,
          flex: "0 0 220px",
          borderRight: `1px solid ${chrome.border}`,
        }}
      >
        <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted }}>sidebar</div>
      </aside>

      <main style={{ ...column, flex: "1 1 auto" }}>
        <Pane fixture={rendererFixture} scheme={scheme} />
      </main>

      <aside
        style={{
          ...column,
          flex: "0 0 280px",
          borderLeft: `1px solid ${chrome.border}`,
        }}
      >
        <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted }}>accessory</div>
      </aside>
    </div>
  );
}
