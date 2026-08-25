# Right Panel Redesign + Background Jobs

## Metadata
- **Spec ID**: `20260504-id6a`
- **Feature name**: Right panel redesign + background user commands
- **Owner**: Andrew Cohen
- **Status**: In Progress
- **Last updated**: 2026-05-05

## Goal
Reclaim the deck's right-side column from the static keymap and turn it into a
selection-driven panel that surfaces what just happened in the focused
workspace — including a new class of "silent" / background user commands whose
output lives outside the live transcript.

## User Problem
Today the right panel in `internal/deckui/model.go::renderDetails` is dominated
by a full keymap that duplicates the `?` help overlay. Users learn the keys
once and then the column pays rent forever. Meanwhile there is nowhere good to
land the output of long-running or fire-and-forget user commands (e.g.
`/install`) — running them inline pollutes the transcript and blocks the
agent's attention on output the user usually doesn't care about.

## Scope
### In scope (v1)
- Replace the right panel's keymap section with a selection-driven layout:
  workspace facts, active prompt preview, and a "Recent activity" list of
  background-job runs for the selected workspace.
- One-line footer hint (`? help · enter open · / find`) replaces the panel's
  keymap so discoverability is preserved.
- Background-command execution surface:
  - Frontmatter on user-command files: `mode: background` and
    `notify: on-failure | always | never` (default `on-failure`).
  - Harness runs the command detached, captures stdout+stderr to a per-run log
    file, writes a sibling `meta.json` with cmd/start/end/exit, and drops a
    `.done` marker on completion.
  - On completion the deck's existing attention-dot path picks up the marker;
    failures stay pinned until acknowledged.
- Storage: `~/.awp/jobs/<workspace-id>/<cmd>-<ts>.{log,meta.json,done}`.
- Deck reads the latest N (default 10) job entries for the selected workspace
  on render.
- Selecting an entry (or pressing a key, e.g. `o`) opens the log in
  `$PAGER` / `less`.

### Out of scope (v1)
- Cross-workspace job aggregation view (`/jobs`).
- Job cancellation / re-run from the deck.
- Streaming/tailing job output inside the TUI.
- Quotas, retention/GC of old logs (document but defer; manual cleanup ok).
- Background mode for non-user-command tooling (hooks, agents).

## UX
### CLI
- User-command files gain optional frontmatter:
  ```yaml
  ---
  mode: background       # foreground (default) | background
  notify: on-failure     # never | on-failure (default) | always
  ---
  ```
- Invoking a `mode: background` command returns immediately with a one-line
  acknowledgement: `▸ install started — log: ~/.awp/jobs/<ws>/install-<ts>.log`.

### TUI
- Right panel layout, top-to-bottom:
  1. **Header**: `Project / Workspace`
  2. **Facts block**: status glyph + label, Live, Session, Path, Last activity.
  3. **Prompt block**: active prompt preview (expands to fill remaining space
     above the activity list).
  4. **Recent activity**: up to N rows.
     - Row format: `<glyph>  <cmd>   <relative-time>   <exit-or-running>`
     - Running → spinner glyph; failed → red; succeeded → dim/check.
     - Press `o` on the focused row in the activity list to open the log.
- Footer (existing row) gains: `? help · enter open · / find` — keymap moves
  fully into `?` overlay (`renderHelp`).
- `?` overlay must list the new `o` (open job log) binding; update
  `deckKeyGroups` in one place per CLAUDE.md guidance.

## Discovery Questions
1. **First user**: Andrew, running `/install` and similar setup commands from
   the deck without blocking the agent transcript.
2. **When**: any user-command that is slow, noisy, or doesn't need the agent's
   attention on its output.
3. **Result they need**: pass/fail signal + a path to the log when they care.
4. **Data sources**: harness exec wrapper writes log+meta; deck reads
   `~/.awp/jobs/<ws>/`.
5. **Smallest useful slice**: background-mode frontmatter + log-on-disk +
   right-panel "Recent activity" list with open-in-pager.
6. **Non-goals**: in-TUI streaming, cancellation, cross-workspace dashboards.
7. **Done looks like**: keymap is gone from the right panel; `/install`
   (marked `mode: background`) runs without taking over the chat, and its
   pass/fail + log path appear in the right panel for the active workspace.

