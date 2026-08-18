import ReactDOM from "react-dom/client";
import App from "./App";

// No StrictMode, and this is not laziness about double-render bugs.
//
// StrictMode mounts every component twice in development, so a pane is built,
// disposed and built again. ghostty-web does not survive that: the second
// Terminal's first write dies with "Out of bounds memory access" inside
// ghostty_terminal_write, because dispose() frees wasm state the module-level
// Ghostty instance is still handing out. It is the same fault behind a pane that
// renders overlapped garbage after switching fonts, which also disposes and
// rebuilds a Terminal.
//
// The bug is real and worth fixing upstream, but it is a lifecycle bug in the
// library, not something the app can hold correctly — and while StrictMode is on
// it fires on every mount, which makes every other rendering question
// unanswerable. So it is off, and the remaining reason to want it is one this
// codebase does not have: React state so tangled that double-invocation is how
// you find the bugs in it.
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(<App />);
