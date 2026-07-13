# Agent Dev-Loop Watch (`awp watch`)

## Metadata
- **Spec ID**: `20260713-w4tc`
- **Feature name**: Agent dev-loop progress watcher (`awp watch`)
- **Owner**: andrewcohen
- **Status**: In Progress (v1 built + validated live; breadth via task tool / checklist / prose; tests + lint + README done)
- **Last updated**: 2026-07-13

## Goal
Let a developer observe an agent's progress on a task **against the shape of the
work** — where it is in the project's development loop and whether it is
thrashing — instead of staring at the raw Claude Code chat. Turn a binary
"is the agent running?" signal into "what is it doing, and is it going well?"

## User Problem
Today awp only knows a single status string per workspace (`working` / `waiting`
/ `idle` / `exited`), written by Claude Code hooks. A green dot that has been lit
for 20 minutes says nothing about whether the agent is 10% or 90% through the
task, stuck retrying the same failing gate, or off scope. The only place the
task-level truth lives is the chat, so the developer has to stare at it — and
can't do that across many concurrent workspaces.

## Concept: the dev loop
Progress is measured against a **development loop** — the repeatable cycle one
unit of work passes through, e.g. `implement → test → gates → commit`. Each
phase carries **gates**: named checks (format, lint, test, build, commit)
recognized by matching the shell commands the agent runs in its transcript.

Two axes of progress:
- **Breadth** — which unit of work, how many remain. Source: the agent's task
  decomposition. **Finding (2026-07-13):** across 151 local transcripts the
  agent emits **0** `TodoWrite` and **1** `ExitPlanMode` call — there is no
  structured decomposition in the transcript. Planning is an *organic* phase of
  read-only investigation, not plan mode. So breadth has no honest transcript
  source; it must be inferred behaviorally or instructed (preamble).
- **Depth** — within the current unit, position in the loop, each gate's
  pass/fail, and the **churn / loop-back count** (a gate failing repeatedly),
  which is the key stall signal the chat does not surface at a glance.

## Scope
### In scope (v1 — POC, landed)
- `awp watch [workspace]` — read-only, **observe-only** (awp never runs gates or
  steers the agent), live-repainting (1s) view built from the workspace's newest
  Claude Code transcript JSONL.
- Picker fallback (charm list) when no workspace argument is given.
- `--transcript <path>` + `--once`: replay a specific finished transcript and
  print one frame — the simulation/validation harness.
- `--suggest`: print a ready-to-paste prompt that asks an agent to inspect the
  repo and write a `dev_loop` block into `.awp/config.json`.
- `dev_loop` config section in `.awp/config.json` (`phases` + `gates`; each gate
  = `{name, phase, match-regex}`), merged global→project.
- Depth view (loop ring + gate lights + churn line) as the live progress signal.
- Graceful degradation to a loop-only view when there is no todo list (the
  normal case for this user).

### Out of scope (v1)
- **Breadth axis / plan parsing** (deferred — "option C now, B next"). No plan
  step extraction, no completion inference in v1.
- **Steering / control loop.** v1 is observe-only. awp does not run gates itself
  or re-inject failures to the agent.
- Deck integration (per-workspace row summary, stall alerts) — future.
- LLM spec-criterion progress summary — future.

## UX
### CLI
- `awp watch` — picker, then live view of the selected workspace.
- `awp watch <name|project/name>` — live view of a specific workspace.
- `awp watch --once [--transcript P] [--repo R]` — one frame to stdout (replay).
- `awp watch --suggest [--repo R]` — print the dev_loop setup prompt.
- Unconfigured repos show an inline hint: gates are a generic guess, run
  `awp watch --suggest`.

### TUI (live view)
```
awp watch · <project/workspace>        <agent status> · <time on unit>

UNITS  (no todo list — showing current work)      # v1 degraded breadth
      loop   implement ─ test ─ ▶GATES ─ commit    # current phase highlighted
      gates  ✔ fmt  ✔ vet  ✗ lint ×3  ○ test  ✔ build  ○ commit
      ⇄ implement⇄lint 3×  ·  6m on unit  ⚠ thrash  # churn line when RedCount≥2
  q quit · repaints every 1s
```
- Selection/emphasis uses the shared charm palette (`Warning` for current,
  `Success` pass, `Danger` fail/thrash, `Muted` upcoming).

## Discovery Questions
1. **First user?** The developer (andrewcohen) running many concurrent agent
   workspaces.
2. **When?** While an agent works a task, in a split pane, to know at a glance if
   it needs attention without reading the chat.
3. **Exact output?** Current loop phase, per-gate pass/fail, and a stall/churn
   flag; eventually a per-workspace summary and push alert.
4. **Data sources?** The Claude Code transcript JSONL under
   `~/.claude/projects/<slug>/`; the repo `dev_loop` config; workspace-state for
   the workspace list + agent status.
5. **Smallest useful slice?** The loop-only depth view driven purely from gate
   commands in the transcript (v1).
6. **Non-goals?** Steering the agent; inventing fake completion percentages.
7. **Done?** A live view that reliably shows loop position + gate churn for a
   real buildout, judged more useful than reading the chat.

## Spec Change Log
- 2026-07-13: Initial draft. POC implemented: `internal/watch/` +
  `internal/cli/watch.go` + `config.DevLoop`.
- 2026-07-13: Decision — breadth axis deferred (option C). No structured
  decomposition in transcripts (0 TodoWrite, 1 ExitPlanMode across 151); planning
  is organic. Live progress driven by the loop only for v1.
