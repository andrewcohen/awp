# Feature Spec: hook-authoritative dev-loop status cache

## Metadata
- **Spec ID**: `20260714-v8pb`
- **Feature name**: hook-authoritative dev-loop status cache
- **Owner**: acohen
- **Status**: In Progress (implemented — pending review)
- **Last updated**: 2026-07-14

## Goal
While an agent's dev-loop hooks are firing, keep the full dev-loop status
(phase, done/total, current unit, gates) live in the cached
`DevLoopSnapshot` so the deck renders the *current* state instantly on open —
including across phase/unit switches — without waiting on a transcript scan.
Fold in a correctness fix so gate results reset on every unit boundary, not
only when the agent explicitly marks a task `in_progress`.

## User Problem
The deck persists a `DevLoopSnapshot` for fast first paint (`deck.go:1342`),
but only part of it is hook-written today:

| Field | Written by | Fresh on open when hook-bound? |
|-------|-----------|--------------------------------|
| `Gates` | `awp gate record` (PostToolUse Bash) | yes |
| `UnitKey`, `Task` | `awp gate check` (PreToolUse TaskUpdate→in_progress) | yes |
| `Phase`, `Done`, `Total` | **only the transcript scan at open** (`deck.go:1360`) | **no** |

So on open the gates and current unit are instant, but **phase and progress
render from whatever the last transcript scan left behind** — stale across a
phase switch until the background scan finishes (bounded by
`deckEnrichTimeout`). The hooks already fire on the exact `TaskCreate` /
`TaskUpdate` events that move phase and done/total; they just don't write
those fields.

### Companion bug — gates don't reset across a unit boundary
Gate reset lives only in `resetGateUnit`, called only from
`runGateCheckHook`'s `case "in_progress"` (`gate.go:273`). It fires only when
a `TaskUpdate(status=in_progress)` with a new `taskId` arrives. But
`gate record` deliberately records gates even when the agent never marks
`in_progress` (`gate.go:162-167`, "a common lapse"), and it has **no
unit-boundary guard**. So when the next unit starts without that explicit
event:
1. `gate record` keeps appending results into the existing `Gates` map.
2. `resetGateUnit` never runs → the prior unit's green gates persist.
3. The completion check (`case "completed"` → `currentGates` →
   `gatesAllGreen`) reads the stale map → a later unit can pass the gate on
   an earlier unit's results.

There is no association between a recorded gate and the unit it ran under, so
nothing detects the boundary. Confirmed reachable whenever the agent skips
`in_progress` marking; the clean-marking path is correct and tested
(`gate_test.go:361`).

Both problems share a root: the hook reacts to a single event type
(`TaskUpdate(in_progress)`) instead of the full task-event stream. Approach A
(full tally) fixes both.

## Scope
### In scope (v1)
- **Full hook tally.** A hook observing all task lifecycle events maintains,
  in the cached snapshot, incrementally and without a transcript scan:
  - `Total` — `++` on `TaskCreate`.
  - `Done` — recomputed from per-unit completed state on `TaskUpdate(completed)`.
  - `Phase` — set from the phase signal for the unit going `in_progress`.
  - `Task`, `UnitKey` — as today.
- **Unit-boundary gate reset.** Clear `Gates` (and rebind `UnitKey`) on *any*
  observed unit transition, not only an explicit `in_progress` mark. Stamp
  recorded gates with the unit they belong to so a boundary is detectable
  even when `in_progress` is skipped.
- **Hook-bound trust signal.** A per-workspace marker that the agent's hooks
  are installed and firing, so the deck can trust the cache outright on open
  (and treat the transcript scan as a cold-start / not-hook-bound fallback).
- **Write discipline preserved.** Keep the existing compare-and-skip
  (`nplr`): write the state file only when a tallied field actually changes,
  so per-tool-call PostToolUse firings don't churn the file or bounce the
  deck's fsnotify watcher. Task events are infrequent relative to all tool
  calls, so gating writes to real changes keeps volume at the agent's task
  pace.

### Out of scope (v1)
- Changing the transcript scan's own logic; it stays as the authoritative
  source when not hook-bound and as the reconciler at open.
- Persisting Claude's task list itself; the tally is derived from observed
  events, not a mirrored todo store.
- Any deck UI change beyond rendering the now-fresh cached fields.

## UX
### CLI
- No new top-level command. The gate hook(s) gain task-event handling and
  emit the same style of log lines already used (unit reset, gate record).

### TUI
- Deck open reflects the true current phase / progress / gates immediately
  when the workspace is hook-bound — no visible "catch-up" repaint after the
  background scan. No key changes.

## Discovery Questions
1. **First user?** Anyone watching an agent's dev-loop progress from the deck.
2. **When?** Every deck open while an agent is mid-loop with hooks installed.
3. **Exact result?** Cached snapshot equals the live loop state without a scan.
4. **Data sources?** `TaskCreate`/`TaskUpdate` hook payloads; the phase map
   from the loop config; existing `DevLoopSnapshot` in the state file.
5. **Smallest useful slice?** Phase + unit-boundary reset first (both cheap
   from a single event); done/total tally second.
6. **Non-goals?** Mirroring the full todo store; UI changes; touching the scan.
7. **Done?** Open reflects current phase/progress/gates instantly when
   hook-bound; gates reset on every unit boundary; writes only on change.

