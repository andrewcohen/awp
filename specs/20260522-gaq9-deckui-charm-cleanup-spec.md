# Deck UI Charm Cleanup

## Metadata
- **Spec ID**: `20260522-gaq9`
- **Feature name**: Deck UI Charm-ecosystem cleanup
- **Owner**: andrew
- **Status**: Discovery
- **Last updated**: 2026-05-22

## Goal
Move the deck and every modal it owns onto idiomatic Charm primitives
(`bubbles/list`, `bubbles/viewport`, `bubbles/help`, `bubbles/key`, `huh`)
and codify component-choice rules in `CLAUDE.md` so the next agent (or
session) starts from the right primitive instead of copying hand-rolled
precedent.

The visible behavior of the deck does not change. This is comprehensive
debt paydown plus a guidance update. User-visible side effects are minor
wins that fall out of moving to bubbles: built-in `/` filter on pickers,
auto-rendered help footers, huh's automatic field navigation in forms.

Phased so each commit is independently reviewable, revertable, and
shippable on its own — no fear, but one at a time.

## User Problem
The single user is andrew, working on the deck day-to-day. Two pains:

1. **Component drift.** Three pickers (bookmark, review, open), the deck row
   list, and the jobs overlay each hand-roll the same `*Cursor` /
   `*Offset` / `clamp*` / "… N more" logic. Each new modal accretes another
   copy. State that `list.Model` would own internally lives on the deck
   `Model` struct (`bookmarkCursor`, `reviewCursor`, `openCursor`,
   `deckListOffset`, `jobsOverlayCursor`).
2. **Guidance hardcodes the finish, not the start.** `CLAUDE.md`'s TUI
   section is excellent on colors, padding, selection style, and
   one-program-one-renderer. It is silent on "which primitive to reach for."
   When an agent looks for prior art and finds hand-rolled scroll math in
   the review picker, it gets copied as if it were the convention. Every
   new modal makes the next refactor harder.

`internal/cli/picker.go` already does this right (`bubbles/list` +
`DefaultDelegate` + themed via `charm.ApplyListTheme`). It is the reference
that should have been mirrored from the start.

## Scope

### In scope (v1)
Everything below is in. Implementation Plan sequences them as phases so
they can be tackled one at a time and shipped independently.

- **`CLAUDE.md` component-choice guidance** — new "TUI components: which
  primitive to reach for" section under the existing TUI guidance,
  containing:
  1. **Decision table** — for selectable list, scrollable log, fixed
     menu, paginated picker, multi-field form, markdown text, name the
     bubble (`list`, `viewport`, `help`, `huh`, `glamour`). Cite
     `internal/cli/picker.go` as the canonical `list.Model` integration.
  2. **Smell list** — patterns that mean the wrong primitive was chosen:
     `*Cursor` / `*Offset` / `capacity` / `clamp*` fields on a model;
     `if cursor >= len(...)` math in a render function; "… N more"
     footers built by hand; manual two-char easymotion mapping;
     hand-rolled tab/shift-tab between textinputs.
  3. **"Don't follow hand-rolled precedent" rule** — if existing code
     hand-rolls a primitive that exists in bubbles, treat it as debt to
     replace, not a convention to extend.
  4. **Pre-migration debt list** — name the current offenders with
     `file:line` so the next reader knows they are tech debt, not
     pattern. Removed entries as phases complete.
  5. **"Check for a bubble before adding state to Model"** rule —
     applies whenever a new modal or list-like view is added.

