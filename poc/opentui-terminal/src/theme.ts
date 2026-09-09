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
  live: "#a6da95",
  ask: "#eed49f",
  warn: "#ed8796",
} as const;

/** Braille, because it turns in one cell and reads as motion without colour. */
export const SPIN = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"] as const;
