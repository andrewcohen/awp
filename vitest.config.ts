import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // archive/ is the Go implementation, kept as reference only.
    //
    // .tsbuild holds tsc output, and nothing imports it: every package's
    // exports point at src/*.ts and both Vite and Bun read TypeScript
    // directly, so tsc here is a typechecker and nothing else. Left in scope,
    // its emitted copy of each test file is collected alongside the source and
    // every test runs twice.
    exclude: ["**/node_modules/**", "archive/**", ".tsbuild/**"],
  },
});
