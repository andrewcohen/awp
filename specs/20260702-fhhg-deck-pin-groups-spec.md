# Feature Spec: Deck pin groups (register-style)

## Metadata
- **Spec ID**: `20260702-fhhg`
- **Feature name**: Deck pin groups
- **Owner**: andrewcohen
- **Status**: In Progress
- **Last updated**: 2026-07-02

## Goal
Let the user pin workspaces to the top of the deck, clustered into
named "registers" (vim-mark style, `a`–`z` plus a default register),
so a working set that spans projects stays one glance away regardless
of alphabetical project ordering.

## User Problem
The deck sorts by `(project, label)`. A user juggling a handful of
active workspaces across several repos has to hunt for them in the
project-grouped list every time. Pinning floats the current working
set to a stable region at the top, and registers let that region be
sub-grouped (e.g. all the workspaces for one feature under `a`).

## Scope
### In scope (v1)
- A per-workspace **pin register**: a single-letter group `a`–`z`, or a
  reserved **default** register (bound to `gg`).
- Pinned workspaces render in a **pinned region at the very top** of the
  deck, above the project groups, sub-sectioned by register.
- Register **display aliases**: an optional human label per register
  shown in the section header (cosmetic only — the register key stays
  the letter). Aliases are global (shared across repos).
- `g`-prefix chord for all pin operations (see UX).
- Pinned workspaces **move** out of their project group (no duplicate
  rows).
- Pinned region appears in the **All** and **Attention** scopes only.
  The **Inbox** scope keeps its urgency-bucket layout unchanged.
- Persistence of pins (per-workspace) and aliases (global).
- `?` help overlay + README updated.

### Out of scope (v1)
- Reordering workspaces within a register.
- Reordering registers (fixed order: default first, then the rest
  case-insensitive alphabetical by alias-or-letter).
- Pinning in the Inbox scope.
- Multi-register membership (a workspace is in at most one register).
- Bulk pin operations.

## UX
### TUI — key chord (`g` prefix, row mode)
Pressing `g` enters a transient pin-chord mode. While it is pending,
the pinned-region section headers **highlight their register letter**
so the user can see which registers are already in use before choosing
one. A short status hint also shows the command keys.

| Key | Action |
|-----|--------|
| `g g` | pin selected workspace → **default** register. Toggle: if already in default, unpin. |
| `g` + `a`–`z` | pin selected → that register. Same register ⇒ unpin; different ⇒ move. |
| `g D` | unpin / ungroup the selected workspace. |
| `g R` | rename (set display alias) for the register the **selected** row is pinned to. Opens a text input. If the selected row isn't pinned, show a hint and no-op. |
| `g esc` / any other key | cancel the chord. |

Notes:
- Lowercase letters are register targets; capitals (`D`, `R`) are
  commands. `gd` therefore pins to register `d`; `gD` unpins.
