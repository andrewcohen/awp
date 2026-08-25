# Open prompt + interactive workflow spec

## Metadata
- **Spec ID**: `20260410-k3m8`
- **Feature name**: `awp w open --prompt` and interactive open flow
- **Owner**: AI coding agent
- **Status**: Done
- **Last updated**: 2026-04-10

## Goal
Let users open or create a workspace through a richer interactive flow, and optionally start an agent prompt automatically in a newly created workspace tmux window.

## User Problem
Users currently have to remember the exact `awp w open` argument combinations, and there is no built-in way to kick off an agent session automatically after a new workspace is bootstrapped.

## Scope
### In scope (v1)
- Add `--prompt` support to `awp w open`.
- When `awp w open` is launched interactively without a positional workspace name, show a Bubble Tea form.
- Prefill the interactive form with any parsed open flags.
- On newly created workspaces, send an agent command to the tmux window after bootstrap hooks complete.

### Out of scope (v1)
- Persisting prompt history.
- Configurable agent command selection.
- Reworking other subcommands to use the same form framework.

## UX
### CLI
- `awp w open --prompt "fix failing tests" qa`
- `awp w open --bookmark team/feature --prompt "investigate merge conflict"`
- Existing non-interactive flows continue to work.

### TUI
- Interactive open form includes workspace name, bookmark, prompt, and auto-create confirmation.
- Existing workspace names are visible in the form to make opening an existing workspace easy.
- If flags are provided before entering the form, their values are prefilled.

## Discovery Questions
1. Who is the first user? Andrew working inside jj + tmux repos.
2. When do they use this feature? When opening or creating a workspace from the terminal.
3. What exact output/result do they need? A tmux window opened to the workspace, optionally with an agent command started automatically.
4. What data sources are required? Existing workspace list, jj workspace existence, tmux window state.
5. What is the smallest useful slice? `--prompt` plus a single Bubble Tea form for `w open`.
6. What are explicit non-goals? General form framework or configurable providers.
7. What does “done” look like? New tests cover CLI parsing, interactive flow entry, and prompt execution for newly created workspaces.

## Spec Change Log
- 2026-04-10: Initial draft.
- 2026-04-10: Implemented Bubble Tea open form for interactive `awp w open`; `--bookmark`, `--prompt`, and `--yes` prefill the form when it is shown. Prompt auto-launch is limited to newly created workspaces.

## Implementation Plan
1. Extend open parsing to accept `--prompt` and trigger an interactive Bubble Tea form when appropriate.
2. Add a reusable open-form model that can display existing workspaces and prefilled values.
3. Extend workspace/tmux behavior so newly created workspaces can receive an agent command in their tmux window.
4. Add/update CLI and workspace tests.

## Acceptance Criteria
- [x] `awp w open` accepts `--prompt` / `-p`.
- [x] Interactive `open` form appears for interactive no-name usage and prepopulates parsed flags.
- [x] A prompt is only launched automatically for newly created workspaces.
- [x] Existing open/list/delete flows continue to work.

## QA / Human Review Test Plan
### Setup
- [ ] Prerequisites installed and available in PATH (e.g., `jj`, `tmux`, project binary).
- [ ] Test environment/state prepared (repo, config, seed data, clean shell).

### Core Happy Path
- [ ] Run `awp w open` in a tty and verify the Bubble Tea form appears.
- [ ] Submit the form for an existing workspace and verify it opens.
- [ ] Submit the form for a new workspace with `--prompt` set and verify the tmux window starts the prompt command.

### Edge Cases & Failure Modes
- [ ] Blank workspace + blank bookmark is rejected.
- [ ] `--prompt` without workspace still works when bookmark supplies the name.
- [ ] Existing workspace open does not inject the prompt command.

### Regression Checks
- [ ] `awp w open qa`, `awp w open -b team/feature`, and picker/piped flows still behave correctly where applicable.
- [ ] `awp w delete` and other workspace subcommands still behave correctly.

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
