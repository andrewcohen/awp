import { type Meter as Reading, readMeter, resetMeter } from "@awp-kit/pane";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { colors, space, text } from "../tokens.stylex";

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

const styles = stylex.create({
  panel: {
    padding: `${space.titlebar} ${space.gutter} ${space.gutter}`,
    display: "flex",
    flexDirection: "column",
    gap: "0.35rem",
    fontSize: text.small,
    lineHeight: 1.6,
  },
  row: { display: "flex", justifyContent: "space-between", gap: "0.5rem" },
  muted: { color: colors.muted },
  // Tabular figures, so a number that changes every quarter second does not
  // shuffle the column it sits in.
  figure: { fontVariantNumeric: "tabular-nums" },
  heading: { color: colors.muted, marginTop: "0.75rem" },
  first: { color: colors.muted, marginBottom: "0.5rem" },
  right: { float: "right" },
  verdict: { color: colors.live },
  dropping: { color: colors.warn },
  buttons: { display: "flex", gap: "0.35rem", marginTop: "1rem" },
  button: {
    flex: 1,
    padding: "0.25rem 0.5rem",
    backgroundColor: "transparent",
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: colors.border,
    color: colors.text,
    font: "inherit",
    cursor: "pointer",
  },
  secondary: { color: colors.muted },
  done: { color: colors.live },
});

// `peak` is the whole reason this column is worth looking at after the fact: by
// the time a hand has left the trackpad the live figure is already zero.
const row = (label: string, value: string, peak?: string) => (
  <div key={label} {...stylex.props(styles.row)}>
    <span {...stylex.props(styles.muted)}>{label}</span>
    <span {...stylex.props(styles.figure)}>
      {value}
      {peak !== undefined && <span {...stylex.props(styles.muted)}>{`  ${peak}`}</span>}
    </span>
  </div>
);

const DELTA_MODE = ["pixel", "line", "page"];

const emptyRate = { events: 0, lines: 0, writes: 0, bytes: 0 };
type Rate = typeof emptyRate;

const peakOf = (a: Rate, b: Rate): Rate => ({
  events: Math.max(a.events, b.events),
  lines: Math.max(a.lines, b.lines),
  writes: Math.max(a.writes, b.writes),
  bytes: Math.max(a.bytes, b.bytes),
});

export function Meter() {
  const [reading, setReading] = useState<Reading | undefined>();
  const [rate, setRate] = useState<Rate>(emptyRate);
  // The instantaneous rate is what a live readout should show and the worst
  // thing to copy: whatever the last quarter second happened to hold, which is
  // usually zero by the time anyone reaches for the button. The peak is what
  // the gesture actually asked for.
  //
  // Shown as well as copied. Keeping it only for the button meant the only way
  // to read a peak was to catch the live number mid-gesture, which is the
  // opposite of what a peak is for.
  const [peak, setPeak] = useState<Rate>(emptyRate);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let previous = readMeter();
    const id = setInterval(() => {
      const next = readMeter();
      const per = 1000 / SAMPLE_MS;
      const sample = {
        events: Math.round((next.wheelEvents - previous.wheelEvents) * per),
        lines: Math.round((next.linesSent - previous.linesSent) * per),
        writes: Math.round((next.writes - previous.writes) * per),
        bytes: Math.round((next.bytes - previous.bytes) * per),
      };
      setRate(sample);
      // Only when it actually moves. The rate re-renders on every tick anyway,
      // but a peak that sets state to the value it already held would keep this
      // column re-rendering long after the numbers stopped changing.
      setPeak((current) => {
        const raised = peakOf(current, sample);
        return raised.events === current.events &&
          raised.lines === current.lines &&
          raised.writes === current.writes &&
          raised.bytes === current.bytes
          ? current
          : raised;
      });
      setReading(next);
      previous = next;
    }, SAMPLE_MS);
    return () => clearInterval(id);
  }, []);

  if (reading === undefined) {
    return <div {...stylex.props(styles.panel, styles.muted)}>meter</div>;
  }

  // 16.7ms is one frame at 60Hz. Past 20 the window is dropping them, and that
  // is what "looks bad" means when the numbers above it look fine.
  const dropping = reading.frameP50 > 20;

  return (
    <div {...stylex.props(styles.panel)}>
      <div {...stylex.props(styles.first)}>
        pane <span {...stylex.props(styles.right)}>now / peak</span>
      </div>
      {row("wheel /s", String(rate.events), String(peak.events))}
      {row("lines /s", String(rate.lines), String(peak.lines))}
      {row("last deltaY", reading.lastDeltaY.toFixed(1))}
      {row("delta unit", DELTA_MODE[reading.lastDeltaMode] ?? "?")}

      <div {...stylex.props(styles.heading)}>from the session</div>
      {row("writes /s", String(rate.writes), String(peak.writes))}
      {row("bytes /s", String(rate.bytes), String(peak.bytes))}

      <div {...stylex.props(styles.heading)}>frames</div>
      {row("p50 ms", reading.frameP50.toFixed(1))}
      {row("worst ms", reading.frameMax.toFixed(1))}
      <div {...stylex.props(dropping ? styles.dropping : styles.verdict)}>
        {dropping ? "dropping frames" : "60Hz"}
      </div>

      <div {...stylex.props(styles.buttons)}>
        <button
          type="button"
          onClick={() => {
            // Awaited inside an async handler rather than left as a floating
            // then: the fallback path writes into the DOM, and a rejection that
            // nothing catches would leave a hidden textarea behind.
            void (async () => {
              await copyReading(reading, rate, peak);
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            })();
          }}
          {...stylex.props(styles.button, copied && styles.done)}
        >
          {copied ? "copied" : "copy"}
        </button>
        <button
          type="button"
          onClick={() => {
            resetMeter();
            setPeak(emptyRate);
          }}
          {...stylex.props(styles.button, styles.secondary)}
        >
          reset
        </button>
      </div>
    </div>
  );
}

/**
 * The reading as text, on the clipboard.
 *
 * Peaks as well as the current sample, because the reason to copy this is to
 * describe a gesture that has already finished — by the time the button is
 * reachable the live numbers have fallen back to nothing.
 */
const copyReading = async (reading: Reading, rate: Rate, peak: Rate): Promise<void> => {
  const report = [
    "pane meter",
    `  wheel /s      ${rate.events}  (peak ${peak.events})`,
    `  lines /s      ${rate.lines}  (peak ${peak.lines})`,
    `  last deltaY   ${reading.lastDeltaY.toFixed(1)} ${DELTA_MODE[reading.lastDeltaMode] ?? "?"}`,
    `  writes /s     ${rate.writes}  (peak ${peak.writes})`,
    `  bytes /s      ${rate.bytes}  (peak ${peak.bytes})`,
    `  frame p50     ${reading.frameP50.toFixed(1)}ms`,
    `  frame worst   ${reading.frameMax.toFixed(1)}ms`,
    `  totals        ${reading.wheelEvents} events, ${reading.linesSent} lines, ` +
      `${reading.writes} writes, ${reading.bytes} bytes`,
  ].join("\n");

  try {
    await navigator.clipboard.writeText(report);
  } catch {
    // A webview can refuse the async clipboard depending on how the page was
    // loaded. The old path still works and needs no permission, so it is the
    // fallback rather than an error nobody can act on.
    const scratch = document.createElement("textarea");
    scratch.value = report;
    scratch.style.position = "fixed";
    scratch.style.opacity = "0";
    document.body.append(scratch);
    scratch.select();
    document.execCommand("copy");
    scratch.remove();
  }
};
