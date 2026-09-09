#!/bin/bash
# The renderer's dev server, as a zmx task.
#
# `--strictPort`, always: a Vite that quietly moved to the next free port
# leaves the window loading whatever is on 5273, which may be a Vite nobody is
# watching. `exec` for the same reason as the daemon's.
cd "$(dirname "$0")/../../apps/amoeba" || exit 1
exec bunx vite --port 5273 --strictPort --clearScreen false
