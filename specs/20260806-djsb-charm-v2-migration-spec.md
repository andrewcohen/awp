# Charm v2 Migration

## Metadata
- **Spec ID**: `20260806-djsb`
- **Feature name**: Charm v2 migration (bubbletea, bubbles, lipgloss, huh)
- **Owner**: andrewcohen
- **Status**: Planned
- **Last updated**: 2026-08-06

## Goal

Move every TUI surface in awp from the Charm v1 stack to v2, so that the deck,
the diff viewer, the watch view and the CLI pickers all run on the current
renderer and the current key model — with no user-visible change in behaviour.

This is a migration, not a feature. Nothing a user can see should differ except
where v1 was already wrong.

## User Problem

Two problems, one immediate and one structural.

**Immediate**: awp is on a stack that stopped receiving features. bubbletea
v2.0.0 shipped 2026-02-24; awp's first commit is 2026-04-08. The project was
born six weeks late and never noticed, because `go get
github.com/charmbracelet/bubbletea` resolves to v1.3.x forever — the v2 module
lives at a different path (`charm.land/bubbletea/v2`) and nothing warns you.
awp is pinned at v1.3.6; v1.3.10 is the last v1 release.

**Structural**: the next thing we want to build is an embedded terminal widget
— rendering a live agent pane inside a deck panel instead of handing the whole
terminal over to it. The candidate library, `charmbracelet/x/vt`, exposes its
clean integration through `Draw(scr uv.Screen, area uv.Rectangle)` — that's
Ultraviolet, the v2 rendering layer. On v1 we would render to a string and
paste it into a view, which works but fights the grain of both libraries. The
widget is the forcing function; the migration is worth doing on its own merits
either way.

## Scope

### In scope (v1)

- `bubbletea` v1.3.6 → `charm.land/bubbletea/v2`
- `lipgloss` v1.1.0 → `charm.land/lipgloss/v2`
- `bubbles` → `charm.land/bubbles/v2` (list, viewport, textinput, textarea,
  help, key, spinner)
