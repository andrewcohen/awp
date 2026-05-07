# Default new workspaces to the main bookmark

## Metadata
- **Spec ID**: `20260522-h6tw`
- **Feature name**: Default new workspaces to the main bookmark
- **Owner**: saltor
- **Status**: In Progress
- **Last updated**: 2026-05-22

## Goal
When a user starts a new workspace through the interactive flows, pre-fill the bookmark field with `main` so the common case is one keystroke (enter) instead of a bookmark picker round-trip. Keep all other start options (bookmark picker, review-an-open-PR) available, and keep "start from current revision" reachable as an explicit action (clear the prefilled field, then submit).

## User Problem
Almost every new workspace is started against `main`. The previous flow required either picking through the bookmark list each time or accepting an "empty workspace from current revision" default that most users did not want. The default did not match the modal behavior.

## Scope

### In scope (v1)
- Pre-fill the bookmark field with `main` in both interactive flows:
  - Deck `n` → new menu → `enter` on the "main" option launches the inline new-workspace form with the bookmark field set to `main`.
  - `awp w open` interactive new-flow → `enter` on the "main" option flows through to the open form with bookmark `main`.
- One shared `workspace.DefaultBookmark` constant; do not duplicate per package.
- Status text, help overlay, README, and form placeholders updated to reflect the new default.

### Out of scope (v1)
- Making the default configurable per repo or per user (e.g. `master`, `trunk`). Track as a follow-up if requested.
- Changing the non-interactive `awp w open --bookmark=...` behavior. Explicit flags still pass through verbatim.
- Removing the bookmark picker or the review-PR start path. Both stay one keystroke away (`b`, `r`).

## UX
### CLI (`awp w open` with no args)
- New-flow menu lists three options: `main`, `bookmark [b]`, `review [r]`. `main` is the cursor's starting position so `enter` advances to the open form with bookmark `main`.
- Open form's bookmark placeholder reads `jj bookmark/revision to track (blank = current revision)`. Users who want to start from the current revision delete the prefilled `main` and submit; that submission is honored (no silent re-coerce to `main`).

### TUI (deck `n`)
- Deck's new-workspace menu lists `main`, `bookmark`, `review`. `enter` on `main` launches the inline new-workspace form with the bookmark field set to `main`.
- Same placeholder copy in the inline form. Clearing the bookmark and submitting creates a workspace with an empty bookmark (jj `add-workspace` against the current revision).

### Symmetry guarantee
The deck inline form and the CLI standalone form behave identically on submit: the request carries whatever the user typed. Neither path applies a post-submit fallback. The prefill is the one and only mechanism.

## Discovery Questions
1. Who is the first user? — Anyone running `awp w open` or pressing `n` in the deck.
2. When do they use this feature? — Every new workspace.
3. What exact output/result do they need? — A workspace tracking `main` (or whatever bookmark/revision they typed) with one or two keystrokes.
4. What data sources are required? — None new; the bookmark name is hardcoded.
5. What is the smallest useful slice? — Prefill only; do not change submit semantics. (This spec.)
6. What are explicit non-goals? — Configurability, removing existing start options, changing non-interactive flag behavior.
7. What does "done" look like? — One shared constant, two forms with identical submit semantics, tests covering both the prefill and the "user cleared the field" path, README/help/copy updated.

## Spec Change Log
- 2026-05-07: Initial implementation prefilled the bookmark in both flows but the CLI `awp w open` path also applied a post-form fallback that re-coerced an empty submit back to `main`. The deck inline form did not. Reviewer flagged the asymmetry.
- 2026-05-22: Removed the CLI post-form fallback. The newFlow result now carries the default bookmark explicitly (so the form opens prefilled), and both submit paths honor the user's value verbatim. Added tests for the empty-submit case in both packages.

## Implementation Plan
1. Add `workspace.DefaultBookmark = "main"` as the single source.
2. In `internal/cli/new_flow.go`, set `newFlowResult.bookmark = workspace.DefaultBookmark` when the user picks the "main" menu entry.
3. In `internal/cli/app.go`, extend the result-kind switch so `newFlowDefault` and `newFlowBookmark` both copy `result.bookmark` onto `req.Bookmark`. Remove the post-form fallback.
4. In `internal/deckui/model.go`, use `workspace.DefaultBookmark` when launching the inline form from the new menu. Update the deck status and renderNewMenuDetails strings so wording is consistent.
5. Unify the form placeholder copy across `internal/cli/open_form.go` and `internal/deckui/new_workspace_form.go`.
6. README: deck key table and CLI table reflect the new default.
7. Tests: prefill assertion in both packages; empty-submit assertion in both packages.

## Acceptance Criteria
- [ ] Deck `n` → `enter` opens the inline form with the bookmark field showing `main`.
- [ ] `awp w open` → `enter` on the "main" option opens the form with bookmark `main`.
- [ ] In either form, clearing the bookmark field and submitting (with a workspace name) creates a workspace with an empty bookmark.
- [ ] No duplicate `defaultWorkspaceBookmark` / `defaultNewWorkspaceBookmark` constants remain.
- [ ] README key table and CLI table list the new default.
- [ ] `?` help overlay row for `n` shows "new workspace (defaults to main)".

## QA / Human Review Test Plan
### Setup
- [ ] `jj` and `tmux` available in PATH.
- [ ] A test repo with a `main` bookmark and at least one other bookmark.

### Core Happy Path
- [ ] Open the deck, press `n`, press `enter`. Inline form shows bookmark `main`. Submit with default name; workspace is created tracking `main`.
- [ ] In a fresh shell, run `awp w open`. Press `enter` on the "main" option, then submit. Workspace is created tracking `main`.

### Edge Cases & Failure Modes
- [ ] Deck `n` → form opens with `main` prefilled. Clear the bookmark field. Type a workspace name. Submit. Workspace is created with an empty bookmark (current revision).
- [ ] `awp w open` → form opens with `main` prefilled. Clear the bookmark field. Type a workspace name. Submit. Same outcome.
- [ ] Deck `n` → `b` still opens the bookmark picker; pick a non-main bookmark; form opens with that bookmark prefilled.
- [ ] `awp w open --bookmark=feature` still creates a workspace tracking `feature` without touching the new-flow menu.

### Regression Checks
- [ ] `?` help overlay shows the updated `n` row.
- [ ] Status bar wording in the new menu mode is readable and does not reference "default main".

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
