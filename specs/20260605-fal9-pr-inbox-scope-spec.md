# Feature Spec: PR Inbox Scope

## Metadata
- **Spec ID**: `20260605-fal9`
- **Feature name**: PR inbox scope (replaces the open-PR deck scope)
- **Owner**: andrewcohen
- **Status**: In Progress (implementation landed; human QA pending)
- **Last updated**: 2026-06-05

## Goal
When the deck is scoped to PRs, rows are grouped by *what your next move
is* — like GitHub's pull request inbox — instead of by project. One
glance at the scope answers "what should I act on?" without scanning
glyphs row by row.

## User Problem
The current `P` scope cycle (`all → attention → open PR`) filters to
workspaces with a non-draft open PR but still groups by project and
alphabetizes. The status signals (review requested, changes requested,
CI failing, approved) live in per-row glyphs, so triaging means reading
every row. GitHub's inbox solves this with status sections ("Needs your
review", "Ready to merge", …); the deck already caches every field
needed to do the same.

## Scope
### In scope (v1)
- Replace `ScopeOpenPR` with `ScopeInbox`. `P` still cycles three
  scopes: `all → attention → inbox`.
- Bucket classification over the cached `PRStatus` (no new fetch — the
  fields all exist: `Mine`, `ReviewRequested`, `ReviewRerequested`,
  `ReviewDecision`, `CIState`, `MergeStateStatus`, `IsDraft`,
  `IsInMergeQueue`).
