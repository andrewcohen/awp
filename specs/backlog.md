# TODO (Later)

## State / Data Model
- [ ] Use a canonical shared-repo identity for global state keys (not workspace-root paths).
- [ ] Add migration to merge duplicate repo buckets in `~/.awp/workspace-state.json`.
- [ ] Add optional state cleanup command (e.g., `awp state gc` or `awp w gc-state`).

## Workspace UX
- [ ] Consider multi-repo listing support (`awp w list --all`, `--repo <path>`).
- [ ] Consider `awp w info --all` view for cross-project diagnostics.
- [ ] Add optional `--prompt` support to `awp w open` (and future interactive flow): after bootstrap completes in a newly initialized workspace, run configured agent command in the new tmux window (e.g., `pi <prompt>`).

## CLI Ergonomics
- [ ] Add shell completion command/install flow (`awp completion ...`, optional `install`).
- [ ] Add machine-readable output mode for list/info (e.g., `--json`).
- [ ] Consider migrating CLI parsing to Cobra once command surface grows further (doctor + workspace subcommands are increasing complexity).

## Quality / Ops
- [ ] Upgrade Go toolchain to 1.26.2 (go.mod + CI/dev environment alignment).
- [ ] Add integration tests covering real jj workspace flows across multiple workspaces.
- [ ] Add regression tests for global-state key stability across workspace roots.
