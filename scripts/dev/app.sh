#!/bin/bash
# The Electron shell, as a zmx task.
#
# The three bundles come out of Bun.build rather than Vite, so they have to be
# rebuilt before the window loads — see AGENTS.md on the shell's four seams.
# `electron` is not on a plain shell's PATH; it is a workspace binary.
cd "$(dirname "$0")/../../apps/amoeba" || exit 1
bun run build:electron || exit 1
export AMOEBA_DEV_SERVER=http://127.0.0.1:5273

# ── run a bundle with this app's own name on it ───────────────────────────
#
# `./node_modules/.bin/electron .` runs ELECTRON's bundle, so macOS reads
# `CFBundleName: Electron` off it and puts that in the menu bar and under the
# dock icon. There is no runtime call that changes either — see the header of
# dev-bundle.ts, which clones a bundle that says the right thing.
#
# The app path is still `.`, so `process.defaultApp` and everything downstream
# of it are exactly as they were.
BUNDLE="$(bun run --silent scripts/dev-bundle.ts | tail -1)" || exit 1
exec "$BUNDLE" .
