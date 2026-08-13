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
- Handle errors explicitly and return actionable messages: name what was
  being attempted and what the reader can do about it. `review: no comment
  "abc" to approve` beats `not found`.
- Avoid hidden global state where possible.
- **One way to say a thing, and make it a required argument.** When something
  is ambient (which repo, which workspace, which review), give it exactly one
  spelling and put it where it cannot be forgotten. `github.New(runner, dir)`
  takes the directory as an argument for this reason: it used to be sayable
  three ways — a runner wrapper, an `In(dir)` method, a per-method `repoDir`
  parameter — all composing correctly, so nothing looked broken right up until a
  fourth caller spelled it none of the ways and addressed whatever repo the
  process started in. `internal/github/dir_test.go` walks every exported method
  by reflection to check none of them forgot; add the same kind of guard when a
  new invariant is only as strong as every call site remembering it.

## TUI / lipgloss

The deck and other TUIs are built on Bubble Tea + lipgloss, **v2**. The
module paths are `charm.land/{bubbletea,bubbles,lipgloss,huh}/v2` — note
`charm.land`, not `github.com/charmbracelet`, and note the `/v2`. A plain
`go get github.com/charmbracelet/bubbletea` silently resolves to v1 and
nothing warns you; that is how this repo spent its first months on v1 by
accident. When working on UI:

- Use lipgloss styling primitives (`MarginTop`, `MarginBottom`, `PaddingLeft`, `Border*`, `Foreground`, `Bold`, `Width`) rather than ad-hoc tricks like inserting `""` rows or padding strings with spaces. Spacing should belong to a style, not the row list.
- Inter-block spacing (between project groups, sections, headers) → `MarginTop` / `MarginBottom` on the block's style. Inner spacing → `Padding*`.
- Reuse helpers like `statusGlyph` / `statusColor` for status colors. Don't hardcode color codes inline; if you need a new semantic color, add it to the existing helpers so the legend in `?` help and the row renderer stay in sync.
- Terminals render whole rows only — half-line gaps don't exist. If a layout feels too dense, prefer a faint top/bottom border (`BorderTop(true).BorderForeground(...)`) over stacking blanks.
- Keep the `?` help overlay in `internal/deckui/model.go::renderHelp` updated whenever you add or rebind a key, or change a status state.

### Components: which primitive to reach for

The "Design system" and "Bubble Tea program structure" subsections below
tell you how to *finish* a TUI screen — colors, padding, selection
style, modal repaint, no nested programs. This subsection tells you how
to *start* one: which Charm primitive to reach for. If you find
yourself hand-rolling something on the smell list, you've taken the
wrong primitive and the rest of the work will compound the mistake.

**Decision table.**

| You need… | Reach for | Reference implementation |
|-----------|-----------|--------------------------|
| Selectable list / picker | `bubbles/list` + `charm.ApplyListTheme` | `internal/cli/picker.go` |
| Scrollable text region (log, long body, details pane) | `bubbles/viewport` | adopt — debt list below |
| Multi-field form (input + textarea + submit) | `huh.Form` | adopt — debt list below |
| `?` help footer / overlay | `bubbles/help` reading from `deckKeyGroups` | `new_workspace_form.go` short help |
| Key dispatch | `key.Binding` + `key.Matches(msg, binding)` | `internal/cli/picker.go`, form key maps |
| Single-line input | `bubbles/textinput` | filter / bookmark modes |
| Spinner | `bubbles/spinner` | activity bar |
| Rendered markdown | `glamour` | adopt only when there's markdown to render |

`internal/cli/picker.go` is the canonical `list.Model` integration —
mirror it for every new selectable list. Theme via
`charm.ApplyListTheme(&l, &delegate)` so selection style matches
everything else.

**Smells (these mean you took the wrong primitive).**

If you find yourself writing any of these, stop and pick the right
bubble:

- `*Cursor`, `*Offset`, `capacity`, or `clamp*` fields on a `Model` →
  `bubbles/list` (selection + scroll) or `bubbles/viewport`
  (scroll-only) owns this state.
- `if cursor >= len(...)` math inside a render function → same.
- "… N more" footers built by hand → `list.Model`'s paginator.
- Manual two-char easymotion mapping duplicated across surfaces →
  the consolidated easymotion helper (post-phase 8 of the cleanup
  spec).
