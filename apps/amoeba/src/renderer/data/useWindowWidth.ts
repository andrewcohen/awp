import { useSyncExternalStore } from "react";

// The window's width, as React state.
//
// The window and not an element, deliberately: the three columns span it
// exactly, so there is no container to observe that is not this. A
// ResizeObserver here would be a second source for the same number and a
// lifecycle to get wrong.
//
// Same shape as useColorScheme, and for the same reason — the value already
// exists outside React, and reading it during render is what keeps the first
// paint from being a frame behind.

const subscribe = (onChange: () => void): (() => void) => {
  window.addEventListener("resize", onChange);
  return () => window.removeEventListener("resize", onChange);
};

export const useWindowWidth = (): number =>
  useSyncExternalStore(subscribe, () => window.innerWidth);
