# Deck Global Activity Bar

## Metadata
- **Spec ID**: `20260513-lbkj`
- **Feature name**: Deck global activity bar
- **Owner**: Andrew
- **Status**: Discovery
- **Last updated**: 2026-05-13

## Goal
When the deck is doing background work, the user can see a compact, always-on
indicator of what is currently in flight. Today the deck looks frozen during a
multi-repo PR-status refresh on first open; nothing in the UI says "we are
talking to gh for 5 repos right now."

## User Problem
The deck dispatches a fan of background commands on open (PR status per repo,
enrichment, etc.) and on demand (project discovery, bookmark fetch, PR list
for review, workspace create/delete/rename/link). Each one currently lands as
a one-shot done message that either updates a row, prints a transient status
toast, or silently mutates state. While they are in flight there is no
feedback. Slow repos make the deck feel broken on cold open.

We want a single global "activity" surface that names what is running, and a
disciplined start/finish lifecycle so future async commands plug in without
re-inventing UX.

## Scope
### In scope (v1)
- New `Activity` value type (`ID string`, `Label string`, `Done int`,
  `Total int`) and an `activities []Activity` field on `deckui.Model`,
  ordered by start time.
- Model helpers `m = m.startActivity(id, label, total)` /
  `m = m.tickActivity(id, doneDelta)` / `m = m.finishActivity(id)`. No new
  message types just for activity bookkeeping — the existing done messages
  drive `finishActivity`.
- Render an "activity" segment in the bottom status bar
  (`internal/deckui/jobs.go::composeStatusBar`). Format:
  `⠼ pr-status 2/5 · enrich · projects`. The deck's existing
  `m.spinner` provides the leading glyph. Empty when no activities are
  running.
- Wire activities into the background commands that are not already
  fronted by a modal "loading…" subtitle (see Decision 5):
  - `pr-status` — single activity for the whole fan-out, with `Total = N`
    repos and `Done` incremented per repo. Requires refactoring
    `PRStatusFetcher` so it emits a per-repo intermediate message
    (`PRStatusRepoDoneMsg{Repo, ByHead, Err}`) followed by a final
    `PRStatusDoneMsg{FetchedAt}`. The model uses the per-repo messages to
    update both glyphs and the activity counter incrementally.
  - `enrich` — `Refresher` invocation from `Init` and from user-initiated
    refreshes only (not the periodic `refreshTickMsg`); finished on
    `refreshDoneMsg`.
  - `workspace:<action>` (`create`, `delete`, `rename`, `link`) — handler
    dispatch, finished on `actionResultMsg` / `NewWorkspaceDoneMsg` /
    `StateEditDoneMsg`.
- A 500ms `✓ <label>` completion flash; an `activityExpireMsg{id}`
  scheduled via `tea.Tick(500ms)` from `finishActivity` removes the entry
  and triggers a repaint.
- Update `?` help overlay (`renderHelp` in `internal/deckui/model.go`) and
  README to mention the activity indicator alongside the jobs counts.
- Layout discipline in `composeStatusBar`: when width is tight, drop
  segments in order `hint → activities → jobs counts → right status`. The
  right segment (filter/find/error toast) is the highest-priority and is
  preserved last.

### Out of scope (v1)
- Percentage progress bars. `N/M` is the only progress representation.
- A historical activity log (post-completion). Activities disappear the
  instant they finish.
- Cancellation of in-flight activities.
- Tracking spinner ticks, `refreshTickMsg`, `stateWatcher` events, or any
  cmd that is not a discrete user-meaningful operation.
- Showing per-repo names inside the `pr-status` activity (just the count).
- Surfacing per-repo PR-status errors in the activity bar; errors continue
  to land in `m.status` as today.
- Activities for `bookmarks`, `projects`, and review-mode `pr-list`
  fetches — those modals own the screen and already render their own
  `loading…` subtitle (Decision 5).

## UX
### CLI
- No CLI changes.

### TUI
- Bottom status bar gains a new segment between the jobs counts and the
  right-aligned status text:

  ```
  ▶ 2  ⠼ pr-status 2/5 · enrich   filter: "foo" · ready                    ? help
  └ jobs└─ activities (new) ──────┘└─ right segment ────────────────────┘└ hint ┘
  ```

