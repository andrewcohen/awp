import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: 4318,
    strictPort: true,
    // The page and the agent live in different processes, so everything the UI
    // asks for is proxied to the Bun server rather than fetched cross-origin.
    // Keeping the frontend same-origin with its API means no CORS, and means
    // the built bundle — which the Bun server serves itself — behaves exactly
    // like the dev page.
    proxy: Object.fromEntries(
      ["/sessions", "/events", "/say", "/permit", "/mode", "/config", "/commands", "/upload"].map((path) => [
        path,
        {
          target: "http://localhost:4317",
          changeOrigin: true,
          // An event stream must not be buffered by the proxy or the UI sees a
          // turn arrive all at once at the end of it.
          ws: false,
        },
      ]),
    ),
  },
  resolve: {
    // shadcn's convention, mirrored in tsconfig.json — tsc type-checks through
    // that one and Vite resolves through this one, so both have to agree or the
    // build and the editor disagree about whether an import exists.
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    // Served by the Bun server in production, from a path it knows.
    outDir: "dist",
    emptyOutDir: true,
  },
  plugins: [react(), tailwindcss()],
});
