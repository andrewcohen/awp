# TODO (Later)

## State / Data Model
- [ ] Use a canonical shared-repo identity for global state keys (not workspace-root paths).
- [ ] Add migration to merge duplicate repo buckets in `~/.awp/workspace-state.json`.
- [ ] Add optional state cleanup command (e.g., `awp state gc` or `awp w gc-state`).

## Workspace UX
- [ ] Consider multi-repo listing support (`awp w list --all`, `--repo <path>`).
- [ ] Consider `awp w info --all` view for cross-project diagnostics.
- [ ] When building a future awp UI / agent-deck, add first-class agent attention notifications (done/waiting for user, blocked on approval, errored, and background-session needs-attention states) owned by awp instead of ad hoc pi extensions.

## Deck Imports
- [ ] `awp tmux-import [--apply] [--rename-agent]`: scans all tmux windows, finds ones whose cwd matches a known awp workspace path (longest-prefix), moves them into the proper `[awp]<repo>__<workspace>` session (creates if missing). Optionally renames the first moved window to `agent`. Dry-run by default.
- [ ] `awp repo-import [--default <name>]` (name TBD): adopt a repo as awp-managed when no secondary workspaces exist yet — records the primary jj workspace under the canonical source-repo key in state so it appears in the deck. Does NOT run `jj workspace add`. Default workspace name `default` (or CLI flag).

## Deck Theming
- [ ] Add first-class deck theming with a cohesive palette (target: Catppuccin Macchiato), and apply consistent semantic colors across list rows, status, hints, and details panes.

## Deck Commands
- [ ] Extend `.awp/config.json` with `commands: [{name, run}]`; per-command tmux window named `<name>` in workspace session.
- [ ] Built-in defaults merged by name (user wins): `agent` (interactive), `codereview` (`tuicr -r main..@`).
- [ ] Deck UX: list commands in details pane, picker key (`c`) or digit shortcuts; keep `l`/`t` as aliases for `logs`/`tests` if defined.
- [ ] Focus existing window vs force-rerun policy (force-rerun key TBD).

## CLI Ergonomics
- [ ] Add shell completion command/install flow (`awp completion ...`, optional `install`).
- [ ] Add machine-readable output mode for list/info (e.g., `--json`).
- [ ] Consider migrating CLI parsing to Cobra once command surface grows further (doctor + workspace subcommands are increasing complexity).

## Quality / Ops
- [ ] Upgrade Go toolchain to 1.26.2 (go.mod + CI/dev environment alignment).
- [ ] Add integration tests covering real jj workspace flows across multiple workspaces.
- [ ] Add regression tests for global-state key stability across workspace roots.
