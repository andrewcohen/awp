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

# ── the renderer is not profilable without this ───────────────────────────
#
# Chrome DevTools attaches over a port the process has to be started with, and
# there is no way to open one afterwards. So a window that is behaving badly
# cannot be measured — it has to be restarted first, which is the moment the
# behaviour usually stops.
#
# Paid for once, here. `bun run dev logs app` still reads the same; what this
# adds is `http://127.0.0.1:9222/json` listing the renderer, so a trace or a
# CPU profile can be taken of the window somebody is actually complaining
# about rather than of a fresh one.
#
# Loopback only, and this is the DEV shell: `bun run amoeba` and every packaged
# build go nowhere near this file. Set AMOEBA_DEBUG_PORT= (empty) to drop it.
: "${AMOEBA_DEBUG_PORT:=9222}"
if [ -n "$AMOEBA_DEBUG_PORT" ]; then
  exec "$BUNDLE" . --remote-debugging-port="$AMOEBA_DEBUG_PORT" \
    --remote-allow-origins=http://127.0.0.1:"$AMOEBA_DEBUG_PORT"
fi
exec "$BUNDLE" .
