# Post Workspace Start Hooks Spec

## Metadata
- **Spec ID**: `20260409-a9q3`
- **Feature name**: Post workspace initialization hooks
- **Owner**: Andrew Cohen
- **Status**: Planned
- **Last updated**: 2026-04-09

## Goal
Allow projects to define setup commands that run automatically after creating a brand-new workspace.

## User Problem
When starting a new workspace, users repeatedly run bootstrap steps manually (copy `.env`, trust tooling, install deps). This is error-prone and slows down feature startup.

## Scope
### In scope (v1)
- Repo-local config file at `.awp/config.json`.
- Hook list at `hooks.bootstrap` as an ordered array of shell commands.
- Execute hooks when `awp w open` (with optional `-b`) initializes a missing workspace.
- Do not execute hooks when opening an already-existing workspace.
- Execute commands in workspace directory (`cwd = new workspace path`) via shell (`sh -lc`).
- Support `<root>` token replacement with absolute invocation working directory.
- On hook failure during initialization, rollback created resources:
  - forget JJ workspace,
  - kill created tmux window (if present),
  - remove workspace directory when under managed base,
  - remove workspace state entry.
- Return actionable error including failed command and command output.

### Out of scope (v1)
- Global/user-level config merging.
- Hook execution for `awp w open`.
- First-open-only or per-workspace once semantics.
- Additional template tokens beyond `<root>`.
- Structured retries/parallel hooks.

## UX
### CLI
Config example:
```json
{
  "hooks": {
    "bootstrap": [
      "cp <root>/.env .env",
      "mise trust",
      "pnpm i"
    ]
  }
}
```

Behavior:
- `awp w open qa` (workspace does not exist): confirms, creates workspace, runs hooks in order, creates tmux window after hooks succeed, then prompts before switching tmux window.
- If hook command 2 fails, command returns error and newly-created workspace artifacts are cleaned up.
- `awp w open qa` (workspace already exists): opens existing workspace only; no hooks run.

### TUI
- No new TUI surface in v1.

## Discovery Questions
1. Who is the first user?  
   Developers using `awp` in polyglot repos with setup steps.
2. When do they use this feature?  
   Immediately after running `awp w start` for a new workspace.
3. What exact output/result do they need?  
   New workspace is ready-to-work without manual bootstrap commands.
4. What data sources are required?  
   Local config file `.awp/config.json`, invocation cwd, created workspace metadata.
5. What is the smallest useful slice?  
   Ordered shell command list on new workspace creation only.
6. What are explicit non-goals?  
   Open-time hooks, global config, advanced templating.
7. What does “done” look like?  
   New workspace setup commands run automatically and rollback is reliable on failure.

## Spec Change Log
- 2026-04-09: Initial draft.
- 2026-04-09: Scope updated: hooks now run when `open` initializes a missing workspace; existing-open path remains hook-free.
- 2026-04-09: Hook config key renamed from `hooks.post_workspace_start` to `hooks.bootstrap`.
- 2026-04-09: New-workspace flow now switches tmux only after setup completes and user confirms by pressing a key.
- 2026-04-10: Tmux window creation moved after bootstrap hook success so first-open shell state reflects completed setup.

## Implementation Plan
1. Add `internal/config` loader for `.awp/config.json` with structs + validation and tests.
2. Add hook runner utility that runs command list in workspace cwd using existing command runner, with `<root>` substitution from invocation cwd.
3. Capture invocation cwd at process startup and pass into workspace service dependencies.
4. Integrate hook execution into workspace initialization path for `open` when creating a missing workspace.
5. Add rollback helper for partial start failures (JJ/tmux/state/filesystem cleanup).
6. Add/adjust tests in `internal/workspace/service_test.go` for:
   - hooks run on new start,
   - hooks skipped on existing start/open,
   - `<root>` substitution uses invocation cwd,
   - rollback on hook failure.
7. Surface concise error messaging in CLI path for failed hook command.

## Acceptance Criteria
- [ ] `.awp/config.json` with `hooks.bootstrap` is parsed and used.
- [ ] Hooks execute in declared order only when `awp w open` initializes a missing workspace.
- [ ] Hooks do not execute when opening an already-existing workspace.
- [ ] `<root>` resolves to absolute invocation cwd where command was run.
- [ ] Hook failure aborts initialization and fully rolls back newly created workspace artifacts.
- [ ] Error clearly identifies failing hook command and includes command output context.

## QA / Human Review Test Plan
### Setup
- [ ] Prerequisites installed and available in PATH (`jj`, `tmux`, shell tools).
- [ ] Test repo has `.awp/config.json` with a known command list.
- [ ] Clean state: target workspace name does not already exist.

### Core Happy Path
- [ ] Run `awp w open qa` (missing workspace) with valid hooks and confirm commands executed in workspace dir.
- [ ] Verify resulting JJ workspace exists, tmux window exists, state entry exists.

### Edge Cases & Failure Modes
- [ ] Use a failing hook (e.g., command 2 fails) and verify:
  - [ ] command returns non-zero with actionable error,
  - [ ] JJ workspace was forgotten,
  - [ ] no tmux window was created,
  - [ ] workspace directory removed (managed base),
  - [ ] state entry removed.
- [ ] Verify `<root>` expansion points to invocation cwd by running start from non-repo directory context where supported by current command flow.
- [ ] Verify malformed config returns clear parse/validation error.

### Regression Checks
- [ ] `awp w open <existing>` behavior unchanged (no hooks).
- [ ] list/info/rename/delete workflows unchanged.

### Reviewer Notes
- Capture commands run, observed output, and any cleanup anomalies.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
