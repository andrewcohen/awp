import type { GadgetHead } from "@awp-kit/protocol";
import { Tabs } from "@base-ui/react/tabs";
import * as stylex from "@stylexjs/stylex";
import { useEffect, useState } from "react";
import { Gadget } from "./Gadget";
import { listGadgets, watchGadgets } from "../data/daemon";
import { typeset } from "../design/typeset";
import { colors, space, text } from "../design/tokens.stylex";

// The documents an agent wrote for this piece of work, one strip of them.
//
// ── why this is not the web panel ─────────────────────────────────────────
//
// Gadgets arrived riding the page feed, drawn over the web panel's stage. That
// works for exactly one of them. A thread accumulates gadgets over an
// afternoon — the latency table, then the comparison, then the thing that
// explains the first two — and a feed holding one address per thread meant
// each new one destroyed the last. The one somebody asks about is rarely the
// most recent.
//
// So: a panel with a strip, and a feed that says *one more exists* rather than
// *look here now*. The web panel goes back to being a browser and its
// navigation guard goes back to two schemes.
//
// ── the strip is not selected for you ─────────────────────────────────────
//
// Writing a gadget does not switch the accessory column to this panel, which
// is the one thing the page feed did that is not reproduced here. A navigation
// is a claim that replaces what was there, so moving to it costs nothing; a
// gadget is added beside its predecessors, and the visible change is the tab
// appearing. Stealing the column from somebody reading a diff, for a document
// that will still be here in an hour, is a worse trade than the one the page
// feed makes.

/**
 * A thread's gadgets, kept current.
 *
 * ── the subscription is here and the list is not ──────────────────────────
 *
 * Base UI unmounts a hidden tab, so a subscription owned by the panel would be
 * absent whenever somebody is looking at the diff — which is most of the time
 * an agent has something to show them. `Accessory` calls this instead and is
 * mounted for as long as the column is, which is also what lets the tab exist
 * only when there is something in it.
 *
 * The feed carries every thread's, and the filter is here: a per-thread
 * subscription would be torn down and rebuilt every time somebody clicks a
 * different row in the sidebar.
 */
export const useGadgets = (thread: string | undefined): ReadonlyArray<GadgetHead> => {
  // The thread is *in* the state rather than cleared by an effect when it
  // changes: clearing is a `setState` in an effect body, which is a second
  // render, and for one frame between them the previous thread's gadgets are
  // drawn under the new thread's name. Held together, they cannot disagree —
  // the read below simply does not recognise the old pair.
  const [held, setHeld] = useState<{
    readonly thread: string | undefined;
    readonly heads: ReadonlyArray<GadgetHead>;
  }>({ thread, heads: [] });

  useEffect(() => {
    let live = true;
    void listGadgets(thread).then((found) => {
      if (live) {
        // Folded into whatever arrived while the question was in flight,
        // rather than replacing it: the list was taken at the daemon before a
        // gadget written since, and dropping that one leaves a tab missing
        // until something else happens to the thread.
        setHeld((was) => ({
          thread,
          heads: found.reduce(withOne, was.thread === thread ? was.heads : []),
        }));
      }
    });
    const stop = watchGadgets((one) => {
      if (one.thread !== thread) {
        return;
      }
      setHeld((was) => ({
        thread,
        heads: withOne(was.thread === thread ? was.heads : [], one),
      }));
    });
    return () => {
      live = false;
      stop();
    };
  }, [thread]);

  return held.thread === thread ? held.heads : [];
};

/**
 * One gadget, into a list of them: newest first, one tab per address.
 *
 * By address and not by name, because the address is what the document is read
 * by — and a revision keeps its name, so appending would draw two tabs reading
 * the same thing. The later `at` wins, so this is the same function whether
 * what arrives is newer than what is held or older.
 */
const withOne = (heads: ReadonlyArray<GadgetHead>, one: GadgetHead): ReadonlyArray<GadgetHead> => {
  const had = heads.find((other) => other.address === one.address);
  const kept = had !== undefined && had.at > one.at ? had : one;
  return [kept, ...heads.filter((other) => other.address !== one.address)].toSorted(
    (a, b) => b.at - a.at,
  );
};