- 2026-07-13: Correction — an earlier "142 use plan mode" finding was a grep
  false positive (matched tool availability, not invocation). Added an `explore`
  phase detected behaviorally (read-only activity before first edit), not via
  plan mode.
- 2026-07-13: Decision — unconfigured repos suggest an agent prompt
  (`--suggest`) rather than inferring or hard-trusting the Go-shaped default.

## Implementation Plan
1. `config.DevLoop` + `config.DevLoopGate` in `internal/config/config.go`,
   merged global→project. (done)
2. `internal/watch/loop.go` — `Loop`, `Gate`, `DefaultLoop`, `Resolve`,
   `IsConfigured`. (done)
3. `internal/watch/transcript.go` — `Locate` (slugify abs path, newest jsonl).
   (done)
4. `internal/watch/state.go` — `BuildState`: replay transcript → todos, current
   phase, per-gate result + RedCount (loop-backs), unit timing. (done)
5. `internal/watch/render.go` — combined panel (header, units, loop ring, gate
   lights, churn line). (done)
6. `internal/watch/suggest.go` — `SuggestConfigPrompt`. (done)
7. `internal/cli/watch.go` — command, flag parse, picker fallback, `--once`,
   `--transcript`, `--repo`, `--suggest`, Bubble Tea live model. (done)
8. Dispatch `case "watch"` in `internal/cli/app.go`. (done)
9. Breadth beyond TodoWrite: reconstruct the list from `TaskCreate`/`TaskUpdate`
   (the todo tool in this environment), with a markdown-checkbox and `Unit N:`
   prose fallback. Per-unit reset so gates/phase track only the current unit.
   (done)
10. `internal/watch/preamble.go` — `GeneratePreamble` (+ `awp watch --preamble`):
    turn `dev_loop` into the agent loop instruction, single-sourced with the
    watcher. (done)
11. Tests (`internal/watch/watch_test.go`), `golangci-lint` 0 issues, README
    (CLI + `dev_loop` config). (done)
12. Auto-inject the generated preamble at coding-workspace launch when
    `dev_loop` is configured and the agent is Claude — via
    `claude --append-system-prompt` (persists across the session; excludes the
    review flow). The preamble is written to `~/.awp/dev-loop/<repo>.md` and
    passed via `claude --append-system-prompt-file <path>` (no inline text /
    shell substitution — embedding it inline floods/garbles the command line).
    Verified via canary that the appended system prompt reaches the model; note
    it is invisible in the Claude Code UI *and* transcript (system prompt isn't
    logged). `codingAgentInvocation` / `writeDevLoopPreamble` in
    `internal/cli/watch.go`. (done)
13. Sticky live transcript selection: stay on the current session file unless
    it goes idle past `StableWindow`, so concurrent session files in the same
    project dir don't flicker/blank the view. `LocateSticky` in
    `internal/watch/transcript.go`. (done)
14. Cross-session lineage stitching — **investigated, declined.** A scan of all
    153 local transcripts found 0 resume/`summary` lines and 0 first-message
    `parentUuid` references crossing files: compaction and `--resume`/`--continue`
    append to the *same* session file, and a new session *file* is always an
    independent session. There is no lineage to stitch, and doing so would only
    risk merging unrelated sessions. Stickiness (item 13) handles the real case.
15. **Future:** deck-row progress summary + stall alert; optional observe+nudge
    steering; "all units complete" state; prompt-prepend injection for
    non-Claude agents.

## Acceptance Criteria
- [x] `awp watch --once --transcript <finished session>` renders a correct loop
      view (validated on real awp + redwood transcripts).
- [x] Gate commands in the transcript flip the correct gate to pass/fail; repeat
      failures increment the churn count.
- [x] Unconfigured repo shows the hint + `--suggest` prints a usable prompt.
- [x] `dev_loop` config in `.awp/config.json` overrides the default loop.
- [x] Live view (`awp watch <ws>`) validated against a real in-progress buildout
      (the `tuicr-stale-loop` session, watched live).
- [x] Breadth reconstructed from `TaskCreate`/`TaskUpdate`; per-unit gate reset
      verified (gates no longer carry over between units).
- [x] `go test ./...`, `go vet ./...`, `go build ./...`, `golangci-lint run` all
      green; README updated.

## QA / Human Review Test Plan
### Setup
- [ ] `go install ./cmd/awp` (or build to a temp path via mise).
- [ ] A repo with a `dev_loop` block in `.awp/config.json` (this repo has one).
- [ ] At least one finished Claude Code transcript to replay.

### Core Happy Path
- [ ] `awp watch --once --transcript <path>` prints header, loop ring with the
      current phase highlighted, and gate lights matching the session's commands.
- [ ] `awp watch <ws>` opens the live view and repaints as the agent works.
- [ ] Repeated gate failures surface the `⇄ … N×` churn line and `⚠ thrash`.

### Edge Cases & Failure Modes
- [ ] No transcript for the workspace → clear actionable error.
- [ ] Unconfigured repo → hint banner + `--suggest` prompt; still renders a
      best-effort generic loop.
- [ ] Session with no gate commands → loop position from edits only, gates `○`.
- [ ] Malformed/huge transcript lines are skipped, not fatal.

### Regression Checks
- [ ] Other `awp` subcommands unaffected; config still loads for repos without a
      `dev_loop` block.

### Reviewer Notes
- Capture the commands run and the observed frames; note any gate that fails to
  match a real command (tighten the regex in `dev_loop`).

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
- [x] `golangci-lint run ./...` (0 issues)
