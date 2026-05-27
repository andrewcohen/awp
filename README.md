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

- `~/.claude/settings.json` — hooks that report state to awp on `SessionStart` (idle), `UserPromptSubmit` / `PreToolUse` / `PostToolUse` (working), `Stop` (idle), and `Notification` (waiting). The `UserPromptSubmit` hook also pipes the prompt JSON into `awp internal report-status --prompt-stdin` so the deck can show the active prompt under each workspace. The `PreToolUse` hook passes `--waiting-when-tool AskUserQuestion` so the row flips to `waiting` while the agent is paused on an `AskUserQuestion` call (which never fires `Notification`); `PostToolUse` flips it back to `working` once the answer is in.
- `~/.pi/agent/extensions/awp-status.ts` — a pi.dev extension that reports state on `session_start` / `before_agent_start` / `agent_end` / `tool_execution_start` / quit-time `session_shutdown`. `before_agent_start` forwards the user's prompt text so the deck stays in sync with Claude. If statuses aren't landing, set `AWP_DEBUG=1` in the pi pane to write diagnostics to `~/.awp/pi-extension.log`.

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

### Mini deck (quick-jump)

```sh
awp mini-deck
```

A short, filtered version of the deck that surfaces only workspaces that want your attention *right now*. Use it when you want to alt-tab between live agents without scrolling the full deck.

- `j` / `k` move the cursor
- `g` / `G` jump to top / bottom
- `f` easymotion-style hint jump — type the single (or two-char) hint that appears next to a row to land the cursor on it
- `enter` summons (creates or focuses) the workspace's tmux session and clears its unread badge
- `q` / `esc` quit without jumping

