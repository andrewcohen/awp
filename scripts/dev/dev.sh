#!/bin/bash
# The dev processes, as zmx sessions.
#
#   bun run dev up          the daemon, vite and the app window
#   bun run dev up daemon   one of them
#   bun run dev down [what]
#   bun run dev restart [what]
#   bun run dev status
#   bun run dev logs daemon [lines]
#
# ── why these are sessions rather than background jobs ─────────────────────
#
# Three of them have to outlive whatever started them, and one has to be
# readable after it dies. A `&` gives neither: `bun run amoeba` is
# `vite … & bun run dev`, which returns as soon as it has forked — so under
# `zmx run` the *task* completes, the session's shell reaps what it left
# behind, Vite dies and Electron is left with nothing to load. That is a black
# window, and it happened twice before the log was read.
#
# So each is `exec`ed as its own task in its own session, and this script is
# the one place that knows which. `up` is idempotent per session: a session
# already running its task is left alone rather than restarted, because
# `up` is what somebody types when they are not sure.
#
# ── and why `restart` goes through a session of its own ────────────────────
#
# See restart.sh: the daemon is the parent of every ACP adapter, so an agent
# in a chat that restarts it kills the process issuing the command halfway
# through. `restart` therefore hands the work to `awp-dev-ops`, which the zmx
# server owns and the daemon does not.
set -u

here=$(cd "$(dirname "$0")" && pwd)
what=${2:-all}

case "$what" in
  daemon | vite | app) parts=("$what") ;;
  all) parts=(daemon vite app) ;;
  *)
    echo "unknown: $what — expected daemon, vite, app or all" >&2
    exit 2
    ;;
esac

# ── whether it is running, asked of the process table ─────────────────────
#
# **Not from `ended=`.** That field is about the session's *last task*, and it
# stays there after the next one starts: with a daemon up and holding :5274,
# its row still read `ended=… exit_code=130` from the interrupt before it, so
# a check on that field reported `stopped` for a process that was answering
# requests. The same distinction AGENTS.md records about `SessionInfo.ended`.
#
# What answers it is a child of the session's shell: a task is a process under
# it, and a session sitting at a prompt has none. That is the check
# `probe:session-start` uses for the same reason — every field in the listing
# is about the session, and none of them is about what is running in it.
row() { zmx ls | grep "name=awp-dev-$1[[:space:]]" || true; }
pidOf() { row "$1" | sed 's/.*[[:space:]]pid=\([0-9]*\).*/\1/'; }
live() {
  local pid
  pid=$(pidOf "$1")
  [ -n "$pid" ] && [ -n "$(pgrep -P "$pid" 2>/dev/null)" ]
}

up() {
  if live "$1"; then
    printf "  %-8s already running\n" "$1"
    return
  fi
  env -u ZMX_SESSION zmx run "awp-dev-$1" -d bash "$here/$1.sh" > /dev/null
  printf "  %-8s started\n" "$1"
}

down() {
  if [ -z "$(row "$1")" ]; then
    printf "  %-8s not there\n" "$1"
    return
  fi
  # An interrupt, not a kill: the task ends and the session keeps its
  # scrollback, which is what somebody reads when a process died on its own.
  env -u ZMX_SESSION zmx send "awp-dev-$1" $'\003' > /dev/null 2>&1 || true
  env -u ZMX_SESSION zmx wait "awp-dev-$1" > /dev/null 2>&1 || true
  printf "  %-8s stopped\n" "$1"
}

case ${1:-status} in
  up)
    for part in "${parts[@]}"; do up "$part"; done
    ;;
  down)
    for part in "${parts[@]}"; do down "$part"; done
    ;;
  restart)
    # Handed to the ops session, which nothing here is the parent of.
    for part in "${parts[@]}"; do
      env -u ZMX_SESSION zmx run awp-dev-ops -d bash "$here/restart.sh" "$part" > /dev/null
      printf "  %-8s restarting, through awp-dev-ops\n" "$part"
    done
    ;;
  status)
    for part in daemon vite app; do
      state=$(if live "$part"; then echo "running"; elif [ -n "$(row "$part")" ]; then echo "idle"; else echo "-"; fi)
      printf "  %-8s %-8s %s\n" "$part" "$state" "$(if [ -n "$(pidOf "$part")" ]; then echo "session $(pidOf "$part")"; fi)"
    done
    # The ports, which are the honest answer: a session can be running a
    # process that never bound anything.
    for port in 5273 5274; do
      printf "  :%s     %s\n" "$port" "$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2 {print $1 " " $2}' || echo "free")"
    done
    ;;
  logs)
    zmx history "awp-dev-$what" | tail -"${3:-40}"
    ;;
  *)
    echo "usage: dev.sh <up|down|restart|status|logs> [daemon|vite|app|all]" >&2
    exit 2
    ;;
esac
