import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath, URL } from "node:url";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  // Mirrors the "paths" entry in tsconfig.json — tsc type-checks through that
  // one and Vite resolves through this one, so both have to agree or the build
  // and the editor disagree about whether an import exists.
  resolve: {
    alias: {
      "@bindings": fileURLToPath(
        new URL("./bindings/github.com/andrewcohen/awp/gdeck", import.meta.url),
      ),
    },
  },
  plugins: [react(), wails("./bindings")],
});
