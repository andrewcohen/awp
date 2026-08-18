import type { ITheme } from "ghostty-web";

// The deck's palette states its chrome as ANSI 16 indices so the developer's
// terminal theme remaps them — that indirection is the point there, and it is
// why `internal/charm/palette.go` holds indices rather than hexes.
//
// A webview has no terminal to remap anything. So gdeck has to state the hexes
// the working theme resolves those indices to, and this file is the one place it
// does: Catppuccin Macchiato, the theme the TUI is developed against. Keeping the
// two in the same order as the Go palette's table is deliberate — a pane and a
// deck row showing the same status should not be two different greens.
export const macchiato = {
  base: "#181926",
  text: "#cad3f5",
  black: "#494d64",
  red: "#ed8796",
  green: "#a6da95",
  yellow: "#eed49f",
  blue: "#8aadf4",
  magenta: "#f5bde6",
  cyan: "#8bd5ca",
  white: "#b8c0e0",
  brightBlack: "#5b6078",
  brightWhite: "#a5adcb",
} as const;

// The pane's theme, in ghostty-web's shape. Bright variants that Macchiato does
// not distinguish are pointed at their normal counterparts rather than invented:
// a made-up bright red is a colour the terminal the deck is being compared
// against would never produce.
export const paneTheme: ITheme = {
  background: macchiato.base,
  foreground: macchiato.text,
  cursor: macchiato.text,
  black: macchiato.black,
  red: macchiato.red,
  green: macchiato.green,
  yellow: macchiato.yellow,
  blue: macchiato.blue,
  magenta: macchiato.magenta,
  cyan: macchiato.cyan,
  white: macchiato.white,
  brightBlack: macchiato.brightBlack,
  brightRed: macchiato.red,
  brightGreen: macchiato.green,
  brightYellow: macchiato.yellow,
  brightBlue: macchiato.blue,
  brightMagenta: macchiato.magenta,
  brightCyan: macchiato.cyan,
  brightWhite: macchiato.brightWhite,
};