- `gg` toggling to unpin keeps a single consistent rule ("aim at the
  register you're already in ⇒ unpin").
- Pin ops act on the currently selected workspace row. On a project
  header / collapsed default / virtual inbox row, show a hint and
  no-op.

### TUI — rendering
- New pinned region at the top of the body (All/Attention scopes),
  before the first project group.
- One section per register, in order: **default first**, then the rest
  sorted case-insensitively by (alias if set, else letter).
- Section header renders like a project header (`Accent`, bold) with a
  leading `★` marker (distinguishes pinned sections from project
  headers at a glance), using the register's alias, or a default label:
  - default register → `pinned`
  - lettered register with no alias → the letter (e.g. `a`)
  - aliased register → the alias (e.g. `auth`)
- During the `g` chord, the register letter in each pinned header is
  emphasized (e.g. bracketed / `Warning`) so the legend is visual.
- Workspace rows inside the pinned region render with the normal
  primary + meta layout (their project context still available via the
  meta line's existing fields — no project header inside the region).
- Empty pinned region (no pins) ⇒ no header, deck looks exactly as
  today.

## Data model & persistence
### Per-workspace pin (register letter)
- Add `PinGroup string` to `workspace.Entry` (`json:",omitempty"`).
  Value is `""` (unpinned), `"default"` (the `gg` register), or a
  single lowercase letter `a`–`z`.
- Add `PinGroup string` to `deckui.Item`; map `e.PinGroup` → item in
  `cli/deck.go`'s entry→item loop.
- Persist via a new Model callback `PinGroupHandler func(item Item,
  group string) error` (mirrors `prNumberLinkHandler`), wired in
  `cli/deck.go` through `linkStore.Update`. `group == ""` clears.

### Register aliases (global, cross-repo)
- New global JSON file `~/.awp/pin-groups.json` = `map[string]string`
  (register key → alias). `"default"` is a valid key.
- Add `state.LoadPinGroupAliases()` / `state.SavePinGroupAlias(key,
  alias)` (atomic write mirroring the existing store style).
- Deck loads aliases at open and passes them to the model
  (`WithPinGroupAliases`); `gR` writes through a
  `PinGroupAliasHandler func(key, alias string) error` callback and
  updates the in-memory map so the header re-renders immediately.

## Implementation Plan
1. `workspace.Entry.PinGroup` field (+ keep `UnmarshalJSON` working).
2. `deckui.Item.PinGroup`; map it in `cli/deck.go`.
3. Register-alias global store in `internal/state` (+ tests).
4. Model: chord state fields (`gChordMode bool`), `PinGroupHandler`,
   `PinGroupAliasHandler`, alias map + `With*` setters; alias-rename
   text-input sub-mode (clone the `p s` `prNumberSetMode` shape).
5. Key dispatch: `g` enters chord; chord handler implements
   `gg`/`g<letter>`/`gD`/`gR` with toggle/move semantics.
6. Rendering: split items into pinned vs unpinned; build pinned-region
   `deckBodyRow`s (register sections) ahead of the project rows; wire
   into `bodyRows` for All/Attention; register-letter highlight while
   chord pending; cursor math via the existing `deckBodyRow` machinery.
7. `deckKeyMap` + `deckKeyGroups` + `renderHelp`: add a "Pin / group"
   group.
8. Wire callbacks in `cli/deck.go`; update `deckFakeService` /
   test doubles as needed.
9. README: key table, new file `~/.awp/pin-groups.json`, prose.
10. Tests: pin/unpin/move/toggle semantics, alphabetical register
    order, pinned-region body layout, alias rendering.

## Acceptance Criteria
- [ ] `gg` pins the selected workspace to the default register; `gg`
      again unpins it.
- [ ] `ga` pins to register `a`; `gb` moves it to `b`; `gb` again
      unpins.
- [ ] `gD` unpins from any register.
- [ ] `gR` on a pinned row edits that register's alias; header updates
      immediately and persists across restarts.
- [ ] Pinned workspaces appear only in the pinned region (not in their
      project group) in All/Attention; Inbox scope is unchanged.
- [ ] Register sections order default-first then alphabetical by
      alias-or-letter.
- [ ] Pins persist across deck restarts (`~/.awp/workspace-state.json`).
- [ ] `?` help lists the `g` chord; README updated.

## QA / Human Review Test Plan
### Setup
- [ ] `jj`, `tmux` available; a repo with several workspaces across ≥2
      projects.
### Core Happy Path
- [ ] Pin a few workspaces to default / `a` / `b`; confirm they float
      to the top in the right sections and leave their project groups.
- [ ] Alias register `a` via `gR`; confirm header shows the alias and
      survives a deck restart.
- [ ] Move a workspace between registers and unpin; confirm layout.
### Edge Cases & Failure Modes
- [ ] `g` chord on a project header / virtual inbox row shows a hint,
      no crash.
- [ ] `gR` on an unpinned row shows a hint, no input opens.
- [ ] Inbox scope shows no pinned region.
- [ ] Empty alias clears the alias (falls back to letter/`pinned`).
### Regression Checks
- [ ] `p` chord, find mode, filter, scope cycle still behave.
- [ ] Old state files without `PinGroup` load fine (field defaults to
      unpinned).

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