## Implementation Plan
1. **Broaden the task hook.** Extend the PreToolUse(TaskUpdate) handler (and
   add TaskCreate coverage) to observe every task lifecycle transition, not
   just `in_progress`. Derive Phase from the loop's phase map for the unit
   going active.
2. **Tally in the snapshot.** On each observed event, update `Total`/`Done`/
   `Phase`/`Task`/`UnitKey` in `DevLoopSnapshot`, guarded by compare-and-skip
   so unchanged results don't rewrite the file.
3. **Unit-stamped gate reset.** Record the owning unit alongside each gate
   result (or key the gate map by unit) so `gate record` can detect a new
   unit and reset even without an explicit `in_progress`; keep the existing
   idempotent same-unit behavior.
4. **Hook-bound marker + deck trust.** Set a marker when the hooks run; in
   `deck.go`'s open path, trust the cached snapshot for a hook-bound
   workspace and demote the transcript scan to fallback / reconciliation.
5. **Docs**: README dev-loop / gate-hooks section; note the cache is
   hook-authoritative while bound and scan-backed otherwise.

## Acceptance Criteria
- [ ] With hooks firing, deck open shows the current Phase and Done/Total
      without waiting on the transcript scan, including immediately after a
      phase switch.
- [ ] `Total`/`Done`/`Phase` update from task events with no scan involved.
- [ ] Gate results reset at every unit boundary, including when the agent
      never marks the new unit `in_progress`.
- [ ] A completed unit is never gated against a prior unit's recorded gates.
- [ ] Unchanged tallies do not rewrite the state file (no watcher churn).
- [ ] Not-hook-bound workspaces behave exactly as today (scan-authoritative).

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `tmux`, awp binary in PATH; a workspace with a `dev_loop` config
      and gate hooks installed.

### Core Happy Path
- [ ] Drive an agent through two units with clean `in_progress`/`completed`
      marking; confirm Phase/Done/Total in the cached snapshot track live and
      the deck open renders them with no post-scan repaint.
- [ ] Switch phase mid-loop; reopen the deck; confirm the new phase shows
      instantly.

### Edge Cases & Failure Modes
- [ ] Start the next unit **without** an `in_progress` mark; confirm the
      prior unit's gates are cleared and the new unit is gated on its own
      results only.
- [ ] Re-mark the same unit `in_progress`; confirm idempotent (gates kept, no
      write).
- [ ] Malformed / missing task payload → no panic, no spurious reset.
- [ ] Not-hook-bound workspace → cache untouched by hooks; scan drives status.

### Regression Checks
- [ ] `awp gate record` / `gate check` existing tests still pass
      (`gate_test.go`).
- [ ] Deck fast-first-paint + background reconcile still works for cold start.
- [ ] No increase in state-file write frequency for gate-only (no task-event)
      activity.

### Reviewer Notes
- Capture snapshot values vs. transcript-scan values at several points to
  confirm they agree while hook-bound.

## Spec Change Log
- 2026-07-14: Initial draft. Approach A (full hook tally) chosen over
  phase-only. Folded in the gate-reset-on-unit-boundary correctness fix,
  which shares the root cause (hook reacting to one event type instead of the
  full task-event stream).
- 2026-07-14: Implemented, with three scope decisions made once the code was
  read:
  - **Hook scoped to Phase + Started, not Done/Total.** `Phase` (in
    `watch/state.go`) is derived from *tool activity*, not task events — so
    "instant on open" needed a per-tool hook. That is the acute gap the user
    called out ("even when the phases switch"). Hook-tallying `Done`/`Total`
    was cut: it would create a second source of truth fighting the transcript
    scan reconciler (which re-derives them on every deck open — a missed hook
    would make the two oscillate) for low marginal value, since `Done`/`Total`
    already refresh via the scan. They stay scan-derived (unchanged, no
    regression). The `Units`/`TaskSeq` snapshot fields the draft implied were
    dropped; only `Started` was added (plus `GatesSealed`, below).
  - **No deck change / no hook-bound trust marker.** The deck's fast first
    paint already renders the cached `DevLoop` snapshot (`deck.go:1466`), so a
    hook-fresh `Phase` is instant with no deck change. The scan stays as the
    background reconciler (it also self-heals dropped hooks), so the
    skip-scan-when-hook-bound optimization was unnecessary and omitted. The
    deck re-persist now carries forward the hook-owned fields
    (`UnitKey`/`GatesSealed`/`Started`) so the scan doesn't clobber them.
  - **Gate-reset via a `GatesSealed` seal, not unit-stamped gates.** A green
    completion seals the unit (results kept for idempotent re-complete); the
    next recorded gate clears them. This resets gates across a boundary even
    without an explicit `in_progress` — the reachable form of the bug —
    without a per-gate unit stamp or full task-event observation.
  - Shared `watch.Loop.PhaseForTool` is the single phase-derivation source for
    both the scan (`handleToolUse`) and the new `awp internal loop track`
    PostToolUse hook, so the two can't drift.
  - Tests: `TestGateCompletionSealsAndNextGateResets` (gate reset) and the
    `TestLoopTrack*` set (phase hook). `go test ./...`, `go vet`, `go build`,
    `gofmt`, `golangci-lint` all clean.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
