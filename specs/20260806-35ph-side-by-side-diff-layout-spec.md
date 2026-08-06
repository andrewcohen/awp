# Side-by-side diff layout

## Metadata
- **Spec ID**: `20260806-35ph`
- **Feature name**: Side-by-side diff layout (`|`)
- **Owner**: andrewcohen
- **Status**: In Progress
- **Last updated**: 2026-08-06

## Goal

`|` in the diff viewer switches between the current inline (unified) layout and a
side-by-side one, and back. Same surface, same keys, same review store — only how
a changed line is drawn differs.

## User Problem

A unified diff shows a rewritten line twice, stacked: the old text, then the new,
and you diff them in your head. That is fine for an added or deleted line and
bad for a *modified* one, which is most of what a review is. Side-by-side puts
the two versions on the same row, so the eye compares horizontally and the change
is a difference in position rather than something to reconstruct.

Unified stays the default. It is right for narrow panes, for changes that are
mostly additions, and for reading a hunk as prose — the point is a toggle, not a
replacement.

## Scope

### In scope (v1)
- `|` toggles the layout, from the diff pane and from the two lists.
- Line pairing: a run of removals is paired against the run of additions that
  follows it; context lines pair with themselves.
- Two gutters, one per column, each showing that side's line number.
- Comments, GitHub threads, the compose box and the review/detached sections
  render **full width** beneath the pair, exactly as they do today.
- `c` / `v` / `r` / `R` / `A` / search / hunk jumps all keep working, on rows.
- A width floor: below it `|` refuses and says why.
- `w` (wrap) is refused while side-by-side, and says so.

### Out of scope (v1)
- Intra-line (character-level) highlighting of what changed within a pair. It is
  the obvious next thing and it is a separate problem — a diff algorithm per
  pair, not a layout.
- Wrapping inside a column (see Decisions).
- Remembering the layout across sessions. It is a reading preference for the
  change in front of you, like the scope menu.
- A different layout per file.

## Decisions

Settled with the user before implementation; both were chosen because the
alternative costs the row-count contract that the whole stream depends on.

1. **Wrap is off in side-by-side.** `w` says `wrap is off in side-by-side — h/l
   pans` rather than silently doing nothing. One line-pair is always exactly one
   row, so `buildStream` stays arithmetic and every seg index keeps its meaning.
   The alternative — each column wrapping independently, pair height being
   `max(left, right)` — makes a row's height depend on both sides' content and
   forces every `seg` calculation to know which column it is in.

2. **Comments render full width, under the pair.** Same `commentRows`, same
   cache, same counts. A conversation is about the change, and the change is the
   pair; rendering it in one column would halve the width prose is read at and
   duplicate the row-count work per column.

