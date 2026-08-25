# Deck Inline New Workspace Form

## Metadata
- **Spec ID**: `20260505-ucc0`
- **Feature name**: Deck inline new-workspace form
- **Owner**: andrew
- **Status**: Done
- **Last updated**: 2026-05-06

## Goal
Stop the alt-screen bleed when pressing `n` (new workspace) in the deck. The form
should render as a state inside the deck's existing `tea.Program` instead of a
second nested `tea.Program` launched via `tea.Exec`.

## User Problem
When the user presses `n`, the deck calls `tea.Exec(deckOpenCommand)` which
ends the deck's alt-screen, runs a *separate* `tea.NewProgram(...,
tea.WithAltScreen())` for the form, then restores the deck on completion.
The transition leaves visible artifacts: cells outside the form's card show
content from before the deck started (shell history, `jj` output, the deck's
own prior frame). This was originally diagnosed as a render-padding issue and
patched with `tea.ClearScreen` + `lipgloss.Place` + `/dev/tty` size probing —
none of which fully fixed it because the root cause is the nested-program
architecture, not the form's view.

For comparison, the deck's other modal states (jobs overlay, confirm-delete,
quick-action prompts) live as flags in `deckui.Model` and switch the deck's
`View()`. Those don't bleed because they share the deck's renderer and
alt-screen.

## Scope
### In scope (v1)
- Move the new-workspace form from `internal/cli/open_form.go` (separate
  `tea.Program`) into `internal/deckui` as a state of the deck `Model`.
- The deck gains a `newWorkspaceMode bool` (or equivalent enum) plus the
  embedded `textinput.Model` / `textarea.Model` fields needed for the form.
- `n` toggles the flag; `View()` renders the form via a new
  `renderNewWorkspaceForm` helper instead of the row list when active.
- Submit dispatches the create job through the existing
  `AsyncJobLauncher` / `NewWorkspaceRequest` path. Cancel restores the row list
  with no side-effects.
- Delete `deckOpenCommand`, `WithNewWorkspaceLauncher`, the `tea.Exec` call site,
  and `runOpenWithCharm`. Wiring in `internal/cli/deck.go` shrinks accordingly.
- Preserve the `Ctrl+G → $EDITOR` flow for the prompt field. Since we no longer
  have a clean `tea.Exec` boundary for the form alone, the deck issues
  `tea.ExecCommand` for `$EDITOR` directly when the user requests it; the deck
  re-renders cleanly on return because exec on the parent program is the
  supported path.

### Out of scope (v1)
- Visual redesign of the form (keep the existing fields, layout, key map, and
  Ctrl+G-to-editor behavior).
- Changing what happens after submission (still hands off to the async job
  launcher; no inline progress changes).
- Migrating `runOpenWithCharm`'s standalone usage outside the deck (it has
  none — `app.go`'s `openForm` field is currently set to `runOpenWithCharm`
  but only the deck calls into it; verify and remove).
- Re-using the form as a generic dialog component for other deck flows.

## UX
### CLI
- No CLI surface changes. `awp deck` still launches the same way.

### TUI
- Press `n` in the deck → form replaces the deck row list (full viewport, no
  card popup, no alt-screen handoff).
- Tab / Shift+Tab cycle fields (workspace name, bookmark, prompt, submit/cancel).
- Enter submits when on the action row, otherwise focuses the action row.
- Shift+Enter inserts a newline in the prompt textarea.
- Ctrl+G opens the prompt in `$EDITOR` (issued via `tea.ExecCommand` on the
  deck program so the editor takes over the deck's terminal cleanly).
- Esc cancels and returns to the row list.
- The deck's `?` help overlay gets a new section / row documenting the form
  keys (or notes that `n` opens the form and lists its keys inline).

## Discovery Questions
1. **Who is the first user?** Andrew, on every workspace creation.
2. **When?** Pressing `n` from the deck row list.
3. **What exact result?** A submitted `NewWorkspaceRequest` dispatched through
   the existing async job launcher; deck stays open with no alt-screen bleed.
4. **Data sources?** Bookmarks list (already fetched lazily by the deck for
   bookmark-quick), the user's typed inputs.
5. **Smallest useful slice?** Move the three fields + submit/cancel into the
   deck model, render in `View()`, route keys, dispatch on submit. Defer
   visual polish.
6. **Explicit non-goals?** Form redesign, removing async-job indirection,
   changing prompt-editor flow shape.
7. **Done looks like?** Pressing `n` shows the form with no bleed; submit
   creates a workspace via the async job; cancel returns cleanly; existing
   tests for `NewWorkspaceDoneMsg` paths in `deckui` still hold (or are
   migrated to the new in-model flow).

## Implementation Plan
1. **Sketch the model surface**: add `newWorkspaceMode bool`,
   `newWorkspaceForm` (struct holding the textinput/textarea models +
   focus state) to `deckui.Model`. Define an internal helper
   `(m *Model) startNewWorkspaceForm(initial NewWorkspaceInitial)` that
   resets and focuses the first field.
2. **Move form code into deckui**: copy the relevant pieces of
   `internal/cli/open_form.go` into `internal/deckui/new_workspace_form.go`
   (Update / View helpers, key bindings, validation). Drop the standalone
   `tea.Program` / `runOpenWithCharm`. Strip the `lipgloss.Place` /
   `WindowSizeMsg` workarounds that I added to the orphan version.
3. **Route keys**: in `Model.Update`, when `newWorkspaceMode` is true,
   delegate `tea.KeyMsg` to `updateNewWorkspaceForm`. Submit emits
   `startCreateAction` (existing async dispatch path); cancel just clears
   the flag and emits a `status` toast.
4. **Render in View**: when `newWorkspaceMode` is true, replace the
   row-list section of the deck view with the rendered form. Reuse the
   deck's existing width/height accounting so the form lays out correctly.
5. **Editor exec**: use `tea.ExecCommand` from the deck program (the
   editor exec is supported — that's exactly what `tea.Exec` was designed
   for). Wrap the result in a `promptEditedMsg` like before.
6. **Wire `n` key**: replace the `tea.Exec(deckOpenCommand)` branch in
   `Model.Update`'s `n` handler with `m.startNewWorkspaceForm(...)`.
7. **Strip dead code**: delete `deckOpenCommand`,
   `WithNewWorkspaceLauncher`, the launcher field/closure in `internal/cli/deck.go`,
   `runOpenWithCharm`, and the standalone `openFormModel`. Clean up
   `internal/cli/app.go::openForm` plumbing if unused.
8. **Tests**: keep `TestAsyncCreateSkipsProgressMode` and
   `TestAsyncCreateLauncherErrorSetsStatus`; update them so they exercise
   the inline-form `Submit` path rather than `NewWorkspaceDoneMsg`. Add a
   test that pressing `n` enters form mode, fills inputs, and Submit emits
   the expected `AsyncJobSpec`.
9. **Docs**: update README's deck-keys table entry for `n` to note the
   form is inline now (no popup); leave key bindings table untouched.
   Update `?` help overlay if it references the form.

## Acceptance Criteria
- [ ] Pressing `n` shows the form with **no** characters from the prior
      deck frame or shell history visible anywhere on screen.
- [ ] Tab / Shift+Tab / Enter / Shift+Enter / Esc behave exactly as in
      the prior standalone form.
- [ ] Ctrl+G opens the prompt in `$EDITOR` and returns to the form with
      the edited value populated; the deck's alt-screen restores cleanly
      with no bleed.
- [ ] Submit dispatches an `AsyncJobSpec` with the correct
      `Action`/`RepoRoot`/`Name`/`Bookmark`/`Prompt`/`Title` (matching
      what `runOpenWithCharm` → `NewWorkspaceDoneMsg` produced).
- [ ] Cancel returns to the row list with `status = "new: cancelled"`
      and no side effects.
- [ ] `deckOpenCommand`, `WithNewWorkspaceLauncher`, and
      `runOpenWithCharm` are removed (or their last reference deleted).
- [ ] All existing tests pass; new test covers entering the form,
      submitting, and verifying the dispatched spec.

## QA / Human Review Test Plan
### Setup
- [ ] `awp` built from this branch.
- [ ] `jj` repo with at least two existing workspaces and one bookmark
      (so the form has plausible inputs).
- [ ] tmux running so the popup-launched deck path is exercised.

### Core Happy Path
- [ ] Open the deck inside the tmux popup binding. Press `n`. Confirm
      the form renders cleanly — no left-edge artifacts, no shell history
      bleed.
- [ ] Type a name + bookmark + prompt, Tab to Submit, Enter. Confirm a
      workspace creation job is dispatched (jobs overlay shows it),
      and the deck stays interactive.
- [ ] Press `n` again, then Ctrl+G in the prompt field. Confirm `$EDITOR`
      opens, save+quit, return to the form with the edited prompt
      populated and no bleed.

### Edge Cases & Failure Modes
- [ ] Submit with both name and bookmark blank → form shows the
      "workspace name or bookmark is required" error inline.
- [ ] Esc from any field → returns to row list, status reads
      "new: cancelled".
- [ ] Open the deck **outside** a tmux popup (regular pane). Press `n`.
      Same — no bleed.
- [ ] Resize the terminal while the form is open. Confirm it re-lays
      out cleanly.

### Regression Checks
- [ ] Jobs overlay (`J`) still opens and closes cleanly.
- [ ] Confirm-delete (`D`) still works and returns cleanly.
- [ ] `P` scope cycle still repaints (already fixed in
      `c578fa49`; verify it didn't regress).
- [ ] The async job created by the form completes and the new
      workspace appears in the deck row list as before.

### Reviewer Notes
- The deletion of `deckOpenCommand` + `runOpenWithCharm` should
  produce a noticeable line-count drop in `internal/cli/deck.go` and
  `internal/cli/open_form.go`. Flag if not.
- Watch for any remaining `tea.Exec` call in the deck flow apart from
  the editor exec — there shouldn't be one for the form anymore.

## Spec Change Log
- 2026-05-05: Initial draft.
- 2026-05-06: Implemented. Form lives at
  `internal/deckui/new_workspace_form.go` as a plain struct (not a
  `tea.Model`), wired into `Model` via `newWorkspaceMode` /
  `newWorkspaceForm` / `newWorkspaceRepo` fields. `n` enters form
  mode directly; submit dispatches via the existing `startCreateAction`
  path. Architectural rule documented in `CLAUDE.md` (TUI / lipgloss
  → Bubble Tea program structure) and `internal/deckui/doc.go`. The
  legacy `NewWorkspaceDoneMsg` handler is retained as a no-op safety
  net but the dispatch path no longer fires it. `runOpenWithCharm`
  remains in `internal/cli/open_form.go` because the standalone
  `awp open` CLI still uses it as its sole `tea.Program` (no nesting,
  no bleed).

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
