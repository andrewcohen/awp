import type { ColorScheme } from "@awp-kit/pane";
import { useSyncExternalStore } from "react";

// The window follows the system's appearance, and changes with it.
//
// useSyncExternalStore rather than useState + useEffect, because the preference
// is state that already exists outside React. The effect version reads it a
// frame late, which is a visible flash of the wrong theme on launch, and it is
// also the version React Compiler and StrictMode are entitled to run twice.
// getSnapshot is called during render instead, so the first paint is right.

const query = "(prefers-color-scheme: dark)";

const subscribe = (onChange: () => void): (() => void) => {
  const media = window.matchMedia(query);
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
};

// Must return a primitive, not an object. useSyncExternalStore re-renders when
// the snapshot changes by Object.is, so a fresh object every call is an infinite
// loop — the string is what keeps this honest.
//
// Exported as well as subscribed to, for effects that need the scheme without
// depending on it: reading the preference is always current, where closing over
// a rendered value ties the effect to re-running when it changes.
export const currentColorScheme = (): ColorScheme =>
  window.matchMedia(query).matches ? "dark" : "light";

export const useColorScheme = (): ColorScheme =>
  useSyncExternalStore(subscribe, currentColorScheme);
