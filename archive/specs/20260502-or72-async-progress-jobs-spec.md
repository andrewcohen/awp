# Async Progress Jobs

## Metadata
- **Spec ID**: `20260502-or72`
- **Feature name**: Async / detached progress actions in the deck
- **Owner**: andrew
- **Status**: In Progress
- **Last updated**: 2026-05-02

## Goal
Make long-running deck actions (workspace create, review, CI watch, custom)
dispatchable in the background. The user kicks off a job, the deck stays
interactive, the user can dispatch more jobs, and quitting the deck no
longer aborts work in flight. A later deck instance shows the same
in-flight set.

## User Problem
Today the deck enters a modal `progressMode` while a long action runs:
all keypresses are ignored (`internal/deckui/model.go:706-708`), the
deck calls `tea.Quit` on success (`model.go:608`), and only one action
runs at a time. The most common case — "create three workspaces from
three bookmarks and walk away" — is impossible. Even creating one then
inspecting another workspace blocks until creation finishes.

## Scope
### In scope (v1)
- New `internal/jobs/` package: file-backed job store, spawn, orphan
  detection, heartbeat.
- Hidden subcommand `awp run-job <id>`: re-execs the same `awp` binary
  to run the requested action as a detached subprocess; writes status
  to `~/.awp/jobs/<id>.json`.
- Deck dispatches `ActionCreateWorkspace`, `ActionReview`, `ActionCI`,
  `ActionCustom` via `jobs.Spawn` instead of running the handler
  inline.
- Deck UI: tray line (running / failed / orphaned counts), `j` key
  jobs overlay, toast on completion (no more auto-quit / auto-switch).
- Cleanup runs as a non-blocking `tea.Cmd` on `Init()`.
- Orphan defenses: signal handlers + deferred final-flush in the
  subprocess; pid liveness + start-time + hostname checks on the
  deck side.

### Out of scope (v1)
- SQLite migration. We stay on JSON-per-file. See "Deferred storage
  migration" in the plan; revisit when at least two of the trigger
  conditions fire.
- Cross-host job visibility (records from another machine via NFS /
  Dropbox) — surfaced as "remote" in UI but not actionable.
- A separate `awp jobs` CLI (list / cancel / inspect from outside the
  deck). Could be added later; for v1 the deck plus `cat`/`tail` on
  the JSON / log files is enough.

## UX
### CLI
- `awp run-job <id>` — hidden subcommand used by `awp deck` to
  spawn detached jobs. Not documented for direct user use, but the
  job files live at `~/.awp/jobs/<id>.json` and `~/.awp/jobs/<id>.log`
  for shell-based inspection (`cat`, `tail -f`).

### TUI
- Tray line under the deck status line, only when jobs exist:
  `▶ 2 running   ⚠ 1 failed   ☠ 1 orphaned   [j: jobs]`.
- `j` opens a jobs overlay: list of jobs with status glyph + title,
  arrows to switch, steps + tail of logs in the right pane.
  - `c` cancel a running job (sends SIGTERM).
  - `x` dismiss a finished/failed/orphaned job (deletes the record).
  - `o` open the sidecar `<id>.log` in `$PAGER`.
  - `esc` close the overlay.
- Toast: one-line message in `m.status` for ~3 s on completion; no
  auto-quit, no auto-switch. Completed workspaces appear in the deck
  list naturally via the existing 2 s refresher.

## Discovery Questions
1. **First user**: andrew, dispatching multiple workspaces in a row.
2. **When**: any time a creation / review / CI / custom action is
   slow enough that waiting feels bad (>5 s).
3. **Exact result**: dispatch returns immediately; completion is
   surfaced via the tray and a toast; the new workspace shows up in
   the deck list once finished; the user picks it and presses enter
   to switch tmux (existing summon flow).
4. **Data sources**: `~/.awp/jobs/<id>.json` (status), optional
   `~/.awp/jobs/<id>.log` (full subprocess output).
5. **Smallest useful slice**: `ActionCreateWorkspace` only, with the
   tray + completion toast. Other actions are mechanical follow-on
   once the foundation is in place.
6. **Non-goals**: detached jobs that survive across machines;
   pre-emptive scheduling; resource limits; queueing.
7. **Done**: dispatching N create actions in a row works, deck stays
   interactive, quitting the deck doesn't abort, and a follow-up
   deck instance sees the same jobs.

## Spec Change Log
- 2026-05-02: Initial draft.
- 2026-05-02: Land overlay (`J` key, `c`/`x`/`o`) and async dispatch
  for review / CI / custom / delete in the same change as create.
  All progress actions now route through `awp run-job <id>` when the
  deck has the launcher configured.