- Activity segment styling: dim foreground (color `245`), spinner colored
  to match `m.spinner`, separator ` · ` between activities.
- When the deck is otherwise idle (no jobs, no activities, no filter,
  default status) the bar reads exactly as today.
- The `?` help overlay gains one line in the legend: `⠼ activity (background
  fetch / refresh)`.

## Decisions (locked 2026-05-13)
1. **Completion feedback**: finished activities flash `✓ <label>` in the
   bar for ~500ms before disappearing. Implementation: each activity gets
   an optional `finishedAt time.Time`; the render skips it once
   `now - finishedAt > 500ms`, and `finishActivity` schedules a
   `tea.Tick(500ms)` that emits a `activityExpireMsg{id}` to drop it from
   the slice and trigger a final repaint.
2. **Slow-repo callout**: always render `pr-status N/M`. No special case
   for the last-remaining repo. Revisit if a hung `gh` call becomes a
   recurring support question.
3. **Position**: activities are the single bottom-bar segment for
   in-flight background work. Layout left to right: activities · right
   status · `? help`. Drop order under width pressure: hint → activities
   → right status.
4. **Periodic enrichment ticks**: the `enrich` activity only registers on
   the initial `Init` pass and on user-initiated refreshes. The 5s
   `refreshTickMsg` heartbeat runs silently so the bar stays empty during
   steady-state.
5. **Modal-owned fetches**: skip global activities for `bookmarks`,
   `pr-list` (review-mode), and `projects`. The picker modal already
   renders its own `loading…` subtitle, so a duplicate global indicator
   is noise.
6. **Jobs are activities (added during implementation)**: rather than
   keeping the separate `▶ N · ⚠ N · ☠ N` aggregate JobCounts tray, every
   async job is projected into the activity slice as `job:<id>` (label
   from `Job.Title`). Running/pending jobs use the spinner glyph; failed
   jobs render with `⚠` (red) and orphans with `☠` (orange) and stay
   visible until dismissed via the `J` overlay. Clean-terminal jobs
   (`done`, `cancelled`) flash `✓` for 500ms and disappear via the
   standard expiry tick. This means the spec's `workspace:create:<name>`
   and `workspace:delete:<name>` explicit activity IDs are unused — the
   underlying jobs already drive activity entries. Only `workspace:rename`
   and `workspace:link` still register explicit activities, since they
   don't go through the jobs subsystem.

## Spec Change Log
- 2026-05-13: Initial draft.
- 2026-05-13: Locked discovery decisions (see Decisions section).
- 2026-05-13: Implementation merged async-jobs counts into the activity
  facility (see Decision 6). The bottom bar no longer renders the
  separate `▶/⚠/☠` segment — each job appears as its own activity
  with status-derived glyph and color.

## Implementation Plan
1. **Activity type and model helpers** (no behavior change yet).
   Add `Activity` struct, `activities` slice, and `startActivity` /
   `tickActivity` / `finishActivity` methods on `Model`. Pure-function
   table tests in `model_test.go` cover ordering, dedup-by-ID, and the
   no-op cases (finish unknown ID, tick when not started).
2. **Render the activity segment.** Add `renderActivitiesCompact` in
   `internal/deckui/jobs.go`, extend `composeStatusBar` to accept it, and
   wire it into `View`. Tests cover empty, single, multi, and narrow-width
   drop order. `? help` legend updated in the same change.
3. **Refactor `PRStatusFetcher` to per-repo streaming.** Introduce
   `PRStatusRepoDoneMsg{Repo, ByHead, Err}`. The fetcher emits one such
   msg per goroutine completion via a `tea.Cmd` per repo, then a closing
   `PRStatusDoneMsg{FetchedAt}` when the fan-out completes (or times out).
   Update `internal/cli/deck.go`'s `prStatusFetcher` and the persisted
   cache merge to write incrementally per repo (or buffer until close —
   decide during implementation; cache writes are best-effort either way).
   Update `Update` to merge `ByRepo` from per-repo msgs and tick the
   `pr-status` activity.
