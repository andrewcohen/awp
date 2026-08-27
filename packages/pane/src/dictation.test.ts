import { describe, expect, test } from "vitest";
import { type Insertion, textFrom } from "./dictation";

// Whether a `beforeinput` carries text the program should receive.
//
// The bug behind this: ghostty-web listens for keys, and text inserted by
// something that is not a keyboard — dictation, an assistive tool, a snippet
// expander — arrives as a `beforeinput` and nothing else. The emulator cancels
// every one of those without reading it, so speaking at the terminal did
// nothing at all, with no error and no event anyone was watching.
//
// The danger in fixing it is the opposite failure. Ordinary typing can raise a
// `beforeinput` too, and sending on both routes doubles every character — which
// reads as a broken keyboard and is worse than the drop.

const at = (over: Partial<Insertion> = {}): Insertion => ({
  inputType: "insertText",
  isComposing: false,
  sinceKey: Number.POSITIVE_INFINITY,
  data: "hello",
  transfer: null,
  ...over,
});

describe("text arriving without a keystroke", () => {
  test("is sent", () => {
    expect(textFrom(at())).toBe("hello");
  });

  test("is not sent when a key just fired", () => {
    // The key already carried it. A `beforeinput` immediately after a keydown
    // is that keystroke's own echo, and there is no flag on the event saying
    // so — only the ordering, which is why this is a time and not a boolean.
    expect(textFrom(at({ sinceKey: 0 }))).toBeUndefined();
    expect(textFrom(at({ sinceKey: 5 }))).toBeUndefined();
    // Far enough after to be something else entirely.
    expect(textFrom(at({ sinceKey: 500 }))).toBe("hello");
  });

  test("is left to the input method while it is composing", () => {
    // ghostty-web sends the finished word on `compositionend`. Reading it here
    // as well would double a whole word rather than a character.
    expect(textFrom(at({ isComposing: true }))).toBeUndefined();
  });

  test("is read from the transfer when that is where it is", () => {
    // A paste and a drop put the text on `dataTransfer` and leave `data` null.
    // A reader that only knows one spelling drops the other in silence.
    expect(textFrom(at({ inputType: "insertFromPaste", data: null, transfer: "pasted" }))).toBe(
      "pasted",
    );
    expect(textFrom(at({ inputType: "insertFromDrop", data: null, transfer: "dropped" }))).toBe(
      "dropped",
    );
  });

  test("ignores an edit that is not an insertion", () => {
    // These describe an edit to a document, and this host is not a document —
    // there is nothing under the canvas to edit. A terminal's backspace is a
    // keystroke and arrives as one.
    for (const kind of [
      "deleteContentBackward",
      "deleteContentForward",
      "historyUndo",
      "historyRedo",
      "formatBold",
      "insertLineBreak",
    ]) {
      expect(textFrom(at({ inputType: kind }))).toBeUndefined();
    }
  });

  test("ignores an insertion carrying nothing", () => {
    expect(textFrom(at({ data: null }))).toBeUndefined();
    expect(textFrom(at({ data: "" }))).toBeUndefined();
    expect(textFrom(at({ data: null, transfer: "" }))).toBeUndefined();
  });

  test("sends a whole dictated sentence in one go", () => {
    // Dictation arrives as one event with the whole phrase, not per character.
    // Nothing here may split or trim it: the spaces are part of what was said.
    const said = "run the tests and tell me what broke";
    expect(textFrom(at({ data: said }))).toBe(said);
  });
});
