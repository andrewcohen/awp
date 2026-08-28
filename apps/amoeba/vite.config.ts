import babel from "@rolldown/plugin-babel";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { styleXPlugin } from "./stylex.babel.mjs";

// The renderer's build. The Electron shell serves the output over `app://`; it
// does not compile it — see src/electron/protocol.ts.
//
// ── why the separate Babel plugin ──────────────────────────────────────────
// `@vitejs/plugin-react` v6 transforms with oxc and **removed the inline
// `babel` option**. Passing one is not an error — it is silently ignored, which
// is exactly how this was found: a build with the compiler configured produced
// a bundle byte-identical to one without it, same content hash and all.
//
// So React Compiler comes through `@rolldown/plugin-babel`, which is what
// react.dev prescribes for Vite 6+. That plugin is a real Babel pass, which
// also settles a question the spec had answered wrongly for v6: StyleX is a
// Babel plugin too, and it goes in here beside the compiler preset rather than
// needing an oxc port or a plugin of its own.
//
// react-compiler is wired now rather than deferred because the first component
// compiled should also be the proof the pass works, and because
// auto-memoization changes what code is correct — a component relying on a
// render-to-render identity it should not have relied on breaks quietly if the
// compiler arrives later.

export default defineConfig({
  root: import.meta.dirname,
  base: "./",
  plugins: [
    react(),
    babel({
      presets: [reactCompilerPreset()],
      // StyleX's options live in stylex.babel.mjs, because the PostCSS pass
      // needs the identical set — see the note there.
      plugins: [styleXPlugin],
    }),
  ],
  server: {
    host: "127.0.0.1",
    watch: {
      // The shell's bundles and any packaged .app land under dist/ and build/.
      // Vite watches those by default and answers with a full page reload —
      // which is the one thing HMR exists to avoid, and which would wipe a
      // pane's scrollback every time the shell is rebuilt.
      ignored: ["**/build/**", "**/artifacts/**", "**/dist/**"],
    },
    // Fixed, and strict: the main process is told this port by env, and a Vite
    // that quietly moved to the next free one would leave the window pointed at
    // nothing, with no error to read.
    port: 5273,
    strictPort: true,
  },
  optimizeDeps: {
    // ── the worker entry the optimizer cannot find on its own ──────────────
    //
    // A worker is its own module graph. Vite's initial scan crawls the page's
    // imports, and `highlight.worker.ts` is not one of them — it is reached
    // through `new Worker(new URL(…))`, and only when the diff panel first
    // tokenizes something. So the dependency was discovered *at runtime*, which
    // is the one discovery that cannot be handled quietly:
    //
    //   [optimizer] bundling dependencies...
    //   dependency optimized: @pierre/diffs/worker/worker.js
    //   optimized dependencies changed. reloading      ← a full page reload
    //
    // A full reload is the thing this config already goes out of its way to
    // avoid — see `watch.ignored` — because it wipes a pane's scrollback. It
    // arrived a minute or two into a session, on the first patch anybody opened,
    // and read as the window blacking out.
    //
    // Naming it here puts it in the initial scan instead, where it costs a few
    // hundred milliseconds of a cold start and nothing at all afterwards.
    include: ["@pierre/diffs/worker/worker.js"],
  },
  build: {
    outDir: "dist/renderer",
    emptyOutDir: true,
  },
});
