// Pointing at a piece of a conversation, so a reply can be about that piece.
//
// A conversation with an agent goes wrong in a specific place — one claim in a
// paragraph, one command in a tool call, one file it named — and describing
// where you mean in prose, at the bottom, after it has scrolled away, is the
// worst way to say so. This window has answered the same question twice
// already, and both are the pattern rather than something to reinvent:
//
//   the diff       drag across line numbers  →  a comment on that range
//   the web panel  point at an element       →  a note carrying its selector
//
// Both anchor to something addressable. Here the anchor is a text selection,
// and the degenerate case — selecting nothing — is a reply about the whole
// item, which is why the control exists on the row rather than only in a
// floating bubble over a selection.
//
// Kept out of `Chat.tsx` so it can be tested: a file importing StyleX cannot
// be loaded by vitest.

/**
 * Where a selection is on screen, so a control can be put beside it.
 *
 * The range's own rectangle rather than the row's: what somebody highlighted
 * is a phrase halfway down a paragraph, and an affordance at the top of the
 * message is an affordance about something else.
 */
export interface Spot {
  readonly text: string;
  /** Viewport coordinates of the highlighted range. */
  readonly left: number;
  readonly top: number;
  readonly width: number;
}

/**
 * What is selected inside `el`, and where — or nothing.
 *
 * ── read on a settled gesture, never during one ──────────────────────────
 *
 * `selectionchange` fires all through a drag. Reading it there puts a control
 * under a pointer that is still selecting, which is the hazard the diff
 * panel's line selection already recorded: a render during a gesture ends the
 * gesture.
 *
 * ── the Selection API, not the text ─────────────────────────────────────
 *
 * An agent's message is rendered markdown — React elements this file did not
 * create, several nodes deep — so there is no single text node to read and no
 * `value` to slice. `toString()` is what flattens a range spanning a
 * `<strong>`, two list items and half a paragraph.
 *
 * ── and it must be a selection in THIS element ─────────────────────────────
 *
 * A selection that starts in the transcript and ends outside it is not a
 * quote of the transcript, so the test is on the range's common ancestor
 * rather than on either end: `commonAncestorContainer` is inside `el` only
 * when the whole range is.
 */
export const spotIn = (el: HTMLElement | null): Spot | undefined => {
  if (el === null) {
    return undefined;
  }
  const selection = globalThis.getSelection?.();
  if (
    selection === null ||
    selection === undefined ||
    selection.isCollapsed ||
    selection.rangeCount === 0
  ) {
    return undefined;
  }
  const range = selection.getRangeAt(0);
  const text = selection.toString().trim();
  if (text === "" || !el.contains(range.commonAncestorContainer)) {
    return undefined;
  }
  const box = range.getBoundingClientRect();
  return { text, left: box.left, top: box.top, width: box.width };
};

/**
 * A piece of the conversation, as markdown a person can edit.
 *
 * A blockquote, because the reply is markdown and this is what markdown's
 * quoting looks like — so the agent reads it as a quotation rather than as a
 * line of text that happens to start with an angle bracket, and a person can
 * delete it by deleting the lines.
 *
 * Trailing whitespace is dropped per line: `> ` on its own is a line with two
 * characters of nothing, and a diff or a code block quoted whole is full of
 * them.
 */
export const quoted = (text: string): string =>
  text
    .trim()
    .split("\n")
    .map((line) => `> ${line}`.trimEnd())
    .join("\n");

/**
 * The composer's new contents, with a quote put in front of what was typed.
 *
 * In the box and not sent, which is the whole point: the diff panel batches
 * comments because six remarks are one prompt, where a quoted reply *is* the
 * next turn and wants a sentence written after it. It is also what makes the
 * gesture undoable — by deleting it.
 *
 * A blank line after the quote, so what somebody types is not inside it. Two
 * quotes in a row stack rather than merging, for the same reason.
 */
export const withQuote = (draft: string, text: string): string => {
  const quote = quoted(text);
  const rest = draft.replace(/^\s+/, "");
  return rest === "" ? `${quote}\n\n` : `${quote}\n\n${rest}`;
};
