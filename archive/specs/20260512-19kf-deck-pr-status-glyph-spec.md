# Deck PR status glyph (MVP)

## Metadata
- **Spec ID**: `20260512-19kf`
- **Feature name**: Deck PR status glyph (MVP)
- **Owner**: Andrew Cohen
- **Status**: Planned
- **Last updated**: 2026-05-12

## Goal
Surface each workspace's PR status directly in the deck row, so it is obvious at a
glance which workspaces have an open PR, which have failing CI, and — crucially
for cleanup — which have been merged and are safe to delete.

## User Problem
Andrew accumulates many workspaces and currently has no in-deck signal for
whether the corresponding PR is open, in-review, draft, failing CI, merged, or
closed. The most acute pain is not knowing which workspaces are merged (and
therefore safe to delete). Today the answer requires switching to GitHub /
`gh pr view` per workspace.

## Scope

### In scope (v1)
- One async fetch on deck open: one `gh pr list --repo <slug> --state all --limit 100 ...` per *distinct* repo represented in the deck.
- A **single combined glyph** rendered to the right of the workspace name, picking the most-important signal (priority order: merged → closed → CI failed → CI pending → draft → open).
- Match each workspace to a PR by `workspace.Entry.Bookmark` ↔ PR `headRefName`. Workspaces with empty `Entry.Bookmark` get **no glyph** in v1.
- Skip repos whose *only* workspace is the default (`Path == RepoRoot`); they have no non-default workspaces and so no useful PRs to fetch.
- Per-repo refresh throttle: never re-fetch a given repo more than once per minute. A manual `r`-style refresh respects the same throttle.
- Errors per-repo are logged to the status line but never block other repos.

### Out of scope (v1)
- Background polling / adaptive intervals.
- Bookmark → workspace relinking UX (`B`-key bookmark picker rewriting `Entry.Bookmark`).
- Bulk "delete all merged workspaces" affordance.
- Webhooks, SSE, or any push-based update channel.
- Persisting the PR cache across deck restarts.
- A separate "review picker" overhaul — the existing review picker stays as is.

## UX

### CLI
No new commands.

### TUI
Each workspace row keeps its existing leading status glyph (agent status). A
second glyph appears in a dedicated slot after the workspace name, drawn from
the Nerd Font Octicon set (the deck assumes a patched font — this is acceptable
for v1 since Andrew is the primary user). Codepoints are listed by their
Octicon name for legibility:

| Glyph | Octicon | Codepoint | Meaning |
|-------|---------|-----------|---------|
|  | `nf-oct-git_pull_request` | `U+F407` | PR open |
|  | `nf-oct-git_pull_request_draft` | `U+F4DD` | PR draft |
|  | `nf-oct-check` | `U+F42E` | PR approved (review decision = APPROVED) |
|  | `nf-oct-git_merge` | `U+F417` | PR merged |
|  | `nf-oct-git_pull_request_closed` | `U+F4DC` | PR closed without merge |
|  | `nf-oct-hourglass` | `U+F252` | CI running / pending |
|  | `nf-oct-x` | `U+F467` | CI failed |
| (none) | — | — | No PR matched, or no `Entry.Bookmark` on this workspace, or fetch failed |

Priority when multiple states apply (highest wins):
**merged → closed → CI failed → CI pending → approved → draft → open**.

So a merged PR always shows the merge icon (even if its last CI was failing);
an open PR with failing CI shows the alert icon rather than the open-PR icon;
an approved PR with passing/pending CI shows the check icon.

Colors come from the existing `statusColor` helper; new states pick the closest
existing palette entry rather than introducing new colors in v1.

The `?` help overlay gains a "PR status" section listing the glyphs above.
`internal/deckui/model.go::deckKeyGroups` is the canonical source; no new key
binding is required in v1 (refresh on Init only; existing `r` refresh, if any,
covers manual re-fetch).

## Discovery decisions
- **First user**: Andrew, who currently keeps too many workspaces around because the merge state is invisible.
- **When**: Every time the deck is opened — passive, no extra keystroke.
- **Output**: Per-row glyph; no list view.
- **Data source**: `gh pr list --repo <owner/name> --state all --limit 100 --json number,headRefName,state,isDraft,statusCheckRollup`. Run via the existing `Runner` indirection so it is testable.
- **Smallest useful slice**: this spec.
- **Non-goals**: polling, relink UX, bulk cleanup — listed above.
- **Done**: opening the deck with a few workspaces across at least one repo with merged + open PRs renders the expected glyphs within a few seconds of first paint, and a second open within a minute reuses the cache without re-running `gh`.

## Implementation Plan

