#!/bin/bash
# The Electron shell, as a zmx task.
#
# The three bundles come out of Bun.build rather than Vite, so they have to be
# rebuilt before the window loads — see AGENTS.md on the shell's four seams.
# `electron` is not on a plain shell's PATH; it is a workspace binary.
cd "$(dirname "$0")/../../apps/amoeba" || exit 1
bun run build:electron || exit 1
export AMOEBA_DEV_SERVER=http://127.0.0.1:5273
exec ./node_modules/.bin/electron .