4. **Wire activities to background commands.** For each command site
   in the wired set, call `startActivity` when dispatching and
   `finishActivity` in the matching done handler. Stable IDs:
   - `pr-status` (singleton, with `Total = N` repos)
   - `enrich` (singleton; only on Init / user-initiated refresh, not on
     periodic `refreshTickMsg`)
   - `workspace:create:<name>`, `workspace:delete:<name>`,
     `workspace:rename:<old>`, `workspace:link:<name>` (per-target)
   Modal-owned fetches (`bookmarks`, `pr-list`, `projects`) are
   intentionally excluded — see Decision 5.
5. **Docs.** Update README's "Deck" section to note the activity indicator.
6. **Manual QA + targeted unit tests.** Cover composeStatusBar with the
   new segment in narrow-width fallbacks; cover per-repo PR-status streaming
   end-to-end via a fake `PRStatusFetcher`.

## Acceptance Criteria
- [ ] Opening the deck against a config with 5 repos that have
      non-default workspaces shows `pr-status 0/5` immediately and
      decrements as each repo's `gh pr list` returns; the activity
      disappears when the last repo lands.
- [ ] If a single repo's `gh` call hangs to the 10s timeout, the activity
      bar still shows `pr-status 4/5` (or whatever count) for the duration,
      then disappears when the timeout fires.
- [ ] Creating, deleting, renaming, or linking a workspace shows a
      `workspace:<action>:<name>` activity until the handler returns,
      followed by a 500ms `✓` flash, then it disappears.
- [ ] Opening the project picker (`o`), bookmark link (`l`), or review
      (`r`) does NOT add a global activity — the modal's own
      `loading…` subtitle is the source of truth.
- [ ] The periodic 5s enrichment tick runs silently (no `enrich`
      activity in the bar during steady-state).
- [ ] When no background work is running, the bottom bar looks exactly as
      it did before this change (regression test on `composeStatusBar`).
- [ ] At width 60 the right-side filter/error text is still visible; the
      activity segment drops before the right segment.
- [ ] `?` help overlay lists the activity indicator.
- [ ] README mentions the activity indicator.

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `gh`, `tmux` available in PATH; `awp` built from this branch.
- [ ] Test config has at least 3 project repos, each with at least one
      non-default workspace, so PR-status fan-out is non-trivial.
- [ ] Optional: stub one repo to be slow (e.g. by pointing it at a path
      where `gh pr list` takes >2s) to validate incremental decrement.

### Core Happy Path
- [ ] `awp deck` cold open shows `⠼ pr-status N/N` immediately, decrements
      as repos return, flashes `✓ pr-status` for ~500ms, then disappears.
- [ ] Create a new workspace via the inline form → shows
      `workspace:create:<name>` until the handler completes, then flashes
      `✓ workspace:create:<name>` for ~500ms and disappears.
- [ ] Wait 10+ seconds idle on the deck → the bar stays empty (the 5s
      enrichment heartbeat does not register an activity).

### Edge Cases & Failure Modes
- [ ] All repos in cache (within 60s cooldown) on open → no `pr-status`
      activity registered, no flicker.
- [ ] A repo errors out (`gh` not configured) → `m.status` shows the
      error toast and the activity still decrements/finishes; the activity
      bar never gets stuck.
- [ ] Resize the terminal to width 50 mid-fetch → activity segment is the
      first thing dropped after the `? help` hint; right status remains.
- [ ] Run two background commands at once (e.g. PR-status refresh + open
      project picker) → both activities render with ` · ` separator.

### Regression Checks
- [ ] Filter mode (`/`), find mode, and quick-action mode still display
      their input/hint on the right of the bar.
- [ ] Async jobs surface in the bar as activities (one per job) with
      the correct glyph: spinner for running, `✓` + flash for clean
      terminal, `⚠` for failed, `☠` for orphaned. (No separate
      aggregate counts segment — see Decision 6.)
- [ ] PR glyphs on rows still appear at the same time as before
      (per-repo streaming should not regress glyph latency — it should
      improve it, since each repo's glyphs land as soon as that repo
      returns instead of waiting for the slowest peer).

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
