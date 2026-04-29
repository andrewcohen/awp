# Agent Status Reporting

## Metadata
- **Spec ID**: `20260429-i021`
- **Feature name**: Agent Status Reporting
- **Owner**: Andrew Cohen
- **Status**: Planned
- **Last updated**: 2026-04-29

## Goal
Show the user, at a glance in the deck, whether each workspace's coding agent is
currently working, idle (waiting on user), waiting on a permission prompt, or
exited — so they can tell which agents need their attention without switching
to each tmux session.

## User Problem
Today every workspace row in the deck renders a static "IDLE" indicator
regardless of the agent's actual state. `workspace.Service.UpdateStatus` exists
but is never called, so the indicator is decoration. With multiple concurrent
agents (Claude Code, pi.dev) in tmux sessions, the user has no way to know which
ones are actively producing output, which are blocked on a permission prompt,
and which are sitting idle waiting for a new prompt — short of attaching to
each pane and looking.

## Scope

### In scope (v1)
- A hidden CLI endpoint the agent calls to report state transitions:
  `awp internal report-status --state <working|idle|waiting|exited>`.
- Per-tmux-session env vars (`AWP_WORKSPACE`, `AWP_REPO`) so the endpoint
  knows which workspace is reporting, regardless of cwd.
- Lazy env repair: when summoning a workspace, verify those vars are set on
  the tmux session and inject them if missing/stale.
- Globally-installed agent integrations (one-time, not per-workspace):
  - Claude Code: hooks merged into `~/.claude/settings.json`
    (`UserPromptSubmit`, `PreToolUse` → `working`; `Stop` → `idle`;
    `Notification` → `waiting`).
  - pi.dev: an extension installed into pi's user extensions directory that
    subscribes to `turn_start` / `turn_end` / `input` / `session_shutdown`.
- A bootstrap command `awp init hooks` (idempotent) that installs/updates
  both integrations. Auto-run on first `awp deck` if not already installed.
- Each hook/extension is a no-op when `$AWP_WORKSPACE` is unset, so it never
  affects non-awp Claude/pi sessions.
- Deck rendering: replace the `IDLE`/`RUN`/`WAIT` text on each row with a
  single colored `●` glyph driven by the reported state. The full state word
  remains in the details pane.
- Crash fallback: if the agent pane's current command resolves to a shell,
  override the in-memory status to `exited`. No content diffing.

### Out of scope (v1)
- Push-based deck refresh (fsnotify on the state file). Status updates appear
  on the next deck refresh tick — instant updates are a follow-up.
- Heuristic detection (capture-pane diffing, cursor-blink filtering).
  Detection is event-driven only.
- Per-repo or per-workspace overrides of the global hook config.
- Custom-agent (non-Claude, non-pi) adapters. Other agents can hit the same
  CLI endpoint but we don't ship adapters for them.
- Migrating already-running agents that pre-date the env injection. We surface
  a one-line hint in the deck rather than auto-restarting.

## UX

### CLI
- `awp init hooks` — install/update Claude Code and pi.dev integrations
  globally. Idempotent. Reports what was added/updated/skipped.
- `awp internal report-status --state <state>` — hidden, used by hooks.
  Resolves workspace from `$AWP_WORKSPACE` + `$AWP_REPO`. Silent on success;
  exits 0 even when env is missing (no-op) so a misconfigured hook never
  breaks an agent turn.

### TUI
- Each deck row renders `<glyph> <workspace-name>` in place of
  `<STATUS-TEXT> <workspace-name>`. Glyph color encodes state:
  - `working` → green
  - `waiting` → yellow
  - `idle` → dim grey
  - `exited` → red
  - unknown / unmanaged → dim grey
- Details pane keeps `Status: <word>` for the full state.
- If a workspace's running agent lacks `AWP_WORKSPACE` (legacy/adopted
  session), the deck status footer shows: `agent missing AWP_WORKSPACE —
  restart agent to enable status reporting` while that workspace is selected.

## Discovery Questions
1. **Who is the first user?** Andrew, running multiple concurrent agents in
   awp-managed tmux sessions.
2. **When do they use this feature?** Continuously while the deck is open —
   it's a passive at-a-glance signal, not a command.
3. **What exact output/result do they need?** A single-glyph color signal per
   row that flips within a few seconds of the agent transitioning between
   working and idle/waiting.
4. **What data sources are required?** Agent-emitted events via Claude hooks
   and pi extensions; tmux session env vars for workspace identity; existing
   `workspace.Service` state file.
5. **What is the smallest useful slice?** CLI endpoint + env injection on
   summon + Claude hook install + glyph rendering. pi extension can land
   immediately after; push refresh is a follow-up.
6. **What are explicit non-goals?** Heuristic/poll-based detection, per-repo
   hook config, custom-agent adapters, instant push refresh.
7. **What does "done" look like?** Sending a prompt to Claude in a workspace
   flips that row's glyph to green within one refresh tick; on `Stop` it
   returns to grey; on a permission prompt it turns yellow. Same for pi.dev.
   Killing the agent process turns it red.

## Spec Change Log
- 2026-04-29: Initial draft.

## Implementation Plan

1. **CLI endpoint + workspace status writer.**
   - Add `awp internal report-status --state <state>` subcommand.
   - Resolves the target workspace from `$AWP_WORKSPACE` (workspace name) and
     `$AWP_REPO` (project name). If either is unset, exit 0 silently.
   - Calls `workspace.Service.UpdateStatus(name, state)`. Validate the state
     against an allowlist (`working|idle|waiting|exited`); reject unknowns.
   - Tests: stub service, env-var resolution, allowlist rejection.