- Hand-rolled `tab` / `shift+tab` between `textinput.Model`s →
  `huh.Form` owns field navigation.
- A `msg.String()` switch on key names (`"esc"`, `"enter"`, `"tab"`)
  → `key.Binding` + `key.Matches`.
- A string-literal 256-color code inside `lipgloss.Color(...)` — like
  `lipgloss.Color("245")` — anywhere outside
  `internal/charm/palette.go`. Add a palette token instead.
- `lipgloss.NewStyle()` inside `View` or a per-row render helper →
  cache the style on the shared `Theme` or `Model`. (Initialization
  paths are fine.)

**Don't follow hand-rolled precedent.** The conventions below are not
suggestions — every body panel wears `deckStyles.Panel`, every selection
uses `Foreground(Warning).Bold(true)` + `┃ `. Component choice is the
same kind of rule: if you're scrolling, you're using `viewport`. If
existing code hand-rolls a primitive that exists in bubbles, it is
**tech debt to replace**, not a precedent to extend. The debt list
below names the current offenders.

**Check for a bubble before adding state to `Model`.** Any time you
reach for a new `*Cursor`, `*Offset`, or `*Filter` field on
`deckui.Model`, first check whether a bubble already owns that state
internally. The default answer is yes — `list.Model` owns cursor,
filter, paginator; `viewport.Model` owns scroll offset; `huh.Form`
owns focus and validation.

**Pre-migration debt list.** These surfaces currently hand-roll Charm
primitives. Each line is tech debt — do not extend, do not copy as
precedent. Phases reference
`specs/20260522-gaq9-deckui-charm-cleanup-spec.md`; strike entries
here as each phase lands.

- ~~`internal/deckui/model.go:430` — bookmark picker
  (`bookmarkCursor`, bespoke filter input)~~ → phase 2 ✓ (migrated to
  `bubbles/list` via `bookmarkList`).
- ~~`internal/deckui/model.go:420` — review picker (`reviewCursor`)~~ →
  phase 3 ✓ (migrated to `bubbles/list` with custom
  `reviewItemDelegate` for per-field PR row colors).
- ~~`internal/deckui/model.go:459` — open / project picker
  (`openCursor`)~~ → phase 4 ✓ (migrated to `bubbles/list` via
  `openList`; gained `/` filter to match the other pickers).
- ~~`internal/deckui/model.go:471` + `internal/deckui/jobs.go:249`
  (`clampLines`) — jobs overlay (`jobsOverlayCursor`, line-truncation
  hack)~~ → phase 5 ✓ (left pane = `bubbles/list` with
  `jobItemDelegate`; right pane = `bubbles/viewport` with pgup/pgdn
  scroll; `clampLines` deleted).
- ~~`internal/deckui/mini.go` — mini-deck selection~~ → phase 6 ✓
  (selection moved onto `bubbles/list` with a `miniItemDelegate`;
  inline project headers replaced with `[project]` prefix chips).
- ~~`internal/deckui/model.go:383` — deck row list scroll
  (`deckListOffset`, `clampDeckListOffset`, `scrollDeckBody`)~~ →
  phase 7 ✓ (deck row list scrolls via `bubbles/viewport`; sticky
  project header rendered as a static line above the viewport when
  the cursor's project header has scrolled off; `scrollDeckBody`
  deleted, `clampDeckListOffset` renamed to `clampDeckViewport` and
  now drives `deckYOffset` → `viewport.SetYOffset`).
- ~~`internal/deckui/model.go` + `internal/deckui/mini.go` — duplicate
  easymotion implementations~~ → phase 8 ✓ (the rune-step state
  machine lives in one generic `findHintStep[T]` helper; mini-deck,
  deck project stage, and deck workspace stage all call it).
- ~~`internal/deckui/new_workspace_form.go` — hand-rolled
  `textinput`/`textarea` composite with manual tab/shift-tab~~ →
  phase 9 ✓ (rebuilt on `huh.Form`; tab/shift-tab navigation,
  validation, and `Ctrl+G → $EDITOR` all owned by huh).
- ~~`internal/deckui/rename_workspace_form.go` — same shape~~ →
  phase 10 ✓ (one-field `huh.Form` with inline `Validate` for empty
  and unchanged-name rejection; `formWrap` and the bespoke key
  bindings deleted).