A row qualifies for the mini-deck when **all of** (the same rules the full deck's `P`-cycled "attention" scope uses, so the two stay in sync):

1. Its status is one of:
   - `working` — agent is generating output or running a tool. Always surfaced.
   - `waiting` **with the unread flag set** — Claude fired its `Notification` hook (typically a permission prompt). The unread flag is only true if you weren't already attached to the session when the hook fired, so requiring it skips prompts you already saw and dealt with in-session. A `waiting` row without unread is just stale noise because Claude has no "no longer waiting" hook.
   - `idle` **with the unread flag set** — agent finished a turn since you last visited.

   `exited` workspaces never appear; nothing's listening on the other end. `idle` without unread is a quiet workspace and doesn't appear either.
2. Its tmux session is actually alive and the `:agent` pane is still running an agent, not a bare shell. This catches the common case where the agent process died without firing an exit hook (Claude has no exit hook), so a stale `working` from days ago doesn't keep cluttering the list.

The `default` workspace per project is no longer filtered out by name — if an agent really is running in it (or it has an unread turn waiting on you), it surfaces just like any other row.

Suggested tmux binding under capital `A` (lowercase `a` already opens the full deck):

```tmux
bind A display-popup -E -w 50% -h 60% awp mini-deck
```

The same PATH caveats as `awp deck` apply (see above) — use absolute paths or `set-environment -g PATH ...` if the popup exits 127.

### Agent status (the colored dot at the start of each row)

| Color | State | Meaning |
|---|---|---|
| 🟢 Green | `working` | Agent is actively producing output or running a tool |
| 🟡 Yellow | `waiting` | Paused on a permission prompt — needs your input |
| ⚪ Grey | `notified` | Agent finished a turn (or exited) and you haven't summoned the workspace since |
| 🔴 Red | `exited` | Process gone, pane back at a shell (rendered when notified) |
| _(blank)_ | `idle` / `starting` | Quiet — no badge until the agent actually surfaces something |

The grey "notified" dot is a per-workspace unread badge: it lights up when the agent transitions into `waiting`, `idle`, or `exited`, and clears the next time you summon that workspace (any of `enter`, `a`, `e`, `c`, `v`, `s`, `i`, `x`).

### PR status (the Octicon glyph after each workspace name)

Each workspace is matched to a PR by its jj bookmark (PR `headRefName`). If a match is found, a single glyph rendered from the Nerd Font Octicon set sits to the right of the workspace name. Workspaces with no bookmark on file, or no matching PR, show no glyph.

| Glyph | Meaning |
|---|---|
|  | PR open — no review yet |
|  | PR draft |
|  | PR approved — at least one approving review |
|  | PR in merge queue — GitHub has queued the PR to merge |
|  | CI pending — checks in flight |
|  | CI failed — at least one check failing |
|  | PR merged — safe to delete this workspace |
|  | PR closed without merging |

Priority (highest wins): merged → closed → CI failed → CI pending → in merge queue → approved → draft → open. So a merged PR always shows the merge icon (even if its last CI was failing); an open PR with failing CI shows the alert icon rather than the open-PR icon; once a PR enters GitHub's merge queue (and CI is still green) it reads as queued rather than approved.

When an open PR is out of date with its base branch, a second glyph renders to the right of the primary PR glyph:

| Glyph | Meaning |
|---|---|
|  | Behind base — the base branch has moved past this PR. Only signaled when the repo's branch protection requires up-to-date branches before merging; otherwise GitHub reports the PR as clean even when behind. |
|  | Merge conflicts — the PR can't merge cleanly until the conflicts are resolved. |

The status is fetched once when the deck opens, with a single `gh pr list --state all` call per distinct repo that has at least one non-default workspace. The fetch is throttled so the same repo is never re-queried within a minute. The throttle is bypassed for actions that materially change the PR↔workspace mapping: linking a bookmark to an existing workspace, creating a new workspace from a bookmark, and opening a PR review — those refresh the affected repo immediately.

The fan-out runs as a **detached job** in the same jobs subsystem that powers workspace create / delete / review. It's spawned via `Setsid`, so closing the deck (or its tmux popup) mid-fetch no longer drops in-flight work. Per-repo PRs are persisted to `~/.awp/pr-status-cache.json` atomically as each repo finishes; the job record itself lives at `~/.awp/jobs/<id>.json` and shows up in the deck's `J` overlay (you can dismiss / open the log there). The next deck open reuses an existing active pr-status job instead of spawning a duplicate.

**Requires a patched (Nerd Font) terminal font.** Anyone running awp without a Nerd Font will see empty rectangles where the PR glyphs would render.

### Activity bar (bottom of the deck)

The bottom status line shows in-flight background work as a single segment between the row body and the right-aligned status / `? help`. It surfaces:

- `⠼ pr-status N/M` while gh PR-status is fanning out across repos; ticks down per repo as each one returns.
- `⠼ enrich` during the cold-start refresh, post-rename / post-delete / post-state-edit refreshes, and post-bookmark-link refreshes. The 5-second periodic refresh runs silently.
- `⠼ workspace:rename:<name>` / `workspace:link:<name>` for the deck-local lifecycle actions that don't go through the async-jobs subsystem.
- Each async deck job (workspace `create`, `delete`, `review`, custom `background: true` actions). Failed (`⚠`) and orphaned (`☠`) jobs stay visible in the bar until dismissed via the `J` overlay.

Finished entries flash `✓ <label>` for 500ms before disappearing. When no background work is running, the bar is empty.

### Dev URL capture

When a workspace's tmux session has a process listening on a TCP port (e.g. `pnpm dev` launching Vite on 5173), the deck auto-discovers it and shows a `Dev: http://localhost:<port>` line in the right details panel. Press `u` to open the URL in your default browser.

Detection works by enumerating listening sockets owned by descendants of any tmux pane in the workspace's session (no log scraping, no per-framework config), then picking the numerically lowest port in the range **1024–9999** — typically the HTTP server, since dev-tool sidecars like Vite's HMR socket sit on random high ports. The 1024–9999 cap also keeps ephemeral-range listeners (Claude Code's IPC socket, MCP servers, language servers) from being mistaken for dev URLs. The URL is always `http://localhost:<port>` regardless of whether the server binds to `127.0.0.1` or `0.0.0.0`; the bind address controls who can *reach* the server, not what URL works locally. The line disappears within ~2 seconds of the server stopping.

Backed by `lsof` on macOS and `ss` on Linux. On other OSes the feature is a silent no-op.

### Key bindings

| Key | Action |
|---|---|
| `enter` | Summon (create or focus) the workspace's tmux session. If the row has the unread badge, lands you on the `:agent` window so you see what changed; otherwise tmux's last-focused window wins |
| `a` | Open agent window — re-launches the agent if its pane is at a shell |
| `A` | Send a typed prompt to the workspace's agent (inline form). Header confirms the target project/workspace. If the agent is already running, the prompt is bracket-pasted as a user message; if it isn't, the agent launches with the prompt as its first message. Deck stays in focus — switch with `a` once you want to follow along. |
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
| `P` | Cycle scope: all → open PR (bookmark maps to a non-draft open PR) → attention (mini-deck criteria: active agent or unread notification). Resets to `all` each launch — not persisted. |
| `L` | Switch to last tmux session |
| `R` | Rename workspace (inline form: edit name, `enter` to rename, `esc` to cancel). Updates jj workspace, tmux session + window, and state — the on-disk directory keeps its original path. Not allowed on `default`. |
| `B` | Link a jj bookmark to the selected workspace (drives the per-row PR glyph) |
| `d` | Open the selected workspace's auto-discovered dev URL in your default browser |
| `p o` | Open the selected workspace's PR in your default browser (chord — press `p`, then `o`). `esc` cancels the chord. |
| `p r` | Repair the selected workspace's PR. Detects actionable conditions (merge conflicts, failing CI, branch behind base) and sends a fix prompt to the workspace's agent via the same path as `A`. Reports "nothing to repair" if the PR is healthy. |
| `p s` | Set (or clear, via blank/0) the PR # for the selected workspace. Pins the workspace to a specific PR so the deck resolves status by number rather than guessing from the bookmark. Persisted to `~/.awp/...` workspace state. |
| `D` | Delete workspace · on a `default` row, deletes the **project**: removes every other workspace under that repo and drops the project from the deck (the default workspace itself is left intact). Requires typing the project name to confirm. |
| `,` | Edit global state file in `$EDITOR` |
| `J` | Jobs overlay (running async dispatches — cancel, retry, dismiss, open log, yank to clipboard) |
| `?` | Help overlay |
| `q` / `esc` | Quit |

## CLI reference (highlights)

| Command | Purpose |
|---|---|
| `awp deck` | Open the workspace dashboard |
| `awp mini-deck` | Quick-jump list of workspaces with an active agent or unread notification |
| `awp w open [name]` | Create or attach to a workspace (interactive form when run alone) |
| `awp w list` | List workspaces in the current repo |
| `awp w info <name>` | Show details for a workspace |
| `awp w rename <old> <new>` | Rename |
| `awp w delete <name>` | Delete (use `--force` to skip prompts) |
| `awp w prune [--dry-run] [--force]` | Remove orphan workspace dirs under `~/.awp/workspaces` not tracked in state |
| `awp w bootstrap [name]` | Re-run bootstrap hooks for a workspace |
| `awp w bootstrap --all` | Re-run bootstrap hooks for every tracked workspace in the current repo (continues on failure) |
| `awp review [pr#]` | Pick or open a PR for review in a fresh workspace. Opens a `review` window running `tuicr pr <n>`, resolves the persisted session JSON path from tuicr's `active_sessions.json` / `index.json`, and primes the agent with that absolute path plus a precise commit-SHA diff range so it can `tuicr review add` findings without falling back to the (broken-for-PR-mode) `--repo .` lookup. The fetched PR is also written through to `~/.awp/pr-status-cache.json` and pinned to the new workspace as `PRNumber`, so `p o` / row glyphs resolve the instant `awp review` returns — no waiting for the next periodic fetch. Agent makes no file edits, commits, or GitHub comments. |
| `awp diff` | Charm-styled diff viewer |
| `awp doctor [--global] [--fix]` | Health checks; `--fix` repairs missing hooks/env |
| `awp init hooks` | Install/update global Claude + pi integrations (idempotent) |
| `awp config init` | Bootstrap `<repo>/.awp/config.json` (must run from repo root) |
| `awp config edit [--global]` | Open the project (or `--global`) config in `$EDITOR` |
| `awp internal report-status --state <…> [--prompt <text>\|--prompt-stdin] [--waiting-when-tool <list>]` | Hidden — used by hooks to write status. `--prompt` stores the active prompt text on the workspace; `--prompt-stdin` reads it from a Claude-style hook JSON payload on stdin. `--waiting-when-tool` takes a comma-separated list of tool names; when a `PreToolUse` payload's `tool_name` matches, the recorded state is overridden to `waiting` so blocking tools (e.g. `AskUserQuestion`) badge the row instead of staying in `working`. |
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
    "project_roots": ["~/p", "~/go/src/github.com/andrewcohen"],
    "bookmark_prefix": "andrew"
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

List of directories the deck's `o` (open) screen scans for projects. Tilde-expanded. The walker descends up to 4 levels and stops at any directory containing `.git` or `.jj`. Selecting a project summons (or creates) a tmux session named `[awp]<basename>__default` at that path and records a `default` workspace entry under that repo root in `~/.awp/workspace-state.json`, so the project appears in the deck on subsequent launches.

When the deck exits, `deck-cleanup` also kills any leftover `[awp]<repo>__<workspace>` tmux sessions that no longer have a matching entry in the workspace state file (the current session is always preserved). This keeps stray sessions from accumulating after a project is deleted from the deck.

### `deck.bookmark_prefix`

When set, a new workspace created with **no explicit bookmark** auto-creates a jj bookmark named `<prefix>/<workspace-name>` at the new workspace's revision and records it in `Entry.Bookmark`. The deck's per-row PR glyph matches `Entry.Bookmark` against PR `headRefName`, so the auto-bookmark lets a freshly-created workspace's PR (once pushed) light up in the deck without a manual `B`-link step.

Unset = no auto-create. The `B` key in the deck stays available for backfilling existing workspaces whose bookmark is empty.

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
  process-start-time check that also catches PID reuse. Terminal records
  — done, failed, or orphaned — are GC'd after 24 hours on next deck
  startup (cleanup runs in a background `tea.Cmd`, never blocks startup).
  Successful `pr-status` jobs are deleted as soon as the subprocess exits
  cleanly, since they're background polls the user never inspects;
  failures stick around so the `J` overlay can surface them.

Press `J` to open the jobs overlay:

| Key | Action in overlay |
|---|---|
| `↑` / `↓` (or `k` / `j`) | Move cursor |
| `g` / `G` | Jump to top / bottom |
| `c` | Cancel the selected running job (sends `SIGTERM`; subprocess flushes a `cancelled` record before exiting) |
| `r` | Retry a failed/cancelled/orphaned job (re-spawns from the original spec; useful after manually resolving a stale workspace and similar fixable conditions) |
| `D` | Delete the workspace named in the spec and re-spawn the job. Only enabled when the failed job's `ErrorKind` is `stale_workspace` — surfaced today by the workspace reconciler when an existing workspace can't be aligned to the requested bookmark (e.g. a half-finished prior review left it in a weird state). Press `D` to start clean. |
| `x` | Dismiss a finished/failed/orphaned record (deletes the JSON + log file) |
| `o` | Open the sidecar log file. Active jobs open with `less +F` (follow mode — new output streams in like `tail -f`; press Ctrl-C inside less to drop into normal navigation). Terminal jobs open with `$PAGER` (default `less`). |
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
