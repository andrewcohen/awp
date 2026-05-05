// Package deckui implements the awp deck — a Bubble Tea TUI that lists
// workspaces across projects, surfaces async job state, and dispatches
// workspace lifecycle actions.
//
// # Architecture: one program, many modal states
//
// The deck is a single `tea.Program`. Every "modal" UI it presents — the
// jobs overlay, the confirm-delete prompt, the new-workspace form, the
// review-PR picker, find-mode navigation, the project-open list — lives
// as a state of the deck `Model`, not as a separate Bubble Tea program
// launched via `tea.Exec`.
//
// Concretely, each modal contributes:
//
//   - A flag (or enum) on `Model`: `jobsOverlay`, `confirmDelete`,
//     `newMenuMode`, `bookmarkMode`, `reviewMode`, `findMode`,
//     `openMode`, `actionMode`, `newWorkspaceMode`, etc.
//   - Any sub-component state alongside the flag: cursor, filter input,
//     loaded data slice, sub-form fields. These are plain structs; they
//     do not implement `tea.Model`.
//   - A branch in `Model.Update` that takes precedence when the flag is
//     set, delegating key handling to the modal's local state.
//   - A branch in `Model.View` (or one of the panel-composition helpers)
//     that renders the modal in place of the row list.
//
// # Why not nest tea.Programs?
//
// `tea.NewProgram(...).Run()` from inside another Bubble Tea program (via
// `tea.Exec` / `tea.ExecCommand`) was tried for the new-workspace form
// and produced unfixable alt-screen bleed: the outer program exits its
// alt-screen before the inner enters its own, and the alt-screen buffer
// state at handoff varies by terminal/tmux. `tea.ClearScreen`,
// `lipgloss.Place` padding, and `/dev/tty` size probing all failed to
// cure it. The architectural fix — and the one this package now
// requires — is to keep modals inside the deck's existing program.
//
// `tea.Exec` and `tea.ExecProcess` are still appropriate for **external**
// commands: `$EDITOR` for prompt-edit, pagers for log viewing, ad-hoc
// shell commands. The terminal handoff there is well-defined because
// the external command is not a Bubble Tea renderer.
//
// # Adding a new modal
//
//  1. Add a flag (and any sub-state) to `Model`. Document it next to
//     the existing flags so the scope is obvious.
//  2. Build the sub-component as a plain struct with:
//     `update(msg tea.Msg) (self, tea.Cmd, action)` where `action` is
//     a small enum the deck inspects to know whether the modal is
//     done, cancelled, or wants to fire a side-effect.
//     `view(width int) string` returns rendered lipgloss output.
//  3. In `Model.Update`, when the flag is set, delegate to the
//     sub-component's `update`, then act on the returned action
//     (clear the flag, dispatch a job, etc.). Keep the rest of the
//     deck's key handling untouched — the flag short-circuits.
//  4. In `Model.View` (or the helper that builds the bottom panel),
//     branch on the flag to render the sub-component instead of the
//     row list.
//  5. Update `?` help and `deckKeyGroups` so any new key bindings
//     show up in both the help overlay and the right-panel hint
//     surface.
//
// Reference implementations: `confirmDelete` (simplest — yes/no
// prompt), `jobsOverlay` (full-screen with cursor + per-row actions),
// `findMode` (multi-stage with derived hint state), `newWorkspaceMode`
// (sub-form with text inputs + textarea + editor exec).
package deckui
