# awp — Agentic Workspace Pilot

> ⚠️ **Disclaimer**: This project is heavily vibe-coded. The maintainer doesn't necessarily know what's inside any given file at any given moment. Treat the source as a sketch, not a contract: read it before depending on it, and assume any behavior may change without warning. Bug reports and PRs welcome; "this code is weird" is fair feedback.

`awp` is a Go CLI and TUI for running multiple AI coding agents (Claude Code, pi.dev, …) in parallel across isolated [Jujutsu](https://github.com/martinvonz/jj) workspaces, each in its own [tmux](https://github.com/tmux/tmux) session. It gives you a single keyboard-driven dashboard ("the deck") to summon, switch between, and observe agents — including a live status indicator showing which agents are working, idle, or waiting on you.

## What problem it solves

You want to run 5+ agents at once on different branches without:

- Manually managing `jj` workspaces, branch checkouts, and tmux layout.
- Tab-switching to figure out which agent is blocked, which is generating, and which has finished.
- Re-typing the same `claude` / `pi` invocation in every pane.

`awp` automates the whole loop: create a workspace from a bookmark or PR, drop you into a tmux session running your default agent, wire up status reporting via hooks, and give you a one-screen overview of every running workspace.

## Installation

```sh
go install github.com/andrewcohen/awp/cmd/awp@latest
awp init hooks   # one-time: install Claude Code + pi.dev integrations globally
```

`awp init hooks` installs:

- `~/.claude/settings.json` — hooks that report state to awp on `SessionStart` (idle), `UserPromptSubmit` / `PreToolUse` (working), `Stop` (idle), and `Notification` (waiting).
- `~/.pi/agent/extensions/awp-status.ts` — a pi.dev extension that reports state on `session_start` / `before_agent_start` / `agent_end` / `tool_execution_start` / quit-time `session_shutdown`. If statuses aren't landing, set `AWP_DEBUG=1` in the pi pane to write diagnostics to `~/.awp/pi-extension.log`.

Both integrations are no-ops outside awp-managed sessions (they only run in tmux and `awp internal report-status` ignores sessions without awp workspace metadata), so they never affect your standalone Claude or pi usage. Both honor `$AWP_BIN` if you need the hook to invoke a non-PATH `awp`.

## The deck

```sh
awp deck
```

Recommended invocation as a tmux popup (in `~/.tmux.conf`):

```tmux
bind a display-popup -E -w 90% -h 90% awp deck \; run-shell "awp deck-cleanup"
```

> **Heads up — "exit code 127" from the popup or `deck-cleanup`?**
> tmux's popup/run-shell commands run under a non-interactive `/bin/sh` that does **not** read your `~/.zshrc` / `~/.config/fish/config.fish`. If `awp` lives in `~/go/bin` (the `go install` default) and your shell rc adds that to PATH, tmux won't see it and you'll get a bare exit 127. Two fixes:
>
> 1. **Inject PATH into the tmux server** (recommended — covers all popups):
>    ```tmux
>    set -g update-environment "PATH DISPLAY ..."   # if not already
>    set-environment -g PATH "$HOME/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
>    ```
> 2. **Use absolute paths** in the binding:
>    ```tmux
>    bind a display-popup -E -w 90% -h 90% "$HOME/go/bin/awp deck" \; run-shell "$HOME/go/bin/awp deck-cleanup"
>    ```

Press `?` inside the deck for the full key + status legend.

### Agent status (the colored dot at the start of each row)

| Color | State | Meaning |
|---|---|---|
| 🟢 Green | `working` | Agent is actively producing output or running a tool |
| 🟡 Yellow | `waiting` | Paused on a permission prompt — needs your input |
| ⚪ Grey | `notified` | Agent finished a turn (or exited) and you haven't summoned the workspace since |
| 🔴 Red | `exited` | Process gone, pane back at a shell (rendered when notified) |
| _(blank)_ | `idle` / `starting` | Quiet — no badge until the agent actually surfaces something |

The grey "notified" dot is a per-workspace unread badge: it lights up when the agent transitions into `waiting`, `idle`, or `exited`, and clears the next time you summon that workspace (any of `enter`, `a`, `e`, `c`, `v`, `s`, `i`, `x`).

### Key bindings

| Key | Action |
|---|---|
| `enter` | Summon (create or focus) the workspace's tmux session |
| `a` | Open agent window — re-launches the agent if its pane is at a shell |
| `e` | Open editor window (`$EDITOR`) |
| `c` / `C` | Review window: `tuicr -r @` / `tuicr -r main..@` |
| `v` | VCS window (`jjui`) |
| `s` | Shell window |
| `i` | CI window (`gh run watch`) |
| `r` | Pick a PR to review |
| `x` | User actions menu (configurable via `actions` in config) |
| `n` | New workspace (inline form: workspace name / bookmark / agent prompt) |
| `o` | Open: fuzzy-pick a project from configured roots (tmux-sessionizer style) |
| `f` | Find: easymotion-style project → workspace jump |
| `/` | Filter rows · `esc` clears |
| `P` | Cycle scope: current project → all projects → awaiting input (persisted across runs) |
| `L` | Switch to last tmux session |
| `R` | Relink session |
| `D` | Delete workspace |
| `,` | Edit global state file in `$EDITOR` |
| `J` | Jobs overlay (running async dispatches — cancel, retry, dismiss, open log, yank to clipboard) |
| `?` | Help overlay |
| `q` / `esc` | Quit |

## CLI reference (highlights)

| Command | Purpose |
|---|---|
| `awp deck` | Open the workspace dashboard |
| `awp w open [name]` | Create or attach to a workspace (interactive form when run alone) |
| `awp w list` | List workspaces in the current repo |
| `awp w info <name>` | Show details for a workspace |
| `awp w rename <old> <new>` | Rename |
| `awp w delete <name>` | Delete (use `--force` to skip prompts) |
| `awp w prune [--dry-run] [--force]` | Remove orphan workspace dirs under `~/.awp/workspaces` not tracked in state |
| `awp w bootstrap [name]` | Re-run bootstrap hooks for a workspace |
| `awp w bootstrap --all` | Re-run bootstrap hooks for every tracked workspace in the current repo (continues on failure) |
| `awp review [pr#]` | Pick or open a PR for review in a fresh workspace |
| `awp diff` | Charm-styled diff viewer |
| `awp doctor [--global] [--fix]` | Health checks; `--fix` repairs missing hooks/env |
| `awp init hooks` | Install/update global Claude + pi integrations (idempotent) |
| `awp config init` | Bootstrap `<repo>/.awp/config.json` (must run from repo root) |
| `awp config edit [--global]` | Open the project (or `--global`) config in `$EDITOR` |
| `awp internal report-status --state <…>` | Hidden — used by hooks to write status |
| `awp internal unread-summary` | Print a tmux-status-bar badge of workspaces needing attention (waiting + notified counts). Empty when nothing's unread. |
| `awp internal mark-read [--workspace <name>]` | Clear the unread badge for one workspace. Resolves from `$AWP_WORKSPACE` when no flag given. |

`awp doctor` checks environment tooling, the agent hook installs, and (when run inside a repo) per-repo configuration. `--global` skips repo-scoped checks and scans every live `[awp]*` tmux session across all projects. `--fix` reinstalls missing hooks and re-injects `AWP_WORKSPACE` / `AWP_REPO` into any session that's missing them.

## Configuration

awp reads JSON config from two locations and merges them (project wins):

- `~/.config/awp/config.json` — global
- `<repo>/.awp/config.json` — per-project

Example:

```json
{
  "agent": "claude",
  "actions": {
    "logs": { "command": "tail -f /tmp/app.log", "alias": "l" },
    "install": { "command": "pnpm install", "alias": "i", "background": true }
  },
  "hooks": {
    "bootstrap": ["pnpm install", "make migrate"]
  },
  "deck": {
    "project_roots": ["~/p", "~/go/src/github.com/andrewcohen"]
  }
}
```

> **Note**: awp will refuse to operate on `$HOME` as a repo (deck open, workspace open/create/delete/rename, project picker selection) so workspace dirs and bookmarks don't end up scattered across your home.

### `agent`

Command used to launch the workspace agent. Invoked as `<agent> <prompt>` (with the prompt shell-quoted) when summoning with a prompt, or just `<agent>` when re-attaching via the `a` key. Defaults to `pi`. Common values: `pi`, `claude`, `aider`. Anything that accepts a prompt as its first positional argument works.

### `actions`

Custom commands surfaced by the deck's `x` action menu. By default each action runs in a new tmux window in the workspace.

Set `"background": true` to run the action detached via the jobs subsystem instead. The deck dispatches it without opening a tmux window; output is captured to `~/.awp/jobs/<id>.log` and the run shows up in the right panel's **Recent activity** list for that workspace. Failures appear in the bottom status bar's `⚠` count and stay until dismissed in the `J` overlay. Best for installs, lints, builds, or anything you'd rather not babysit.

Set `"focus": false` to keep the action foregrounded (it gets a real tmux window, runs interactively, scrollback intact) but **don't** switch the tmux client to it on launch. Useful for spawning a long-running watcher you'll check on later without losing your place in the deck. Ignored when `background` is true.

### `hooks.bootstrap`

Shell commands run after a workspace's jj layout exists but before the agent starts. Used for things like `pnpm install` or `make seed`.

> Built-in bootstrap **symlinks** `<repo>/.awp/` into each workspace rather than copying it, so config edits propagate across all workspaces immediately. Editing `<workspace>/.awp/config.json` writes through to the source repo.

### `deck.project_roots`

List of directories the deck's `o` (open) screen scans for projects. Tilde-expanded. The walker descends up to 4 levels and stops at any directory containing `.git` or `.jj`. Selecting a project summons (or creates) a tmux session named `[awp]<basename>__default` at that path.

## Tmux status bar badge

Add this to `~/.tmux.conf` to surface waiting / notified workspaces from any session:

```tmux
set -g status-interval 5
set -g status-right '#(awp internal unread-summary) | %H:%M'
```

`awp internal unread-summary` prints `▲ N` (waiting, yellow) and/or `● N` (notified) — empty output when nothing is pending, so the divider/clock collapses cleanly.

## Async deck jobs

All "progress" actions in the deck — create workspace (`n`), review a PR (`r`),
CI watch (`i`), user actions (`x`), delete (`D`) — now dispatch a detached
subprocess (`awp run-job <id>`) instead of blocking the deck. The deck stays
fully interactive: navigate, dispatch more, `q` out — jobs keep running.

Each job lives at `~/.awp/jobs/<id>.json` (status record) and
`~/.awp/jobs/<id>.log` (full subprocess output). The deck's bottom status bar
inlines an active-set summary on the left:

```
▶ 2 ⚠ 1 ☠ 1                                  ready                       ? help
```

The selected workspace's right panel includes a **Recent activity** block
listing its most recent job runs (newest first, up to 5) — handy for seeing
what `install` or `lint` last did without opening the `J` overlay.

- `▶` Running / pending.
- `⚠` Failed — error details visible in the `J` overlay.
- `☠` Orphaned — subprocess died without flushing a final state (SIGKILL,
  OOM, crash). Detected via heartbeat staleness + `kill(pid, 0)` + a
  process-start-time check that also catches PID reuse. Orphans persist
  for 7 days; clean terminal records are GC'd after 24 hours on next deck
  startup (cleanup runs in a background `tea.Cmd`, never blocks startup).

Press `J` to open the jobs overlay:

| Key | Action in overlay |
|---|---|
| `↑` / `↓` (or `k` / `j`) | Move cursor |
| `g` / `G` | Jump to top / bottom |
| `c` | Cancel the selected running job (sends `SIGTERM`; subprocess flushes a `cancelled` record before exiting) |
| `r` | Retry a failed/cancelled/orphaned job (re-spawns from the original spec; useful after manually resolving a stale workspace and similar fixable conditions) |
| `x` | Dismiss a finished/failed/orphaned record (deletes the JSON + log file) |
| `o` | Open the sidecar log file in `$PAGER` |
| `y` | Yank current job details (id, status, error, steps, recent log) to the system clipboard via OSC 52 |
| `esc` / `q` / `J` | Close the overlay |

The `y` yank exists because tmux popups don't expose copy-mode, so dragging
to select text inside the deck doesn't work the way it does in a normal
tmux pane. If you want native mouse selection instead, hold your terminal's
"bypass tmux mouse" modifier while dragging — Option (⌥) in iTerm2 / Terminal.app
/ Ghostty, Shift in Alacritty — or turn off `set -g mouse` in tmux. On
terminals that honor OSC 52 (iTerm2, Ghostty, Kitty, WezTerm, modern xterm,
foot) the yank lands directly in your system clipboard; tmux must have
`set -g set-clipboard on` for the escape to pass through.

Completion is non-intrusive: created workspaces appear in the deck list via the
existing 2-second refresher, and you press `enter` on the new row to summon as
usual. No auto-quit, no auto-switch.

Inspecting a running job from a shell:

```sh
ls ~/.awp/jobs/                             # list records
jq . ~/.awp/jobs/20260502-or72.json         # status
tail -f ~/.awp/jobs/20260502-or72.log       # streaming subprocess output
```

`awp run-job <id>` is an internal subcommand spawned by the deck — you
shouldn't need to run it directly.

## Concurrent writes

Workspace state lives in a single `~/.awp/workspace-state.json` written from many places (every Claude/pi hook, the deck refresh tick, summon/delete/rename). Writes are guarded by an OS-level advisory lock (`flock`) on `~/.awp/workspace-state.json.lock` and committed via temp-file + atomic `rename`, so concurrent writers don't drop each other's changes or leave a torn file. The lock has a 2-second timeout — if a writer ever stalls, agent hooks fail loudly rather than blocking the agent's turn.

## How status reporting works

1. When awp creates or summons a tmux session, it sets `AWP_WORKSPACE`, `AWP_REPO`, and `AWP_REPO_ROOT` on the session env.
2. The globally-installed hooks (Claude) / extension (pi) run on every state transition. Each one calls `awp internal report-status --state <state>` from tmux; the CLI is a silent no-op when awp workspace metadata is missing.
3. The status writer mutates the workspace entry's `Status` field in `~/.awp/workspace-state.json`.
4. The deck watches the state file for changes and refreshes immediately when possible, while keeping a periodic poll as a fallback.
5. Crash fallback: if the agent pane has dropped back to a shell, the deck overrides the in-memory status to `exited` regardless of what's on disk.

If the deck's status looks stuck, run `awp doctor --fix` to repair env injection and reinstall hooks.

## Repository layout

- `cmd/awp/` — main entry point
- `internal/cli/` — command dispatch, deck wiring, init/hooks installer
- `internal/deckui/` — Bubble Tea TUI model/view
- `internal/workspace/` — workspace lifecycle (jj + state + hooks)
- `internal/tmux/` — tmux client
- `internal/jj/` — jj client
- `internal/agenthooks/` — Claude Code + pi.dev integration installers
- `internal/config/` — project + global JSON config
- `internal/state/` — workspace state JSON store
- `internal/doctor/` — `awp doctor`
- `internal/diff/`, `internal/review/`, `internal/github/` — diff and PR review flows
- `specs/` — feature specs (start from `specs/spec-template.md`; use `scripts/new-spec`)

See `AGENTS.md` for contributor and AI-agent guidance.

## License

See repository.
