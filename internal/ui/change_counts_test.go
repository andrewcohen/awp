package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
)

// The file divider's +N -M, which is #337.
//
// It replaced a hunk count — how many places a file was edited, which a rename and
// a rewrite answer identically — and, folded, a single "N lines hidden" that added
// the two directions together. Since #336 folds a split's files by default, the
// divider is often the only thing about a file that is on screen, so what it says
// has to be the file's shape.

// mixedFile gained three lines and lost two, so the two counts differ and a test
// cannot pass by reading either one twice.
func mixedFile() diff.FileDiff {
	return diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1,
		Lines: []diff.HunkLine{
			{Type: '+', Content: "one"},
			{Type: '+', Content: "two"},
			{Type: '+', Content: "three"},
			{Type: '-', Content: "gone"},
			{Type: '-', Content: "also gone"},
			{Type: ' ', Content: "kept"},
		},
	}}}
}

// TestTheDividerCountsBothDirections, open and folded alike. Folded is the case
// that motivated it; open it has to agree, or the counts change meaning when a file
// is opened.
func TestTheDividerCountsBothDirections(t *testing.T) {
	m := streamModel(t, mixedFile())
	for _, folded := range []bool{false, true} {
		m.fileFold = map[string]bool{"a.go": folded}
		m.rebuildStream()
		row := ansi.Strip(m.renderStreamRowAt(m.stream.fileStart[0], 90))
		for _, want := range []string{"+3", "-2"} {
			if !strings.Contains(row, want) {
				t.Errorf("folded=%v: the divider is missing %q: %q", folded, want, row)
			}
		}
	}
}

// TestAPureDeletionStillShowsBothCounts. A zero is printed rather than dropped, so
// the counts sit in the same columns down the stream and the eye can compare files
// without reading them.
func TestAPureDeletionStillShowsBothCounts(t *testing.T) {
	f := diff.FileDiff{NewPath: "gone.go", Status: "D", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1,
		Lines: []diff.HunkLine{{Type: '-', Content: "was here"}},
	}}}
	m := streamModel(t, f)
	row := ansi.Strip(m.renderStreamRowAt(m.stream.fileStart[0], 90))
	if !strings.Contains(row, "+0") {
		t.Errorf("a pure deletion drops its added count: %q", row)
	}
}

// TestTheCountsWearTheDiffsOwnHues. Green for what arrived and red for what left —
// the colours those lines have in the body, so the divider needs no legend.
func TestTheCountsWearTheDiffsOwnHues(t *testing.T) {
	m := streamModel(t, mixedFile())
	row := m.renderStreamRowAt(m.stream.fileStart[0], 90)
	for _, c := range []struct {
		what  string
		style lipgloss.Style
		count string
	}{
		{"added", styleAdded, "+3"},
		{"removed", styleDeleted, "-2"},
	} {
		if want := c.style.Render(" " + c.count); !strings.Contains(row, want) {
			t.Errorf("the %s count is not in the body's %s hue: %q", c.what, c.what, ansi.Strip(row))
		}
	}
}

// TestAFoldedFilesChipsReadAsAList. The reviewed mark and the conversation count are
// separate facts about a folded file, and both sit before the counts — so the
// divider reads left to right as claim, discussion, shape.
func TestAFoldedFilesChipsReadAsAList(t *testing.T) {
	m := streamModel(t, mixedFile())
	// Keyed by the file's content hash, so the mark lapses when the file changes
	// under it — a literal here would just read as unreviewed.
	m.ReviewedFiles = map[string]string{"a.go": fileContentHash(m.filtered[0])}
	m.fileFold = map[string]bool{"a.go": true}
	m.rebuildStream()
	row := ansi.Strip(m.renderStreamRowAt(m.stream.fileStart[0], 90))
	reviewed, counts := strings.Index(row, reviewedChip), strings.Index(row, "+3")
	if reviewed < 0 || counts < 0 {
		t.Fatalf("the divider is missing the reviewed chip or the counts: %q", row)
	}
	if reviewed > counts {
		t.Errorf("the reviewed chip sits after the counts: %q", row)
	}
}