3. **A pair anchors to its new side when it has one, otherwise to its old side.**
   Not a new rule — it is exactly what a mixed `v` range already does ("a mixed
   selection anchors to the new side, dropping the removals from its ends; a
   selection of nothing but removals anchors to the old side"). Side-by-side
   makes an existing rule visible rather than introducing one.

4. **`|` as the key.** It draws the split it produces. `w` was taken by wrap and
   `s` by nothing memorable.

## UX

### TUI

```
  ══ internal/ui/stream.go ══════════════════════════════════════════
  @@ -12,4 +12,5 @@

   12 │ func old(a int) {        │  12 │ func new(a, b int) {
   13 │   return a               │  13 │   return a + b
      │                          │  14 │   // and the new line
```

- **Two gutters.** Each column carries its own line number and its own `│`.
  A cell with no counterpart on that side is blank — an addition has no left, a
  deletion no right.
- **Colour is per column**, so a paired modification is red on the left and green
  on the right. A context pair is unstyled on both sides, which is what makes the
  changed rows stand out at a glance.
- **The cursor spans the row**, both columns, because the row is what every key
  acts on. One selection, as everywhere else in the app.
- **Below `sideBySideMinWidth` columns `|` refuses**: `side-by-side needs a wider
  pane — 100 columns, this is 78`. Two columns of 30 is not a diff, it is two
  truncated diffs, and silently falling back would leave the key looking broken.
  `\` (hide the left column) is the thing to reach for, and the message says so.
- **`w` refuses** with `wrap is off in side-by-side — h/l pans`.
- Toggling **keeps your place**: the cursor stays on the same source line, the
  same way switching scope does not. Row indices change, so the cursor is
  re-resolved against the new geometry rather than carried across as a number.

## Implementation Plan

1. **Geometry.** `buildStream` takes a `sideBySide bool`. When set, a hunk's
   lines are paired by `pairHunkLines` before rows are emitted: a maximal run of
   `-` is zipped against the `+` run immediately following it, leftovers on
   either side pair with nothing, and any other line pairs with itself. `rowRef`
   gains `lineOld` / `lineNew` (the existing `line` keeps meaning "the row's
   source line" for unified and is set to whichever side the row anchors to).
2. **`hunkMeta`** gains the per-column widths: in side-by-side the prefix is
   computed twice, and the content width per column is
   `(width - 2*prefixWidth - dividerWidth) / 2`.
3. **Render.** `renderStreamLine` branches: unified as now, side-by-side composes
   `cell(old) + divider + cell(new)`, each cell built by the same
   number+gutter+content code path so the two layouts cannot drift on styling.
4. **Anchoring.** `AnchorAtCursor` and the range code read the row's anchor side
   via one helper (`rowRef.anchorSide()`), so decision 3 has exactly one
   spelling.
5. **The toggle.** `|` in the key switch, the width guard, the `w` refusal,
   `rebuildStream` on change, cursor re-resolution, and `sideBySide` in the
   render cache key.
6. **Docs.** `viewerKeyGroups`, the README key list, and this spec's status.

## Acceptance Criteria

- [ ] `|` switches to side-by-side and back, from any pane.
- [ ] A modified line shows old and new on one row, coloured per column.
- [ ] A pure addition has a blank left cell; a pure deletion a blank right cell.
- [ ] Row count is unchanged by *rendering* — geometry and render agree, asserted
      the way the unified stream already asserts it.
- [ ] `c` on a pair anchors to the new side; on a deletion-only pair, the old.
- [ ] Comments, threads and the compose box render full width and unchanged.
- [ ] `v` ranges, `/` search, `{`/`}`, `r`, `R` and `zz` behave the same in both.
- [ ] `|` below the width floor refuses with a message naming the floor and the
      actual width.
- [ ] `w` while side-by-side refuses with a message.
- [ ] Toggling keeps the cursor on the same source line.
- [ ] `?` and the README document the key.

## QA / Human Review Test Plan

### Setup
- [ ] `awp deck`, `c` into a change with real modifications (not just additions).

### Core Happy Path
- [ ] `|` splits the pane; modified lines read across, not down.
- [ ] `|` again returns to unified, on the same line.
- [ ] Comment on a paired line; it spans the full width and publishes to the
      right side.

### Edge Cases & Failure Modes
- [ ] Narrow the terminal below the floor, press `|` — refused, with the numbers.
- [ ] `w` while split — refused, with the reason.
- [ ] A file that is purely additions, and one purely deletions.
- [ ] A hunk whose removal run is longer than its addition run, and the reverse.

### Regression Checks
- [ ] Unified is untouched: same rows, same colours, same keys.
- [ ] `\`, `T`, `P` and the publish preview behave identically in both layouts.

## Validation
- [ ] `mise exec -- gofmt -l .`
- [ ] `mise exec -- golangci-lint run ./...`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`

## Spec Change Log
- 2026-08-06: Initial draft. Wrap and comment-placement decisions settled with
  the user before implementation (see Decisions 1 and 2); `|` chosen as the key
  earlier, recorded on the task.
