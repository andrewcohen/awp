# UI polish and Charm migration spec

## Metadata
- **Spec ID**: `20260410-mstc`
- **Feature name**: `awp UI polish and Charm migration`
- **Owner**: AI coding agent
- **Status**: Planned
- **Last updated**: 2026-04-10

## Goal
Make awp’s interactive experiences feel like one coherent product by standardizing styling, key help, and interaction patterns across the existing Bubble Tea UIs, while selectively adopting more of the Charm ecosystem where it reduces custom code and improves usability.

## User Problem
Andrew already has several interactive awp surfaces, but they do not yet feel unified. The diff viewer, workspace picker, interactive open form, and stdin prompts all use different patterns, different levels of polish, and different implementation styles. That inconsistency makes the app feel rougher than it needs to and increases maintenance cost because similar UI behavior is hand-rolled in multiple places.

## Scope
### In scope (v1)
- Audit existing interactive UI surfaces and standardize them around a shared Charm-based design system.
- Add a shared theme/style/key-help layer for interactive UIs.
- Improve `awp diff` by adopting higher-leverage Bubble components where they fit, especially `help`, `key`, `viewport`, and loading/status affordances.
- Replace the custom workspace picker with a richer searchable Bubble-based picker.
- Replace the custom `awp w open` form with a Huh-based interactive flow if the library supports the needed UX cleanly.
- Move interactive confirmations/prompts for workspace flows behind a shared UI layer, with plain stdin/stdout fallback preserved for non-interactive contexts.
- Add/update tests for the changed interaction behavior.
- Keep non-interactive and scripted CLI behavior intact.

### Out of scope (v1)
- Rewriting every CLI command as a full-screen TUI.
- Forcing Huh onto the diff viewer or other multi-pane browser UIs.
- Adding net-new product features unrelated to polish, such as patch staging, comments, PR review actions, or multi-repo dashboards.
- Persisted themes, prompt history, or plugin-driven UI extension points.
- Introducing heavy new dependencies beyond Charm libraries already aligned with the project direction.

## UX
### CLI
- Existing command entrypoints stay the same: `awp diff`, `awp w open`, `awp w delete`, and related flows.
- Non-interactive use remains script-safe:
  - piped input still works
  - plain text output still works
  - dumb terminals still get actionable fallbacks/errors
- Interactive flows become more consistent:
  - common key hints
  - common cancel/submit behavior
  - common empty/error/loading states
- Forms and confirmations should prefer guided interactive UX when a real terminal is available.

### TUI
- All interactive UIs should share a recognizable visual language:
  - title/header treatment
  - borders/padding
  - semantic colors for success/warning/error/selection
  - footer help
- `awp diff` diff viewer should feel more polished and robust:
  - shared keymap/help footer via Charm help components
  - better loading/refresh feedback
  - independent scrolling for larger diff content where appropriate
  - predictable focus and refresh behavior
- Workspace selection should be searchable and visually polished.
- Guided forms should use Huh where it simplifies form structure, validation, and navigation.

## Discovery Questions
1. Who is the first user? Andrew, using awp daily inside jj repositories and tmux-oriented workflows.
2. When do they use this feature? During normal coding loops: opening workspaces, deleting/confirming actions, and reviewing diffs.
3. What exact output/result do they need? Interactive awp commands that feel cohesive, discoverable, and polished without sacrificing scriptability.
4. What data sources are required? Existing workspace lists, jj repo/diff state, tmux/workspace status, and terminal capability checks.
5. What is the smallest useful slice? Shared theme + key/help layer, a better workspace picker, and a more polished diff UI footer/status system.
6. What are explicit non-goals? Full TUI conversion for every command, feature creep in the diff viewer, and Huh-driven rewrites of browser-style UIs.
7. What does “done” look like? The main interactive awp flows share styles and help conventions, the picker/search/form UX feels modern, and the codebase has less hand-rolled UI state for standard interaction patterns.

## Spec Change Log
- 2026-04-10: Initial draft based on repository UI audit and a phased Charm ecosystem adoption plan.
- 2026-04-10: Decision: use Huh selectively for forms/confirmations, not as a replacement for custom multi-pane Bubble Tea UIs like the diff browser.

## Implementation Plan
### Build approach
We will build this incrementally, landing small reviewable changes that each improve one interaction surface while preserving existing command contracts and non-interactive behavior.

### Phase 1: shared Charm foundation
1. Add a new shared package, likely under `internal/charm/`, to hold reusable UI primitives:
   - semantic lipgloss styles rather than per-screen ad hoc colors
   - shared color palette tokens
   - shared key bindings via `bubbles/key`
   - shared footer help model via `bubbles/help`
   - terminal capability helpers used across interactive flows
2. Keep this package intentionally small and product-focused; it should not become a generic framework.
3. Update at least one existing UI to consume the shared styles and key definitions so the package proves its value immediately.