- `huh` v1.0.0 → `charm.land/huh/v2`
- Go directive `1.23.0` → `1.25.8` (huh v2's floor)
- Deleting the 52 now-inert `tea.ClearScreen` calls (see Phase 1)
- Keeping every existing test green, including the render/bench tests

### Out of scope (v1)

- The embedded terminal widget (`x/vt`). Separate spec, after this lands.
- Deck-as-entry-point / dropping the tmux popup. Separate spec; we agreed to
  POC that together once this migration is done.
- Any session-backend change (zmx, a native daemon, tmux-as-session-store).
- Any behaviour change, restyle, or new key binding. If the migration makes
  something look different, that is a bug in the migration.
- Moving off the `github.com/charmbracelet/x/*` helper packages that are not
  part of the v2 stack (`x/ansi`, `x/term`) unless a v2 package forces it.

## UX

### CLI

No change. Every command keeps its current output.

### TUI

No change. Same keys, same colors, same layout, same help overlay.

The one place a user could notice is color fidelity: lipgloss v2 moves
downsampling from `Style.Render()` to the output layer. Inside a Bubble Tea
program the program handles it. Outside one — six call sites that print a
`.Render()` result through `fmt` — we must switch to `lipgloss.Fprint*` or
those sites will emit full-fidelity ANSI to terminals that can't take it.

## Findings

Measured against awp's actual usage rather than the upgrade guides alone.
Where the published docs and the compiler disagreed, the compiler won — the
huh findings below come from building a scratch module against
`charm.land/huh/v2`, which contradicted pkg.go.dev twice.

### The good news: two structural bets already paid off

**Only 5 files implement `tea.Model`.** 62 files import bubbletea, but the
repo rule in `CLAUDE.md` — *sub-components are plain structs with
`update(msg)` / `view(width)` methods, not `tea.Model` implementations* —
means the rest never touch the interface. The `View() string` → `View()
tea.View` change lands on exactly five files:

- `internal/ui/model.go`
- `internal/deckui/model.go`
- `internal/deckui/mini.go`
- `internal/cli/picker.go`
- `internal/cli/watch.go`

**Colors need no work at all.** lipgloss v2 changes `Color` from a string type
to a function returning `image/color.Color`. Every awp call site already
spells it `lipgloss.Color(charm.Accent)` — a call, not a conversion — because
`internal/charm/palette.go` holds plain string constants. So they compile
unchanged. `lipgloss.Color` is never used as a type anywhere in the repo. The
palette discipline was written for theming reasons and happens to have
pre-paid for this.

### Breaking changes, by count

| Change | Sites | Nature |
|---|---|---|
| Import paths → `charm.land/…/v2` | all | mechanical |
| `tea.KeyMsg` → `tea.KeyPressMsg` | 222 | mechanical |
| `viewport.New(w, h)` → `New(WithWidth(w), WithHeight(h))` | 7 | mechanical |
| `viewport.Width/Height/YOffset` fields → methods | 4 | mechanical |
| `textinput`/`textarea` field writes → `SetX` methods | 13 | mechanical |
| `View() string` → `View() tea.View` | 5 | structural |
| `tea.WithAltScreen()` → `view.AltScreen = true` | 4 | structural |
| `huh.Theme` struct → interface | 2 files | structural |
| `lipgloss.AdaptiveColor` → `compat.AdaptiveColor` / `LightDark` | 1 | structural |
| `SetColorProfile` / `TerminalColor` | 3 | tests only |
| `.Render()` printed via `fmt` → `lipgloss.Fprint*` | 6 | correctness |

### Confirmed non-issues

- **Zero mouse usage.** The entire mouse section of the upgrade guide is moot.
- `tea.ClearScreen`, `WithInput`, `WithOutput`, `Exec`, `ExecProcess`,
  `Batch`, `Quit` all still exist in v2. The 12 `tea.Exec` sites are fine.
- No `tea.Sequentially`, no `p.Start()`.
- No space-key literals in production code (v2 changes `" "` to `"space"`).
- **`bubbles/list` is effectively unchanged.** `list.New(items, delegate,
  width, height)` keeps its signature and all 24 list APIs awp uses still
  exist. This was the predicted big risk and it evaporated — five pickers ride
  on it.
- `bubbles/key`, `bubbles/help`, `bubbles/spinner` need no changes beyond the
  import path. `key.Matches` and `msg.String()` both still work on v2 key
  messages, so the 13 remaining `switch msg.String()` blocks survive as-is.

### huh v2 is the real work

Four findings, all from compiling against it:

1. **`huh.Theme` is now an interface**, `type Theme interface { Theme(isDark
   bool) *Styles }`. The struct that used to be `huh.Theme` is now
   `huh.Styles`. `internal/charm/theme.go`'s `HuhTheme() *huh.Theme` has to
   return a type implementing that method instead. This is a better shape —
   the theme becomes light/dark aware by construction — but it is a rewrite of
   that function, not a rename.

2. **`huh.ThemeBase()` → `huh.ThemeBase(isDark bool) *huh.Styles`.** New
   argument, new return type. Two call sites.

3. **`internal/deckui/start_from_field.go` implements `huh.Field` by hand**
   and must match the v2 interface. The delta is smaller than feared:
   `WithTheme(*huh.Theme)` becomes `WithTheme(huh.Theme)` (no pointer — it's
   an interface now), and `WithAccessible` is no longer in the interface so it
   becomes dead code. Everything else awp already implements — `Blur`,
   `Focus`, `Error`, `Run`, `RunAccessible`, `Skip`, `Zoom`, `KeyBinds`,
   `WithKeyMap`, `WithWidth`, `WithHeight`, `WithPosition`, `GetKey`,
   `GetValue` — is unchanged.

   Notably `huh.Field` embeds `huh.Model`, which is an alias for
   `compat.Model` — the v1-shaped `View() string`. So the custom field's
   `View() string` and `Update(msg) (tea.Model, tea.Cmd)` stay as they are.
   huh fields do **not** move to `tea.View`.

4. `huh.Form.State`, `StateCompleted` and `StateAborted` all still exist.
   pkg.go.dev claimed otherwise; a compile said otherwise back. Both forms
   keep their submit/cancel detection.

## Discovery Questions

1. **Who is the first user?** Andrew, on every awp surface, immediately.
2. **When do they use it?** Continuously — this is the whole TUI.
3. **What result do they need?** Byte-identical behaviour on a supported
   stack.
4. **What data sources?** None new.
5. **Smallest useful slice?** Phase 1 (delete the dead `ClearScreen` calls) is
   independently valuable and lands on v1.
6. **Non-goals?** Any behaviour change. See *Out of scope*.
7. **What does done look like?** All gates green, and a human pass over each
   TUI surface confirms nothing moved.

## Implementation Plan

Each phase is independently committable. Phase 3 is a single compile-unit —
the tree does not build between its start and its end.

1. **Delete the inert `tea.ClearScreen` calls (on v1).** 52 sites.
   `CLAUDE.md` already records that these stopped being necessary when the
   deck moved to alt-screen; v2 keeps `ClearScreen`, so this is not forced —
   it is done first purely to shrink the diff everything else has to be read
   against. Gates must be green here on the old stack.
2. **Bump the Go directive** to 1.25.8. Toolchain in use is 1.26.5.
3. **The atomic swap.** Import paths, the 5 `View() tea.View` signatures, and
   the 4 `WithAltScreen()` → `view.AltScreen = true` moves. Nothing compiles
   until all of it lands, so it is one commit.
4. **`tea.KeyMsg` → `tea.KeyPressMsg`.** 222 sites, mechanical.
5. **bubbles surface fixes.** 7 `viewport.New` call sites, 4 viewport field
   writes, 13 textinput/textarea field writes.
6. **lipgloss color system.** `charm.Cursorline` off `AdaptiveColor`, the 3
   test-only `SetColorProfile`/`TerminalColor` sites, and the 6 `fmt`-printed
   `.Render()` sites onto `lipgloss.Fprint*`.
7. **huh v2.** `internal/charm/theme.go` Theme-as-interface, the two
   `ThemeBase` calls, and `start_from_field.go`'s `WithTheme` signature.
8. **README + CLAUDE.md.** The Components decision table and the design-system
   section reference v1 APIs (`lipgloss.NewStyle()` caching advice,
   `bubbles/list` integration notes). Update the ones the migration
   invalidates.

## Acceptance Criteria

- [ ] `go.mod` requires `charm.land/{bubbletea,bubbles,lipgloss,huh}/v2` and
      no `github.com/charmbracelet/{bubbletea,bubbles,lipgloss,huh}`.
- [ ] All five `tea.Model` implementations return `tea.View`.
- [ ] Alt-screen is declared on the `View`, not on `tea.NewProgram`, in all
      four programs.
- [ ] No `tea.ClearScreen` calls remain.
- [ ] `internal/charm/palette.go` still routes every color through a semantic
      token, and `grep -E 'lipgloss\.Color\("[0-9]'` across `internal/deckui`
      and `internal/charm` still returns zero hits.
- [ ] Styled output printed outside a Bubble Tea program goes through
      `lipgloss.Fprint*`.
- [ ] `internal/deckui/start_from_field.go` satisfies `huh.Field` with no
      adapter shim.
- [ ] Every existing test passes unmodified, except tests that assert on v1
      API shapes (the 3 color-profile sites), which are updated in place.
- [ ] Deck startup time is not measurably worse — compare
      `internal/deckui/frame_bench_test.go` before and after.

## QA / Human Review Test Plan

Gates cannot catch a rendering regression. Every surface below needs eyes.

### Setup
- [ ] `jj`, `tmux`, `gh` available; build to a temp path, not the on-PATH
      binary.
- [ ] A repo with at least one active agent session, one PR-backed workspace,
      and one review with mirrored threads.

### Core Happy Path
- [ ] **Deck**: rows, project headers, status dots, selection bar, meta line,
      title row and scope label all render as before.
- [ ] **Deck scopes**: `P` cycles all → attention → inbox; bucket header
      colors unchanged.
- [ ] **Diff viewer** (`c`): stream, file tree, comment index, side-by-side
      toggle, cursorline banding, and the unfocused-pane treatment (muted
      `┃`, no band).
- [ ] **Pickers**: open (`o`), bookmark (`B`), review (`r`), jobs (`J`) —
      selection style, `/` filter, paginator.
- [ ] **Forms**: new workspace (`n`) and rename (`R`) — tab/shift-tab, the
      "Start from" custom field, validation, `Ctrl+G` → `$EDITOR`, submit and
      cancel both detected.
- [ ] **Watch view** (`w` / `W`) and the progress modal's scrollback.
- [ ] **Help overlay** (`?`): two-column layout above 70 cols, stacked below.

### Edge Cases & Failure Modes
- [ ] Resize the terminal narrow and wide on each surface; no wrap artifacts.
- [ ] `tea.ExecProcess` handoff still works: `$EDITOR` from a form, and the
      window keys that shell out.
- [ ] Ctrl+C from every modal and picker exits cleanly.
- [ ] Run a styled CLI command with output piped to a file — confirm the ANSI
      is downsampled, not full-fidelity.

### Regression Checks
- [ ] Colors match the Catppuccin Macchiato terminal theme as before — the
      ANSI-16 tokens must still be resolved by the terminal, not by lipgloss.
- [ ] Spinner animates in the activity bar from a cold start.
- [ ] No frame bleed on modal open/close.

### Reviewer Notes
- Capture before/after screenshots of the deck and the diff viewer; those two
  carry the most custom rendering.

## Risks

- **Render diffs that no test catches.** awp's styling is dense and mostly
  unasserted. The v2 renderer is a rewrite, so cell-level output may differ
  even where the code is equivalent. Mitigated only by the human pass above.
- **`x/ansi` / `x/cellbuf` version skew.** awp depends on `x/ansi v0.9.3`
  directly; the v2 stack pulls its own. A conflict is likely to surface as a
  build error, not a silent bug.
- **Phase 3 is all-or-nothing.** If it stalls half-done the tree does not
  build. Do it in one sitting.

## Spec Change Log
- 2026-08-06: Initial draft. API deltas measured against awp's real usage;
  huh findings verified by compiling a scratch module rather than reading
  pkg.go.dev, which was wrong about `Form.State` and about `Field`'s embedded
  model.

## Validation
- [ ] `mise exec -- gofmt -l .`
- [ ] `mise exec -- golangci-lint run ./...`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
