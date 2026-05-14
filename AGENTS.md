# AGENTS.md

Guidance for AI coding agents working on **awp** (Agentic Workspace Pilot), a Go CLI and TUI application.

## Purpose

This repository is under active redesign. Prior behavior should not be treated as a requirement.
Focus on the current task request and keep solutions simple, correct, and easy to change.

## Working Principles

- Prefer small, incremental changes.
- Clarify assumptions before implementing anything ambiguous.
- Favor readability over cleverness.
- Keep dependencies minimal unless explicitly requested.
- Preserve backward compatibility only when the task asks for it.

## Engineering Standards

- Keep code idiomatic for the language and project style.
- Add or update tests for behavior you change.
- Keep public interfaces minimal and stable.
- Handle errors explicitly and return actionable messages.
- Avoid hidden global state where possible.

## TUI / lipgloss

The deck and other TUIs are built on Bubble Tea + lipgloss. When working on UI:

- Use lipgloss styling primitives (`MarginTop`, `MarginBottom`, `PaddingLeft`, `Border*`, `Foreground`, `Bold`, `Width`) rather than ad-hoc tricks like inserting `""` rows or padding strings with spaces. Spacing should belong to a style, not the row list.
- Inter-block spacing (between project groups, sections, headers) → `MarginTop` / `MarginBottom` on the block's style. Inner spacing → `Padding*`.
- Reuse helpers like `statusGlyph` / `statusColor` for status colors. Don't hardcode color codes inline; if you need a new semantic color, add it to the existing helpers so the legend in `?` help and the row renderer stay in sync.
- Terminals render whole rows only — half-line gaps don't exist. If a layout feels too dense, prefer a faint top/bottom border (`BorderTop(true).BorderForeground(...)`) over stacking blanks.
- Keep the `?` help overlay in `internal/deckui/model.go::renderHelp` updated whenever you add or rebind a key, or change a status state.

### Design system (colors, padding, selection)

The TUI runs against Catppuccin Macchiato as the developer's working theme.
The conventions below keep every screen — deck, diff viewer, CLI pickers,
charm prompts — looking consistent. **Don't introduce variant styles.**

**Colors.** All colors route through the shared palette in
`internal/charm/palette.go` (exported tokens) or its package-local aliases
(`internal/deckui/palette.go`). The tokens are ANSI 16 indices ("0"–"15")
so the user's terminal palette remaps them automatically:

| Token | ANSI | Semantic role |
|-------|------|---------------|
| `Accent` | 6 (teal) | Titles, headers, "starting" status, open PR, focus |
| `Info` | 4 (blue) | PR numbers, job-running glyph |
| `Success` | 2 (green) | Working / approved / done / author |
| `Warning` | 3 (yellow) | Waiting / pending / draft / **row selection** / orphaned |
| `Danger` | 1 (red) | Errors, CI failing |
| `Spinner` | 5 (magenta) | Spinner only |
| `Strong` | 15 (bright white) | Emphasized text |
| `Muted` | 8 (bright black) | Hints, footer, dim labels |
| `BgPanel` | 0 (surface) | Reserved — currently unused |

- **Never** call `lipgloss.Color("123")` with a raw 256-color code. Add a
  semantic token to `internal/charm/palette.go` first if you need a new
  role.
- **Never** call `.Background(...)` on body or padding cells. The deck
  renders in inline mode (no alt-screen — see `internal/cli/deck.go`), so
  unpainted cells inherit the tmux pane bg and blend naturally. Painted bg
  cells stand out as a different shade.

**Selection style.** Every list / picker / overlay uses the same
selection treatment so the eye instantly recognizes "this is the active
row" regardless of which screen you're on:

- `Foreground(charm.Warning).Bold(true)` on the row's label
- A `┃ ` left-bar prefix in `colWarning` + bold (use the prefix slot
  pattern — see `renderList` in `internal/deckui/model.go`)
- No background fill, no inverse video, no border-left chip

Touched lists: deck workspace list, jobs overlay, new menu, open picker,
bookmark picker, review picker, `new_flow.go` standalone pickers, the diff
viewer's `styleSelected`, and bubbles list theming in `charm/theme.go`.
Add new selectable lists in the same shape.

**Panel padding.** All body-area panels use `Padding(1, 1, 1, 1)` — 1 row
top/bottom, 1 col left/right. The footer (`composeStatusBar` wrapper) does
the same. This gives the deck a uniform 1-cell breathing margin and keeps
every panel's content aligned at col 1.

- Modals/popovers (help, jobs overlay) have their own `Padding(1, 2)`
  inside a rounded border — keep that pattern.
- The footer renders via `composeStatusBar(activities, spinnerGlyph,
  rightSeg, m.width-2)` wrapped in `Padding(1, 1, 1, 1)`.

**Status messages.** Cancellations clear `m.status` to `""` instead of
printing `"...: cancelled"`. The user already pressed esc; echoing the
fact is noise. Errors and completion messages still set `m.status` for
durable feedback.

**Spinner.** Tick handler in `case spinner.TickMsg` continues while
either `m.busy` (foreground work) OR `len(m.activities) > 0` (background
work in the activity bar). Bootstrap a `m.spinner.Tick` from
`case jobsListMsg` when `len(m.activities) > 0` so the spinner glyph in
the bottom bar actually animates from a cold start.