2. **Tmux env injection on session creation and summon.**
   - At session creation, run `tmux set-environment -t <session>
     AWP_WORKSPACE <name>` and `AWP_REPO <project>`.
   - In the summon path (`internal/cli/deck.go`), before launching/attaching
     the agent: query `tmux show-environment -t <session> AWP_WORKSPACE`. If
     unset or doesn't match the workspace being summoned, set both vars.
   - If an agent process is already running in the agent pane and is *not* a
     shell, surface the "missing AWP_WORKSPACE — restart agent" hint via the
     deck footer (don't auto-kill).
   - Wrap the tmux calls in `internal/tmux/tmux.go`
     (`SetSessionEnv`, `GetSessionEnv`).

3. **Global Claude Code hook install.**
   - `awp init hooks` (or first-run auto-bootstrap from `awp deck`) merges a
     hook block into `~/.claude/settings.json`:
     - `UserPromptSubmit`, `PreToolUse` → `awp internal report-status
       --state working`
     - `Stop` → `--state idle`
     - `Notification` → `--state waiting`
   - Each command is wrapped: `[ -n "$AWP_WORKSPACE" ] && awp internal
     report-status …` so it no-ops outside awp sessions.
   - Idempotent: detect prior install via a marker key (`"awp": {"version":
     N}`) and update in place.

4. **Global pi.dev extension install.**
   - Ship a TypeScript/JS extension under
     `internal/embed/pi-extension/awp-status.ts` (embedded via `embed.FS`).
   - `awp init hooks` writes it to pi's global extensions directory (path
     to be confirmed against pi-mono docs on first run).
   - Extension subscribes to `turn_start`, `turn_end`, `input`,
     `session_shutdown`, and shells out to the CLI endpoint when
     `process.env.AWP_WORKSPACE` is set.

5. **Deck rendering: glyph instead of text.**
   - Replace `compactStatus(item.Status)` at `internal/deckui/model.go:1213`
     with `statusGlyph(item.Status)` returning a colored `●`.
   - Mapping: `working`→green(82), `waiting`→yellow(214), `idle`→grey(244),
     `exited`→red(203), default→grey(244).
   - Keep `normalizeStatus` for the details pane (`model.go:1253`).
   - Remove `compactStatus` if no remaining callers.

6. **Crash fallback in `loadDeckItems`.**
   - For each entry with a live `AgentPaneID`, call
     `tmuxClient.PaneCurrentCommand`. If the result is a shell, override
     the in-memory `Item.Status` to `exited` before returning. Do not
     persist this to the entry.

## Acceptance Criteria
- [ ] `awp internal report-status --state working` updates the workspace
  entry's `Status` field when `AWP_WORKSPACE` and `AWP_REPO` are set,
  and exits 0 silently when they are not.
- [ ] Sending a prompt to Claude inside an awp workspace flips the deck row
  glyph to green within one refresh tick; pressing Stop returns it to grey;
  a permission prompt turns it yellow.
- [ ] Same observable behavior for a pi.dev session in an awp workspace.
- [ ] Killing the agent process (so the pane is back at a shell) turns the
  glyph red within one refresh tick.
- [ ] `awp init hooks` is idempotent: running it twice produces no diff in
  `~/.claude/settings.json` or the pi extensions directory.
- [ ] Hooks/extensions installed globally do not affect Claude/pi sessions
  outside awp (i.e., when `AWP_WORKSPACE` is unset).
- [ ] Summoning a workspace whose tmux session pre-dates this feature
  injects the env vars before the next agent launch and shows the
  "restart agent" hint if an agent is already running there.
- [ ] Deck row no longer shows `IDLE` / `RUN` / `WAIT` text; details pane
  still shows the full state word.

## QA / Human Review Test Plan

### Setup
- [ ] `jj`, `tmux`, `claude` (Claude Code CLI), and `pi` available in PATH.
- [ ] Build awp from this branch: `go build ./...`.
- [ ] Back up `~/.claude/settings.json` before running `awp init hooks`.
- [ ] Run `awp init hooks` once; confirm reported install summary.

### Core Happy Path
- [ ] In an awp workspace, launch Claude in the agent window. Send a prompt;
  observe deck row glyph turn green within one refresh.
- [ ] Wait for Claude's response to finish; observe glyph return to grey.
- [ ] Trigger a tool that requests permission; observe glyph turn yellow.
- [ ] Repeat with pi.dev as the agent.

### Edge Cases & Failure Modes
- [ ] Run `claude` outside an awp tmux session: confirm hooks are no-ops
  (no `awp` errors in Claude output, no state changes).
- [ ] Run `awp internal report-status --state working` with no env vars:
  exits 0 silently.
- [ ] Run with an invalid state (`--state foo`): exits non-zero with an
  actionable error.
- [ ] Summon a workspace whose tmux session was created before this feature:
  confirm env vars get injected, and if an agent is already running, the
  "restart agent" hint appears.
- [ ] Kill the agent process directly (`Ctrl-C` to shell): glyph turns red.
- [ ] Run `awp init hooks` twice: second run reports "no changes."

### Regression Checks
- [ ] Existing deck navigation, find, and review actions still work.
- [ ] Workspace creation/deletion flows unaffected.
- [ ] Non-awp Claude/pi usage on the same machine is unaffected.
- [ ] Details pane still renders `Status: <word>` correctly.

### Reviewer Notes
- Capture before/after screenshots of the deck.
- Capture the diff `awp init hooks` produces in `~/.claude/settings.json`.
- Note any latency between agent state change and glyph update.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
