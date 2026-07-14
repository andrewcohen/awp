# Dev-Loop Enforcement (gate hooks)

## Metadata
- **Spec ID**: `20260714-q4rt`
- **Feature name**: Dev-loop enforcement — event-driven gate recording + completion gate
- **Owner**: andrewcohen
- **Status**: Planned
- **Last updated**: 2026-07-14

## Goal
Make the dev loop actually *stick* instead of merely being observed: an agent
can't mark a unit of work complete until that unit's gates have passed. Do it
**cheaply** (event-driven, no heavy transcript scanning) and **safely** (start
with the low-risk completion gate, not a blanket write-block).

This is the step from the observe-only `awp watch` (spec `20260713-w4tc`) to an
actual control loop.

## User Problem
The dev-loop preamble injected into agents is only a *nudge*. Observed live
(Exhibit A): an agent given the preamble still skipped its task tool and
"finished" work without running the gates. A system prompt cannot reliably
force behavior — especially tool use. If we want *consistent* adherence (units
tracked, gates green before "done"), the harness has to enforce it, not ask.

## Concept
awp already installs Claude Code hooks (`PreToolUse`/`PostToolUse`/`Stop`, see
README "How status reporting works"). Use them:

- **Record gate results as they happen**, from the `Bash` tool, instead of
  re-deriving them by scanning the (potentially tens-of-MB) transcript. A
  `PostToolUse(Bash)` hook matches the command against the project's `dev_loop`
  gates and writes the pass/fail into the workspace's persisted
  `DevLoopSnapshot`.
- **Block completion** with a `PreToolUse(TaskUpdate → completed)` hook that
  reads that snapshot and denies unless the current unit's gates are all green.

### Enforcement ladder (escalating strength, escalating risk)
1. **Preamble** — one-time system-prompt nudge. Fades over a session. (Shipped
   in `20260713-w4tc`.)
2. **`PostToolUse(Bash)` nudge** — after a *matched gate*, `awp gate record`
   optionally feeds a terse `additionalContext` message back to the agent. This
   is recurring, in-context reinforcement at the exact moment a gate runs —
   what the preamble can't be. **Low risk:** a message can't wedge the agent
   even if occasionally wrong. Speak only on transitions (gate went red → "fix
   & re-run before completing"; all green → "mark complete & commit");
   intermediate passes stay silent or compact.
3. **`PreToolUse(TaskUpdate→completed)` block** — hard stop unless the unit's
   gates are green. Higher risk (false denials).
4. **`PreToolUse(Edit|Write)` block** — write gate. Highest risk (straitjacket).
   Deferred (see out of scope).

**Rung 3 is the point of this feature** — gates-must-be-green before a unit can
be marked completed. That's the guarantee; everything else supports it. Rung 2
(the nudge) is an *optional complement* that reinforces the loop in-context — it
is **not** a substitute for the block, and the feature isn't "done" on the nudge
alone. The `PostToolUse(Bash)` recording exists primarily to feed rung 3 cheaply
(so the completion check reads a snapshot, not a transcript scan); the nudge is a
bonus it can emit while it's there.

### Why not the transcript / why not SQLite
- **Not the transcript (for the hot path):** `watch.BuildState` scans the whole
  transcript; running that on every check doesn't scale (36 MB transcripts ×
  ~35 workspaces). Event-driven recording is O(1) per tool call.
- **Not SQLite:** `internal/state.JSONStore` already serializes writes with an
  **OS advisory flock** (`withLock`, sidecar `.lock`, timeout tuned for agent
  hooks) and does atomic `Update` read-modify-write. Multiple hook processes +
  the deck can write the same per-workspace entry safely today. A DB buys
  nothing here; revisit only if cross-workspace aggregate queries appear.
- **Keep a periodic scan as a reconciler:** hooks can miss (agent killed
  mid-tool, hook error, an unmatched gate invocation). The existing
  `BuildState` scan runs occasionally (e.g. on deck open) to reconcile the
  snapshot against ground truth. Event-driven for freshness/perf; scan for
  self-heal.

## Scope
### In scope (v1)
- `DevLoopSnapshot.Gates` — per-gate result for the current unit
  (`pass`/`fail`/`pending`), persisted on the workspace entry.
