# Feature Spec: Deck read-model, model decomposition & store seam

## Metadata
- **Spec ID**: `20260710-l13c`
- **Feature name**: Deck read-model, model decomposition & store seam
- **Owner**: andrewcohen
- **Status**: Planned
- **Last updated**: 2026-07-10

## Goal
Make the deck tight, testable, and easy to change, and make persistence
swappable. Three coupled moves:

1. **Read-model** — extract the "assemble the deck's view of the world"
   logic (join workspace entries × jobs × PR status, filter by scope,
   classify inbox buckets, sort) out of `deckui.Model` and `cli/deck.go`
   into a dedicated package.
2. **Decompose the god model** — replace the 18+ orthogonal modal-state
   flags (and their sidecar fields) with a single active-modal abstraction
   made of self-contained sub-components, and collapse the ~20 injected
   callbacks into a small dependency interface.
3. **Store seam** — put every persisted store behind an interface so the
   JSON→SQLite decision becomes a later, low-risk swap.

This is **option B** from the architecture review: build the seams first,
defer SQLite to a follow-up spec.

## User Problem
The "user" is the maintainer (andrewcohen). Symptoms from the review:

- **`deckui.Model` is a god object.** `internal/deckui/model.go:923` —
  ~64 fields; `Update()` is ~1,400 lines (`model.go:2031`); **18+ modal
  flags** (`helpMode`, `findMode`, `reviewMode`, `prMenuMode`,
  `prNumberSetMode`, `pinChordMode`, `pinAliasMode`, `bookmarkMode`,
  `actionMode`, `progressMode`, `openMode`, `newWorkspaceMode`,
  `renameMode`, `promptMode`, `jobsOverlay`, `confirmDelete`,
  `confirmMergePR`, `filtering`) each with its own cursor/list/input/loading
  sidecar fields. Every new feature adds another flag + cluster, and every
  change touches the one struct and the one switch. State-flag combinations
  are effectively unbounded and untested.
- **Business logic lives in the TUI.** `resolvePRStatus`
  (`model.go:6676`), `prInboxBucket`, scope filtering, and deck sort live
  *inside* the model, so `unread`, `report_status`, and tests can't reuse
  them and can't be tested without the whole TUI apparatus.
- **The read model is split across packages.** `cli/deck.go`
  (`loadDeckItems`, `migrateBookmarkPRNumbersIfNeeded`, PR-cache
  persistence) and `deckui/model.go` (`items`, `itemsAll`,
  `resolvePRStatus`, `prInboxBucket`) both own pieces of it.
- **~20 raw callbacks** injected via `WithXxx` at `cli/deck.go:812` are the
  model's entire seam to the rest of the app — no interface, just a bag of
  function pointers held as fields.
- **Storage seams are inconsistent.** `workspace.Store`/`UpdaterStore`/
  `AllStore` (`workspace/service.go:97`) already abstract entries, but
  `jobs.Store`, the PR-status cache (`cli/pr_status_cache.go` + `deck.go`
  `persistPRStatusMerge`/`persistPRStatusBulkMerge`), and pin groups
  (`state/pin_groups.go`) are concrete. "Swap the backing store later" is
  only half-possible today, and the single-PR PR-cache write races with the
  bulk merge (unserialized).

## Scope

### In scope (v1)
- **Store interfaces** for the stores that lack them and are needed
  downstream:
  - `jobs.ReadStore` (list/get) consumed by the read model; the concrete
    `*Store` keeps its write methods and satisfies it.
  - `internal/prstatus` — the PR-status cache moved out of `internal/cli`
    into a lower package behind a `Cache` interface, and the single-PR vs.
    bulk-merge write race closed.
  - `state.PinGroupAliasStore` over the existing pin-group functions.
  - Reuse `workspace.Store`/`AllStore` unchanged.
- **`internal/deckdata` read-model package** owning the deck row view type,
  the entries×jobs×PR-status assembly, and the derivation logic currently
  split across `deckui` and `cli` (`resolvePRStatus`, `prInboxBucket`,
  bookmark→PR-number migration, scope filter, sort, pin grouping). No
  `tea`/`lipgloss`/`cli`/`deckui` imports.
