# PR-association refactor

## Metadata
- **Spec ID**: `20260527-s1hm`
- **Feature name**: PR-association refactor — collapse Bookmark/PROverride/headRefName into a single PRNumber
- **Owner**: Andrew
- **Status**: Done
- **Last updated**: 2026-05-27

## Goal
Make a workspace's PR association explicit and direct so every code path looks it up the same way. Removes the race that lets `awp review` create a workspace whose PR isn't visible in the deck until the next periodic refresh.

## User Problem
Today three concepts approximate "which PR is this workspace?":

1. `workspace.Entry.Bookmark` — a jj bookmark name, matched against a PR's `headRefName` to derive the PR.
2. `workspace.Entry.PROverride` — explicit PR-number pin, used when the bookmark→headRefName join misses.
3. The PR-status cache keyed by `headRefName` (the join key).

This produces concrete bugs:

- **The "no PR for this workspace" bug.** `awp review <N>` runs outside the deck, creates a workspace, returns. The deck's state watcher sees the new workspace, but the deck's PR-status fetcher (`prStatusRefreshCmd`) is only triggered from `initKickMsg` and a few in-deck flows. Repos that became eligible while the deck was running don't get fetched. `p o` reports "no PR for this workspace" until the user does something that calls `forcePRStatusRefresh`.
- **Implicit join, silent miss.** `prStatusLabelForItem` (`internal/deckui/model.go:4475`) hides a two-branch lookup: PROverride → PR-number scan; else Bookmark → headRefName map. Every read site has to know about both. New code is one inattentive lookup away from another regression.
- **Two projection blocks for the same data.** `pr_status_job.go::convertGithubStatusesToDeckui` and the inline projection in `topUpMissingOverrides` both convert `github.PRStatus` → `deckui.PRStatus`. Adding the `awp review` write-through would be a third site if we don't consolidate.

## Scope

### In scope (v1)
- Add `workspace.Entry.PRNumber int` (JSON: `omitempty`).
- Rename `PROverride` → `PRNumber` (one field; the override use case is just "you set the PR number explicitly").
- Set `PRNumber` in every flow that knows the PR:
  - `awp review <N>` after `FetchPR` succeeds.
  - `B` (link bookmark) — resolve `gh pr list --head <bm>` once at link time; if a single PR matches, persist its number.
  - `p s` (current PROverride flow) — write to the same field.
  - New-workspace form when a bookmark is selected and a PR matches.
- One-time lazy migration on deck load: for each existing `Bookmark != "" && PRNumber == 0` entry, resolve via the current cache (bookmark → headRefName key lookup → PR number), persist `PRNumber`. No bookmark match → leave `PRNumber = 0` (entry behaves as it does today: no PR).
- Move the projection (`github.PRStatus` → `deckui.PRStatus` or a unified shared type) into `internal/prstatus` or equivalent. Both fetch paths call the same function; both write through `persistPRStatusMerge`.
- `awp review` writes its `FetchPR` result into the cache before returning. The deck's `refreshDoneMsg` pass sees a fully-populated cache.
- Extract the policy in `prStatusRefreshCmd` (which repos are stale, which to spawn) into a package-level pure function. The deck calls it from `refreshDoneMsg` (cheap; the spawn-or-no-op decision is centralized).
- Update `prStatusLabelForItem` and `prGlyphForItem` to a single lookup: `byPRNumber[entry.PRNumber]`. Delete the bookmark-fallback branch.
- Cache file format: continue keying `prs` by `headRefName` on disk (no schema rewrite). Build a secondary in-memory `byPRNumber` index at read time. Avoids a disk migration; keeps the cache human-inspectable.

### Out of scope (v1)
- A long-running pr-status daemon. The detached-job model stays; we just consolidate who decides to spawn.
- Changes to the github CLI dependency or the cache file location.
- Multi-PR-per-workspace support (the model is still one PR per workspace).
- UI changes beyond the `p s` chord prompt's existing copy. The `?` overlay copy stays the same.
- Removing the `Bookmark` field from `workspace.Entry` — it's still a useful jj concept for the new-workspace flow.