- ~~`internal/deckui/model.go:2760` (`renderHelp`) — ~500-line
  string-assembly help overlay~~ → phase 11 ✓ (key-binding column
  rendered via `bubbles/help.Model.FullHelpView`; the status / PR /
  activity legend column stays custom since it's glyph art, not a
  key map).
- ~~`internal/deckui/model.go:498` (`progressLogMax = 50`) — progress
  modal log cap with no scroll~~ → phase 12 ✓ (log lines stream into
  a `bubbles/viewport`; pgup/pgdn/ctrl+u/ctrl+d scroll back through
  history with auto-follow when pinned to the tail).
- ~~`internal/deckui/model.go` — ~22 `msg.String()` switches throughout
  `Update`~~ → phase 13 ✓ (partial — the central `deckKeyMap` is the
  canonical source for row-mode bindings, and the main `case
  tea.KeyMsg` switch in row mode dispatches via `key.Matches`. ~17
  smaller `switch msg.String()` blocks remain inside modal / picker /
  overlay close-key handlers; those are the deck's internal
  "esc/q/ctrl+c → close" pattern rather than user-discoverable
  bindings. Converting them to `key.Matches` if-chains would add
  boilerplate without changing behavior — left as a follow-up if
  modal key patterns ever need to be themed or rebindable).
- ~~`internal/deckui/model.go` — ~92 `lipgloss.NewStyle()` calls in hot
  render paths~~ → phase 14 ✓ (partial — `deckStyles` struct in
  `internal/deckui/styles.go` caches the base lipgloss styles the
  deck reaches for, and the deck row-list render loop reuses them
  via `m.styles`. Lipgloss styles are immutable value types so
  cheaper than they look; the remaining `lipgloss.NewStyle()` sites
  in cold-path renderers are intentionally left alone — new code
  should prefer `m.styles`).
- ~~`internal/deckui/activity.go:271-272`,
  `internal/deckui/model.go:3226` — raw 256-color codes
  (`lipgloss.Color("245" / "78" / "39")`)~~ → phase 15 ✓ (all
  routed through palette tokens; added `charm.Link` for the
  underlined-hyperlink role. Progress-step glyph color strings
  (`"82"`/`"203"`/`"117"`/`"245"`) and `activity.go`'s default glyph
  color (`"245"`) also converted. `grep -E 'lipgloss\.Color\("[0-9]'`
  across `internal/deckui` and `internal/charm` returns zero hits).

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
| `Accent` | 6 (teal) | Project headers (all / attention scopes), "starting" status, open PR, focus, inbox "needs your review" bucket |
| `Info` | 4 (blue) | PR numbers, job-running glyph, meta-line `:port` |
| `Success` | 2 (green) | Working / approved / done, inbox "ready to merge" bucket |
| `Warning` | 3 (yellow) | Waiting / pending / draft / **row selection** / **find-target header** / orphaned / inbox "waiting for review" bucket |
| `Danger` | 1 (red) | Errors, CI failing, inbox "needs action" bucket |
| `Spinner` | 5 (magenta) | Spinner only |
| `Strong` | 15 (bright white) | Emphasized text |
| `Muted` | 8 (bright black) | Hints, footer, dim labels, scope label, meta-line author/branch/prompt/stale chip/separators, inbox drafts/other buckets |
| `BgPanel` | 0 (surface) | Reserved — currently unused |

The `awp deck` title is plain bold (terminal-default fg / white) — it deliberately stays uncolored so the teal project headers carry the structural hue without the title competing. The scope label (`scope: <label>`) is pinned to the top-right corner of the title row (muted); pressing `P` cycles it and flashes the new scope in the status bar.

**Header colors.** Project headers (all / attention scopes) use `Accent` (teal, bold) so the structural skeleton carries a hue. The find-mode target header moves to `Warning` (yellow, bold) — the selection hue — to stay distinct from the teal headers. Inbox bucket headers are urgency-colored per `inboxBucketColor` — see the bucket table in the README. The deck caches these as `deckStyles.ProjectHeader` / `deckStyles.FindHeader` / `deckStyles.BucketHeader[bucket]`; resolve a header's style via `Model.headerStyle(label)`.

**Row labels.** Workspace row labels stay at the terminal default fg — the colored status dot carries the agent state. Tinting every label by status was tried and flooded the list (yellow "waiting" rows collided with the yellow selection bar), so only the cursor (`Warning` + bold + `┃`) and find-mode dimming recolor a label.