- **Model decomposition** in `deckui`:
  - Replace the 18+ modal bools + their sidecar fields with a single
    `activeModal` abstraction: one interface (`update(msg) (modal, cmd,
    action)` / `view(width) string`, per the existing house convention for
    `jobsOverlay`/`confirmDelete`/`newMenuMode`) and a small set of
    concrete modal structs. `Model` holds at most one active modal; `View`
    and `Update` delegate to it.
  - Collapse the ~20 `WithXxx` callbacks into a small `deckui.Deps`
    interface (grouped by concern: workspace actions, PR data, jobs,
    project nav, persistence) backed by the `cli` wiring.
  - Net target: `Model` drops from ~64 fields to a data view + a modal
    slot + a `Deps` + genuine cross-cutting UI state (spinner, activity
    bar, dimensions); `Update()` shrinks to row-mode dispatch + delegate to
    the active modal.
- **Tests**: `deckdata` join/classification tests (no TUI, fake stores);
  per-modal sub-component tests where the modal carries non-trivial logic.

### Out of scope (v1)
- **SQLite.** No new backend. This spec builds the seam; the JSON→SQLite
  swap is a follow-up spec implementing the same interfaces.
- **Picker dedup** — the bookmark/review/open pickers share near-identical
  filter/enter/esc handling. The modal decomposition makes each a
  sub-component; *unifying* them into one generic picker is a natural
  follow-on but not required for v1 (they may stay three structs that each
  satisfy the modal interface).
- **On-disk format / path / `~/.awp/` layout changes.** None.
- **User-visible behavior changes.** None — this is behavior-preserving.

### On phasing vs. scope
Everything in "in scope" ships, but as separate reviewable phases (below).
Phase 1–2 deliver the read model + seam; Phase 3–4 decompose the model.
Each phase compiles, tests, and preserves behavior on its own, so a phase
can be deferred at review without stranding the others.

## UX
### CLI
- No change. Same commands, flags, output.

### TUI
- No change. Same rows, scopes, buckets, sort, keys, modals, help overlay.
  Acceptance bar: **indistinguishable from the pre-refactor binary.**

## Discovery Questions
1. **First user?** The maintainer; internal quality work.
2. **When used?** Every future deck change — payoff is lower friction and
   fewer latent-state bugs.
3. **Exact result?** `deckdata` owns the read model; stores sit behind
   interfaces; `Model` holds one active modal + a `Deps` instead of 18
   flags + 20 callbacks; zero behavior change.
4. **Data sources?** Existing stores: workspace entries, jobs, PR-status
   cache, pin-group aliases.
5. **Smallest useful slice?** Phase 1 alone unblocks the SQLite decision;
   Phase 2 delivers testability; Phase 3 delivers the god-model win.
6. **Non-goals?** SQLite, picker unification, format changes.
7. **"Done"?** `deckdata` tested without a TUI; `Model` field count and
   `Update()` length materially down; one active-modal slot replaces the
   flags; a `Deps` interface replaces the callback bag; deck behaves
   identically; lint/test/vet/build clean.

## Target architecture

```
internal/
  workspace/   lifecycle Service + Store/UpdaterStore/AllStore (EXISTS, reuse)
  jobs/        *Store (EXISTS) + new ReadStore interface (list/get)
  state/       JSONStore + pin groups (EXISTS) + PinGroupAliasStore iface
  prstatus/    NEW — PR-status cache moved out of cli, behind Cache iface
  deckdata/    NEW — read model: DeckView assembly + joins/filter/sort/buckets
               (no tea/lipgloss/cli/deckui imports)
  deckui/      TUI only — DeckView + one activeModal + a Deps interface
  cli/         wiring — constructs concrete stores, builds Deps, injects them
```

Import direction (extend the existing `depguard` rule):
`cli → deckui → deckdata → {workspace, jobs, state, prstatus}`. `deckdata`
imports neither `cli` nor `deckui` nor Bubble Tea; `prstatus` does not
import `cli`.