- Buckets in action-first order (most urgent next-move on top):
  1. **Needs your review** — `ReviewRequested || ReviewRerequested`
     (someone else's PR, your move).
  2. **Needs action** — `Mine` && open && (changes requested || CI
     failing || merge conflicts || behind base).
  3. **Ready to merge** — `Mine` && open && !draft && approved && CI
     passing-or-none && merge state clean, or `IsInMergeQueue`.
  4. **Waiting for review** — `Mine` && open && !draft && none of the
     above (review pending, CI pending counts here too).
  5. **Your drafts** — `Mine` && open && `IsDraft`. (New: today's
     open-PR scope excludes drafts entirely.)
  6. **Other open PRs** — catch-all for open PRs that are neither mine
     nor awaiting my review (e.g. a coworker's branch checked out
     locally). Keeps every workspace the old scope showed reachable.
- Bucket headers render with counts — `Needs your review (3)` — in the
  slot project headers use today. Empty buckets are hidden (no `(0)`
  rows).
- Rows within a bucket sort by (project, display label). Each row gains
  a `[project]` prefix chip (mini-deck pattern) since project headers
  are gone in this scope.
- Row source unchanged: **workspaces only**. A PR with no local
  workspace does not appear.
- Merged/closed PRs stay excluded from the scope, as today.
- README + `?` help/keymap text updates (`cycle scope (all → attention
  → inbox)`).

### Out of scope (v1)
- **Workspace-less PRs in "Needs your review" (planned v2).** The
  bucket should eventually list review-requested PRs you haven't
  pulled down yet — the status cache already holds them. Those rows
  need a distinct "no workspace yet" rendering, and `enter` on one
  should trigger the review flow (`awp review <n>`: create the review
  workspace, prime the agent) instead of summoning a session. Decided
  2026-06-05 to land the workspaces-only pass first and layer this on
  once the bucketed layout is right.
- "Needs your team's review" (requires team-membership data we don't
  fetch).
- Re-bucketing the review picker (`R`) — it stays a flat recency list.
- Per-bucket collapse/expand.

## UX
### CLI
- No new commands or flags. Anything that sets the initial scope
  (`WithInitialScope`) keeps working with the renamed scope.

### TUI
- `P` cycles `all → attention → inbox`; status line shows
  `scope: inbox`.
- In inbox scope the body reads:

  ```
  Needs your review (2)
    ● #412 fix checkout race            [shop-api]
         󰻞 @teammate · fix/checkout-race
    ● #398 add rate limiter             [gateway]
         󰻞 @teammate · feat/rate-limit

  Needs action (1)
    ● #405 migrate orders table         [shop-api]
         󰭹  @andrewcohen · andrew/orders-migration

  Waiting for review (3)
    …
  ```

- Cursor movement, meta lines, and per-row PR glyph clusters are
  unchanged — only grouping/headers/sorting differ.
- Find mode (`f`) skips the project stage in this scope — there are no
  project headers to hint, so every row is hinted directly (mini-deck
  style, hint names project-qualified to avoid collisions). Backspace
  cancels rather than returning to a project stage that never ran.
- The collapsed default-only-project row treatment does not apply in
  inbox scope (collapse is a project-grouping concept).

## Discovery Questions
1. **Who is the first user?** andrewcohen, triaging PRs across several
   active repos from the deck.
2. **When do they use this feature?** Start of a work block: "what's my
   next move across all open PRs I have workspaces for?"
3. **What exact output/result do they need?** Deck rows sectioned by
   next-move buckets with counts, most urgent first.
4. **What data sources are required?** Existing `prStatusByRepo` cache
   (1-min throttled `gh pr list` fetch). No new fetches.
5. **What is the smallest useful slice?** Re-sort + re-header the
   existing open-PR scope by bucket. Everything else (chips, drafts)
   layers on.
6. **What are explicit non-goals?** Workspace-less PR rows; team review
   requests; review picker changes.
7. **What does "done" look like?** `P` lands on `inbox` and the deck
   shows bucketed sections matching GitHub's inbox semantics for the
   same PRs.

## Spec Change Log
- 2026-06-05: Initial draft. Decisions: workspaces-only row source;
  action-first bucket order; hide empty buckets; include drafts;
  `[project]` chips replace project headers in this scope.
- 2026-06-05: Marked workspace-less "Needs your review" rows as planned
  v2 with enter-triggers-review semantics (user decision: get the
  workspaces-only pass right first).
- 2026-06-05: Implementation notes. `--scope` keeps `pr` / `open-pr`
  as parse aliases for `inbox` (docs advertise `inbox` only). The
  inbox filter resolves PRs via `resolvePRStatus`, so a pinned
  `PRNumber` (from `awp review`) qualifies even with no bookmark on
  file — a superset of the old bookmark-only filter. Find mode is
  single-stage in this scope (see UX). Scroll-math helpers now take
  precomputed `deckBodyRows` so renderer and scroll math share one
  layout call.

## Implementation Plan
1. **Bucket classifier** — `prInboxBucket(s PRStatus) inboxBucket`
   (enum in bucket order) + `inboxBucketLabel`. Pure function over
   `PRStatus`; table-driven tests covering each predicate and the
   priority between them. Proposed precedence inside "mine": draft
   check before CI/decision checks (a draft isn't submitted for review
   yet, so its CI state is informational) — lock the choice in tests
   with comments.
2. **Scope rename** — `ScopeOpenPR` → `ScopeInbox`, label `inbox`,
   update `ParseScope`/`scopeLabel`/keymap help string and any
   `WithInitialScope` callers in `internal/cli`.
3. **items() ordering** — in inbox scope, filter to workspaces with an
   open-PR cache hit (now *including* drafts), sort by (bucket,
   project, label). Keep the existing alphabetical sort for other
   scopes.
4. **Row assembly** — `deckBodyRows` gains a grouping mode: in inbox
   scope the header rows carry `inboxBucketLabel + " (n)"` instead of
   project names, and the collapsed-project path is bypassed. Sticky
   header logic reuses `headerRow` unchanged.
5. **Row rendering** — `[project]` chip prefix on primary rows in inbox
   scope, reusing the mini-deck chip style.
6. **Docs** — README scope section + key table; `?` help text via
   `deckKeyGroups`.

## Acceptance Criteria
- [x] `P` cycles `all → attention → inbox`; the old `open PR` label is
      gone everywhere (status line, help, README).
- [x] Inbox scope groups rows under bucket headers with counts, in
      action-first order; empty buckets don't render.
- [x] Draft PR workspaces appear under "Your drafts" (they were
      invisible in the old open-PR scope).
- [x] Every workspace visible in the old open-PR scope is still visible
      in inbox scope (catch-all bucket holds non-mine,
      non-review-requested open PRs).
- [x] Rows show a `[project]` chip; meta lines and glyph clusters are
      unchanged.
- [x] Bucket classification has table-driven unit tests; row assembly
      has a grouping test.
- [x] Cursor/easymotion/find work across bucket sections (find is
      single-stage in this scope by design — see UX). Manual pass
      still owed in QA.

## QA / Human Review Test Plan
### Setup
- [ ] Repos with a mix of PR states: yours (approved/clean, changes
      requested, CI failing, draft, awaiting review) and a coworker's
      PR with your review requested, each with a local workspace.
- [ ] `gh` authenticated; PR status cache warm (open deck, wait one
      fetch).

### Core Happy Path
- [ ] Press `P` twice from `all` → status line reads `scope: inbox`.
- [ ] Buckets appear in order with correct counts and membership
      matching github.com's inbox for the same PRs.
- [ ] Select a row in each bucket; enter/summon behaves as in `all`.

### Edge Cases & Failure Modes
- [ ] Cold cache (no PR status yet): inbox scope shows the empty-state
      message, not a crash; rows appear after the first fetch lands.
- [ ] A PR moving state (e.g. approve it on GitHub) re-buckets after
      the next fetch without restarting the deck.
- [ ] All workspaces in one bucket → single header, no spacer artifacts.
- [ ] Narrow terminal: chips + labels truncate, no wrapping.

### Regression Checks
- [ ] `all` and `attention` scopes render exactly as before (project
      headers, collapse, sort).
- [ ] Mini-deck unaffected.
- [ ] Sticky header pins the bucket header in inbox scope while
      scrolling.

### Reviewer Notes
- Capture a screenshot of inbox scope next to github.com's inbox for
  the same account/day.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