**Meta line.** `renderMetaText` / `metaSegStyle` tint only the `:port` token (blue) after truncation (so the width math stays ANSI-free); everything else — author, branch, prompt, stale chip, the virtual-row keyboard-return (`nf-md-keyboard_return`) `to review` / `to check out` hint, and the `·` separators — stays `Muted`. Coloring the author (teal) and branch (green) was tried and read as too much color repeated on every row, so the meta line stays mostly muted.

- **Never** call `lipgloss.Color("123")` with a raw 256-color code. Add a
  semantic token to `internal/charm/palette.go` first if you need a new
  role.
- Prefer leaving body and padding cells unpainted — they pick up the
  terminal's default bg, which is what blends with the rest of the
  app surface. Background fills are reserved for buttons, chips,
  selection treatments, and diff change tints, where the contrast is
  intentional. (The historical "never .Background()" rule applied to
  inline mode; the deck now runs alt-screen so painted bg cells no
  longer matter for blending with the surrounding tmux pane.)

**Chrome is ANSI 16. Content and background tints are not.** The 16-slot
rule above is about awp's own *chrome* — a status dot, a project header, a
selection bar — where being remapped by the user's terminal theme is the
whole point. Two things are outside it, and both live in
`internal/charm/palette.go` beside everything else rather than inlined at a
call site:

- **Background tints** (`Cursorline`, `AddedBg`, `RemovedBg`, and their
  `*Cursor` variants). A low-contrast background has no ANSI 16 slot at
  all — the nearest, `BgPanel` ("0", surface), is sized for chip and badge
  fills where contrast is the point, and reads far too strong for a tint
  you are meant to read text through.
- **Syntax colours** (`Syntax*`), which are Catppuccin — Latte against a
  light terminal, Macchiato against a dark one, via
  `github.com/catppuccin/go`. Code in a diff is not chrome, it is
  *content*, and it should look the way the same code looks in the editor
  the reader is about to open it in. Sixteen slots also cannot carry a
  syntax palette: six are already spoken for by status roles, so a token
  would either share its hue with "CI failing" or get none — which is why
  punctuation and operators had none until this moved.

Those two blocks are the whole set of off-palette values. A new one belongs
in one of them, not next to the style that wanted it.

The diff tints exist because syntax highlighting spends the foreground on
the lexer, so the change type has to move to the background or a `+` line
and a `-` line differ only in the gutter glyph. Each has a brighter
`*Cursor` variant rather than letting `Cursorline` win outright: the cheap
version makes a `+` row lose its tint for exactly as long as the cursor
sits on it, so the tint blinks off and on down the file as you scroll. One
rule instead — the cursor's row is a step up from the row beneath it —
true of added, removed and unchanged alike.

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

**The one variation: a pane the keyboard has left.** In a multi-pane screen
the treatment above says *this is the active row*, so exactly one pane may
wear it. In the diff viewer the other panes' selections drop a tier:

- the cursorline band is not painted at all (`rowBanded` = `rowSelected`
  gated on focus)
- the `┃` bar stays, in `Muted` rather than `Warning` + bold — see
  `selectionBarStyle` in `internal/ui/stream_render.go`
- so does any other selection-hued chrome that follows the cursor: the
  stream's current-file divider goes back to `Accent` (`fileRuleActive`)

The bar has to stay — it is where the keys go back to — but at full
strength it was the brightest thing in a pane whose keys were dead, and it
*moved*, since seeking from the file list or the comment index drags the
diff's cursor along. Motion in an inactive pane reads as the cursor, which
is the one thing it isn't.

Anything focus-dependent in a cached render path has to be in the cache key:
`rowKey.fileRule` exists because the divider's hue follows `filesCursor` and
the focus, neither of which goes through `rebuildStream`.

**`Width` includes the border.** lipgloss v2 counts border cells inside
`Style.Width(n)`; v1 drew them outside it, so a bordered panel used to
render two columns wider than the number you set. Pass a bordered panel
the width it should actually span. Padding was always inside `Width` and
still is — only the border moved.

This is the migration's quietest trap: nothing fails, the panel is just
two columns narrow, and only one test in the repo happened to measure a
box against its border. `charm.BorderCells` (aliased in `deckui` as
`borderCells`) names the two columns wherever a call site still thinks in
v1 terms and has to add them back.

**Panel padding.** All body-area panels and the footer share one style —
`deckStyles.Panel`, which is `Padding(panelPadY, panelPadX)` = **zero, in
both directions**. The deck reaches all four edges of its terminal.