## UX

### CLI
- `awp review <N>` behavior unchanged from the user's perspective. Internally: writes through to PR cache before returning so the deck has data on next read.
- `awp w open` / new-workspace flow: when bookmark resolves to exactly one PR, the resulting workspace has `PRNumber` set silently. (Multiple PRs on the same head → leave unset; user can `p s` later.)

### TUI
- `p o`, `p r`, `p s` behavior unchanged.
- `p s` prompt copy and current "Pin PR #" header stay the same. (The underlying field rename is internal.)
- Help overlay copy: no changes.

## Discovery Questions
1. **First user**: Andrew (today). Anyone who runs `awp review` from a tmux pane outside the deck.
2. **When**: Every `awp review` invocation, every workspace open that's tied to a PR.
3. **Exact output**: `p o` opens the PR URL in the browser immediately after `awp review` returns. Glyphs render on the workspace row on the next refresh tick (~5s).
4. **Data sources**: `gh pr view` (review), `gh pr list` (deck periodic), `~/.awp/pr-status-cache.json`, `~/.awp/workspace-state.json`.
5. **Smallest useful slice**: `awp review` writes through the cache + reading the cache by PR number works end-to-end. Without the workspace-state rename, this fixes the immediate bug. Slice 1 below.
6. **Non-goals**: see "Out of scope".
7. **"Done"**: all three smells from the proposal are addressed and `p o` works the instant `awp review` returns. No regressions on `p r`, `p s`, glyph rendering, or bookmark linking.

## Spec Change Log
- 2026-05-27: Initial draft.
- 2026-05-27: Implemented all four slices in a single change. Notes:
  - The single projection helper lives in `internal/cli/pr_status_projection.go` (not a new `prstatus` package) since both callers (`pr_status_job.go`, `review.go`) are in `internal/cli` — avoided widening `deckui`'s dependency graph for one helper.
  - `workspace.Entry` keeps `PRNumber` (rename) plus a custom `UnmarshalJSON` that accepts the legacy `PROverride` key. Writes always emit `PRNumber`.
  - The bookmark-fallback branch in `resolvePRStatus` is retained as legacy code until every stale entry has been migrated (the deck's load-time migration runs on every startup; once we're confident the field is universal we can delete the branch).
  - `forcePRStatusRefresh` bypasses the new "must have PRNumber > 0" eligibility check entirely — direct user signals (`p s`, `B`, new-workspace from bookmark) always dispatch regardless of current item state.
  - `refreshDoneMsg` now invokes `prStatusRefreshCmd` so externally-created PR workspaces (`awp review` in another tmux pane) get their cache populated on the next state refresh tick. Throttled per repo via `prStatusMinInterval`, so already-fresh repos are a no-op.
  - `awp review` write-through additionally pins the new workspace to its PR number via `RecordPROverride` — even with an empty cache, the lookup is direct.

## Implementation Plan

The refactor is split into four slices. Each slice is independently reviewable and shippable; nothing breaks between slices. Order matters — earlier slices set up the data shape later ones depend on.

### Slice 1 — `awp review` writes through the cache (fixes the immediate bug)
Surgical change; no schema or rename work. Lets us ship the bug fix today while the larger refactor goes through review.

1. Extend `internal/github/github.go::FetchPR` to also fetch `state`, `isDraft`, `reviewDecision`, `statusCheckRollup`, `mergeStateStatus` in the same `gh pr view --json` call. Populate the new fields on `PRInfo`.
2. Add an exported projection helper (e.g. `github.PRStatusFromPRInfo`) that converts the augmented `PRInfo` → `github.PRStatus`. Reuses `rollupCIState`.
3. In `internal/cli/review.go::runReviewOpts`, after `FetchPR` succeeds and we know `repoRoot`, project to `deckui.PRStatus` and call `persistPRStatusMerge(map[string]map[string]deckui.PRStatus{repoRoot: {pr.HeadRef: status}}, time.Now())`. Log the write at debug level.
4. Add a unit test that runs `runReviewOpts` against a stubbed runner and asserts the cache file contains the PR keyed by `HeadRef` after return.

