# Deck: rename workspace from the deck (`R`)

## Metadata
- **Spec ID**: `20260513-azuv`
- **Feature name**: Deck rename workspace
- **Owner**: Andrew
- **Status**: Planned
- **Last updated**: 2026-05-13

## Goal
Let a user rename a workspace from inside the deck with a single keypress, without dropping to a shell to run `awp w rename`.

## User Problem
Workspace names get stale (you started a workspace called `fix-flaky-test`, but the work expanded into a bigger refactor). Today the only way to rename is `awp w rename <old> <new>` from a shell — that means leaving the deck, remembering both names, and re-summoning afterward. A deck keybinding makes rename a 5-second action.

## Scope
### In scope (v1)
- `R` keybinding on a workspace row opens an inline rename modal.
- Modal is a single text input prefilled with the current normalized name.
- Submit (`enter`) calls `svc.Rename(old, new)`. Cancel (`esc`) closes without action.
- Validation: new name normalizes to a non-empty value, is different from the current name, and is not already taken. Errors render inside the modal.
- **Refuse rename when an agent is running.** The deck-side handler checks the workspace's `<session>:agent` pane; if its current process is not a shell (`bash`/`zsh`/`fish`/`sh`/`dash`), the rename fails with `workspace "X" has a live agent (Y) — stop it first…`. Reason: the running agent has `AWP_WORKSPACE=<old>` frozen in its environ, so renaming would silently break status reporting until the agent restarts.
- **Rename the tmux session, not just the window.** `svc.Rename` only renames the window-inside-session; the handler additionally calls `tmux rename-session` (in-place, preserves all windows/panes) so `[awp]<project>__<old>` becomes `[awp]<project>__<new>`. Without this, `killDesyncedAwpSessions` would nuke the orphaned session on the next deck open.
- **Update the tmux session env** via `ensureWorkspaceSessionEnv` so any fresh shell falling back to `tmux show-environment` picks up the new `AWP_WORKSPACE` value.
- After success, the deck reloads state so the renamed row reflects the new name immediately.
- Help overlay (`?`) and README keybinding table updated.
- Remove the existing `R` → relink action and its `relinkSession` helper; it is no longer reachable in normal flows (see [Removing relink](#removing-relink)).

### Out of scope (v1)
- Renaming the workspace directory on disk. `svc.Rename` updates the state map key, the jj workspace, and the tmux window; the deck-side handler additionally renames the `[awp]<project>__<workspace>` tmux session. `entry.Path` continues to point at the original on-disk directory. We accept that divergence in v1.
- Renaming bookmarks, branches, or PR titles tied to the workspace.
- Bulk rename, undo, history.

## UX
### CLI
- No CLI change. `awp w rename <old> <new>` already exists and is unchanged.

### TUI
- Press `R` on a workspace row → modal appears with a centered card: title (`rename "<old>"`), text input prefilled with the current name, hint footer (`enter submit · esc cancel`).
- Typing edits the name. Submit runs the rename; the modal closes and status bar shows `renamed <old> → <new>`.
- Validation errors (empty, unchanged, duplicate, normalization error) render below the input in red; modal stays open.
- Pressing `R` on a non-workspace row (project header, default-workspace placeholder) shows a status-bar message and does nothing.

### Removing relink
- The `R` → relink session action is removed in the same change. Rationale and residual-edge-case analysis in the conversation that preceded this spec; the auto-cleanup (`killDesyncedAwpSessions`) plus state-file watching make manual relink redundant.
- Removed symbols: `ActionRelink`, the `case "R"` (replaced by the rename trigger), the action-name `case ActionRelink`, the help-group entry, the `cli/deck.go` dispatch, and `relinkSession`.

## Implementation Plan
1. **Remove relink.** Strip `ActionRelink` from `internal/deckui/model.go` (enum, action-name case, help group entry, key handler), `internal/cli/deck.go` (dispatch case and `relinkSession` helper), and the `R` row in `README.md`. Commit standalone so it can be reverted in isolation.
2. **Rename modal component.** Add `internal/deckui/rename_workspace_form.go` mirroring `new_workspace_form.go` but minimal: one `textinput.Model`, a target `Item`, an error string, `update(msg) (self, cmd, action)`, `view(width) string`. Plain struct, not `tea.Model`.
3. **Wire modal into `Model`.** Add `renameMode bool` and `renameForm renameWorkspaceForm` fields. Route keys in `Update` when `renameMode` is true. Branch `View` to render the form when active. Mirror the `confirmDelete` pattern.
4. **Bind `R`.** Replace the existing `case "R"` in the main key switch: require a selected workspace row, populate `renameForm` from the row, set `renameMode = true`, return `textinput.Blink`.
5. **Action dispatch.** Add `ActionRename` to the action enum and action-name switch in `model.go`. On form submit, call `m.trigger(ActionRename, newName)`. In `internal/cli/deck.go`, add a `case deckui.ActionRename` that (a) snapshots the live tmux session id (if any) under the old `DeckSessionName`, (b) calls `svc.Rename(item.WorkspaceName, payload)`, then (c) if a session existed, renames the tmux session to the new `DeckSessionName` and calls `svc.RecordSession` so the state's `SessionName` field also reflects the new name. Without the session rename, `killDesyncedAwpSessions` would nuke the orphaned session on next deck open. Triggers a deck reload (follow the `ActionDelete` pattern).
6. **Help + README.** Update `deckKeyGroups` in `model.go` so `R` shows `rename workspace`. Update the keybinding table in `README.md` to match.
7. **Tests.** Add deckui modal-flow tests in `internal/deckui/model_test.go`: `R` opens the modal, submit emits `ActionRename`, esc cancels, validation errors render. Service-level `Rename` is already covered by `internal/workspace/service_test.go:532`.
8. **Validate.** `go test ./...`, `go vet ./...`, `go build ./...`.

## Acceptance Criteria
- [ ] Pressing `R` on a workspace row opens a rename modal prefilled with the current name.
- [ ] Submitting a valid new name renames the workspace (state, jj, tmux session + window) and the deck row reflects it immediately.
- [ ] Submitting empty / unchanged / duplicate names shows an in-modal error and keeps the modal open.
- [ ] Renaming a workspace with a live agent fails with a clear "live agent — stop it first" error; state, jj, and tmux are untouched.
- [ ] After a successful rename, `tmux show-environment -t [awp]<project>__<new> AWP_WORKSPACE` returns the new name.
- [ ] `esc` cancels the modal without side effects.
- [ ] Pressing `R` on a non-workspace row is a no-op with a status-bar hint.
- [ ] The `?` overlay and README list `R` as `rename workspace`.
- [ ] `relinkSession`, `ActionRelink`, and the relink help-group row are no longer present in the codebase.
- [ ] `go test ./...`, `go vet ./...`, `go build ./...` all pass.

## QA / Human Review Test Plan
### Setup
- [ ] Fresh deck with at least two workspaces in the active repo and at least one in a second repo.
- [ ] One workspace has a live tmux session attached (via `s` / summon).

### Core Happy Path
- [ ] Select an idle workspace row, press `R`, edit the name, press `enter`. Verify status bar shows `renamed <old> → <new>` and the row label updates.
- [ ] Run `jj workspace list` and `tmux list-windows`; both reflect the new name.
- [ ] `awp w list` shows the new name.

### Edge Cases & Failure Modes
- [ ] Empty input → in-modal error.
- [ ] Submit unchanged name → in-modal error.
- [ ] Submit a name that matches another existing workspace → in-modal error.
- [ ] Press `R` on a project header or default-workspace placeholder → status-bar hint, no modal.
- [ ] Rename a workspace with an attached tmux session but agent stopped; verify `tmux list-sessions` shows the new session name, all windows still present, and resummon (`s`) attaches without re-creating panes.
- [ ] Rename a workspace whose agent is currently running → handler returns a "live agent" error; state, jj, and tmux session/window are unchanged. Stop the agent (`ctrl-c` in the agent pane) and retry — should succeed.
- [ ] Cancel with `esc` → modal closes; state unchanged.

### Regression Checks
- [ ] Other single-letter bindings (`r`, `B`, `D`, `n`, `o`) still work.
- [ ] Delete confirm flow still works.
- [ ] No relink-related references remain in `?` help or README.

### Reviewer Notes
- Spec deliberately keeps the on-disk directory name unchanged. Surface this as a follow-up if it becomes a paper cut.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`

## Spec Change Log
- 2026-05-13: Initial draft.