The deck is the outermost program in its terminal, so there is no
surrounding surface for a margin to separate it from — an inset is a cell of
the user's screen buying a gap against the edge of the world. Rows still
read as rows because their own content indents them: the selection's `┃ `
prefix and the status dot occupy the columns an outer pad was holding, and
they belong to the row rather than to the frame. Vertically the same
argument is sharper still, since a blank row above the title is a workspace
row the list does not get — the frame used to spend 7 of 24 rows on itself
and now spends 3 (title, one gap, status bar). The status bar sits on the
terminal's last row, the way vim's and tmux's do.

The constants stay even at zero. Every panel and the footer derive from
them, so reintroducing an inset is one edit in `layout.go` rather than a
hunt through the call sites that file exists to have replaced.

- The one gap the deck keeps is the blank under the title row. It earns
  the row: the attention badge sits on `deckTextCol`, the same column the
  workspace rows' status dots do, so butted against the first project
  header it reads as a row rather than as a title.
- **Bordered popovers** (help, jobs overlay, confirms, inputs) keep their
  own `Padding(1, 2)`. They float over a blank canvas rather than competing
  for the frame's height, and content flush against a border reads as a
  mistake. That is padding *inside* a border; nothing goes outside one.
- **A pane has nothing outside its border either.** `paneBox` renders
  exactly `m.width × m.height`, so the `lipgloss.Place` that centres it adds
  no canvas. A pane shows someone else's full-screen program, and a column
  of blank beside the border is a second edge next to the one already there.
  `TestNothingSitsOutsideThePanesBorder` pins it.
- **A child renders its box exactly.** The popover branch of `view()` centres
  what a modal returns inside `childBox`, so a child that comes back narrower
  than its box is padded on the left — and everything in it moves. That is how
  #339 happened: `diffModal.view` reserved a literal 2 columns for panel padding,
  `panelPadX` went to 0, and the diff rendered two columns short. In a split the
  composed halves were then narrower than the box, the centring shifted both one
  column right, and the pane's cursor — placed from `boxOf` — landed one column
  left of the text being typed. Nothing failed; the halves were just narrow.
  `split_fill_test.go` pins every row of a split at the box's width, and it is
  where a new half that gets its width wrong should fail.
- **`internal/deckui/layout.go` is the one place the frame budget is
  written down** — `panelPadX/Y`, `panelRows`, `panelCols`, `footerRows`,
  `deckHeaderRows`, `deckTitleRowIndex`. Anything that subtracts chrome
  from `m.height` derives from those. It used to be literals at each site
  (`m.height - 5` in three pickers, `2 + 2 + 3` in `deckBodyCapacity`,
  `diffModalChrome = 6`), and a wrong one does not fail — it leaves a band
  of dead rows above the footer, or clips a list's pagination off the
  bottom. `layout_test.go` pins the frame budget; `modal_diff_test.go`
  pins that nothing dead sits above the status bar.
- New list picker → `renderPickerPanel(m, &p.list, width)`. Don't
  re-derive the height.
- The footer renders via `composeStatusBar(activities, spinnerGlyph,
  rightSeg, hint, m.width-panelCols)` wrapped in `m.styles.Panel`.

**Status messages.** Cancellations clear `m.status` to `""` instead of
printing `"...: cancelled"`. The user already pressed esc; echoing the
fact is noise. Errors and completion messages still set `m.status` for
durable feedback.

**Spinner.** Tick handler in `case spinner.TickMsg` continues while
either `m.busy` (foreground work) OR `len(m.activities) > 0` (background
work in the activity bar). Bootstrap a `m.spinner.Tick` from
`case jobsListMsg` when `len(m.activities) > 0` so the spinner glyph in
the bottom bar actually animates from a cold start.

**Alt-screen mode.** The deck declares it on the view: `View() tea.View`
sets `v.AltScreen = true`. Bubble Tea v2 moved terminal features off
`tea.NewProgram` options and onto the returned view, so alt-screen,
window title, mouse mode and cursor are stated in one place per program
rather than split between construction and commands. Every frame paints
the full screen, so cells the current frame doesn't write are blanked by
the renderer rather than carrying leftover content from a previous
frame. Practical consequences:

- The `padBlock` between body and footer still exists for vertical
  positioning (keeps the footer pinned to the bottom of the
  viewport), but no longer needs the "space-fill to overwrite
  previous-frame cells" property — the renderer does that.
- Modal transitions don't need `tea.ClearScreen`, and none remain —
  they were deleted during the v2 migration. Don't reintroduce one.
- Exiting the deck restores the terminal to its prior state — deck
  frames don't accumulate in tmux scrollback.

Not every program wants it: the CLI picker (`internal/cli/picker.go`)
and the mini-deck run inline, so their `View()` returns a bare
`tea.NewView(...)` with no features set. Whether a program is
alt-screen is now visible in its `View`, which is where to check.

Historical note: the deck previously ran inline (no alt-screen)
because a nested `tea.Program` for the new-workspace form caused
alt-screen bleed during the entry/exit handoff. Since phase 9 of the
deckui-charm-cleanup spec all forms are states of the deck's own
program, so there's only one renderer and one alt-screen — the bleed
class is gone.

**Help overlay layout.** `renderHelp` uses a two-column layout when
`innerWidth >= 70`: status legend (agent / PR / activity) on the left,
key bindings on the right. Falls back to vertical stacking on narrower
viewports. Status legend rows show glyph + state name only — no verbose
explanations.

### Bubble Tea program structure

**One program, one renderer.** The deck is a single `tea.Program`. Modal flows
(jobs overlay, confirm-delete, new-workspace form, find-mode, review picker,
quick-action prompts, etc.) live as **states inside `deckui.Model`** — flags
plus sub-component structs that `Update` routes keys to, and `View` renders in
place of the row list.

- **Do not** launch a second `tea.Program` from inside the deck.
  `tea.Exec` / `tea.ExecProcess` is for **external** commands
  (`$EDITOR`, `git`, `vi`, pagers) — never for another Bubble Tea
  program. Two programs sharing the deck's alt-screen leak frames
  into each other during the entry/exit handoff (see
  `specs/20260505-ucc0-deck-inline-new-workspace-form-spec.md` for
  the postmortem — the conclusion blamed alt-screen but the actual
  root cause was the nested-program handoff; one program +
  alt-screen is fine).
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

  This rule paid for itself in the v2 migration. v2 changed `View()` from
  returning a string to returning a `tea.View`, and because sub-components
  are not `tea.Model`s, that landed on the six real programs instead of
  the sixty-odd files that import bubbletea. Each of those six keeps a
  `render() string` holding the actual layout, with `View() tea.View` as a
  thin wrapper — so panel helpers and tests still compose plain strings.
- The `?` help overlay (`internal/deckui/model.go::renderHelp`) and the
  `deckKeyGroups` slice are the canonical key-binding surface. Any new modal
  key binding has to be reflected there.

## Security & Safety

- Treat all external input as untrusted.
- Avoid command injection, path traversal, and unsafe shell usage.
- Use least-privilege defaults for files, network calls, and credentials.
- Never hardcode secrets or tokens.

## Linting & formatting

golangci-lint is pinned via `mise.toml` (config in `.golangci.yml`), and the
repo is kept at **zero lint issues**. **Format and lint before every commit:**

- **Format**: `mise exec -- gofmt -w .` (or `go fmt ./...`).
- **Lint**: `mise exec -- golangci-lint run ./...` — must report `0 issues`.

Fix findings rather than suppressing them. If a rule is pure noise for this
codebase, tune it in `.golangci.yml` instead of scattering `//nolint`
directives (and say why in the config comment). The ruleset is curated for
high signal — staticcheck, govet, errcheck, ineffassign, unused, gocritic,
revive, misspell, unconvert, bodyclose — plus a `depguard` rule enforcing the
`internal/deck2 → internal/cli` import boundary. New code lands at 0 issues:
lint as you go, don't let a backlog re-accumulate.

## Validation Before Handoff

When applicable, run:

- `mise exec -- gofmt -l .` (no output = everything is formatted)
- `mise exec -- golangci-lint run ./...` (must be `0 issues`)
- `go test ./...`
- `go vet ./...`
- `go build ./...`

If you cannot run something, state what was not run and why.

### The default gate does not cover the terminal emulator

There is one emulator behind a pane — libghostty-vt — and it is cgo against an
archive built by Zig, so it lives behind `-tags ghosttyvt`. Everything that drives
a real terminal is tagged with it: `internal/vterm`'s corpus and pty tests
(`term_test.go`, `keys_test.go`, `modkeys_test.go`, `host_test.go`, `tap_test.go`,
`reap_test.go`, `fidelity_test.go`) and `internal/zmx/fidelity_test.go`. A plain
`go test ./...` compiles none of them.