1. **`internal/github`**: add `ListPRStatus(repoDir string) ([]PRStatus, error)` returning `Number, HeadRefName, State (OPEN/CLOSED/MERGED), IsDraft, ReviewDecision (APPROVED/CHANGES_REQUESTED/REVIEW_REQUIRED/""), CIState (PASSING/FAILING/PENDING/NONE)`. Derives `CIState` from `statusCheckRollup` (rolling up CHECK_RUN/STATUS_CONTEXT conclusions). Existing `PRSummary` / `ListPRs` stays untouched for the review picker.
2. **`internal/deckui/model.go`**:
   - New types: `PRStatus` (mirrors the github package shape), `PRStatusFetcher func(repoRoots []string) tea.Cmd`, `PRStatusDoneMsg { ByRepo map[string]map[string]PRStatus; Errs map[string]error; FetchedAt map[string]time.Time }`.
   - New `Model` fields: `prStatusFetcher PRStatusFetcher`, `prStatusByRepo map[string]map[string]PRStatus` (repoRoot → headRef → status), `prStatusFetchedAt map[string]time.Time` (repoRoot → wall clock).
   - `Init()` computes the set of distinct repo roots represented in `itemsAll` (or `itemsCurrent` if no all set) **after filtering out repos whose only workspace has `Path == RepoRoot`**, then fires `prStatusFetcher(repos)` once.
   - `Update` handles `PRStatusDoneMsg` by merging into `prStatusByRepo` / `prStatusFetchedAt`.
   - Helper `prGlyph(item Item) string` consults `Entry.Bookmark` → looks up `prStatusByRepo[item.RepoRoot][bookmark]` → returns the priority-ordered glyph (or "").
   - Row renderer appends `prGlyph(item)` after the workspace label, padded so columns stay aligned.
   - `renderHelp` gains a "PR status" group.
3. **`internal/deckui/model.go::Item`**: add `Bookmark string` so the row renderer can derive the glyph without holding a reference to the whole `Entry`. Populated by the deck loader.
4. **`internal/cli/deck.go`**:
   - In `loadDeckItems`, copy `entry.Bookmark` onto the new `Item.Bookmark`.
   - Build the `prStatusFetcher` closure: accept `[]repoRoot`, dedupe, drop any whose `time.Since(prStatusFetchedAt[r]) < 1*time.Minute` (the model passes the current map in; simplest is to do the throttle inside the model before calling the fetcher), call `gh.ListPRStatus` per repo concurrently, fan-in to a single `PRStatusDoneMsg`.
   - Pass the fetcher with a new `WithPRStatusFetcher` builder method.
5. **`README.md`**: add a row to the deck-key/legend section describing the PR glyphs.

### Throttle ownership
The "≥ 60s since last fetch per repo" guard lives **in `Model`**, not the fetcher. The model has the timestamp map, knows which repos are due, and is the single point that decides whether to fire the command. The fetcher just executes what it's told.

### Concurrency
Per-repo `gh` calls run in parallel goroutines inside one `tea.Cmd`; the cmd returns when all complete (or after a 10s overall timeout). Failures are reported per-repo in `PRStatusDoneMsg.Errs` and surface to the status line as a single "PR status: N repos failed" message; details are not rendered per-row in v1.

## Acceptance Criteria
- [ ] Opening the deck triggers exactly one `gh pr list` call per repo with at least one non-default workspace; default-only repos are not queried.
- [ ] After the fetch completes, rows whose `Entry.Bookmark` matches a PR's `headRefName` render the correct glyph per the priority order above.
- [ ] Workspaces with empty `Entry.Bookmark` render no PR glyph.
- [ ] Re-opening the deck (or any internal trigger that would re-fetch) within 60 s of the last successful fetch for a given repo does **not** re-call `gh` for that repo.
- [ ] `gh` failure for one repo does not block glyphs for other repos.
- [ ] `?` help overlay shows the PR glyph legend.
- [ ] No regressions to existing row rendering, statusGlyph, or the review picker.

## QA / Human Review Test Plan

### Setup
- [ ] `gh auth status` healthy, `gh` ≥ 2.40 in PATH.
- [ ] A test repo with at least one workspace whose bookmark has an open PR, one merged PR, and one with failing CI.
- [ ] At least one repo that has only the default workspace (Path == RepoRoot) to verify it is skipped.

### Core Happy Path
- [ ] Open the deck. Within a few seconds, the per-row glyph appears for matched workspaces.
- [ ] Merged-PR workspace shows `✓`; failing-CI workspace shows `⚠`; open-PR workspace shows `●`; draft shows `◐`.
- [ ] Default-only repos: confirm no `gh` call is issued for them (use `--log-level debug` or transient logging).
- [ ] Close and reopen the deck within 60 s: glyphs appear immediately from cache; no new `gh` call for cached repos.

### Edge Cases & Failure Modes
- [ ] Workspace with empty `Entry.Bookmark`: row shows no PR glyph and no error.
- [ ] Workspace whose bookmark matches no PR: row shows no PR glyph and no error.
- [ ] `gh` not installed or unauthenticated: status line shows a single non-blocking error; deck remains usable.
- [ ] One repo's fetch errors while another succeeds: only the failing repo's rows lack glyphs.

### Regression Checks
- [ ] Review picker (`v`) still loads and behaves as before.
- [ ] Existing status glyph + unread + stale decorations unchanged.
- [ ] `?` help overlay layout still fits at typical terminal widths.

### Reviewer Notes
- Capture exact `gh` invocations observed (set a temporary log) and confirm `--repo` is the only filter used.

## Spec Change Log
- 2026-05-12: Initial draft.
- 2026-05-12: Glyph set changed from plain Unicode to Nerd Font Octicons (assumes patched font). Added explicit `nf-oct-check` (approved) state distinct from `nf-oct-git_merge` (merged). Priority order: CI status wins over approval. Default-workspace identification = `Path == RepoRoot`. Unmatched / no-bookmark workspaces show no glyph in v1.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