## Implementation Plan
1. `internal/jobs/store.go` — `Store` type with `Spawn`, `Get`,
   `List`, `Update`, `Delete`, atomic write via flock + rename
   (mirroring `internal/state/json_store.go:80-98`).
2. `internal/jobs/spawn.go` — re-exec `os.Args[0] run-job <id>` via
   `exec.Command` with `Setsid: true`, `Stdin/Stdout/Stderr` →
   `<id>.log`. Capture pid + start time, write the pending record.
3. `internal/jobs/orphan.go` — pid-liveness + start-time check (Linux
   `/proc/<pid>/stat` field 22, macOS `sysctl kern.proc.pid.<pid>`)
   in build-tagged files.
4. `internal/jobs/heartbeat.go` — subprocess-side ticker writing
   `last_heartbeat` every 5 s via `Store.Update`.
5. `cmd/awp/run_job.go` — new `awp run-job <id>` subcommand: signal
   handlers (SIGTERM → cancelled, SIGINT → cancelled, SIGHUP →
   cancelled), deferred recover + "exited without finalizing"
   guard, dispatch by `JobAction` to the existing `app.go` calls
   (`openWorkspaceWithReporter`, `runReviewWithReporter`, etc.) with
   a `jobReporter` that writes Step/Log to the store.
6. `internal/deckui/jobs.go` — `JobsModel` (Add/Get/List/Active/
   MarkDone/Remove), tray rendering helper, jobs overlay state.
7. `internal/deckui/model.go` — refactor `startAction` and
   `startCreateAction` to call `jobs.Spawn` and add to JobsModel
   instead of entering modal `progressMode`. Remove the `tea.Quit`
   on `actionResultMsg` for create. Add `j` key for the overlay,
   wire `hydrateJobsCmd` and `gcJobsCmd` to `Init`. Update
   `deckKeyGroups` for the `?` help.
8. `internal/cli/deck.go` — handler closure becomes "translate
   `ActionRequest` → `JobSpec` → `jobs.Spawn`". Existing handlers
   (`openWorkspaceWithReporter`, `runReviewWithReporter`,
   `handleDeckAction`, `openCustomActionWindow`) stay; they're
   called by `awp run-job` instead.
9. `README.md` — document `~/.awp/jobs/`, the `j` key, the tray
   glyphs, orphan detection, and the `cat`/`tail` debug path.

## Architecture

```
┌──────────────┐  spawn awp run-job <id>      ┌─────────────────────┐
│  awp deck    │ ───────────────────────────▶ │ awp run-job <id>    │
│  (viewer)    │                              │ (subprocess, setsid)│
│              │ ◀──── fsnotify / poll ────── │                     │
│ JobsModel ◀──│   ~/.awp/jobs/<id>.json      │ writes status       │
└──────────────┘   ~/.awp/jobs/<id>.log       │ runs workspace.Svc, │
                                              │ review, ci, custom  │
                                              └─────────────────────┘
```

- **Dispatch:** deck creates a JobID, writes a pending record, spawns
  `awp run-job <id>` with `Setsid: true` and detached stdio, returns
  to its event loop. No goroutine to track; the OS owns the child.
- **Execution:** `awp run-job` reads the spec, calls today's
  `workspace.Service` / review / CI / custom code unchanged, with a
  reporter that writes Step / Log / Done events to the job file.
- **Status streaming:** small JSON file per job at
  `~/.awp/jobs/<id>.json`, updated atomically (flock + rename — same
  pattern as `internal/state/json_store.go:80-98`). Optional
  `~/.awp/jobs/<id>.log` sidecar for chatty stdout (append-only).
  Deck polls every 2 s (the existing refresh tick).
- **Cancellation:** `kill(pid, SIGTERM)`. Subprocess catches it,
  flushes `cancelled`, exits. No `context.Context` plumbing through
  `workspace.Service` — the OS does the work.
- **Quit semantics:** quitting the deck is a no-op for jobs. Next
  deck launch picks them up. No "block exit" guard needed.
- **Cross-process visibility:** multiple deck instances see the same
  jobs because they read the same dir.

### Job file format (`~/.awp/jobs/<id>.json`)

```jsonc
{
  "id": "20260502-7k2m",
  "title": "create · feat/x",
  "spec": { "action": "create-workspace", "name": "feat/x", "bookmark": "main", "prompt": "...", "repo_root": "/Users/me/code/awp" },
  "host": "andrew-laptop",
  "pid": 41822,
  "pid_started_at": 1714600000.123,
  "status": "running",
  "started_at": "2026-05-02T10:00:00Z",
  "ended_at": null,
  "last_heartbeat": "2026-05-02T10:00:14Z",
  "steps": [{"label": "Prepare jj workspace", "state": "done"}],
  "logs_inline": ["..."],
  "log_file": "~/.awp/jobs/<id>.log",
  "error": null
}
```

