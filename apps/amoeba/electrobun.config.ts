import type { ElectrobunConfig } from "electrobun";

// Electrobun bundles views with Bun.build. This app does not use that: the
// renderer is built by Vite and copied in, because StyleX and react-compiler
// are both Babel plugins and ride @vitejs/plugin-react's pass. Bun's bundler
// hosting a Babel-based CSS compiler was the one stack-level risk in the
// original sketch, and Vite removes it.
//
// So Electrobun's job here is the window, the menus, and the native edges.
// gdeck pointed Wails at a Vite dist for the same reason.

const config: ElectrobunConfig = {
  app: {
    name: "amoeba",
    identifier: "dev.awp.amoeba",
    version: "0.0.0",
    description: "agent work platform",
  },
  build: {
    bun: {
      entrypoint: "src/bun/index.ts",
    },
    // No `views` entry — Vite produces the renderer, and it is copied in
    // whole. Naming a view here would make Bun.build compile it a second time.
    copy: {
      "dist/renderer": "views/renderer",
    },
  },
};

export default config;
