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
 * What is selected inside `el`, or nothing.
 *
 * **The Selection API, not the text.** An agent's message is rendered markdown
 * — React elements this file did not create, several nodes deep — so there is
 * no single text node to read and no `value` to slice. `toString()` is what
 * flattens a range that spans a `<strong>`, two list items and half a
 * paragraph.
 *
 * A selection that starts in this item and ends outside it is not a quote of
 * this item, so the test is on the range's common ancestor rather than on
 * either end: `commonAncestorContainer` is inside `el` only when the whole
 * range is.
 */
export const selectedIn = (el: HTMLElement | null): string | undefined => {
  if (el === null) {
    return undefined;
  }
  const selection = globalThis.getSelection?.();
  if (selection === null || selection === undefined || selection.isCollapsed) {
    return undefined;
  }
  if (selection.rangeCount === 0) {
    return undefined;
  }
  const range = selection.getRangeAt(0);
  const words = selection.toString().trim();
  return words !== "" && el.contains(range.commonAncestorContainer) ? words : undefined;
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
