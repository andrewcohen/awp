import { StrictMode } from "react";
import "./stylex.css";
import "./global.css";
import { RouterProvider } from "@tanstack/react-router";
import { createRoot } from "react-dom/client";
import { rememberedPlace } from "./remembered";
import { router } from "./routes";

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

// ── the one read of the remembered address ────────────────────────────────
//
// Before the router is handed the page, and only when the hash has nothing to
// say. A reload keeps the hash, so this does nothing in the case it looks like
// it is for; what it covers is the application being quit and launched again,
// where the history starts empty and the window would otherwise open on the
// fixture every morning.
//
// Written into `location.hash` rather than navigated to, so no entry is pushed:
// arriving here is where the window *starts*, and pressing back from it should
// leave the application rather than return to a screen nobody visited.
const place = rememberedPlace();
if (place !== undefined && (globalThis.location.hash === "" || globalThis.location.hash === "#/")) {
  globalThis.location.hash = place;
}

const root = document.getElementById("root");
if (root === null) {
  throw new Error("no #root — index.html and main.tsx disagree");
}

createRoot(root).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
