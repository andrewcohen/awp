// `assets/icon.svg` → `assets/icon.icns`, plus the PNG the dock wants in dev.
//
// The SVG is the source and the two outputs are derived, which is the whole
// reason this is a script rather than two binaries checked in beside it: an
// icon edited in a vector editor and re-exported by hand is an icon whose
// sizes drift apart, and the one that drifts is the size nobody looks at.
//
// ── the two consumers, and why one file cannot serve both ─────────────────
//
//   the .app     `packager({ icon })` wants an `.icns` — ten renderings of
//                the same art, which `iconutil` assembles from an `.iconset`
//                directory named exactly as Apple specifies
//   dev          there is no bundle, so the dock shows Electron's own atom.
//                `app.dock.setIcon` takes a nativeImage, and nativeImage
//                reads PNG and JPEG — not `.icns`
//
// Rendered with `rsvg-convert` at each size rather than scaled from one big
// PNG: a downscaled 1024 loses the hairline on the squircle's edge, and 16px
// is where that shows.

import { mkdir, rm } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const assets = join(app, "assets");
const svg = join(assets, "icon.svg");
const iconset = join(assets, "icon.iconset");

/** The ten renderings `iconutil` expects, by the names it expects them under. */
const SIZES: ReadonlyArray<readonly [number, string]> = [
  [16, "icon_16x16.png"],
  [32, "icon_16x16@2x.png"],
  [32, "icon_32x32.png"],
  [64, "icon_32x32@2x.png"],
  [128, "icon_128x128.png"],
  [256, "icon_128x128@2x.png"],
  [256, "icon_256x256.png"],
  [512, "icon_256x256@2x.png"],
  [512, "icon_512x512.png"],
  [1024, "icon_512x512@2x.png"],
];

const run = async (cmd: ReadonlyArray<string>): Promise<void> => {
  const done = Bun.spawn([...cmd], { stdout: "pipe", stderr: "pipe" });
  const [code, stderr] = await Promise.all([done.exited, new Response(done.stderr).text()]);
  if (code !== 0) {
    // The tool's own sentence, not one composed here — same rule as run.ts.
    throw new Error(`${cmd[0]} exited ${String(code)}: ${stderr.trim()}`);
  }
};

if (!(await Bun.file(svg).exists())) {
  throw new Error(`no icon source at ${svg}`);
}

await rm(iconset, { recursive: true, force: true });
await mkdir(iconset, { recursive: true });

for (const [size, name] of SIZES) {
  await run([
    "rsvg-convert",
    "-w",
    String(size),
    "-h",
    String(size),
    svg,
    "-o",
    join(iconset, name),
  ]);
}

await run(["iconutil", "-c", "icns", iconset, "-o", join(assets, "icon.icns")]);

// What the dock is handed in development. 512 rather than 1024: it is scaled
// down to 128 at most, and the file is checked in.
await run(["rsvg-convert", "-w", "512", "-h", "512", svg, "-o", join(assets, "icon.png")]);

// The iconset is scaffolding for `iconutil` and nothing reads it afterwards.
await rm(iconset, { recursive: true, force: true });

console.log(`[amoeba] icon: ${join(assets, "icon.icns")} and icon.png`);
