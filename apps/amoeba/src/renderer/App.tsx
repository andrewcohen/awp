import { chromeFor } from "@awp-kit/pane";
import { useState } from "react";
import { Divider } from "./Divider";
import { Pane } from "./Pane";
import { fitColumns } from "./columns";
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
  const [want, setWant] = useState({ sidebar: 220, accessory: 280 });
  const columns = fitColumns(width, want);

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
        <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted }}>sidebar</div>
      </aside>

      <Divider
        label="sidebar width"
        chrome={chrome}
        value={columns.sidebar}
        onChange={(sidebar) => setWant((prev) => ({ ...prev, sidebar }))}
      />

      <main style={{ ...column, flex: "1 1 auto" }}>
        <Pane fixture={rendererFixture} scheme={scheme} />
      </main>

      <Divider
        label="accessory width"
        chrome={chrome}
        invert
        value={columns.accessory}
        onChange={(accessory) => setWant((prev) => ({ ...prev, accessory }))}
      />

      <aside style={{ ...column, flex: `0 0 ${columns.accessory}px` }}>
        <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted }}>accessory</div>
      </aside>
    </div>
  );
}