- **Pickers → `bubbles/list`** (mirror `internal/cli/picker.go`):
  - bookmark picker
  - review picker
  - open / project picker
  - Each gains `list.Model`'s built-in `/` filter, paginator, and help
    footer. The three filter inputs collapse to one (list's own).

- **Jobs overlay** → `list.Model` (left pane) + `viewport.Model` (right
  details / log pane). Removes the `clampLines` truncation hack on the
  right pane.

- **Mini-deck → `bubbles/list`** — same shape as the main pickers; sets
  up easymotion consolidation since selection is now uniform.

- **Deck row list → `bubbles/viewport`** with sticky project headers
  rendered around the viewport content (headers must not scroll out
  with the row body). Remove `scrollDeckBody`,
  `clampDeckListOffset`, `deckListOffset`, and the related `Update`
  defer.

- **Easymotion consolidation** — single helper operating on a
  list-backed selection. Deletes the duplicate implementation between
  deck and mini-deck.

- **Forms → `huh`**:
  - `new_workspace_form` (workspace name, bookmark, prompt textarea,
    submit/cancel) as a `huh.Form` group. huh owns tab / shift+tab
    navigation, validation, and submission state.
  - `rename_workspace_form` as a `huh.Form` group.
  - Both keep `Ctrl+G → $EDITOR` for the prompt field by escaping out of
    huh's input long enough for `tea.ExecCommand($EDITOR)` to take over
    the deck program (the supported path; same as today).

- **Help overlay → `bubbles/help`** — replace the ~500-line `renderHelp`
  string-assembly in `model.go:2760` with a `help.Model.View()` driven
  by the existing `deckKeyGroups` slice. Status legend (agent / PR /
  activity glyphs) stays as a custom block since `help.Model` doesn't
  own it; the rest is generated.

- **Progress modal log → `bubbles/viewport`** — remove the 50-line cap
  and lack of scroll.

- **Global `key.Binding` sweep** — replace every remaining `msg.String()`
  switch in `internal/deckui` with `key.Binding` + `key.Matches`. All
  bindings flow through a central key map so the help overlay reads
  from the same source.

- **Per-render style caching** — promote `lipgloss.NewStyle()` sites
  inside hot render paths (`View`, render helpers) to fields on the
  shared `Theme` or precomputed `Model` styles. Target the ~92 sites in
  `model.go` first; touch others if cheap.

- **Raw color codes → palette tokens** — `activity.go:271`,
  `activity.go:272`, `model.go:3226` (and any others found en route).
  Add a `Link` palette token if needed for the blue/39 case. After this
  phase, every `lipgloss.Color(...)` call in `internal/deckui` and
  `internal/charm` routes through `internal/charm/palette.go`.

- **Glamour for markdown** — evaluated during the help-overlay and
  progress-modal phases. Adopt only if any rendered text is actually
  markdown today; otherwise note and skip without leaving stubs.

### Out of scope (v1)
- Splitting `deckui.Model` itself into separate `tea.Model` structs —
  CLAUDE.md's "one program, one renderer" rule still holds. Sub-components
  remain plain structs with `update / view` methods.
- Replacing Bubble Tea / lipgloss with a different TUI framework.
- Visual redesign — colors, padding, selection style, modal entry
  repaint pattern all stay identical to today.
- Behavioral changes to any deck flow. If a phase needs a behavior
  tweak to land cleanly, that tweak is out of scope and the phase
  stops at "no behavioral change."

## UX

### CLI
- No CLI surface changes.

### TUI
- Pickers gain `bubbles/list`'s default `/` filter and bottom help footer.
- The "… N more" footer becomes list's standard paginator.
- Selection style stays identical: `Foreground(Warning).Bold(true)` + `┃ `
  prefix. Theme applied via `charm.ApplyListTheme` so every list looks
  the same.
- Deck row list scrolls smoothly via viewport; sticky project headers
  stay pinned at the top of the viewport area exactly as today.
- Forms gain huh's automatic tab / shift+tab navigation and validation,
  but the field set, layout, and key map stay identical (`Ctrl+G` still
  opens the prompt in `$EDITOR`).
- Help overlay renders via `bubbles/help` from the same `deckKeyGroups`
  slice the `?` overlay reads today. Visually identical (two-column on
  wide viewports, stacked on narrow); the status legend block stays
  custom.
- Progress modal log scrolls past 50 lines (regression-fix side effect).
- No new key bindings, no removed key bindings.

## Discovery Questions
1. **Who is the first user?** andrew. Every deck session.
2. **When do they use this feature?** Every interaction with a picker or
   the deck row list. The cleanup is invisible — the deck behaves the
   same.
3. **What exact output/result?** No behavioral change. Wins are: built-in
   `/` filter on all pickers; less code on `deckui.Model`; the CLAUDE.md
   guidance prevents the next regression.
4. **What data sources are required?** None new. Existing job state,
   workspace state, review state.
5. **What is the smallest useful slice?** Phase 1 (CLAUDE.md guidance)
   followed by phase 2 (bookmark picker → `list`). The doc change is
   free leverage on its own and the bookmark migration proves the
   `list.Model` pattern end-to-end.
6. **What are explicit non-goals?** Splitting `deckui.Model` into
   separate `tea.Model` structs; replacing the TUI framework; visual
   redesign; behavior changes.
7. **What does "done" look like?**
   - Every hand-rolled selection site is gone (3 pickers + jobs overlay
     + mini-deck).
   - Deck row list scrolls via viewport with sticky headers preserved.
   - Progress modal log scrolls via viewport.
   - Forms run on huh.
   - Help overlay rendered by `bubbles/help`.
   - Every `msg.String()` switch in `internal/deckui` replaced with
     `key.Binding` + `key.Matches`.
   - Every `lipgloss.Color(...)` call routes through
     `internal/charm/palette.go`.
   - Hot-path styles cached on the shared `Theme` or `Model`.
   - `CLAUDE.md` has the component-choice section, smell list,
     "don't follow hand-rolled precedent" rule, and the
     "check for a bubble before adding state to Model" rule. The
     pre-migration debt list shrinks to empty as phases complete.
   - No `*Cursor` / `*Offset` / `clamp*` state remains on
     `deckui.Model`.

## Spec Change Log
- 2026-05-22: Initial draft.

## Implementation Plan
Phased so each commit is independently reviewable, revertable, and
shippable on its own. Each phase ends with
`go test ./... && go vet ./... && go build ./...` green and a manual
smoke of the touched surface. Each phase also strikes its corresponding
line from the `CLAUDE.md` debt list (phase 1).

1. **CLAUDE.md guidance.** Land the component-choice section, smell
   list, debt list, and the "check for a bubble before adding state"
   rule before any code moves. Subsequent commits have something to
   point at and the next agent has the right starting context if work
   pauses mid-migration.

2. **Bookmark picker → `bubbles/list`.** Smallest. Mirrors
   `internal/cli/picker.go`. Delete `bookmarkCursor` + bespoke filter
   input from `deckui.Model`.

3. **Review picker → `bubbles/list`.** Same pattern.

4. **Open / project picker → `bubbles/list`.** Same pattern. After
   this commit, the three picker `*Cursor` fields and their `clamp*`
   helpers are gone.

5. **Jobs overlay → `list` + `viewport`.** Left pane is a list, right
   pane is a viewport. Remove `clampLines`.

6. **Mini-deck → `bubbles/list`.** Brings selection shape into
   alignment with the main pickers; sets up phase 8.

7. **Deck row list → `bubbles/viewport`.** Highest-risk single change
   because of sticky project headers. Render headers outside the
   viewport using the existing project-group layout; the viewport owns
   only the scrollable row body. Validate scroll behavior matches the
   recent "scroll left column to keep cursor in view when rows
   overflow" commit (`2636a8a`).

8. **Easymotion consolidation.** Deck and mini-deck now resolve
   selection via the same list-backed shape; extract the two-char
   mapping into a single helper in `internal/deckui`. Delete the
   duplicate.

9. **`new_workspace_form` → `huh`.** Replace the hand-rolled
   textinput + textarea composite with a `huh.Form` group. huh owns
   tab / shift+tab, validation, submission state. Preserve `Ctrl+G →
   $EDITOR` for the prompt field (escape out of huh's input long
   enough to issue `tea.ExecCommand($EDITOR)` on the deck program; the
   editor takes over, then deck redraws on return).

10. **`rename_workspace_form` → `huh`.** Same pattern; smaller form.

11. **Help overlay → `bubbles/help`.** Replace the ~500-line
    `renderHelp` string-assembly in `model.go:2760` with
    `help.Model.View()` driven by the existing `deckKeyGroups`. Status
    legend block (agent / PR / activity glyphs) stays custom — it's not
    a key map. Two-column / stacked breakpoint behavior preserved.

12. **Progress modal log → `bubbles/viewport`.** Remove the 50-line
    cap. Add scroll keys consistent with the jobs-overlay viewport from
    phase 5.

13. **Global `key.Binding` sweep.** Replace every remaining
    `msg.String()` switch in `internal/deckui` with `key.Binding` +
    `key.Matches`. Bindings flow through a central key map so the help
    overlay (phase 11) reads from the same source. This is the biggest
    diff but mechanical.

14. **Per-render style caching.** Promote `lipgloss.NewStyle()` sites
    inside hot render paths to fields on the shared `Theme` or
    precomputed `Model` styles. Start with the ~92 sites in
    `model.go`; the bar is "called inside `View` or a per-row render
    helper." One-off styles inside cold init paths can stay.

15. **Raw color codes → palette tokens.** Replace `lipgloss.Color(...)`
    calls at `activity.go:271`, `activity.go:272`, `model.go:3226`,
    and any others surfaced en route. Add a `Link` token to
    `internal/charm/palette.go` if needed for the blue/39 case. Grep
    for `lipgloss.Color\(` in `internal/deckui` and `internal/charm`
    and confirm zero hits outside the palette file at the end of this
    phase.

## Acceptance Criteria
- [ ] `CLAUDE.md` has the "TUI components: which primitive to reach
      for" section with decision table, smell list, "don't follow
      hand-rolled precedent" rule, and the "check for a bubble before
      adding state" rule. The pre-migration debt list shrinks to empty
      as phases complete.
- [ ] No `*Cursor`, `*Offset`, or `clamp*` fields remain on
      `deckui.Model` (every selection / scroll surface delegated to a
      bubble).
- [ ] All pickers (bookmark, review, open) use `bubbles/list` with the
      shared theme from `charm.ApplyListTheme`.
- [ ] Mini-deck uses `bubbles/list`.
- [ ] Deck row list uses `bubbles/viewport` for the scrollable body;
      project headers stay pinned at the top of the viewport region.
- [ ] Jobs overlay uses `bubbles/list` (left) + `bubbles/viewport`
      (right); `clampLines` is deleted.
- [ ] Progress modal log uses `bubbles/viewport`; 50-line cap removed.
- [ ] Easymotion has one implementation, not two.
- [ ] `new_workspace_form` and `rename_workspace_form` are
      `huh.Form`-based; `Ctrl+G → $EDITOR` still works for the prompt.
- [ ] Help overlay is rendered by `bubbles/help` from `deckKeyGroups`;
      status legend block stays custom; visual output matches today.
- [ ] Zero `msg.String()` switches remain in `internal/deckui` —
      everything routes through `key.Binding` + `key.Matches`.
- [ ] `grep -r 'lipgloss.Color(' internal/deckui internal/charm` shows
      hits only inside `internal/charm/palette.go`.
- [ ] Hot render paths in `model.go` reuse cached styles; no
      `lipgloss.NewStyle()` calls inside `View` or per-row render
      helpers.
- [ ] `go test ./... && go vet ./... && go build ./...` all green at
      every phase boundary.
- [ ] No user-visible behavior regression — selection style, padding,
      modal entry repaint, help overlay layout, status messages all
      identical to today.

## QA / Human Review Test Plan

### Setup
- [ ] `jj`, `tmux`, `gh`, project binary on PATH.
- [ ] Workspace with at least:
  - 3+ open PRs (one draft, one in merge queue, one with failing CI)
  - 5+ workspaces in multiple projects (to exercise project grouping +
    sticky headers + scroll past one viewport)
  - 3+ background jobs that have completed (for jobs overlay)
  - 5+ bookmarks
  - 3+ active reviews

### Core Happy Path
- [ ] `awp deck` opens; deck row list scrolls past one viewport;
      project headers stay pinned at the top of each project group as
      you scroll within that group.
- [ ] Cursor stays in view when scrolling rows (regression check on
      commit `2636a8a`).
- [ ] Press `b` → bookmark picker opens; `/` filters; arrows move
      selection; enter selects; esc cancels with no echo.
- [ ] Press the review-picker key → review picker opens; same filter +
      navigation behavior.
- [ ] Press the open-picker key → open picker opens; same.
- [ ] Press `j` (or whatever opens jobs overlay) → left list scrolls,
      right viewport scrolls independently with a long log.
- [ ] Mini-deck selection + easymotion still resolves the right
      workspace from a single keystroke.
- [ ] `n` opens the new-workspace form (huh); tab / shift+tab cycle
      fields; `Ctrl+G` opens the prompt in `$EDITOR` and returns
      cleanly; enter on the submit row dispatches the create job.
- [ ] Rename-workspace form (huh) submits with new name; cancel returns
      to row list.
- [ ] `?` help overlay opens with identical layout (two-column wide,
      stacked narrow); status legend block renders alongside the
      `bubbles/help` key map.
- [ ] Progress modal log scrolls past 50 lines without truncation.

### Edge Cases & Failure Modes
- [ ] Empty bookmark / review / open list → list's empty message; no
      panic.
- [ ] Picker with 1 item — enter selects without filter input
      weirdness.
- [ ] Picker with 200+ items — filter is responsive; paginator footer
      renders.
- [ ] Deck row list when total rows exceed viewport height by >2x — no
      flicker, no header drift.
- [ ] Jobs overlay with a 10k-line log — viewport scrolls without
      truncation; no `clampLines` artifacts.
- [ ] Progress modal with a 500-line log — viewport scrolls.
- [ ] Form validation: empty workspace name in `huh` blocks submission
      with an inline error; same for invalid bookmark names.
- [ ] Form `$EDITOR` flow: opening `$EDITOR` from huh's prompt field
      returns cleanly to the form with new content; cancelling
      `$EDITOR` leaves the existing prompt untouched.
- [ ] Resize terminal while a picker / overlay / form is open — layout
      reflows cleanly, no bg-bleed.
- [ ] Esc out of a picker / form → row list returns with cursor where
      it was; `m.status` empty (no "cancelled" echo).

### Regression Checks
- [ ] Spinner still animates while background jobs run.
- [ ] Activity bar still renders the right colors after the
      `activity.go:271-272` palette swap.
- [ ] Modal entry `tea.ClearScreen` pattern still holds — no stripes
      of underlying view leaking through on picker / form / overlay
      open.
- [ ] Selection style identical: `Foreground(Warning).Bold(true)` +
      `┃ ` prefix on every list, no inverse video or bg fill.
- [ ] Panel padding identical: every body panel still
      `Padding(1, 1, 1, 1)`.
- [ ] Help overlay key list matches `deckKeyGroups` exactly — no key
      added or dropped by the migration.
- [ ] No new dependencies beyond `bubbles/list`, `bubbles/viewport`,
      `bubbles/help`, `huh` (and `glamour` only if a markdown-render
      site is found).

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.
- Specifically watch for sticky-header drift on the deck viewport
  migration — that's the riskiest single change.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
