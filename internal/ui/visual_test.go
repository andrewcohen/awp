package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

// rangeModel is a viewer over one file of numbered lines, with a store to save
// into, cursor parked on the first line of code.
func rangeModel(t *testing.T, extra ...diff.FileDiff) Model {
	t.Helper()
	files := append([]diff.FileDiff{fileWith("a.go", 1,
		"one",
		"two",
		"three",
		"four",
		"five",
	)}, extra...)
	m := commentModel(t, files...)
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	return m
}

// esc as a KeyMsg, which is not a runes key.
func pressEsc(m Model) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return updated.(Model)
}

// The gesture: `v` at one end, movement to the other, `c` to comment on it.
func TestVisualRangeAnchorsEveryLineItCovers(t *testing.T) {
	m := pressTimes(press(rangeModel(t), "v"), "j", 2)
	a, err := m.rangeAnchor()
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if a.LineHint != 1 || a.EndLineHint != 3 {
		t.Fatalf("expected lines 1-3, got %d-%d", a.LineHint, a.EndLineHint)
	}
	if a.Text != "one" || a.EndText != "three" {
		t.Fatalf("expected both ends anchored by text, got %q…%q", a.Text, a.EndText)
	}
	if got := a.LineRange(); got != "1-3" {
		t.Fatalf("expected the range spelled 1-3, got %q", got)
	}
}

// Selecting upwards is as natural as downwards, so the anchor can be either end.
func TestVisualRangeSelectsUpwards(t *testing.T) {
	m := pressTimes(rangeModel(t), "j", 3)
	m = pressTimes(press(m, "v"), "k", 2)
	a, err := m.rangeAnchor()
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if a.LineHint != 2 || a.EndLineHint != 4 {
		t.Fatalf("expected lines 2-4, got %d-%d", a.LineHint, a.EndLineHint)
	}
}

// A single row with `v` up is not a range: the anchor is the plain single-line
// one, so a `v`-then-`c` on one line behaves exactly like `c`.
func TestVisualRangeOfOneLineIsNotMultiline(t *testing.T) {
	a, err := press(rangeModel(t), "v").rangeAnchor()
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if a.Multiline() || a.EndLineHint != 0 {
		t.Fatalf("expected a single-line anchor, got %+v", a)
	}
}

// `c` on a range saves one comment covering it, not one per line.
func TestCommentOnARangeSavesOneRangedComment(t *testing.T) {
	m := rangeModel(t)
	var saved []review.Comment
	m.SaveComment = func(c review.Comment) error { saved = append(saved, c); return nil }
	m = press(pressTimes(press(m, "v"), "j", 2), "c")
	if !m.editing {
		t.Fatalf("expected c to open the compose box, status %q", m.status)
	}
	if m.visualActive() {
		t.Fatal("expected the range to be consumed by the compose box")
	}
	for _, r := range "this block" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(saved) != 1 {
		t.Fatalf("expected one comment, got %d", len(saved))
	}
	if got := saved[0].Anchor.LineRange(); got != "1-3" {
		t.Fatalf("expected the saved anchor to cover 1-3, got %q", got)
	}
}

// The compose box says what it is attached to, so a range comment cannot be
// mistaken for a comment on its first line.
func TestComposeBoxHeaderShowsTheRange(t *testing.T) {
	m := press(pressTimes(press(rangeModel(t), "v"), "j", 2), "c")
	if got := stripANSI(m.editor.view(60)); !strings.Contains(got, "a.go:1-3") {
		t.Fatalf("expected the box header to name the range, got %q", got)
	}
}

// Every row of the range is marked, not just its ends — a range whose middle
// looked unselected would not read as a range.
func TestVisualRangeIsSelectedEndToEnd(t *testing.T) {
	m := pressTimes(press(rangeModel(t), "v"), "j", 2)
	lo, hi, ok := m.visualSpan()
	if !ok {
		t.Fatal("expected an active span")
	}
	for i := lo; i <= hi; i++ {
		if !m.rowSelected(i) || !m.rowBanded(i) {
			t.Fatalf("row %d is inside the range but not marked", i)
		}
	}
	if m.rowSelected(hi + 1) {
		t.Fatalf("row %d is outside the range but marked", hi+1)
	}
}

