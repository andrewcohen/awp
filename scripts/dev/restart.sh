#!/bin/bash
# Restart one of the dev sessions, from a process that will outlive it.
#
# ── why this cannot run where you typed it ─────────────────────────────────
#
# Reported as "i tried restarting the daemon from within the acp and failed
# pretty hard", and the reason is a process tree rather than a command:
#
#   the daemon ── spawns ──▶ the ACP adapter ── is ──▶ the agent you are talking to
#
# So an agent in the chat that kills the daemon kills its own adapter, and the
# second half of its command — the half that starts the daemon again — never
# runs. What is left is a stopped session, a free port, and a window with
# nothing to talk to. The same is true of an agent in a *terminal* whose
# session the daemon created.
#
# The repair is to hand the whole restart to a process the daemon does not
# own. `zmx` has one: the session server. This script is meant to be run as a
# task in a session of its own —
#
#   zmx run awp-dev-ops -d bash scripts/dev/restart.sh daemon
#
# — and `bun run dev:restart <what>` is that line. The caller can then die
# immediately, which is exactly what it is about to do.
#
# ── and why ^C rather than `zmx kill` ──────────────────────────────────────
#
# `kill` takes the session with it, so the next `zmx run` has to make a new one
# and the scrollback of what just happened is gone. An interrupt ends the
# *task* and leaves the session, which is the thing a person reads when a
# restart did not work.
set -u

what=${1:-}
case "$what" in
  daemon | vite | app) ;;
  *)
    echo "usage: restart.sh <daemon|vite|app>" >&2
    exit 2
    ;;
esac

here=$(cd "$(dirname "$0")" && pwd)
session="awp-dev-$what"

# Nothing to interrupt if the session is not there — `zmx run` will make it.
if zmx ls | grep -q "name=$session[[:space:]]"; then
  # ETX. The task ends; the session stays, and so does its scrollback.
  env -u ZMX_SESSION zmx send "$session" $'\003' || true
  # Wait for the task rather than sleeping a guessed number of seconds: a
  # daemon still holding :5274 when the next one starts is the one failure this
  # has to avoid, and it presents as "address already in use" in a log nobody
  # is watching.
  env -u ZMX_SESSION zmx wait "$session" || true
fi

env -u ZMX_SESSION zmx run "$session" -d bash "$here/$what.sh"
echo "restarted $session"
