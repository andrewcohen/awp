# Workspace Management Spec

## Metadata
- **Feature name**: Workspace management (new feature workflow)
- **Owner**: @andrewcohen
- **Status**: Done
- **Last updated**: 2026-04-09

## Goal
Let a user start a new feature quickly from within a JJ repo by creating the workspace + tmux context in one flow.

## User Problem
A developer working in a Jujutsu repo wants to begin feature work without manually running multiple commands (workspace create, tmux window setup, switch).

## Scope
### In scope (v1)
- Start flow:
  1. Prompt for feature name if not provided by CLI arg/flag.
  2. Create JJ workspace named for the feature.
  3. Create tmux window named for that workspace.
  4. Switch user to that tmux window.
- Management commands: list, info, open, rename, delete/remove (workspace-oriented).
- `open`/`delete|remove` support stdin-pipe selection (for fzf flows) and interactive picker fallback when no arg is given.
- `start` supports bookmark targeting (`--bookmark`/`-b`).

### Out of scope (v1)
- Cross-repo workspace orchestration.
- Non-tmux terminal multiplexers.
- Advanced naming templates/policies (unless minimal validation required).

## UX
### CLI
- Primary entrypoint is CLI-first.
- If feature name missing, interactive prompt asks for it.

### TUI
- Lightweight picker UI is used for workspace selection when `open`/`delete|remove` are called without args and stdin is not piped.

## Decisions
1. **Command shape**: `awp workspace <start|list|info|open|rename|delete|remove>`.
2. **Feature name input**: `start` accepts positional name and `--name`; prompts only when absent. If name is absent and `--bookmark/-b` is provided, bookmark value is used as default name.
3. **Bookmark support on start**: `start` accepts `--bookmark`/`-b`; if provided, workspace is created from that revision and bookmark is set to `<workspace>@`.
4. **Name normalization**: normalize to lowercase kebab-case.
5. **Workspace root path**: managed workspaces are created under `~/.awp/workspaces/<name>`.
6. **Duplicate handling**: `start` opens existing workspace/window instead of failing.
7. **Repo guardrails**: command fails with clear “not a jj repository” error.
8. **Open semantics**: switch to existing tmux window; create one if missing.
9. **Rename semantics**: rename both JJ workspace and tmux window.
10. **Delete semantics**: delete/remove forgets JJ workspace and removes tmux window by default; confirmation required unless `--force`.
11. **List output**: `list` prints workspace names only (one per line, no header) for tactical use and easy piping; detailed metadata is in `workspace info`.
12. **Picker + pipe behavior**: for `open`/`delete|remove`, if workspace arg is missing then read one name from stdin pipe; if no pipe, open interactive picker.
13. **Open bookmark fallback**: `open --bookmark|-b <bookmark>` works without workspace arg; workspace name defaults from bookmark (normalized), and missing workspace is created from that bookmark revision.
14. **Command help**: `help`/`-h`/`--help` on subcommands (e.g. `awp w open help`) shows subcommand usage.
15. **MVP priority**: ship all commands in first cut.

## Spec Change Log
- 2026-04-09: Added `w` alias for `workspace`.
- 2026-04-09: Prompt text changed from `Feature name:` to `Name:`.
- 2026-04-09: Workspace root changed from `<repo>/.awp/workspaces/<name>` to `~/.awp/workspaces/<name>`.
- 2026-04-09: `workspace list` simplified to tactical output (`name`, `active`), and `workspace info <name>` added for details.
- 2026-04-09: Added `start --bookmark|-b`; create workspace from bookmark/revision and set bookmark to workspace head.
- 2026-04-09: Added picker/pipe ergonomics for `open` and `delete|remove` when no workspace arg is provided.
- 2026-04-09: Added `open --bookmark|-b` support without workspace arg (bookmark-derived workspace name + start fallback).
- 2026-04-09: Added subcommand help handling (e.g. `awp w open help`).

## Implementation Plan
1. Finalize command UX + semantics for start/list/info/open/rename/delete/remove.
2. Implement `start` command end-to-end with prompt + JJ + tmux + switch.
3. Add management commands in priority order with consistent error handling.
4. Add tests for command parsing, naming/validation, adapter calls, and failure cases.

## Acceptance Criteria
- [x] User can run start command in a JJ repo and get a new workspace + tmux window switched into it.
- [x] If no feature name is provided, user is prompted interactively.
- [x] List/open/rename/delete commands behave per agreed semantics with clear errors.
- [x] Edge cases are covered (duplicate names, missing repo, deletion confirmation).

## QA / Human Review Test Plan
### Setup
- [ ] In a terminal with `jj` and `tmux` installed, build binary: `go build ./cmd/awp`.
- [ ] Start inside a valid JJ repo and an active tmux session.

### Core happy path checks
- [ ] `awp w start --name "Add Auth"` creates/opens workspace `add-auth`, creates tmux window `add-auth`, and switches to it.
- [ ] `awp workspace list` prints names only (one per line, no header) and includes `add-auth`.
- [ ] `awp workspace info add-auth` shows detailed metadata (path, managed/jj/tmux status).
- [ ] `awp w open add-auth` switches to the `add-auth` window (creates it if manually removed).
- [ ] `awp w open` with no args opens picker; selecting workspace opens it.
- [ ] `awp w list | fzf | awp w open` works (stdin name selection).
- [ ] `awp w open -b saltor/no-default-standard-delivery-preference` works without workspace arg and creates/opens normalized workspace name.
- [ ] `awp w rename add-auth auth-v2` renames both JJ workspace and tmux window.
- [ ] `awp w delete auth-v2` prompts for confirmation; entering `y` deletes JJ workspace + tmux window.
- [ ] `awp w remove --force` with no args opens picker and deletes selected workspace.

### Input/prompt behavior
- [ ] `awp w start` prompts `Name:` and normalizes input (e.g., `My Feature` -> `my-feature`).
- [ ] `awp w start -b feature/foo qa` creates `qa` workspace from `feature/foo` and sets bookmark to `qa@`.
- [ ] `awp w delete <name>` with `n`/empty response cancels deletion.
- [ ] `awp w delete --force <name>` skips prompt and deletes.

### Edge/error behavior
- [ ] Running commands outside a JJ repo returns a clear "not a jj repository" error.
- [ ] Starting an existing workspace (`awp w start <name>`) opens existing instead of failing.
- [ ] Invalid/empty names after normalization return actionable errors.

### Reviewer notes
- Record exact commands run and any mismatches between expected and actual behavior.

## Validation
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build ./...`