### Phase 2: modernize `awp diff`
1. Refactor `internal/ui/model.go` to replace hardcoded help strings with a shared key map + `help.Model`.
2. Introduce `viewport` for the hunk pane so large diffs scroll cleanly without hand-rolled clipping logic.
3. Add a loading/refresh affordance, likely using `bubbles/spinner`, while keeping current manual refresh semantics.
4. Preserve current behavior that matters:
   - file/hunk navigation
   - filter mode
   - editor jump
   - non-destructive refresh failures
5. Add/update model tests to cover the new help rendering, scroll behavior, and loading states.

### Phase 3: replace the workspace picker
1. Replace `internal/cli/picker.go` with a searchable picker built on `bubbles/list`.
2. Keep the current picker function contract so CLI wiring in `internal/cli/app.go` stays simple.
3. Preserve existing safety behavior:
   - no options still errors clearly
   - dumb terminal still falls back safely
   - cancel still returns a clear cancellation error
4. Add tests for selection, cancellation, filtering, and empty states where practical.

### Phase 4: migrate `w open` to Huh
1. Add `github.com/charmbracelet/huh` as a dependency only if it cleanly supports the existing open flow UX.
2. Replace the custom form implementation in `internal/cli/open_form.go` with a Huh-backed form that captures the same `openRequest` fields:
   - workspace name
   - bookmark
   - prompt
3. Preserve the current application/service boundary:
   - UI layer collects and validates input
   - workspace service still owns open/create behavior
4. Keep prefill behavior from parsed flags.
5. Keep dumb-terminal and non-interactive behavior unchanged.
6. Update CLI/form tests to verify submission, cancel behavior, validation, and prefilled values.

### Phase 5: interactive confirmations and prompts
1. Identify the current plain interactive prompts in workspace flows, especially:
   - missing-name prompt
   - delete confirmation
   - create confirmation
2. Add a small interactive prompting layer that can use Charm/Huh when running in a real terminal and fall back to stdin/stdout prompts otherwise.
3. Keep `--force`, piped input, and other non-interactive cases authoritative over any interactive prompt behavior.
4. Add tests around interactive-vs-non-interactive branching so the UX polish does not regress automation.

### Phase 6: validation and cleanup
1. Remove duplicated styles/help text that become obsolete after the migration.
2. Make naming and structure consistent across the interactive packages.
3. Run full validation:
   - `go test ./...`
   - `go vet ./...`
   - `go build ./...`
4. Manually validate the highest-value terminal flows and record follow-up issues in the spec or backlog.

### Implementation notes / guardrails
- Do not rewrite all UIs at once.
- Do not force Huh into the diff browser; Bubble Tea + Bubbles remains the right fit there.
- Prefer adapting existing command boundaries instead of redesigning service interfaces.
- Preserve scriptability and dumb-terminal behavior in every phase.
- Keep each change independently shippable.

## Acceptance Criteria
- [ ] A shared internal UI foundation exists and is used by at least the main interactive surfaces instead of duplicating styles/help strings.
- [ ] `awp diff` uses shared key bindings/help and provides visibly improved status/loading/scroll behavior without regressing current navigation and editor-jump behavior.
- [ ] Workspace picker flow is searchable and more polished than the current custom cursor-based picker.
- [ ] Interactive `awp w open` form is simplified and improved, preferably via Huh, without changing the underlying workspace service behavior.
- [ ] Interactive confirmations/prompts are more consistent across workspace flows, while non-interactive and dumb-terminal behavior remains safe and clear.
- [ ] Tests cover the changed UI behavior and the repo still passes normal validation commands.

## QA / Human Review Test Plan
### Setup
- [ ] Prerequisites installed and available in PATH (e.g., `jj`, `tmux`, project binary).
- [ ] Test environment/state prepared with at least one jj repo containing changed files and multiple workspaces.
- [ ] Build `awp` from repo root.

### Core Happy Path
- [ ] Run `awp diff` and verify the diff UI opens with a consistent header/footer/help treatment.
- [ ] Verify key help is visible and matches actual working bindings.
- [ ] Verify long diff content scrolls cleanly and refresh/loading states are understandable.
- [ ] Run `awp w open` interactively and verify the form is polished, prefilled correctly from flags, and submits/cancels cleanly.
- [ ] Run workspace selection flows and verify the picker is searchable and easy to navigate.

### Edge Cases & Failure Modes
- [ ] Verify dumb terminal behavior remains clear and safe for all affected interactive commands.
- [ ] Verify blank/invalid interactive input surfaces actionable validation messages.
- [ ] Verify refresh or data-loading errors in `awp diff` remain non-destructive where practical.
- [ ] Verify interactive confirmations can still be bypassed appropriately in non-interactive or forced flows.

### Regression Checks
- [ ] Existing scripted uses of `awp w open`, `awp w delete`, and other workspace commands still behave correctly.
- [ ] Existing editor jump behavior in `awp diff` still works.
- [ ] Existing non-interactive outputs such as `awp doctor`, `awp w list`, and `awp w info` remain correct.

### Reviewer Notes
- Capture terminal used, theme/rendering observations, sample repos/workspaces tested, and any remaining inconsistencies between interactive flows.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
