# CI Run Watch Deck Action

## Metadata
- **Spec ID**: `20260415-i3a3`
- **Feature name**: CI run watch deck action
- **Owner**: Andrew Cohen
- **Status**: Planned
- **Last updated**: 2026-04-15

## Goal
From the deck, press a single key on a workspace row to stream the latest GitHub Actions run for that workspace's branch, or show its summary if already finished.

## User Problem
When iterating on a branch, I frequently want to know the CI state for the most recent push. Today I context-switch to the repo on GitHub or run `gh` manually from the right directory. The deck already knows which workspace/repo I care about — it should dispatch the right `gh` commands for me.

## Scope
### In scope (v1)
- New deck action bound to the `i` key (CI). Operates on the selected workspace row.
- Resolve the current bookmark/branch for the workspace.
- Query `gh run list --branch <b> --limit 1` scoped to the workspace's repo.
- Dispatch:
  - in_progress / queued → `gh run watch <id> --compact --exit-status`.
  - completed + success → `gh run view <id>`.
  - completed + failure → `gh run view <id> --log-failed`.
- No match → friendly message, no error.
- Run all `gh` invocations with `cwd = workspace.Path` so the remote resolves against the right repo (consistent with commit 6f7c46c).
- Action opens in a tmux window for that workspace (same surface as `a`/`e`/`c`/`v`/`s`) so the stream doesn't block the deck.

### Out of scope (v1)
- Picking among multiple recent runs (latest only).
- Filtering by commit SHA (branch only).
- Polling or re-watching after completion.
- Re-triggering runs, cancelling, or any write actions.
- A non-deck CLI entry point (can add `awp ci` later if useful).

## UX
### CLI
- None in v1. Behavior is reached via the deck key binding.

### TUI
- Deck legend gains: `i  ci` (between `v  vcs` and `s  shell`, or wherever reads well).
- Pressing `i` on a row:
  - Status line: `ci: <workspace>...`
  - Opens/focuses a tmux window named `ci` in the workspace and runs the resolved `gh` command there.
  - If no branch / no run found: status line `ci: no runs for <branch>` and no window is opened.

## Discovery Questions
1. **First user**: Andrew, checking CI state while working across multiple workspaces.
2. **When**: After a push, or while waiting for a run to finish to merge/rebase.
3. **Exact output**: Live `gh run watch --compact` output while running; `gh run view [--log-failed]` when done.
4. **Data sources**: `gh` CLI (must be authenticated), jj for current bookmark, workspace store for path.
5. **Smallest useful slice**: Latest run on current branch, per-workspace, branch-only filter.
6. **Non-goals**: Multi-run picker, commit-SHA precision, polling, write actions.
7. **Done**: Pressing `i` on any workspace row reliably streams or summarizes its latest CI run in a dedicated tmux window, using the workspace's repo.

## Decisions
- **Key**: `i` (for CI). Free — existing bindings are `a e c v s n D R P` + nav.
- **Branch resolution**: prefer current bookmark at `@` in the workspace. If none, fall back to `jj log -r @ -T 'bookmarks'` trimmed; if still empty, surface "no branch for workspace".
- **gh invocation**: `gh run list --branch <b> --limit 1 --json databaseId,status,conclusion` then JSON-parse for dispatch.
- **Tmux window name**: `ci` (consistent with short lowercase names used elsewhere).
- **Exit behavior**: `gh run watch --exit-status` exits non-zero on failure — fine; user keeps the pane open to read logs. No special handling.

## Spec Change Log
- 2026-04-15: Initial draft.

## Implementation Plan
1. Add a `ci` action path: extend `deckui.Action` with `ActionCI` (or reuse `ActionOpenWindow` with a new arg like `"ci"` — see note below).
2. Bind key `i` in `internal/deckui/model.go` update loop; include in legend.
3. In the CLI deck wiring (`internal/cli/deck.go`), implement the handler:
   - Look up the workspace entry to get `Path` and branch.
   - Shell out to `gh run list ... --json ...` from the workspace path.
   - Parse; build the follow-up command (`gh run watch` / `gh run view` / `gh run view --log-failed`).
   - Ensure a tmux window named `ci` for the workspace and send the built command.
4. No-run and no-branch paths set a status message and do not open a window.
5. Tests:
   - Unit-test the dispatch logic (given a parsed `gh run list` result, the right follow-up command is chosen) using a fake runner.
   - Unit-test branch resolution fallback.
   - Deck model test for the new binding + legend entry.

**Action-vs-arg note**: simpler to add `ActionCI` than to overload `ActionOpenWindow`, because `ActionOpenWindow` in current code maps a named window (`agent`, `editor`, `tuicr`, `vcs`, shell) rather than running a command in it. CI is command-oriented, so a distinct action keeps the handler clean.

## Acceptance Criteria
- [ ] `i` on a workspace row with an in-progress run opens a `ci` tmux window running `gh run watch <id> --compact --exit-status` in the workspace path.
- [ ] `i` with the latest run completed+success opens the window running `gh run view <id>`.
- [ ] `i` with the latest run completed+failure opens the window running `gh run view <id> --log-failed`.
- [ ] `i` with no runs for the branch sets a status message and opens no window.
- [ ] `i` with no resolvable branch sets a status message and opens no window.
- [ ] All `gh` invocations run with cwd set to the workspace path (so remote = workspace repo).
- [ ] Deck legend shows the new binding.

## QA / Human Review Test Plan
### Setup
- [ ] `gh`, `jj`, `tmux` installed and on PATH; `gh auth status` succeeds.
- [ ] At least two workspaces in the deck across two repos with CI configured.
- [ ] One workspace with a fresh push triggering an in-progress run.
- [ ] One workspace on a branch with the latest completed run green.
- [ ] One workspace on a branch with the latest completed run red.
- [ ] One workspace on a branch with no runs.

### Core Happy Path
- [ ] Press `i` on the in-progress row → `ci` window opens, streams `gh run watch --compact`.
- [ ] Press `i` on the green row → window opens, shows `gh run view` summary.
- [ ] Press `i` on the red row → window opens, shows `gh run view --log-failed` output.

### Edge Cases & Failure Modes
- [ ] No runs for branch → status "no runs for <branch>", no window created.
- [ ] No bookmark resolvable → status explains, no window.
- [ ] `gh` not authenticated → error surfaced verbatim in the window or status.
- [ ] Second press of `i` reuses the existing `ci` window rather than stacking duplicates.

### Regression Checks
- [ ] Existing deck keys (`a e c v s n D R P` + nav) still work.
- [ ] Actions remain repo-aware for other commands.

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