// The band is the focus-dependent half of the selection: it belongs to whichever
// pane holds the keyboard, so a range in an unfocused diff keeps its bars and
// loses its fill.
func TestVisualRangeLosesTheBandWhenUnfocused(t *testing.T) {
	m := pressTimes(press(rangeModel(t), "v"), "j", 2)
	m.focus = FocusFiles
	lo, _, _ := m.visualSpan()
	if !m.rowSelected(lo) {
		t.Fatal("expected the range to stay selected while unfocused")
	}
	if m.rowBanded(lo) {
		t.Fatal("expected no band while another pane holds the keyboard")
	}
}

// The cached row has to know about both halves of the treatment, or a frame is
// served a row painted for a different selection.
func TestCachedRowSeparatesRangeFromPlainRows(t *testing.T) {
	m := pressTimes(press(rangeModel(t), "v"), "j", 2)
	lo, _, _ := m.visualSpan()
	banded := m.cachedStreamRow(lo+1, 60)
	// Same row, no range: it must not come back out of the cache with the band on.
	m.cancelVisual()
	m.cursorRow = 0
	if plain := m.cachedStreamRow(lo+1, 60); plain == banded {
		t.Fatalf("cache served the banded row to an unselected frame: %q", stripANSI(plain))
	}
}

// A second `v` gets you out of a range, and so does esc.
func TestVisualCancels(t *testing.T) {
	m := press(rangeModel(t), "v")
	if !m.visualActive() {
		t.Fatal("expected v to start a range")
	}
	if press(m, "v").visualActive() {
		t.Fatal("expected a second v to cancel")
	}
	if pressEsc(m).visualActive() {
		t.Fatal("expected esc to cancel")
	}
	// Cancelling says nothing: the highlight disappearing is the message.
	if got := pressEsc(m).status; got != "" {
		t.Fatalf("expected a silent cancel, got %q", got)
	}
}

// A range across a hunk (or file) boundary cannot mean what it looks like — the
// lines between the hunks are not in the diff — so it is refused rather than
// clamped, and the range stays up to be adjusted.
func TestRangeAcrossFilesIsRefused(t *testing.T) {
	m := rangeModel(t, fileWith("b.go", 1, "other"))
	m = press(m, "v")
	// Far enough to be inside the second file.
	m = pressTimes(m, "j", len(m.stream.rows))
	if _, err := m.rangeAnchor(); err != errRangeSpansHunks {
		t.Fatalf("expected the cross-hunk error, got %v", err)
	}
	m = press(m, "c")
	if m.editing {
		t.Fatal("expected no compose box for a range spanning files")
	}
	if !m.visualActive() {
		t.Fatal("expected the range to stay up so it can be adjusted")
	}
	if !strings.Contains(m.status, "one hunk") {
		t.Fatalf("expected the reason in the status, got %q", m.status)
	}
}

// A selection of nothing but headers has no lines to comment on.
func TestRangeWithNoDiffLinesIsRefused(t *testing.T) {
	m := rangeModel(t)
	m.cursorRow = 0 // the file divider
	m.startVisual()
	if _, err := m.rangeAnchor(); err != errRangeNoLines {
		t.Fatalf("expected the no-lines error, got %v", err)
	}
}

// A mixed range is about the resulting code, so it takes the new side and the
// removals in it — which have no new-side number — cannot be its ends.
func TestMixedRangeAnchorsToTheNewSide(t *testing.T) {
	f := diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, OldCount: 3, NewCount: 3,
		Lines: []diff.HunkLine{
			{Type: '-', Content: "gone"},
			{Type: '+', Content: "added"},
			{Type: ' ', Content: "kept"},
		},
	}}}
	m := commentModel(t, f)
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = pressTimes(press(m, "v"), "j", 2)
	a, err := m.rangeAnchor()
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if a.Side != review.SideNew {
		t.Fatalf("expected the new side, got %q", a.Side)
	}
	if a.Text != "added" || a.EndText != "kept" {
		t.Fatalf("expected the removal dropped from the ends, got %q…%q", a.Text, a.EndText)
	}
}