## Open Questions
- Workspace-id key for `~/.awp/jobs/<id>/`: reuse the existing deck identity
  (project+workspace slug) or hash of path? Need to match what the attention
  system already uses.
- Should `notify: always` produce a transcript line on success, or only a
  status-line toast? Lean toward toast.
- Where does the harness wrapper live — same hook surface as existing
  post-workspace-start hooks, or new dedicated runner?

## Spec Change Log
- 2026-05-04: Initial draft.
- 2026-05-05: Implementation slice 1 landed.
  - Reused existing `internal/jobs` store rather than per-workspace job dirs;
    added `WorkspaceName/WorkspacePath/RepoRoot` fields to `deckui.Job` so
    the right panel can filter without reshape.
  - Background commands surface as `"background": true` on existing
    `config.UserAction` (no frontmatter file format); `notify` field
    deferred — failures already raise via the existing job tray counts.
  - Added a `runCustomJob` dispatcher (`ActionCustom` in run-job).
  - Replaced the right panel's keymap with a per-workspace **Recent
    activity** block (max 5 rows). Discoverability stays in the `?` overlay.
  - Collapsed the job-counts tray and status footer into a single bottom
    status bar: counts left, status/filter middle, `? help` flush right.
  - The `o` (open log) binding remains scoped to the `J` overlay; not
    re-bound in the right panel for this slice.

## Implementation Plan
1. Define on-disk format for `~/.awp/jobs/<ws>/` (log, meta.json, .done) and a
   small Go reader package shared by the deck.
2. Add background-command runner: reads frontmatter, forks detached, redirects
   I/O, writes meta + done marker, returns ack line.
3. Wire deck to scan job dir for the selected workspace; render "Recent
   activity" block; bind `o` to open the focused job's log via `$PAGER`.
4. Strip keymap from `renderDetails`; add footer hint; update `renderHelp` /
   `deckKeyGroups` to include `o`.
5. Hook `.done` markers into the existing attention-dot/refresh path so
   failures surface without manual refresh.
6. README updates: new frontmatter fields, new persisted dir, new `o` key,
   right-panel screenshot/description.

## Acceptance Criteria
- [ ] Right panel no longer renders the full keymap; layout matches UX spec.
- [ ] `?` overlay still documents every key, including new `o`.
- [ ] A user command with `mode: background` returns immediately and runs to
      completion in the background.
- [ ] Each background run produces `<cmd>-<ts>.log` and `<cmd>-<ts>.meta.json`
      under `~/.awp/jobs/<ws>/`, plus a `.done` marker on exit.
- [ ] Failed runs raise the workspace's attention dot until acknowledged.
- [ ] Pressing `o` on a run in the activity list opens its log in `$PAGER`.
- [ ] README documents frontmatter, persisted dir, and the new key.

## QA / Human Review Test Plan
### Setup
- [ ] Build current binary; ensure `~/.awp/` exists and is writable.
- [ ] Author a sample user command (e.g. `sleep 5 && echo ok`) with
      `mode: background`.

### Core Happy Path
- [ ] Trigger the background command; deck shows ack line, then a "running"
      row in Recent activity, then a success row.
- [ ] Press `o` on the success row; log opens in `$PAGER`.
- [ ] Switch workspaces; activity list updates to that workspace's runs only.

### Edge Cases & Failure Modes
- [ ] Command exits non-zero → row goes red, attention dot lights, log
      contains stderr.
- [ ] Command writes >1MB output → log captures it without truncation in v1
      (document if truncation is added later).
- [ ] Frontmatter missing/invalid → falls back to foreground with clear
      message.
- [ ] No `~/.awp/jobs/<ws>/` yet → activity block shows "No recent runs."

### Regression Checks
- [ ] `?` overlay layout unchanged aside from new entry.
- [ ] Foreground user commands still behave as before.
- [ ] Existing find-mode, new-menu, open, bookmark detail panels untouched.

### Reviewer Notes
- Capture before/after screenshots of the right panel.
- Note any flicker on activity-list refresh.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
