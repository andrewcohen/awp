#!/bin/bash
# The daemon, as a zmx task.
#
# `exec`, so the session's process *is* the daemon rather than a shell holding
# one — otherwise the session's shell reaps it when the task ends. And
# `env -u ZMX_SESSION`, because the daemon spawns `zmx attach`: a child that
# inherits the marker resolves it and switches the *calling* client, which is
# whatever session the daemon happens to be running in. See AGENTS.md.
cd "$(dirname "$0")/../.." || exit 1
exec env -u ZMX_SESSION bun run daemon
