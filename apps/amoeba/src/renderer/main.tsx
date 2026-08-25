import { StrictMode } from "react";
import "./stylex.css";
import "./global.css";
import { createRoot } from "react-dom/client";
import { App } from "./App";

// StrictMode is ON, deliberately, and this is worth revisiting rather than
// assuming.
//
// gdeck had to turn it off: ghostty-web's dispose() frees wasm state the
// module-level Ghostty instance keeps handing out, so StrictMode's double-mount
// built a second Terminal on freed memory and every pane in development ran
// corrupted — which made every rendering question unanswerable. The fix that
// came out of it was one Terminal for the window's life, never disposed, and
// that design should survive a double-mount. Leaving StrictMode on until the
// pane lands is how we find out whether it actually does.

const root = document.getElementById("root");
if (root === null) {
  throw new Error("no #root — index.html and main.tsx disagree");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
