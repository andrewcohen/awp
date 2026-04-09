# Doctor Command Spec

## Metadata
- **Spec ID**: `20260409-g6r4`
- **Feature name**: Doctor command
- **Owner**: Andrew Cohen
- **Status**: In Progress
- **Last updated**: 2026-04-09

## Goal
Provide a single troubleshooting command that reports common misconfigurations and inconsistent workspace state with concrete remediation hints.

## User Problem
When workspace creation/opening fails, users currently need to manually debug JJ, tmux, config, and workspace metadata. This is slow and confusing.

## Scope
### In scope (v1)
- New top-level command: `awp doctor`.
- No flags in v1; command prints check results and fix hints.
- Checks:
  - `jj` availability
  - `tmux` availability
  - repository detection (`jj root`)
  - hook config parse (`.awp/config.json`)
  - managed workspace directory availability/writability
  - JJ workspace working-copy integrity (`<workspace>@` resolvable)
- Exit non-zero when issues are found.

### Out of scope (v1)
- Automated repair mode.
- JSON output.
- Interactive prompts.

## UX
### CLI
`awp doctor` prints line-by-line statuses:
- `✓` success
- `!` warning/non-blocking
- `✗` issue with suggested next action

Examples of hints:
- stale workspace: `try: jj workspace forget <name>`
- broken config: parse error path/details

### TUI
- None.

## Discovery Questions
1. Who is the first user?  
   Developers actively using `awp workspace open/remove`.
2. When do they use this feature?  
   After command failures or when setup behaves unexpectedly.
3. What exact output/result do they need?  
   A short list of checks and clear next actions.
4. What data sources are required?  
   Local shell tools, JJ repo metadata, `.awp/config.json`, filesystem paths.
5. What is the smallest useful slice?  
   Read-only checks with remediation hints.
6. What are explicit non-goals?  
   Full auto-repair and machine-readable output.
7. What does “done” look like?  
   `awp doctor` reliably surfaces likely causes for broken open/remove flows.

## Spec Change Log
- 2026-04-09: Initial draft.
- 2026-04-09: Hook config key renamed to `hooks.bootstrap`; doctor now flags unsupported hook keys.

## Implementation Plan
1. Add `internal/doctor` service to run checks and print status lines.
2. Wire top-level `doctor` command in CLI app.
3. Wire doctor service in `cmd/awp/main.go`.
4. Add unit tests for doctor service and CLI dispatch.

## Acceptance Criteria
- [x] `awp doctor` runs as top-level command.
- [x] It reports environment/repo/config/workspace integrity checks.
- [x] It exits non-zero when issues are found.
- [x] It includes actionable hints for stale workspace issues.

## QA / Human Review Test Plan
### Setup
- [ ] Build binary: `go build ./cmd/awp`.
- [ ] Run inside a JJ repo.

### Core Happy Path
- [ ] Run `awp doctor` in healthy repo and verify all checks pass.

### Edge Cases & Failure Modes
- [ ] Break `.awp/config.json` and verify parse error is reported.
- [ ] Create stale JJ workspace entry and verify doctor flags it with forget hint.

### Regression Checks
- [ ] Workspace commands still behave as expected.

### Reviewer Notes
- Capture doctor output and any false positives.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
