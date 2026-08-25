import { useState } from "react";

// Deliberately almost nothing. This unit answers one question — does an
// Electrobun window render a Vite-built React app, in dev and from a build —
// and anything else in here would make a failure ambiguous.
//
// The counter is not decoration: it proves React is mounted and interactive,
// not that a static HTML file was served. The compiler line proves the Babel
// pass ran, because react-compiler rewrites this component and a component it
// did not touch has no `"use memo"` marker to find.

export function App() {
  const [clicks, setClicks] = useState(0);

  return (
    <main
      style={{
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        display: "flex",
        flexDirection: "column",
        gap: "1rem",
        alignItems: "flex-start",
        padding: "3rem",
      }}
    >
      <h1 style={{ margin: 0, fontSize: "1.25rem" }}>amoeba</h1>
      <p style={{ margin: 0, opacity: 0.7 }}>
        an Electrobun window over a Vite-built React renderer
      </p>
      <button type="button" onClick={() => setClicks((n) => n + 1)}>
        clicked {clicks} {clicks === 1 ? "time" : "times"}
      </button>
    </main>
  );
}