So a change to how a pane interprets bytes, encodes a key, or answers a query is
**not checked by the gates above**. Run the tagged suite when you touch
`internal/vterm`:

```sh
CGO_ENABLED=1 \
CGO_CFLAGS="-I$HOME/.cache/awp/libghostty-vt/out/include" \
CGO_LDFLAGS="$HOME/.cache/awp/libghostty-vt/out/lib/libghostty-vt.a" \
  mise exec -- go test -tags ghosttyvt ./...
```

(`make ghostty-lib` builds the archive if it is not cached yet.)

That is the trade for deleting the pure-Go emulator. Two emulators meant the
default gate always had one to test against — and it also meant every fidelity
question had two answers, one of which was never going to ship.

What kept the coverage on the default gate is `internal/deckui`'s fake terminal:
the deck's own behaviour around a pane is tested there with no emulator at all (see
`Model.openTerm`). Keep it that way. A new test about what the deck does with a
pane belongs on the fake; only one about what a terminal does with bytes belongs
behind the tag, and those skip without an emulator rather than failing — a skipped
pane test in a plain checkout is expected, not a problem to fix.

### A pane's columns are display columns, not cells

`Hosted.Cursor` returns a column of the string `View` returns, and `SendMouse`
takes one. Neither is the emulator's cell column, and the difference is not
cosmetic: a grapheme's footprint in the cell grid is the emulator's measurement,
its footprint in a rendered string is lipgloss/uniseg's, and they disagree.
👩‍💻 is **four cells and two columns**. The deck draws its cursor at an
absolute column of the frame, so a cell column on a row holding one of those put
the cursor beside the text instead of on it — #339, worst in a split, where a
narrow half wraps a program's decorated status line onto the row being typed on.

`ghosttyTerm.displayCol` and `cellForCol` are the two directions of that
translation, and both work by rebuilding the row's prefix as one string and
measuring it whole. Per-cell widths cannot be summed: a ZWJ sequence is stored
across two wide cells, which is two graphemes of two columns apart and one of two
together, so a running total gets 4 where the rendered row has 2. Any new code
that converts between a pane's grid and the deck's screen goes through those two,
and `internal/vterm/cursorcol_test.go` pins the invariant against a real
emulator.

### `zmx attach` means two different things

Three tests in `internal/zmx` drive the real zmx binary. They skip when
`ZMX_SESSION` is set, and that guard is not optional politeness — it is the
difference between a test and an outage.

`zmx attach` branches on `ZMX_SESSION`. From outside a session it creates an
independent client. From **inside** one it tells the daemon to switch the
*calling* client's session; the daemon log records those as
`switch session cur=<yours> next=<theirs>`. So an attach issued from inside a
zmx-hosted terminal does not nest — it steals that terminal, and the session it
was pulled off is re-created empty afterwards, losing whatever agent was in it.

This repo is normally developed from inside a zdeck pane, which is a zmx
session. `go test ./...` there used to kill the session the work was happening
in. `requireRealZmx` in `internal/zmx/real_zmx_test.go` is what stops it, and
`TestEveryRealZmxTestAsksFirst` checks every real-zmx test goes through it.

To actually run them, use a terminal that is not inside zmx. The cost is that
the only tests proving awp agrees with the real zmx do not run by default; that
is the trade.

The same branch is a live hazard in product code, not just tests — anything
spawning `zmx attach` has to give the child an environment with `ZMX_SESSION`
removed, or it hijacks awp's own client instead of making a new one.

## Version Control

- Prefer **Jujutsu (`jj`)** workflows by default.
- Use git only when explicitly requested or when `jj` cannot do the task.
- Name new `jj` bookmarks with the `andrew/` prefix.
- **Before committing, format and lint** (see *Linting & formatting*): the
  tree must be `gofmt`-clean and `golangci-lint run` must report `0 issues`.

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
- Deck key bindings live in `internal/deckui/model.go::deckKeyGroups`; that one slice feeds the `?` help overlay so the keymap and help stay in sync.

## Communication

- Summarize what changed, where, and why.
- Call out tradeoffs and follow-up work clearly.
- Be concise and concrete.