### Modal abstraction sketch
```go
// deckui: one interface, one slot, replacing 18 bools + sidecar fields.
type modal interface {
    update(m *Model, msg tea.Msg) (modal, tea.Cmd, modalAction)
    view(width int) string
    // key bindings this modal contributes to the ? overlay
    helpBindings() []key.Binding
}
type Model struct {
    view      deckdata.DeckView // read model (Phase 2)
    active    modal             // nil = row mode (Phase 3)
    deps      Deps              // was ~20 callbacks (Phase 4)
    // + genuine cross-cutting UI state: spinner, activities, width/height, filter
}
```
`modalAction` is the existing sub-component return convention (close /
run-command / propagate). Row mode is `active == nil`. Existing
sub-components (`jobsOverlay`, `confirmDelete`, forms) are refactored to
satisfy `modal` rather than being addressed by their own bool.

## Implementation Plan

Phased; each phase is a separate change that compiles, passes tests, and
preserves behavior.

### Phase 1 — Store interfaces (pure seam, no logic moves)
1. Add `jobs.ReadStore` (`List`/`Get`); `*Store` already satisfies it.
2. Create `internal/prstatus`: move `cli/pr_status_cache.go` and the
   `loadPRStatusCache`/`invalidatePRStatusCacheRepo`/`persistPRStatusMerge`/
   `persistPRStatusBulkMerge` logic out of `cli/deck.go` behind a `Cache`
   interface; route single-PR and bulk writes through one lock to close the
   race.
3. Add `state.PinGroupAliasStore` over the existing load/save functions.
4. Repoint `cli` wiring at the interfaces; extend `depguard`. No behavior
   change.

### Phase 2 — `deckdata` read model
5. Create `internal/deckdata`; move the deck row view type there from
   `deckui`.
6. Move join/derivation into `deckdata`: `resolvePRStatus`,
   `prInboxBucket`, `migrateBookmarkPRNumbersIfNeeded`, scope filter, sort,
   pin grouping. Add an assembler taking the Phase-1 interfaces →
   `DeckView`.
7. Rewire `cli/deck.go` `loadDeckItems` and `deckui.Model` to consume
   `deckdata`; delete the now-dead methods from both.
8. Unit-test `deckdata`: PR resolution by PRNumber and by
   bookmark→headRefName; bucket classification per status; scope filter;
   sort order; pin grouping; bookmark→PR-number migration.

### Phase 3 — Modal decomposition (the god-model core)
9. Introduce the `modal` interface + `modalAction` and refactor the
   existing sub-component modals (`jobsOverlay`, `confirmDelete`,
   `confirmMergePR`, forms) to satisfy it.
10. Convert each remaining mode (bookmark, review, open, find, progress,
    prMenu, prNumberSet, pinChord, pinAlias, action, help, prompt) into a
    struct satisfying `modal`. Move its sidecar fields (cursor/list/input/
    loading) off `Model` and onto the modal struct.
11. Replace the 18+ bools with a single `active modal` slot; rewrite the
    `Update` modal branches and `View` to delegate to `active`. Row mode is
    `active == nil`.
12. Point the `?` help overlay (`renderHelp`/`deckKeyGroups`) at
    `active.helpBindings()` so the keymap stays in sync automatically.
13. Add sub-component tests for modals with non-trivial logic (find-mode
    hint stepping, progress log, pickers).

### Phase 4 — Callback collapse (Deps interface)
14. Group the ~20 `WithXxx` callbacks into a `deckui.Deps` interface by
    concern (workspace actions, PR data, jobs, project nav, persistence).
15. Implement `Deps` in the `cli` wiring; replace the callback fields and
    the `WithXxx` builder chain with a single injected `Deps`.

### Deferred — SQLite (separate spec)
Implement the Phase-1 interfaces with a SQLite backend; switch only the
`cli` wiring. Success of *this* spec is measured partly by whether that swap
touches only the wiring + one new package — `deckdata`/`deckui` untouched.

