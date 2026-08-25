import { type Chrome, type Meter as Reading, readMeter, resetMeter } from "@awp-kit/pane";
import { useEffect, useState } from "react";

// The pane's vital signs, in the accessory column.
//
// Here because "it feels laggy" is not a bug report anyone can act on, and two
// rounds of guessing at what it meant produced two wrong answers. Each row
// below separates a cause the others cannot explain: what the pointing device
// emitted, how many line reports that became, how much came back, and whether
// the window is drawing frames at all.
//
// Sampled rather than pushed. A meter that re-renders React on every wheel
// event competes with the thing it is measuring.

const SAMPLE_MS = 250;

const row = (label: string, value: string, muted: string) => (
  <div key={label} style={{ display: "flex", justifyContent: "space-between", gap: "0.5rem" }}>
    <span style={{ color: muted }}>{label}</span>
    <span style={{ fontVariantNumeric: "tabular-nums" }}>{value}</span>
  </div>
);

const DELTA_MODE = ["pixel", "line", "page"];

export function Meter({ chrome }: { readonly chrome: Chrome }) {
  const [reading, setReading] = useState<Reading | undefined>();
  const [rate, setRate] = useState({ events: 0, lines: 0, writes: 0, bytes: 0 });

  useEffect(() => {
    let previous = readMeter();
    const id = setInterval(() => {
      const next = readMeter();
      const per = 1000 / SAMPLE_MS;
      setRate({
        events: Math.round((next.wheelEvents - previous.wheelEvents) * per),
        lines: Math.round((next.linesSent - previous.linesSent) * per),
        writes: Math.round((next.writes - previous.writes) * per),
        bytes: Math.round((next.bytes - previous.bytes) * per),
      });
      setReading(next);
      previous = next;
    }, SAMPLE_MS);
    return () => clearInterval(id);
  }, []);

  if (reading === undefined) {
    return <div style={{ padding: "3rem 1rem 1rem", color: chrome.muted }}>meter</div>;
  }

  // 16.7ms is one frame at 60Hz. Past 20 the window is dropping them, and that
  // is what "looks bad" means when the numbers above it look fine.
  const dropping = reading.frameP50 > 20;

  return (
    <div
      style={{
        padding: "3rem 1rem 1rem",
        display: "flex",
        flexDirection: "column",
        gap: "0.35rem",
        fontSize: 11,
        lineHeight: 1.6,
      }}
    >
      <div style={{ color: chrome.muted, marginBottom: "0.5rem" }}>pane</div>
      {row("wheel /s", String(rate.events), chrome.muted)}
      {row("lines /s", String(rate.lines), chrome.muted)}
      {row("last deltaY", reading.lastDeltaY.toFixed(1), chrome.muted)}
      {row("delta unit", DELTA_MODE[reading.lastDeltaMode] ?? "?", chrome.muted)}

      <div style={{ color: chrome.muted, margin: "0.75rem 0 0" }}>from the session</div>
      {row("writes /s", String(rate.writes), chrome.muted)}
      {row("bytes /s", String(rate.bytes), chrome.muted)}

      <div style={{ color: chrome.muted, margin: "0.75rem 0 0" }}>frames</div>
      {row("p50 ms", reading.frameP50.toFixed(1), chrome.muted)}
      {row("worst ms", reading.frameMax.toFixed(1), chrome.muted)}
      <div style={{ color: dropping ? "#ed8796" : "#a6da95" }}>
        {dropping ? "dropping frames" : "60Hz"}
      </div>

      <button
        type="button"
        onClick={() => resetMeter()}
        style={{
          marginTop: "1rem",
          padding: "0.25rem 0.5rem",
          background: "transparent",
          border: `1px solid ${chrome.border}`,
          color: chrome.muted,
          font: "inherit",
          cursor: "pointer",
        }}
      >
        reset
      </button>
    </div>
  );
}
