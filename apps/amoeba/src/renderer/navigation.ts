import { focusPane } from "@awp-kit/pane";
import { useEffect, useRef } from "react";
import type { Collapsed } from "./columns";

// Moving around the window without a pointer, with vim's keys.
//
//   ctrl+h / ctrl+l   between columns — sidebar · agent · accessory
//   ctrl+j / ctrl+k   within one, down and up
//
// ── why ctrl, and not a bare hjkl ──────────────────────────────────────────
//
// Because the middle column is a terminal. An unmodified `j` belongs to
// whatever is running in it, and stealing it would break vim inside the very
// window whose keys are being borrowed from vim. The chord has to be one the
// pane does not want.
//
// ── why capture, on window ────────────────────────────────────────────────
//
// The emulator installs its own keydown handler and calls `stopPropagation`
// for every key it consumes, so a bubble-phase listener never hears a chord
// while a pane has focus. Measured for cmd+N — see the note in App.tsx.
//
// Capture is also the right meaning rather than merely the thing that works:
// an application shortcut is decided before the terminal claims the key.
// `stopPropagation` here is deliberate for the same reason — ctrl+l is clear-
// screen to a shell, and it must not arrive as both.
//
// ── and why a text field keeps them ───────────────────────────────────────
//
// On macOS ctrl+h, ctrl+j, ctrl+k and ctrl+a are the standard emacs bindings
// in any text field: backspace, newline, kill-to-end-of-line. The comment
// composer is a textarea. Stealing them there would break editing in exchange
// for a movement a person can make by pressing escape first.

/** The three columns, in the order `ctrl+h` and `ctrl+l` walk them. */
const ORDER = ["sidebar", "agent", "accessory"] as const;

type ColumnName = (typeof ORDER)[number];

/**
 * What `ctrl+j` and `ctrl+k` step through.
 *
 * Opt-in, and that is the point: the alternative is every focusable element,
 * which in this window means the hover-revealed controls on a sidebar row and
 * every button in a panel's toolbar. A list that moves through those is a list
 * nobody can predict. A column with no marked items still *receives* focus —
 * it simply has nothing to step through.
 */
const ITEM = "[data-nav-item]";

/** Whatever the browser would tab to, for a column that marks no items. */
const TABBABLE = [
  "button:not([disabled])",
  "[href]",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

const columnOf = (node: Element | null): ColumnName | undefined => {
  const found = node?.closest("[data-column]")?.getAttribute("data-column");
  return ORDER.find((one) => one === found);
};

const elementFor = (column: ColumnName): HTMLElement | null =>
  document.querySelector<HTMLElement>(`[data-column="${column}"]`);

const itemsIn = (column: ColumnName): ReadonlyArray<HTMLElement> => {
  const root = elementFor(column);
  return root === null ? [] : [...root.querySelectorAll<HTMLElement>(ITEM)];
};

/**
 * Editing text, where these chords already mean something else.
 *
 * **The terminal is not a text field here, and it says it is.** The emulator's
 * keyboard surface is a `contenteditable` div carrying `role=textbox` — which
 * is correct of it, and it is how the pane reaches an input method — so a
 * plain `isContentEditable` test reported "editing" for the whole agent
 * column. Every chord was then handed straight through and nothing moved:
 *
 *   keydown seen      KeyL, ctrlKey true
 *   defaultPrevented  false            ← the listener returned before acting
 *
 * The tell was a probe pressing the chords and watching focus stay exactly
 * where it started, on a DIV, with no error anywhere. So the question is not
 * "is this element editable" but "is this one of the window's own fields", and
 * the agent column is answered by where it is rather than by what it claims.
 */
const editing = (node: Element | null): boolean => {
  if (node === null || columnOf(node) === "agent") {
    return false;
  }
  const tag = node.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || (node as HTMLElement).isContentEditable;
};

const land = (element: HTMLElement): void => {
  element.focus();
  // `nearest`, so a row already on screen does not jump the column to centre
  // it. Moving one row should move the list by one row or by nothing at all.
  element.scrollIntoView({ block: "nearest" });
};

/**
 * Move between and within the columns.
 *
 * `collapsed` is read so a folded column is stepped over rather than focused —
 * it is on screen as a four-pixel strip, and landing in it looks like the
 * shortcut having done nothing.
 */
export function useColumnKeys(collapsed: Collapsed): void {
  // The last thing focused in each column, so coming back to one returns to
  // where it was left rather than to its first row. Held in a ref because
  // nothing renders differently for it — this is a memory, not state.
  const last = useRef<Partial<Record<ColumnName, HTMLElement>>>({});

  useEffect(() => {
    // The pane's host is not focusable in the ordinary sense; the emulator owns
    // the keyboard, and `focusPane` is how it is handed back. Kept in one place
    // so `enter` below reads the same for all three columns.
    const enter = (column: ColumnName): void => {
      if (column === "agent") {
        focusPane();
        return;
      }
      const remembered = last.current[column];
      if (remembered !== undefined && remembered.isConnected) {
        land(remembered);
        return;
      }
      const items = itemsIn(column);
      const first =
        items[0] ?? elementFor(column)?.querySelector<HTMLElement>(TABBABLE) ?? undefined;
      if (first !== undefined) {
        land(first);
      }
    };

    const onFocus = (event: FocusEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        return;
      }
      const column = columnOf(target);
      if (column !== undefined) {
        last.current[column] = target;
      }
    };

    const onKey = (event: KeyboardEvent) => {
      if (!event.ctrlKey || event.metaKey || event.altKey) {
        return;
      }
      const step =
        event.code === "KeyH"
          ? "left"
          : event.code === "KeyL"
            ? "right"
            : event.code === "KeyJ"
              ? "down"
              : event.code === "KeyK"
                ? "up"
                : undefined;
      if (step === undefined || editing(document.activeElement)) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();

      const here = columnOf(document.activeElement) ?? "agent";

      if (step === "left" || step === "right") {
        const open = ORDER.filter((one) => one === "agent" || !collapsed[one]);
        const at = open.indexOf(here);
        // Clamped rather than wrapped. Wrapping makes ctrl+l at the right edge
        // jump the whole window, which reads as a mis-press rather than as a
        // move — and there are three columns, so the wrap is never the short
        // way round.
        const next = open[Math.min(Math.max(at + (step === "right" ? 1 : -1), 0), open.length - 1)];
        if (next !== undefined && next !== here) {
          enter(next);
        }
        return;
      }

      const items = itemsIn(here);
      if (items.length === 0) {
        // Nothing to step through — the agent column, or a panel that marks
        // no rows. Deliberately not a fallback to every tabbable element:
        // see ITEM.
        return;
      }
      const at = items.indexOf(document.activeElement as HTMLElement);
      const to = at === -1 ? 0 : at + (step === "down" ? 1 : -1);
      const landing = items[Math.min(Math.max(to, 0), items.length - 1)];
      if (landing !== undefined) {
        land(landing);
      }
    };

    window.addEventListener("focusin", onFocus, { capture: true });
    window.addEventListener("keydown", onKey, { capture: true });
    return () => {
      window.removeEventListener("focusin", onFocus, { capture: true });
      window.removeEventListener("keydown", onKey, { capture: true });
    };
  }, [collapsed]);
}
