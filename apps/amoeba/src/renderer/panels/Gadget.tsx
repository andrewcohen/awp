import { readGadgetAddress } from "@awp-kit/protocol";
import * as stylex from "@stylexjs/stylex";
import * as React from "react";
import { useEffect, useState } from "react";
import * as runtime from "react/jsx-runtime";
import { type GadgetDoc, runGadget } from "./gadget-run";
import { components } from "./Markdown";
import { readGadget } from "../data/daemon";
import { Boundary } from "../shell/Boundary";
import { colors, space, text } from "../design/tokens.stylex";

// A document an agent wrote, in the column a person reads.
//
// One of these at a time, under the strip in `Gadgets.tsx`. This half is only
// the document: read it at an address, build it, draw it.
//
// ── it has an address and it is not a web page ────────────────────────────
//
// A `gadget://` address is never handed to a webview. It names a document the
// daemon compiled, which this renders in this process, in this React tree.
//
// That is what makes the scope real. An inline component in the document is
// called by React here, so it can hold state, and it can be handed the
// window's own tokens — see `gadget-run.ts`, which is where the list is bound.
//
// One consequence worth naming: this works in a plain browser, where the rest
// of the panel does not. A native webview needs the app window; a gadget needs
// nothing but React, so `bun run probe:*` and a dev-server tab can both see one.

const styles = stylex.create({
  // The document's own box. It scrolls, because a gadget is a page and the
  // column is short — and it has the reading size `Markdown`'s `reading` prop
  // sets, for the same reason: this is read, not scanned.
  page: {
    height: "100%",
    minHeight: 0,
    overflowY: "auto",
    padding: space.gutter,
    color: colors.text,
    fontSize: text.lead,
    lineHeight: 1.7,
  },
  said: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    padding: "1rem",
    color: colors.muted,
    fontSize: text.small,
    textAlign: "center",
  },
});

/**
 * What an inline component can reach for, with values.
 *
 * The names are the contract's (`gadgetScope`) and the values are this
 * window's. `colors`, `text` and `space` are StyleX variable objects, so what
 * a document receives is `var(--…)` strings — which is the point: they resolve
 * to whatever the theme is at the moment they are painted, so a gadget written
 * in the light theme is not a gadget that glows in the dark one.
 */
const handed: Readonly<Record<string, unknown>> = { React, colors, text, space };

/**
 * A gadget, read and drawn.
 *
 * ── it is mounted under a key, and that is the revision mechanism ─────────
 *
 * An agent revising a gadget keeps the name, so the address does not change —
 * and nothing keyed on the address would re-read the document, or clear an
 * error boundary that has already caught. `Gadgets.tsx` keys this component on
 * the address *and* the head's `at`, which moves on every writing. So a
 * revision is a remount, and everything here starts from the beginning without
 * a line of code to reset.
 */
export function Gadget({ address }: { readonly address: string }) {
  const [state, setState] = useState<
    | { readonly kind: "reading" }
    | { readonly kind: "ready"; readonly doc: GadgetDoc }
    | { readonly kind: "refused"; readonly why: string }
  >({ kind: "reading" });

  useEffect(() => {
    let live = true;
    readGadget(address)
      .then((gadget) => runGadget(gadget.code, runtime, handed))
      .then((doc) => {
        if (live) {
          setState({ kind: "ready", doc });
        }
      })
      .catch((error: unknown) => {
        if (live) {
          // Both halves of what can go wrong before React sees anything: the
          // daemon refusing (no such gadget, or it has restarted since) and
          // the document failing to build. One sentence either way, because
          // the panel is where a person finds out and the sentence is what
          // they can hand back to the agent.
          setState({
            kind: "refused",
            why: error instanceof Error ? error.message : String(error),
          });
        }
      });
    return () => {
      live = false;
    };
  }, [address]);

  if (state.kind === "reading") {
    return <div {...stylex.props(styles.said)}>reading the gadget…</div>;
  }
  if (state.kind === "refused") {
    return <div {...stylex.props(styles.said)}>{state.why}</div>;
  }

  const Doc = state.doc;
  const name = readGadgetAddress(address)?.name ?? address;
  return (
    <div {...stylex.props(styles.page)}>
      {/* ── a gadget that throws says which gadget it was ─────────────────
      
          The document is the agent's code, so a render-time throw is the
          ordinary failure rather than the exceptional one, and what it would
          otherwise leave is an empty column — the same nothing a gadget that
          drew nothing leaves. `Boundary` prints the sentence, the component
          stack and a copy button, which is what makes it useful here: what
          fixes this is the agent, and the way it hears about it is a person
          pasting the report back.
      
          A boundary that has caught stays caught, so it must not outlive the
          document it caught: the whole component is mounted under a key that
          carries the showing — see the note above — and a revised gadget is
          therefore a new boundary as well as a new read. */}
      <Boundary where={`the gadget ${name}`}>
        <Doc components={components} />
      </Boundary>
    </div>
  );
}