// A selection of only removals is about the old side: those lines exist nowhere
// else.
func TestRemovalOnlyRangeAnchorsToTheOldSide(t *testing.T) {
	f := diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, OldCount: 2, NewCount: 0,
		Lines: []diff.HunkLine{
			{Type: '-', Content: "first gone"},
			{Type: '-', Content: "second gone"},
		},
	}}}
	m := commentModel(t, f)
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	a, err := pressTimes(press(m, "v"), "j", 1).rangeAnchor()
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if a.Side != review.SideOld || a.LineHint != 1 || a.EndLineHint != 2 {
		t.Fatalf("expected old side 1-2, got %+v", a)
	}
}

// A range comment hangs under the last line it covers — everything above the
// remark is what the remark is about.
func TestRangeCommentPlacesAtItsLastLine(t *testing.T) {
	m := rangeModel(t)
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: "this block", State: review.Open,
		Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew,
			LineHint: 1, Text: "one",
			EndLineHint: 3, EndText: "three",
		},
	}
	row, ok := m.locateComment(m.stream.rows, c)
	if !ok {
		t.Fatal("expected the range comment placed")
	}
	if got := m.lineText(m.stream.rows[row]); got != "three" {
		t.Fatalf("expected placement on the range's last line, got %q", got)
	}
}

// The end is located by its own text, so an edit that renumbers the file does
// not stretch or shrink what the comment covers.
func TestRangeEndFollowsItsText(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1,
		"inserted",
		"one",
		"two",
		"three",
		"four",
	))
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: "this block", State: review.Open,
		Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew,
			LineHint: 1, Text: "one",
			EndLineHint: 3, EndText: "three",
		},
	}
	row, ok := m.locateComment(m.stream.rows, c)
	if !ok {
		t.Fatal("expected the range comment placed")
	}
	if got := m.lineText(m.stream.rows[row]); got != "three" {
		t.Fatalf("expected the end found by text after a shift, got %q", got)
	}
}

// The end could not be found at all: shown one line high rather than pushed
// somewhere the code does not support.
func TestRangeEndFallsBackToTheStart(t *testing.T) {
	m := rangeModel(t)
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: "this block", State: review.Open,
		Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew,
			LineHint: 1, Text: "one",
			EndLineHint: 90, EndText: "long gone",
		},
	}
	row, ok := m.locateComment(m.stream.rows, c)
	if !ok {
		t.Fatal("expected the comment still placed")
	}
	if got := m.lineText(m.stream.rows[row]); got != "one" {
		t.Fatalf("expected the fallback to the start line, got %q", got)
	}
}

// The index names a location the same way everything else does.
func TestCommentIndexShowsTheRange(t *testing.T) {
	m := rangeModel(t)
	m.comments = []review.Comment{{
		ID: "c1", Author: review.AuthorHuman, Body: "this block", State: review.Open,
		Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew,
			LineHint: 1, Text: "one",
			EndLineHint: 3, EndText: "three",
		},
	}}
	m.rebuildStream()
	if len(m.commentIndex) != 1 {
		t.Fatalf("expected one index entry, got %d", len(m.commentIndex))
	}
	if got := entryLocation(m.commentIndex[0]); !strings.Contains(got, "a.go:1-3") {
		t.Fatalf("expected the index to name the range, got %q", got)
	}
}

// The range is a row index into a set that a reload replaces, so anything that
// rebuilds the rows drops it rather than moving one end onto whatever took that
// slot.
func TestRebuildDropsTheRange(t *testing.T) {
	m := pressTimes(press(rangeModel(t), "v"), "j", 3)
	if got := loadWith(m, 2, fileWith("a.go", 1, "one")); got.visualActive() {
		t.Fatal("expected a reload to drop the range")
	}
	// Wrap re-lays every row, so the indices mean something else entirely.
	if got := press(m, "w"); got.visualActive() {
		t.Fatal("expected a wrap toggle to drop the range")
	}
	// Extending it must not: j/k are how a range gets its length.
	if got := press(m, "j"); !got.visualActive() {
		t.Fatal("expected j to extend the range, not end it")
	}
}