### Avoiding & identifying orphans

Two tiers:

1. **Avoidance** (graceful exit): subprocess installs SIGTERM /
   SIGINT / SIGHUP handlers + a deferred recover guard that flushes
   `cancelled` or `error` before exiting. Catches everything except
   `SIGKILL`, OOM kill, and hard crashes.
2. **Identification** (catch the rest): on every deck refresh tick,
   for each non-terminal record where `now - last_heartbeat > 30 s`,
   probe `kill(pid, 0)`. If `ESRCH`, mark orphaned. If alive, also
   verify the process start time (`/proc/<pid>/stat` field 22 on
   Linux; `ps -o etimes=` on macOS) matches the recorded value —
   catches PID reuse. Records from a different host are skipped
   (we can't probe pids on another machine).

Orphans get a 7-day retention; clean terminal records get 24 hours.
Cleanup runs as a non-blocking `tea.Cmd` on deck startup so it never
delays the UI.

## Deferred: storage migration to SQLite

Stay on the filesystem for now. Triggers that flip this:

1. Want cross-record queries (e.g. all failed jobs joined to
   workspaces in the last 7 d) — not currently a need.
2. flock contention on `workspace-state.json` (look for "lock
   timeout" errors) — current 2 s window is fine.
3. Multi-record atomicity (e.g. delete workspace + cancel its
   in-flight jobs in one transaction) — best-effort with files is
   acceptable for now.
4. History at scale (hundreds of historical jobs, search) — 24 h GC
   keeps us small.

When two of those fire, migrate `workspace-state.json` AND `jobs/`
together (don't migrate one alone; you lose the join story). Use
`modernc.org/sqlite` (pure Go, no cgo), WAL mode, behind the
existing `state.Store` / `jobs.Store` interfaces. Provide
`awp state dump` / `awp state edit` for debuggability that we lose
by leaving plain JSON.

## Acceptance Criteria
- [ ] Dispatching `ActionCreateWorkspace` from the deck does NOT
      enter modal progress mode; deck remains interactive.
- [ ] Dispatching three creates in a row produces three concurrent
      jobs visible in the tray (`▶ 3 running`).
- [ ] Completion shows a toast in the status line; new workspace
      appears in the deck list via the existing refresher.
- [ ] Quitting the deck while a job is running does not abort the
      job; reopening the deck shows the job in the tray.
- [ ] `kill -9 <pid>` on a running job's subprocess causes the deck
      to mark it `orphaned` within ~30 s (heartbeat threshold).
- [ ] `c` in the jobs overlay sends SIGTERM and the subprocess
      flushes a `cancelled` final record before exiting.
- [ ] Done/error/cancelled records older than 24 h are removed on
      next deck startup; orphans retained for 7 d.

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `tmux`, and `awp` (built from this branch) on PATH.
- [ ] Empty `~/.awp/jobs/` dir.
- [ ] A jj repo with at least one bookmark and a remote configured.

### Core Happy Path
- [ ] `awp deck`, press `n`, fill the form, submit. Deck stays
      interactive immediately. Tray shows `▶ 1 running`.
- [ ] Dispatch a second create before the first finishes. Tray
      shows `▶ 2 running`.
- [ ] Press `j` → overlay lists both jobs; arrows switch between
      them; logs stream.
- [ ] Wait for both to complete. Toast `create: <name>` appears for
      each. Workspaces visible in the deck list. Press `enter` to
      switch tmux to one — works as today.

### Edge Cases & Failure Modes
- [ ] Quit deck (`q`) while a job is running — deck exits cleanly,
      no prompt. `pgrep -af awp run-job` shows it still running.
      Reopen deck — tray shows the job.
- [ ] `kill -9 $(jq -r .pid ~/.awp/jobs/*.json | head -1)` on a
      running subprocess. Within ~30 s the tray transitions to
      `☠ 1 orphaned`. Overlay shows the diagnostic message.
- [ ] In overlay, `c` on a running job — subprocess catches SIGTERM,
      writes `cancelled`, exits. Tray transitions running → cancelled.
- [ ] Trigger a real failure (duplicate workspace name) — tray
      shows `⚠ 1 failed`; overlay shows the error message; `x`
      removes the record.
- [ ] Pre-existing `~/.awp/jobs/` with stale records older than 24 h
      → cleared on next deck startup.

### Regression Checks
- [ ] `awp open` (non-deck CLI path) still works synchronously.
- [ ] Existing summon (enter on a workspace), delete, review, ci,
      custom actions still function.
- [ ] `awp deck-cleanup` still works.
- [ ] `?` help overlay lists `j: jobs` alongside the rest.

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