After slice 1: `p o` works post-review without any race. The implicit-join and projection-duplication smells still exist; we tackle them in slices 2–4.

### Slice 2 — One projection function
Eliminate the duplicate projection logic so slice 3 has one place to add a PR-number index.

1. Create `internal/prstatus/prstatus.go`. Move the canonical type (currently `deckui.PRStatus`) into this package. The deckui type becomes a type alias (`type PRStatus = prstatus.PRStatus`) for one release so callers compile unchanged.
2. Move `convertGithubStatusesToDeckui` and the inline projection in `topUpMissingOverrides` into `prstatus.FromGHStatus(s github.PRStatus) prstatus.PRStatus` (single PR) and `prstatus.FromGHStatuses(...)` (slice → map). Update both callers in `pr_status_job.go`. Same call shape, same output.
3. Update slice 1's new write-through to use the canonical projection.
4. Tests: move `pr_status_cache_test.go` cases to use `prstatus.PRStatus` directly. Add a round-trip test (github JSON → projection → cache → read → same shape).

### Slice 3 — `workspace.Entry.PRNumber`, migration, simplified lookup
The bigger structural change. Done after slice 2 so the lookup site only changes shape once.

1. Rename `workspace.Entry.PROverride` → `workspace.Entry.PRNumber`. Bump the schema version field (if there is one) or rely on JSON tag compatibility — `PROverride` becomes a deprecated read-only alias for one release. State-loading code reads either field; writes always go to `PRNumber`. Old state files keep working.
2. Update setters: `B`-link handler in deck, `p s` chord, new-workspace form when bookmark is provided, `awp review` after `FetchPR`. Each does a `gh pr list --head <bm>`-style resolution if the PR isn't already known and persists `PRNumber`.
3. Add a `byPRNumber` index to the in-memory cache representation. Build it lazily from the existing `prs` map at read time (no disk format change). `prStatusByRepo[repo].byPRNumber[N]` returns the same `PRStatus`.
4. Rewrite `prStatusLabelForItem` and `prGlyphForItem` to one path: `if item.PRNumber > 0 { byPRNumber[item.PRNumber] }`. Drop the bookmark-fallback branch and the PROverride scan.
5. One-time lazy migration in the deck's state-load path: for each `Bookmark != "" && PRNumber == 0`, look up the bookmark in the current cache; if found, persist `entry.PRNumber = status.Number`. No match → leave at 0. Migration runs once per entry (idempotent — no-op if `PRNumber` already set).
6. Tests: bookmark-only legacy entry → after first deck load, `PRNumber` populated; round-trip preserves it. Item with `PRNumber > 0` and missing bookmark → `p o` still resolves.

### Slice 4 — Centralize fetch policy
The deck stops being the orchestrator.

1. Extract `prStatusRefreshCmd`'s policy into `prstatus.PoliciesForState(entries, cache, now) []FetchTarget` — pure function, no model dependency.
2. Replace the call sites in `model.go:971` (`initKickMsg`), `model.go:2341` (link bookmark), and `forcePRStatusRefresh` with the extracted function.
3. Add a `refreshDoneMsg` call site so newly-eligible repos (workspace appeared from outside the deck) get fetched on the next state-refresh cycle. Throttling is enforced inside the policy function, not by callers, so this is free for already-fresh repos.
4. Replace the "Path != RepoRoot" eligibility heuristic in `prStatusRepos` with "entry has PRNumber > 0". A repo is eligible iff some entry under it has a PR. Simpler, accurate, no more "default workspace skipped" edge case.
5. Tests: synthetic state with two repos (one fresh, one stale) → policy returns only the stale one. Workspace added between calls → next policy call includes its repo. Workspace removed → policy stops returning it.

