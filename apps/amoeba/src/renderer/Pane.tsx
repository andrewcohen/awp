import {
  type ColorScheme,
  focusPane,
  mountPaneTerminal,
  paneFontFamily,
  paneReady,
  paletteFor,
  paneThemeFor,
  resetPane,
  setPaneSinks,
  setPaneTheme,
  writePane,
} from "@awp-kit/pane";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useRef, useState } from "react";
import { type Attachment, attach, resize, write } from "./daemon";
import { currentColorScheme } from "./theme";
import { colors, space } from "./tokens.stylex";

// The pane, with a session behind it.
//
// The terminal is borrowed rather than built. See @awp-kit/pane — building one
// per view writes into freed wasm state, which is the single cause behind four
// different complaints in gdeck.

const styles = stylex.create({
  failure: {
    padding: `${space.titlebar} ${space.gutter} ${space.gutter}`,
    whiteSpace: "pre-wrap",
    color: colors.warn,
    margin: 0,
    font: "inherit",
  },
  // The container is painted the terminal's own background, not left
  // transparent. FitAddon sizes the canvas in whole cells, so it is always a
  // little smaller than the column, and the chrome showing through that
  // remainder reads as a seam down the edge of the pane rather than as the
  // rounding it is. That colour is the pane's, not the chrome's, so it arrives
  // as a dynamic value rather than a token — and it is read from the palette
  // rather than off the built theme, whose `background` is optional and would
  // have to be defaulted to something that is not a colour.
  backdrop: (base: string) => ({ width: "100%", height: "100%", backgroundColor: base }),
});

export function Pane({
  session,
  fixture,
  scheme,
}: {
  /** The session to attach to, or undefined to render `fixture` instead. */
  readonly session: string | undefined;
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
    let attachment: Attachment | undefined;

    setFailure("");

    paneReady()
      .then(() => {
        if (cancelled) {
          return;
        }
        const { term, fit } = mountPaneTerminal(parent, {
          fontFamily: paneFontFamily,
          // Read live rather than from the prop, deliberately. Mounting
          // replays the session and recolouring must not, so this effect does
          // not depend on `scheme` — and an effect that ignores a prop must not
          // close over it either. The second effect below owns the scheme.
          theme: paneThemeFor(currentColorScheme()),
        });
        resetPane();

        if (session === undefined) {
          setPaneSinks(
            () => {},
            () => {},
          );
          writePane(fixture);
          return;
        }

        // Attach at the size the terminal already is. The alternative — attach
        // at a default and resize once measured — reflows the real session
        // twice, visibly, because the first reflow is at the wrong size and
        // whatever is running redraws for it.
        attachment = attach(session, term.cols, term.rows, {
          // Through writePane, not term.write. The pane has to read the
          // private modes out of the stream to know what the program wants from
          // a wheel, and this is the only place every byte passes through.
          onChunk: (chunk) => writePane(chunk),
          onRefused: (reason) => setFailure(reason),
        });

        // Now the sinks have somewhere to go. Set per view rather than per
        // terminal, because the terminal never unmounts and `onData` has no
        // unsubscribe — a stale handler would type into a session the user has
        // already left.
        setPaneSinks(
          (data) => write(session, data),
          (cols, rows) => resize(session, cols, rows),
        );

        fit.fit();
        focusPane();
      })
      .catch((error: unknown) => {
        // Reported in the pane rather than thrown. A rendering unit whose
        // failure mode is a blank window teaches nothing; gdeck lost a whole
        // debugging session to a missing binding presenting as black.
        setFailure(error instanceof Error ? error.message : String(error));
      });

    return () => {
      cancelled = true;
      // Not merely tidy. This interrupt travels down the socket, cancels the
      // handler, closes the pty's Scope and kills `zmx attach`. Skipping it
      // leaves a client attached to a session sized for a window that has gone.
      attachment?.detach();
    };
  }, [session, fixture]);

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
    return <pre {...stylex.props(styles.failure)}>{failure}</pre>;
  }

  return <div ref={container} {...stylex.props(styles.backdrop(paletteFor(scheme).base))} />;
}
