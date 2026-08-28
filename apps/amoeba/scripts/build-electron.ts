// The main process and its two preloads, bundled.
//
// ── why a script and not another vite config ──────────────────────────────
//
// Vite owns the renderer and is good at it: StyleX and the React compiler are
// both Babel plugins riding one pass, and none of that applies here. What this
// needs is three tiny bundles in two different module formats, which is a
// rollup config's worth of ceremony for something `Bun.build` states in a
// table.
//
// ── the formats are not a preference ──────────────────────────────────────
//
//   main            esm   the app's package is `"type": "module"`, and Electron
//                         has run ESM main since 28
//   both preloads   cjs   an ESM preload requires `sandbox: false`, and the
//                         guest preload runs inside an arbitrary website. The
//                         sandbox is the last thing to trade away for a module
//                         syntax, so the extension is `.cjs` and the format is
//                         what makes it true
//
// `electron` is external in all three: it is not a package on disk to bundle,
// it is a binding the runtime provides.

import { rename, rm } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(app, "dist", "electron");

await rm(out, { recursive: true, force: true });

const build = async (entry: string, name: string, format: "esm" | "cjs"): Promise<void> => {
  const built = await Bun.build({
    entrypoints: [join(app, entry)],
    outdir: out,
    target: "node",
    format,
    external: ["electron"],
    minify: false,
    // Readable stacks. This is a hundred lines of glue, not a bundle worth
    // shrinking, and a stack trace out of the main process is the only report
    // anything here produces.
    sourcemap: "inline",
    naming: `${name}.[ext]`,
  });
  if (!built.success) {
    for (const log of built.logs) {
      console.error(log);
    }
    throw new Error(`could not build ${entry}`);
  }
  if (format === "cjs") {
    // Bun writes `.js` whatever the format, and a `.js` file inside a package
    // declared `"type": "module"` is parsed as one — so a CommonJS preload
    // under that name fails at the first `require`, in a process with nowhere
    // to print it.
    await rename(join(out, `${name}.js`), join(out, `${name}.cjs`));
  }
};

await build("src/electron/main.ts", "main", "esm");
// The shell's own probe. Built with the app because it has to run against the
// same preloads and the same scheme — a probe compiled differently is a probe
// of something else.
await build("src/electron/probe.ts", "probe", "esm");
await build("src/electron/preload/host.ts", "preload-host", "cjs");
await build("src/electron/preload/guest.ts", "preload-guest", "cjs");

console.log(`[amoeba] built the shell into ${out}`);