**Inline mode (no alt-screen).** `internal/cli/deck.go` calls
`tea.NewProgram(model, ...)` without `tea.WithAltScreen()`. The deck
renders directly into the tmux pane so its bg blends with the surrounding
pane. The `padBlock` between body and footer is space-filled (no SGR) to
overwrite previous-frame cells without painting a bg.

**Help overlay layout.** `renderHelp` uses a two-column layout when
`innerWidth >= 70`: status legend (agent / PR / activity) on the left,
key bindings on the right. Falls back to vertical stacking on narrower
viewports. Status legend rows show glyph + state name only — no verbose
explanations.

**Modal open must dispatch `tea.ClearScreen`.** Any handler that flips a
modal flag (`m.jobsOverlay`, `m.helpMode`, `m.newWorkspaceMode`,
`m.filtering`, etc.) MUST return `tea.ClearScreen` in its command batch
on the transition. The deck shares one renderer across modals, and the
renderer's previous-frame buffer otherwise leaves stripes of the
underlying view visible wherever the modal doesn't write
(`lipgloss.Place` padding alone does not cure it — see the
new-workspace-form site and the `/` filter site for the pattern).

### Bubble Tea program structure

**One program, one renderer.** The deck is a single `tea.Program`. Modal flows
(jobs overlay, confirm-delete, new-workspace form, find-mode, review picker,
quick-action prompts, etc.) live as **states inside `deckui.Model`** — flags
plus sub-component structs that `Update` routes keys to, and `View` renders in
place of the row list.

- **Do not** launch a second `tea.Program` from inside the deck. `tea.Exec` /
  `tea.ExecProcess` is for **external** commands (`$EDITOR`, `git`, `vi`,
  pagers) — never for another Bubble Tea program. Nested Bubble Tea programs
  cause alt-screen bleed during the entry/exit handoff that no amount of
  `tea.ClearScreen` / `lipgloss.Place` padding fully cures (see
  `specs/20260505-ucc0-deck-inline-new-workspace-form-spec.md` for the
  postmortem).
- Reference patterns in `internal/deckui/model.go`: `jobsOverlay`,
  `confirmDelete`, `newMenuMode`, `bookmarkMode`, `reviewMode`, `findMode`.
  Mirror those when adding a new modal: a `bool` flag (or enum) on `Model`,
  the sub-component's state held alongside it, a delegated key handler
  inside `Update`, and a branch in `View` (or whatever helper composes the
  bottom panel) that swaps in the modal's render.
- Sub-components (form structs, pickers) should be plain structs with
  `update(msg) (self, cmd, action)` and `view(width) string` methods — not
  `tea.Model` implementations. Keeping them off `tea.Model` prevents future
  drift toward "just call `tea.NewProgram` here."
- The `?` help overlay (`internal/deckui/model.go::renderHelp`) and the
  `deckKeyGroups` slice are the canonical key-binding surface. Any new modal
  key binding has to be reflected there.

## Security & Safety

- Treat all external input as untrusted.
- Avoid command injection, path traversal, and unsafe shell usage.
- Use least-privilege defaults for files, network calls, and credentials.
- Never hardcode secrets or tokens.

## Validation Before Handoff

When applicable, run:

- `go test ./...`
- `go vet ./...`
- `go build ./...`

If you cannot run something, state what was not run and why.

## Version Control

- Prefer **Jujutsu (`jj`)** workflows by default.
- Use git only when explicitly requested or when `jj` cannot do the task.
- Name new `jj` bookmarks with the `andrew/` prefix.

## Spec Workflow

- Store feature specs under `specs/`.
- Start from `specs/spec-template.md`.
- Create a new spec by copying/renaming the template to `specs/<ID>-<feature>-spec.md`.
- Prefer `scripts/new-spec "<feature name>"` to generate the filename automatically.
- `<ID>` must be monotonic and collision-resistant across contributors.
- Use ID format: `YYYYMMDD-<rand4>` (example: `20260409-7k2m`).
- `YYYYMMDD` provides chronological ordering; `<rand4>` (lowercase letters/digits) reduces collision risk for parallel work.
- Ask clarifying questions until the spec is solid before implementation.
- Ensure each spec includes: user problem, scope/non-goals, UX, implementation steps, acceptance criteria, and QA plan.
- Treat the spec as a primary code-review artifact for humans.
- When implementation deviates from the spec, update the spec in the same change (decisions, scope, acceptance criteria, QA notes) so it stays accurate.

## Documentation

- **Always update `README.md` in the same change as any user-facing feature**: new CLI commands or flags, new deck keys / modes, new config fields, new persisted files, or behavior changes a user would notice. The README is part of the change, not a follow-up.
- Update the relevant table (key bindings, CLI reference, configuration) and add a short prose paragraph if the feature needs explanation beyond a one-liner.
- New config field → add it to the example JSON block.
- New persisted file under `~/.awp/` or `~/.config/awp/` → mention it in the relevant section.
- Deck key/mode shared between the right details panel and the `?` help overlay live in `internal/deckui/model.go::deckKeyGroups` — update that one slice rather than two surfaces.

## Communication

- Summarize what changed, where, and why.
- Call out tradeoffs and follow-up work clearly.
- Be concise and concrete.
