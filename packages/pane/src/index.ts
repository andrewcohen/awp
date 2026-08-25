// A terminal pane: libghostty compiled to wasm, rendered to canvas.
//
// One Terminal for the life of the window, reused by every pane, never
// disposed. ghostty-web's dispose() frees wasm state the module-level Ghostty
// instance keeps handing out, so building one per view is what caused four
// distinct bugs with a single cause — see the spec for the list. The canvas
// lives in a host element this package owns and re-parents on mount, so React
// can mount and unmount views without the terminal noticing.

export const paneVersion = 0;
