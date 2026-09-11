// A dev bundle with this app's own name on it.
//
// ── why the menu bar said Electron while the dock icon was right ──────────
//
// Those are two different mechanisms and only one of them has a runtime API:
//
//   the dock icon   app.dock.setIcon()     a call. It worked immediately
//   the NAME        CFBundleName, read by  no API. `app.setName` does not
//                   AppKit off the RUNNING reach it, and the first label in
//                   bundle's Info.plist    the menu template is ignored on
//                                          macOS — AppKit uses the bundle
//
// `bun run dev` runs `node_modules/.bin/electron .`, so the running bundle is
// Electron's own — `CFBundleName: Electron`. The packaged app has always been
// right (`Amoeba.app`, `CFBundleName: Amoeba`, our icns), because `packager`
// writes a bundle of its own; development had nothing doing that job.
//
// So this makes one: a clone of Electron.app with three plist keys and the
// icon replaced, which is the same small set `packager` rewrites.
//
// **A clone.** `cp -c` is an APFS clonefile, so 298MB of Electron is copied by
// reference rather than byte for byte — measured at 0.2s, against several
// seconds for a real copy. (`du` still reports the full size: it counts a
// file's allocated blocks whether or not they are shared.) Checked before this
// was written: the volume is APFS.
//
// **node_modules is deliberately NOT edited.** Patching Electron's own bundle
// in place is the shorter answer and is undone by the next `bun install`, with
// nothing to say it happened. The tree is also not hardlinked into bun's
// shared store *today* — st_nlink is 1, which was measured — and a future bun
// that did hardlink would make an in-place edit reach every project on the
// machine using this Electron.
//
// **The executable keeps its name.** `app.isPackaged` is `basename(execPath)
// !== "electron"`, so renaming the binary would silently flip it — and the one
// thing in this repo reading it is the dock icon, which would then stop being
// set. AppKit reads the *plist* for the title, so the rename buys nothing.

import { cp, mkdir, rm, stat } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = await Bun.file(join(app, "package.json")).json();
const product: string = manifest.productName;

const source = join(app, "node_modules", "electron", "dist", "Electron.app");
const out = join(app, "build", "dev", `${product}.app`);
const plist = join(out, "Contents", "Info.plist");
const icon = join(app, "assets", "icon.icns");

const run = async (cmd: ReadonlyArray<string>): Promise<void> => {
  const done = Bun.spawn([...cmd], { stdout: "pipe", stderr: "pipe" });
  const [code, stderr] = await Promise.all([done.exited, new Response(done.stderr).text()]);
  if (code !== 0) {
    throw new Error(`${cmd[0]} exited ${String(code)}: ${stderr.trim()}`);
  }
};

const named = async (): Promise<boolean> => {
  const done = Bun.spawn(["plutil", "-extract", "CFBundleName", "raw", plist], {
    stdout: "pipe",
    stderr: "pipe",
  });
  const [code, stdout] = await Promise.all([done.exited, new Response(done.stdout).text()]);
  return code === 0 && stdout.trim() === product;
};

// Rebuilt when it is absent, when it is older than the Electron it clones — a
// version bump — or when the icon has been rebuilt since. Anything else is a
// bundle already carrying exactly what this script would write.
const stale = async (): Promise<boolean> => {
  const [bundle, electron, art] = await Promise.all([
    stat(plist).catch(() => undefined),
    stat(source),
    stat(icon).catch(() => undefined),
  ]);
  if (bundle === undefined) return true;
  if (electron.mtimeMs > bundle.mtimeMs) return true;
  if (art !== undefined && art.mtimeMs > bundle.mtimeMs) return true;
  return !(await named());
};

if (await stale()) {
  await rm(join(app, "build", "dev"), { recursive: true, force: true });
  // `cp` writes the bundle itself; the directory above it has to be there, and
  // the rm above has just taken it away.
  await mkdir(join(app, "build", "dev"), { recursive: true });
  // `cp -c` clones on APFS. Node's own `cp` copies bytes, which is 298MB and
  // several seconds every time this is rebuilt.
  await run(["cp", "-Rc", source, out]);

  for (const [key, value] of [
    ["CFBundleName", product],
    ["CFBundleDisplayName", product],
    // Its own identity, or LaunchServices files this under Electron's — which
    // is the other half of what the dock reads.
    ["CFBundleIdentifier", "dev.awp.amoeba"],
  ] as const) {
    await run(["plutil", "-replace", key, "-string", value, plist]);
  }

  if (await Bun.file(icon).exists()) {
    // Written under the name the plist already points at, so CFBundleIconFile
    // needs no edit and there is one less key that can disagree.
    await cp(icon, join(out, "Contents", "Resources", "electron.icns"));
  }

  console.log(`[amoeba] dev bundle: ${out}`);
}

// What app.sh execs. Printed rather than returned, because the caller is a
// shell script.
console.log(join(out, "Contents", "MacOS", "Electron"));
