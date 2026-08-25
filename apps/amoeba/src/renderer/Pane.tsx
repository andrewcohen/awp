import {
  type ColorScheme,
  mountPaneTerminal,
  paneFontFamily,
  paneReady,
  paneThemeFor,
  resetPane,
  setPaneSinks,
  setPaneTheme,
} from "@awp-kit/pane";
import { useEffect, useRef, useState } from "react";
import { currentColorScheme } from "./useColorScheme";

// A pane rendering bytes, with nothing behind it.
//
// This is the step that says whether the emulator is right, separately from
// whether attaching works — the two failed together in gdeck for a while, and
// separating them is how the cause was found. So: no daemon, no zmx, no pty. A
// fixture of escape sequences goes in, and what comes out is either correct or
// it is not.
//
// The terminal is borrowed rather than built. See @awp-kit/pane — building one
// per view writes into freed wasm state.

export function Pane({
  fixture,
  scheme,
}: {
  readonly fixture: string;
  readonly scheme: ColorScheme;
}) {
  const container = useRef<HTMLDivElement | null>(null);
  const [failure, setFailure] = useState<string>("");

  useEffect(() => {
    const parent = container.current;
    if (parent === null) {
      return;
    }

    // The wasm has to finish compiling before a Terminal can exist. Under
    // StrictMode this effect runs twice, so `cancelled` stops the second pass
    // writing into a container the first has already left — the awaited gap is
    // exactly where a double-mount overlaps.
    let cancelled = false;

    paneReady()
      .then(() => {
        if (cancelled) {
          return;
        }
        const { term } = mountPaneTerminal(parent, {
          fontFamily: paneFontFamily,
          // Read live rather than from the prop, deliberately. Mounting
          // replays the fixture and recolouring must not, so this effect does
          // not depend on `scheme` — and an effect that ignores a prop must not
          // close over it either. The second effect below owns the scheme.
          theme: paneThemeFor(currentColorScheme()),
        });
        resetPane();

        // Nothing is behind this pane, so keystrokes have nowhere to go.
        // Setting the sinks anyway stops the terminal holding a stale handler
        // from a previous view — onData has no unsubscribe, which is why the
        // sinks exist at all.
        setPaneSinks(
          () => {},
          () => {},
        );

        term.write(fixture);
      })
      .catch((error: unknown) => {
        // Reported in the pane rather than thrown. A rendering unit whose
        // failure mode is a blank window teaches nothing; gdeck lost a whole
        // debugging session to a missing binding presenting as black.
        setFailure(error instanceof Error ? error.message : String(error));
      });

    return () => {
      cancelled = true;
    };
  }, [fixture]);

  // Recolour without remounting. Still behind paneReady() — the appearance can
  // change while the wasm is still compiling, and there is no renderer to talk
  // to until it is not.
  useEffect(() => {
    let cancelled = false;
    void paneReady().then(() => {
      if (!cancelled) {
        setPaneTheme(paneThemeFor(scheme));
      }
    });
    return () => {
      cancelled = true;
    };
  }, [scheme]);

  if (failure !== "") {
    return (
      <pre style={{ padding: "1rem", whiteSpace: "pre-wrap", color: "#ed8796" }}>
        pane failed to start: {failure}
      </pre>
    );
  }

  // The container is painted the terminal's own background, not left
  // transparent. FitAddon sizes the canvas in whole cells, so it is always a
  // little smaller than the column — 26px at this width — and the chrome
  // showing through that remainder reads as a seam down the edge of the pane
  // rather than as the rounding it is.
  return (
    <div
      ref={container}
      style={{ width: "100%", height: "100%", background: paneThemeFor(scheme).background }}
    />
  );
}
