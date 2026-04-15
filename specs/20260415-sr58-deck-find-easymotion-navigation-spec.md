# Deck Find EasyMotion Navigation

## Metadata
- **Spec ID**: `20260415-sr58`
- **Feature name**: Deck find EasyMotion navigation
- **Owner**: Andrew Cohen
- **Status**: In Progress
- **Last updated**: 2026-04-15

## Goal
Jump quickly to any visible workspace in the deck with at most two keypresses after `f`, modeled after EasyMotion-style hinting.

## User Problem
When many workspaces are visible in deck, arrow-key navigation is slow. The user wants a fast keyboard jump that does not require scrolling through rows.

## Scope
### In scope (v1)
- New deck keybinding: `f` (and `F` alias) to enter find-jump mode.
- Two-level jump flow:
  1. Choose a project by hint key.
  2. Choose a workspace within that project by hint key.
- Hints are rendered inline in the list while find mode is active.
- Targets are based on currently visible rows (current scope + any applied filter).
- Selection only moves cursor (does not trigger summon/action).
- `esc` and `q` cancel find mode.
- Non-matching keys are ignored in find mode.
- While actively editing filter input (`/` mode), find mode is unavailable.
- Add/adjust tests for entry/cancel/jump behavior and filter-mode interaction.

### Out of scope (v1)
- Triggering actions directly after jump (e.g., auto-summon).
- Non-deck CLI behavior.
- Alternate hint schemes, fuzzy matching, or search text entry.
- Backward compatibility with any prior experimental find implementation.

## UX
### CLI
- None.

### TUI
- Legend includes `f find`.
- Press `f`:
  - Footer/status: `find: project`.
  - Each project header shows a one-key hint (home-row-priority alphabet).
- After project key:
  - Footer/status: `find: workspace`.
  - Workspaces in the selected project show one-key hints.
- After workspace key:
  - Cursor moves to that workspace row.
  - Find mode exits.
- Cancel:
  - `esc` or `q` exits find mode with `find: cancelled` status.
- Invalid keys in find mode: ignored.
- If currently in active filter entry mode, `f` is treated as filter input (find does not activate).

## Discovery Questions
1. Who is the first user?
   - Andrew.
2. When do they use this feature?
   - While scanning many workspaces/projects in deck.
3. What exact output/result do they need?
   - Fast cursor jump to target row in two hint keys.
4. What data sources are required?
   - Existing in-memory visible deck rows grouped by project.
5. What is the smallest useful slice?
   - Two-level hinting + cursor move only.
6. What are explicit non-goals?
   - Auto-open/summon; fuzzy/text search replacement.
7. What does “done” look like?
   - `f` reliably drives project→workspace jump and exits on selection/cancel.

## Decisions
- **Flow**: fixed two-level jump (project first, workspace second), to keep interaction predictable.
- **Target set**: visible rows only (respects current scope/filter result).
- **Hint alphabet**: home-row-priority (`asdfghjkl` first; then remaining letters for capacity).
- **Action on selection**: move cursor only.
- **Cancel keys**: `esc` and `q`.
- **Invalid keys**: ignored silently.
- **Filter interaction**: disabled while filter input mode is active.
- **`F` behavior**: same as `f`.

## Spec Change Log
- 2026-04-15: Initial draft.
- 2026-04-15: Implemented deck two-level find state machine (`f`/`F`), inline hints, cancel behavior (`esc`/`q`), and tests; filter-input mode keeps find disabled.

## Implementation Plan
1. Extend `internal/deckui/model.go` with find-mode state machine:
   - stage enum (project/workspace), selected project, and hint maps.
2. Add `f`/`F` handling in key update flow; guard against active filter-input mode.
3. Render project and workspace hints inline in `renderList` based on find stage.
4. Implement selection/cancel semantics and status updates.
5. Update deck details/help strings to include find action.
6. Add/adjust tests in `internal/deckui/model_test.go`:
   - enter find mode,
   - two-level jump,
   - cancel with `q`/`esc`,
   - find disabled during active filter entry.

## Acceptance Criteria
- [ ] Pressing `f` enters find mode and displays project hints.
- [ ] Pressing a project hint transitions to workspace-hint stage for that project.
- [ ] Pressing a workspace hint moves cursor to that workspace and exits find mode.
- [ ] Selection does not trigger summon/open actions.
- [ ] `esc` and `q` cancel find mode.
- [ ] Non-matching keys in find mode do not exit mode or mutate selection.
- [ ] Find mode does not activate while filter input mode is active.
- [ ] `F` behaves the same as `f`.

## QA / Human Review Test Plan
### Setup
- [ ] Build and run deck with multiple projects/workspaces visible.
- [ ] Have at least one filtered list scenario.

### Core Happy Path
- [ ] Press `f`, choose project key, then workspace key; verify cursor lands correctly.
- [ ] Verify selected row is only highlighted (no action dispatched).

### Edge Cases & Failure Modes
- [ ] Press `esc` in project stage → exits find mode.
- [ ] Press `q` in workspace stage → exits find mode.
- [ ] Press invalid/non-hint keys in find mode → ignored.
- [ ] Enter `/` filter mode and press `f` → remains filter input mode.

### Regression Checks
- [ ] Existing navigation/actions (`enter`, `a/e/c/v/s/i`, `D`, `R`, `P`, `/`) still work.
- [ ] Delete confirm mode behavior unchanged.

### Reviewer Notes
- Capture exact keys pressed and observed cursor/status transitions.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
