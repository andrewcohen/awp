import { parseColor, SyntaxStyle } from "@opentui/core";

// Catppuccin Macchiato, the same hues the window uses.
//
// Not a theme system: a POC with one appearance. The one thing worth keeping
// from the amoeba side is that an accent is *spent* — it marks the row you are
// on and the state that wants an answer, and nothing else.

export const CHROME = {
  base: "#1e2030",
  raised: "#24273a",
  bar: "#363a4f",
  text: "#cad3f5",
  muted: "#6e738d",
  accent: "#f5a97f",
  /** What you said. Its own hue: the accent is chrome — a prompt, a selected
   *  row — and a message wearing it reads as furniture. */
  said: "#8aadf4",
  live: "#a6da95",
  ask: "#eed49f",
  warn: "#ed8796",
} as const;

/** Braille, because it turns in one cell and reads as motion without colour. */
export const SPIN = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"] as const;

/**
 * How code and markdown are coloured.
 *
 * The same Catppuccin hues as the chrome, so a fenced block in an agent's
 * answer belongs to this window rather than looking like a screenshot from
 * another one. `SyntaxStyle.fromStyles` wants every key it knows about; what
 * is left out falls back to `default`.
 */
export const SYNTAX = SyntaxStyle.fromStyles({
  default: { fg: parseColor(CHROME.text) },
  keyword: { fg: parseColor("#c6a0f6"), bold: true },
  string: { fg: parseColor("#a6da95") },
  comment: { fg: parseColor(CHROME.muted), italic: true },
  number: { fg: parseColor("#f5a97f") },
  function: { fg: parseColor("#8aadf4") },
  type: { fg: parseColor("#eed49f") },
  operator: { fg: parseColor("#91d7e3") },
  variable: { fg: parseColor(CHROME.text) },
  property: { fg: parseColor("#8aadf4") },
  label: { fg: parseColor("#a6da95") },
  conceal: { fg: parseColor(CHROME.muted) },
  "punctuation.bracket": { fg: parseColor(CHROME.muted) },
  "punctuation.delimiter": { fg: parseColor(CHROME.muted) },
  "punctuation.special": { fg: parseColor(CHROME.muted) },
  "markup.heading": { fg: parseColor("#f5a97f"), bold: true },
  "markup.heading.1": { fg: parseColor("#f5a97f"), bold: true },
  "markup.heading.2": { fg: parseColor("#eed49f"), bold: true },
  "markup.heading.3": { fg: parseColor("#8aadf4"), bold: true },
  "markup.bold": { fg: parseColor(CHROME.text), bold: true },
  "markup.strong": { fg: parseColor(CHROME.text), bold: true },
  "markup.italic": { fg: parseColor(CHROME.text), italic: true },
  "markup.list": { fg: parseColor("#f5a97f") },
  "markup.quote": { fg: parseColor(CHROME.muted), italic: true },
  "markup.raw": { fg: parseColor("#91d7e3") },
  "markup.raw.inline": { fg: parseColor("#91d7e3") },
  "markup.raw.block": { fg: parseColor(CHROME.text) },
  "markup.link": { fg: parseColor("#8aadf4"), underline: true },
  "markup.link.label": { fg: parseColor("#8aadf4") },
  "markup.link.url": { fg: parseColor("#8aadf4"), underline: true },
  // A diff in an answer is the one thing here that must read at a glance.
  "diff.plus": { fg: parseColor("#a6da95") },
  "diff.minus": { fg: parseColor("#ed8796") },
});