const styles = stylex.create({
  column: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0 },
  // A second strip under the column's own, and it has to read as the lesser of
  // the two: no border under it, a smaller type size, and it scrolls sideways
  // rather than wrapping. A wrapping strip changes the height of the chrome as
  // gadgets are written, which moves the document under it.
  list: {
    display: "flex",
    alignItems: "center",
    flexShrink: 0,
    gap: "0.25rem",
    paddingInline: "0.5rem",
    paddingBlock: "0.35rem",
    overflowX: "auto",
    scrollbarWidth: "none",
  },
  tab: {
    flexShrink: 0,
    maxWidth: "12rem",
    overflow: "hidden",
    padding: "0.15rem 0.45rem",
    backgroundColor: "transparent",
    borderStyle: "none",
    borderRadius: "0.25rem",
    color: colors.muted,
    fontSize: text.small,
    // A title is a sentence out of somebody's document and can be any length.
    // One line, clipped, with the whole of it in the tooltip: a tab that wraps
    // is a strip that changes height.
    whiteSpace: "nowrap",
    textOverflow: "ellipsis",
    cursor: "pointer",
    transitionProperty: "background-color, color",
    transitionDuration: "100ms",
    ":hover": { color: colors.text },
    userSelect: "none",
    WebkitUserSelect: "none",
  },
  tabOn: { backgroundColor: colors.raised, color: colors.accent },
  panel: { flex: 1, minHeight: 0, display: "flex", flexDirection: "column" },
  said: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    padding: space.gutter,
    color: colors.muted,
    fontSize: text.small,
    textAlign: "center",
  },
});

/**
 * The strip, and the document under it.
 *
 * ── which one is open is derived, not stored ─────────────────────────────
 *
 * The rule is "the newest, unless you picked another since the newest
 * arrived", and it is written as a derivation rather than as an effect that
 * corrects a stored value. What makes that possible is recording the newest
 * `at` at the moment of the pick: a gadget written after it moves that number,
 * the pick stops matching, and the strip is back on the new one — which is the
 * behaviour somebody wants without a line of code to reset it.
 *
 * An effect would have rendered the previous thread's document for a frame
 * first, which is the panel flickering as a sidebar row is clicked.
 */
export function Gadgets({ gadgets }: { readonly gadgets: ReadonlyArray<GadgetHead> }) {
  const [picked, setPicked] = useState<
    { readonly address: string; readonly newest: number } | undefined
  >(undefined);

  const newest = gadgets[0];
  const open =
    picked !== undefined &&
    newest !== undefined &&
    picked.newest === newest.at &&
    gadgets.some((one) => one.address === picked.address)
      ? picked.address
      : newest?.address;
  const showing = gadgets.find((one) => one.address === open);

  if (newest === undefined) {
    return (
      <div {...stylex.props(styles.said)}>
        nothing here yet — an agent writes these with awp_gadget, and they are forgotten when the
        daemon restarts
      </div>
    );
  }

  return (
    <Tabs.Root
      value={open}
      onValueChange={(value) => {
        setPicked({ address: String(value), newest: newest.at });
      }}
      {...stylex.props(styles.column)}
    >
      <Tabs.List {...stylex.props(styles.list)}>
        {gadgets.map((one) => (
          <Tabs.Tab
            key={one.address}
            value={one.address}
            // The name and not the title: the title is already the label, and
            // what a person needs the tooltip for is the name the agent would
            // have to be told to rewrite.
            title={`${one.title} · ${one.name}`}
            {...stylex.props(typeset.control, styles.tab, one.address === open && styles.tabOn)}
          >
            {one.title}
          </Tabs.Tab>
        ))}
      </Tabs.List>

      {showing === undefined ? undefined : (
        <Tabs.Panel value={showing.address} {...stylex.props(styles.panel)}>
          {/* Keyed by the writing and not by the address alone: an agent
              revising a gadget keeps its name, so the address is the same
              string, and a component mounted under it would keep the document
              it already has — and keep an error boundary that has already
              caught. */}
          <Gadget key={`${showing.address}#${String(showing.at)}`} address={showing.address} />
        </Tabs.Panel>
      )}
    </Tabs.Root>
  );
}
