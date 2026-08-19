import { useEffect, useState } from "react";

// Light and dark for the chrome, with "system" as a real option rather than a
// default someone has to opt out of.
//
// Three states, not two. A binary toggle has to be set once and then stays
// wrong for half of every day — the useful default is following the OS, and the
// override exists for when you want the window to disagree with it. That is why
// the stored value can be "system" instead of the resolved answer: storing
// "dark" at midnight would silently pin it.
//
// shadcn's tokens already define both palettes, keyed on a `dark` class at the
// root, so switching is a class toggle and nothing here needs to know a colour.
// The terminal is untouched: a pane's colours are the agent's colours, answering
// to what the program emits rather than to the window around it.
export type ThemeMode = "system" | "light" | "dark";

const key = "gdeck.theme";

function systemPrefersDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function apply(mode: ThemeMode): void {
  const dark = mode === "dark" || (mode === "system" && systemPrefersDark());
  document.documentElement.classList.toggle("dark", dark);
}

export function useTheme(): [ThemeMode, (mode: ThemeMode) => void] {
  const [mode, setMode] = useState<ThemeMode>(
    () => (localStorage.getItem(key) as ThemeMode | null) ?? "system",
  );

  useEffect(() => {
    apply(mode);
    localStorage.setItem(key, mode);

    // Only while following the system does the OS get a say. Subscribing
    // unconditionally would let a macOS sundown override an explicit choice.
    if (mode !== "system") {
      return;
    }
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => apply("system");
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [mode]);

  return [mode, setMode];
}