## Acceptance Criteria
- [ ] `internal/deckdata` exists, owns the deck row view type and
      join/filter/sort/bucket logic, and imports neither `internal/cli`,
      `internal/deckui`, `bubbletea`, nor `lipgloss`.
- [ ] `jobs.ReadStore`, `prstatus.Cache`, and `state.PinGroupAliasStore`
      interfaces exist; `deckdata` depends on interfaces, not concrete
      stores.
- [ ] PR-status cache no longer lives in `internal/cli`; the single-PR vs.
      bulk-merge write race is closed.
- [ ] `deckui.Model` and `cli/deck.go` no longer contain `resolvePRStatus`,
      `prInboxBucket`, scope filter, or deck sort (delegated to `deckdata`).
- [ ] The 18+ modal bools are replaced by a single `active modal` slot;
      per-modal sidecar fields live on the modal structs, not `Model`.
- [ ] The ~20 `WithXxx` callbacks are replaced by a single `Deps`
      interface.
- [ ] `deckui.Model` field count and `Update()` length are materially
      reduced — report before/after numbers in the change description.
- [ ] `deckdata` has TUI-free unit tests (fake stores); non-trivial modals
      have sub-component tests.
- [ ] The `?` help overlay is driven by the active modal's bindings and
      stays in sync.
- [ ] The deck is behaviorally identical: same rows, scopes, buckets, sort,
      keys, modals (verified per QA plan).
- [ ] `depguard` extended to enforce the new import boundaries.

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `tmux`, and a built `awp` binary in PATH.
- [ ] Test environment with ≥2 repos; several workspaces; ≥1 with a linked
      PR; a waiting/unread agent; a pinned workspace; ≥1 background job
      (running + done).
- [ ] Snapshot `~/.awp/workspace-state.json`, `~/.awp/jobs/`,
      `~/.awp/pr-status-cache.json`, `~/.awp/pin-groups.json` before/after
      to confirm formats unchanged.

### Core Happy Path
- [ ] `awp deck` renders the same rows, order, PR glyphs, and status dots as
      the pre-refactor binary.
- [ ] Cycle scopes (`P`): identical filtering.
- [ ] Inbox buckets classify identically.
- [ ] Pinned workspaces float to the top under the correct register.
- [ ] Every modal opens/closes and behaves identically: help (`?`), jobs
      (`J`), find, bookmark/review/open pickers, new/rename/prompt forms,
      confirm-delete, confirm-merge, pr menu, pr-number set, pin chord/alias,
      action menu.

### Edge Cases & Failure Modes
- [ ] Bookmark without `PRNumber` still resolves its PR via the bulk cache.
- [ ] Legacy `PROverride` state files still load.
- [ ] Empty state (`~/.awp` absent) → deck opens with no rows, no crash.
- [ ] Concurrent write: `report_status` (agent hook) during a deck poll →
      no dropped writes; single-PR PR-cache write during a bulk fetch no
      longer races.
- [ ] Opening a modal, resizing the terminal, then closing it repaints
      correctly (modal delegation handles `WindowSizeMsg`).

### Regression Checks
- [ ] `awp unread`, `awp report-status`, `awp review` still work.
- [ ] Background job spawn → running → done still updates tray + overlay.
- [ ] Deck refresh (5s tick + fsnotify) still reflects external changes.
- [ ] `?` help lists the same bindings as before, per active context.

### Reviewer Notes
- Record `deckui.Model` field count and `Update()` line count before/after.
- Note any logic/state that resisted extraction (and why), documenting the
  seam boundaries for the SQLite follow-up.

## Validation
- [ ] `mise exec -- gofmt -l .` (no output)
- [ ] `mise exec -- golangci-lint run ./...` (`0 issues`)
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`

## Spec Change Log
- 2026-07-10: Initial draft (option B from the architecture review):
  read-model extraction + store interfaces, SQLite deferred.
- 2026-07-10: Expanded to include god-model decomposition as first-class
  in-scope work — modal-flag collapse into a single active-modal
  abstraction (Phase 3) and callback collapse into a `Deps` interface
  (Phase 4). Picker *unification* and SQLite remain deferred.
