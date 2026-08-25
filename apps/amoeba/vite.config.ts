import babel from "@rolldown/plugin-babel";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The renderer's build. Electrobun copies the output in; it does not compile
// it — see electrobun.config.ts.
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
    }),
  ],
  server: {
    host: "127.0.0.1",
    // Fixed, and strict: the main process is told this port by env, and a Vite
    // that quietly moved to the next free one would leave the window pointed at
    // nothing, with no error to read.
    port: 5273,
    strictPort: true,
  },
  build: {
    outDir: "dist/renderer",
    emptyOutDir: true,
  },
});
