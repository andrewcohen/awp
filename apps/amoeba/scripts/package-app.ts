// The .app, assembled out of what the two builds produced.
//
// ── the staging directory is the whole idea ───────────────────────────────
//
// `electrobun build` read `electrobun.config.ts` and assembled a bundle. The
// Electron equivalent copies an application *directory*, and the obvious thing
// to hand it is `apps/amoeba` — which in this repository is a workspace member
// whose `node_modules` is a forest of symlinks into bun's store, containing
// Electron itself, three sibling packages and every build-time dependency.
// Packaged, that is hundreds of megabytes of things the app never loads.
//
// It never loads them because both builds are already bundles: `Bun.build`
// inlines everything but `electron`, and Vite emits a static renderer. So
// nothing outside `dist/` is needed at runtime, and the staged directory is
// exactly `dist/` plus a package.json naming the entry.
//
//   stage/package.json      name, version, main
//   stage/electron/*        main.js and the two preloads
//   stage/renderer/*        what Vite emitted, served over app://
//
// The layout inside `stage` matters: `main.js` finds the renderer at
// `../renderer`, which is true here and true in `dist/` — so a packaged app and
// a development one resolve it the same way, and there is no second path to
// keep right.

import { cp, mkdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { packager } from "@electron/packager";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(app, "dist");
const stage = join(app, "build", "stage");
const out = join(app, "build");

const manifest = await Bun.file(join(app, "package.json")).json();

await rm(stage, { recursive: true, force: true });
await mkdir(stage, { recursive: true });
await cp(dist, stage, { recursive: true });
await writeFile(
  join(stage, "package.json"),
  `${JSON.stringify(
    {
      name: "amoeba",
      productName: "amoeba",
      version: manifest.version,
      description: "agent work platform",
      main: "electron/main.js",
      // The staged tree is what runs, and `main.js` is ESM — the same reason
      // the preloads are `.cjs` rather than `.js`.
      type: "module",
    },
    undefined,
    2,
  )}\n`,
);

const made = await packager({
  dir: stage,
  out,
  name: "amoeba",
  appBundleId: "dev.awp.amoeba",
  appVersion: manifest.version,
  overwrite: true,
  // Nothing is signed. Signing is a decision about distribution and needs an
  // identity this repository does not have; an unsigned local build is what
  // `electrobun dev` produced too. Absence is how that is said — the option's
  // type is `true | options`, so there is no `false` to write.
  prune: false,
  quiet: true,
});

for (const path of made) {
  console.log(`[amoeba] packaged ${path}`);
}