- `awp gate record` — `PostToolUse(Bash)` hook. Matches command → gate, reads
  exit status, writes result via `store.Update`. Silent by default; `--json`
  prints the recorded result + rolled-up unit state (for debug / the deck).
- Unit reset — on `TaskUpdate → in_progress` (new unit), clear the recorded
  gate results so they track only the current unit. (Hook, or detected inside
  `gate record`.)
- `awp gate check` — reads the snapshot; exit 0 if the current unit's gates are
  all green; in `PreToolUse` mode emits a Claude `deny` decision with an
  actionable reason otherwise. Usable by the agent to self-check.
- Completion-gate hook wiring: `PreToolUse(TaskUpdate)` where the input sets
  `status=completed`.
- Reconciler: a `BuildState` pass that overwrites the snapshot's `Gates` from
  the transcript, run on a low cadence / deck open.

### Out of scope (v1)
- **Write gate** (block `Edit`/`Write` when no in-progress task) + directory
  carve-outs (`write_without_task` allowlist). Higher straitjacket risk; design
  its escape hatches before shipping. Planning is read-only, so a write gate is
  what would force task creation — deferred to v2.
- SQLite / any new datastore.
- Non-Claude agents (pi.dev has a different hook/extension model).
- Forcing gate *execution* (awp running ggates itself) — we only observe that
  the agent ran them.