## Acceptance Criteria
- [ ] `awp review <N>` followed immediately by `p o` in the deck opens the PR URL. No "no PR for this workspace" status, no waiting on a periodic refresh.
- [ ] `prStatusLabelForItem` has one lookup path (`byPRNumber[entry.PRNumber]`), no bookmark fallback, no PROverride scan.
- [ ] One projection function from `github.PRStatus` → cache shape, used by all three fetch paths (`gh pr list`, `gh pr view` topup, `awp review` write-through).
- [ ] Legacy workspace state with `Bookmark` set and no `PRNumber` works on first deck load post-upgrade. The migration populates `PRNumber` from the current cache when the bookmark matches a PR; entries without a match continue to function (no PR-related rendering or chords for them).
- [ ] `PROverride` JSON field still readable for one release; writes go to `PRNumber`. Removing the alias is a separate spec.
- [ ] `prStatusRepos`/policy function returns repos with `entry.PRNumber > 0`, not "non-default workspaces."
- [ ] `?` help overlay text is unchanged; `p o`/`p r`/`p s` UX is unchanged.
- [ ] All existing tests pass. New tests cover: (1) `awp review` write-through; (2) projection round-trip; (3) `PRNumber` migration from bookmark; (4) policy function correctness.

## QA / Human Review Test Plan

### Setup
- [ ] `awp`, `jj`, `tmux`, `gh` available and authenticated.
- [ ] A repo with at least three open PRs, one from a fork (mirrors current `andrewcohen/awp` state).
- [ ] Clean `~/.awp/pr-status-cache.json` (or backed up).
- [ ] Working copy at the spec's parent commit.

### Core Happy Path
- [ ] From a clean cache state, open the deck on a repo with no review workspaces. Confirm the repo is not in the cache (because no workspace has `PRNumber > 0`).
- [ ] In another tmux pane, run `awp review 1`. Confirm the workspace lands.
- [ ] In the deck: cursor onto the new `pr-1-...` workspace. Press `p o`. Browser opens the correct PR URL within a couple seconds — no "no PR for this workspace."
- [ ] Press `p r` on a PR with merge conflicts → repair prompt dispatched to agent.
- [ ] Press `p s` and pin a different PR number → glyph and label update to the pinned PR.
- [ ] Press `B` to link a bookmark to a non-review workspace → if the bookmark maps to a PR, the PR glyph renders without a manual refresh.

### Edge Cases & Failure Modes
- [ ] Fork PR (head repo != origin): cache write succeeds, `p o` works.
- [ ] Workspace with `Bookmark` set but no matching open PR → `p o` reports "no PR for this workspace" (same as today).
- [ ] Workspace with `PRNumber = 0` and `Bookmark == ""` → no PR glyph, no chord routes, no panic.
- [ ] Two workspaces pointing at the same PR number (one via `p s`, one via review) → both render the same glyph and resolve the same URL.
- [ ] Cache file deleted while deck is running → next refresh repopulates; `p o` works after the refresh, fails gracefully ("no PR") before it.

### Regression Checks
- [ ] Existing workspaces with `Bookmark` but no `PROverride` from a pre-refactor `workspace-state.json` migrate on first deck load: `PRNumber` gets populated where the bookmark matches a cached PR.
- [ ] Existing workspaces with `PROverride` set (pre-rename) → `PRNumber` reads the legacy field on load and writes back canonically.
- [ ] Deck row rendering (glyphs, labels, sidebar PR title) identical to pre-refactor for unchanged workspaces.
- [ ] `p s` clear-by-blank still works.
- [ ] Periodic pr-status fetch still throttles to once per minute per repo.
- [ ] `?` overlay renders correctly.

### Reviewer Notes
- Capture before/after `~/.awp/workspace-state.json` for at least one workspace to verify the migration path.
- Note any change in `gh pr view` shell-out counts during a review run (we expect one for FetchPR, not two).

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
- [ ] Manual smoke: walk through Core Happy Path on `andrewcohen/awp`.