## UX
### CLI
- `awp gate record` — hook entrypoint (reads Claude `PostToolUse` JSON on
  stdin: `tool_name`, `tool_input.command`, `tool_response` exit/error, session
  → workspace). Side-effect only; exit 0. `--json` emits:
  ```json
  {
    "workspace": "awp/tuicr-stale-loop",
    "unit": "prompt plumbing",
    "matched_gate": "test",
    "result": "pass",
    "unit_gates": { "fmt": "pass", "vet": "pass", "lint": "fail", "test": "pass", "build": "pending" },
    "ready_to_complete": false
  }
  ```
  `matched_gate: null` when the command isn't a gate (records nothing).
  On a **transition** (a gate flips red, or the unit's gates all go green),
  `gate record` also emits a `PostToolUse` nudge back to the agent (rung 2):
  ```json
  {"hookSpecificOutput":{"hookEventName":"PostToolUse",
    "additionalContext":"[dev-loop] all gates green for 'prompt plumbing' — mark it completed and commit."}}
  ```
  Intermediate passes are silent. A config knob (`dev_loop.nudge`: off/transitions/verbose)
  controls how chatty this is.
- `awp gate check [--workspace <ws>] [--task <id>]` — exit 0 if ready; else exit
  non-zero + reason on stderr. In `--hook` (PreToolUse) mode emits:
  ```json
  {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
    "permissionDecisionReason":"unit 'prompt plumbing' can't be marked complete: gate 'lint' is red (last run failed). Run `pnpm lint <files>` and re-check."}}
  ```
- The deny reason must be **actionable and quotable** — name the unit, the red
  gate, and the command to run — so the agent self-corrects instead of
  thrashing.

### TUI
- The deck's dev-loop meta line already renders from `DevLoopSnapshot`; once
  `Gates` is populated event-driven it can show gate lights inline without the
  scan. The `w` watch overlay is unchanged (still full `BuildState`).

## Discovery Questions
1. Does `PostToolUse` reliably carry the Bash exit status (numeric code, or at
   least `is_error`)? The whole design hinges on pass/fail being knowable in the
   hook. **Verify first.** If only `is_error`: nonzero → red, fine.
1b. Does `PostToolUse` support feeding text back to the agent
   (`hookSpecificOutput.additionalContext` or equivalent)? Rung 2 (the nudge)
   depends on it. If not, rung 2 degrades to silent recording and we lean on
   rung 3.
2. How does a hook map its session → awp workspace? (Existing status hooks
   already do this — reuse that resolution.)
3. Compound commands (`gofmt && go test`): one exit code for the whole line.
   Record against the *first* matching gate only (matches the "one gate per
   command" preamble rule) and `log()`/note the limitation.
4. Unit identity: is "current unit" = the in-progress task at record time?
   What if no task exists yet (gates run during exploration)? → record nothing
   until a unit is in progress.
5. What does "ready" mean when `dev_loop` lists gates the agent legitimately
   skipped (e.g. `build` on a docs-only unit)? v1: all configured non-marker
   gates must be `pass`. Consider a per-unit "n/a" escape later.

## Spec Change Log
- 2026-07-14: Initial draft. Chosen approach: event-driven `PostToolUse(Bash)`
  recording (not transcript tailing), completion gate only (write gate
  deferred), reuse the flock'd JSON store (no SQLite), keep a scan reconciler.

## Implementation Plan
1. `workspace.DevLoopSnapshot` + `deckui.DevLoopSummary`: add
   `Gates map[string]string` (name → `pass`/`fail`/`pending`) and a
   `UnitKey`/timestamp so records can be scoped/reset per unit.
2. `awp gate record` (`internal/cli/gate.go`): parse the `PostToolUse` payload,
   resolve workspace, `watch.Loop` from config, match command → gate, read exit
   status, `store.Update` the entry's snapshot. `--json` output. On a
   red/all-green transition, emit the rung-2 `additionalContext` nudge (gated by
   `dev_loop.nudge`: off/transitions/verbose, default transitions).
3. Unit reset: `TaskUpdate → in_progress` clears `Gates` (or `gate record`
   detects the current-unit change and resets).
4. `awp gate check` (`internal/cli/gate.go`): read snapshot, compute
   `ready_to_complete`, `--hook` deny JSON.
5. Hook installer (`internal/config` / `init hooks`): register
   `PostToolUse(Bash) → awp gate record`, `PreToolUse(TaskUpdate) → awp gate
   check --hook`. Gate on `dev_loop` being configured for the repo; no-op
   otherwise (like the other awp hooks outside managed sessions).
6. Reconciler: reuse `buildDevLoopSummary`/`BuildState` to overwrite `Gates`
   from the transcript on a low cadence (deck open) so drift self-heals.
7. Tests: `gate record` matching + exit → snapshot; unit reset; `gate check`
   ready/deny; compound-command first-match; docs/README (`dev_loop`,
   `awp gate`, the new hooks).

## Acceptance Criteria
- [ ] Running a gate command in an agent session updates the workspace's
      `DevLoopSnapshot.Gates` within one tool call (no transcript scan).
- [ ] On a gate transition, the agent receives a terse `additionalContext`
      nudge (rung 2); intermediate passes stay silent; `dev_loop.nudge=off`
      suppresses it.
- [ ] `TaskUpdate → completed` is **denied** when a configured gate is red or
      pending, with an actionable reason; **allowed** when all are green.
- [ ] Starting a new unit (`TaskUpdate → in_progress`) resets the recorded gates.
- [ ] `awp gate check` agrees with `awp watch`'s gate view (reconciler keeps
      them consistent).
- [ ] Repos without a `dev_loop` block are unaffected (hooks no-op).
- [ ] `go test/vet/build ./...`, `golangci-lint run` all green; README updated.

## QA / Human Review Test Plan
### Setup
- [ ] A repo with a `dev_loop` block; `awp init hooks` re-run so the gate hooks
      install.
- [ ] A live agent session in a managed workspace.

### Core Happy Path
- [ ] Agent starts unit → edits → runs each gate (passing) → marks complete:
      completion is allowed.
- [ ] Agent tries to mark complete before running gates (or with a red gate):
      `TaskUpdate` is denied; the reason names the gate + command; after fixing
      and re-running, completion is allowed.

### Edge Cases & Failure Modes
- [ ] Gate command fails (nonzero) → recorded red → completion blocked.
- [ ] Compound command with two gates → only the first matching gate recorded;
      behavior documented.
- [ ] No `dev_loop` configured → no blocking, no recording.
- [ ] Hook missing an event (killed mid-tool) → reconciler corrects the snapshot
      on next scan; completion not permanently wedged.
- [ ] Gates run during exploration (no in-progress unit) → nothing recorded.

### Regression Checks
- [ ] Existing status-reporting hooks still fire; deck meta line + `w` overlay
      unaffected.
- [ ] `store.Update` contention (deck + gate hooks writing) stays correct under
      the existing flock.

### Reviewer Notes
- Confirm the `PostToolUse` payload shape carries exit status (Discovery Q1).
- Watch for false denials — the failure mode that would make a user rip this
  out. An escape hatch (e.g. `AWP_SKIP_GATE=1`, or `awp gate check` returning
  allow with a warning when snapshot is stale) may be warranted.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
- [ ] `golangci-lint run ./...`
