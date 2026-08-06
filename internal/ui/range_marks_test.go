package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/review"
)

// rangedComment is a saved comment covering lines 1-3 of a.go.
func rangedComment(kind review.Kind) review.Comment {
	return review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: "this block", State: review.Open, Kind: kind,
		Anchor: review.Anchor{
			Path: "a.go", Side: review.SideNew,
			LineHint: 1, Text: "one",
			EndLineHint: 3, EndText: "three",
		},
	}
}

func markedModel(t *testing.T, c review.Comment) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "one", "two", "three", "four", "five"))
	m.comments = []review.Comment{c}
	m.rebuildStream()
	return m
}

// rowsOfLines is the stream rows showing the given line contents.
func rowsOfLines(m Model, want ...string) []int {
	var out []int
	for i, r := range m.stream.rows {
		if r.kind != rowLine || r.seg != 0 {
			continue
		}
		for _, w := range want {
			if m.lineText(r) == w {
				out = append(out, i)
			}
		}
	}
	return out
}

// Every line the comment covers is marked — the header says "1-3", and the lines
// have to agree without the reader counting.
func TestRangedCommentMarksTheLinesItCovers(t *testing.T) {
	m := markedModel(t, rangedComment(review.KindSuggestion))
	for _, i := range rowsOfLines(m, "one", "two", "three") {
		kind, ok := m.rangeMark(i)
		if !ok {
			t.Fatalf("row %d (%q) is inside the range but unmarked", i, m.lineText(m.stream.rows[i]))
		}
		if kind != review.KindSuggestion {
			t.Fatalf("row %d carries kind %q, want the comment's own", i, kind)
		}
	}
	for _, i := range rowsOfLines(m, "four", "five") {
		if _, ok := m.rangeMark(i); ok {
			t.Fatalf("row %d (%q) is outside the range but marked", i, m.lineText(m.stream.rows[i]))
		}
	}
}

// A single-line comment marks nothing: there is no block to outline, and a bar on
// one line would say the same thing the comment block below it already says.
func TestSingleLineCommentMarksNothing(t *testing.T) {
	m := markedModel(t, commentOn("a.go", 1, "one", "just here"))
	if len(m.marks) != 0 {
		t.Fatalf("expected no marks for a single-line comment, got %v", m.marks)
	}
}

// The bar is drawn in the kind's colour, so a suggestion's block reads as a
// suggestion at a glance.
func TestMarkedRowDrawsTheKindsBar(t *testing.T) {
	for kind, want := range map[review.Kind]string{
		review.KindSuggestion: charm.Danger,
		review.KindQuestion:   charm.Warning,
		review.KindComment:    charm.Info,
	} {
		m := markedModel(t, rangedComment(kind))
		rows := rowsOfLines(m, "two") // not the cursor's row, so the mark is what shows
		if len(rows) != 1 {
			t.Fatalf("expected one row for the middle line, got %v", rows)
		}
		out := m.renderStreamRowAt(rows[0], 60)
		if !strings.Contains(stripANSI(out), selectionPrefixBar) {
			t.Fatalf("%s: expected the bar in the prefix slot, got %q", kind, stripANSI(out))
		}
		if !strings.Contains(out, want) {
			t.Fatalf("%s: expected the bar in colour %s, got %q", kind, want, out)
		}
	}
}

// The cursor keeps its own row. Losing the cursor is worse than losing one row of
// the marker, and the rows around it still carry the mark.
func TestCursorWinsItsRowOverTheMark(t *testing.T) {
	m := markedModel(t, rangedComment(review.KindSuggestion))
	rows := rowsOfLines(m, "two")
	m.cursorRow = rows[0]
	if got := m.renderStreamRowAt(rows[0], 60); !strings.Contains(got, charm.Warning) {
		t.Fatalf("expected the cursor's own selection bar on its row, got %q", got)
	}
}

// While composing, the range being written about carries the bar — with the box's
// current kind, which `tab` changes without moving a row.
func TestComposingARangeMarksItAndTabRecoloursIt(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one", "two", "three", "four"))
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(pressTimes(press(m, "v"), "j", 2), "c")
	if !m.editing {
		t.Fatalf("expected the box open, status %q", m.status)
	}
	rows := rowsOfLines(m, "two")
	if len(rows) != 1 {
		t.Fatalf("expected the middle line still in the stream, got %v", rows)
	}
	kind, ok := m.rangeMark(rows[0])
	if !ok || kind != review.KindComment {
		t.Fatalf("expected the range marked with the box's kind, got %q ok=%v", kind, ok)
	}
	before := m.renderStreamRowAt(rows[0], 60)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if got, _ := m.rangeMark(rows[0]); got != review.KindSuggestion {
		t.Fatalf("expected tab to recolour the mark, got %q", got)
	}
	// Through the cache, which is what the mark is in the row key for.
	if after := m.cachedStreamRow(rows[0], 60); after == before {
		t.Fatalf("the recoloured row came back stale: %q", stripANSI(after))
	}
}

// A reply carries a copy of its parent's anchor, so marking both would paint the
// same rows twice — and a reply is not a second range.
func TestRepliesDoNotAddTheirOwnMarks(t *testing.T) {
	parent := rangedComment(review.KindSuggestion)
	m := commentModel(t, fileWith("a.go", 1, "one", "two", "three", "four"))
	reply := parent
	reply.ID, reply.ReplyTo, reply.Kind = "c2", parent.ID, review.KindQuestion
	m.comments = []review.Comment{parent, reply}
	m.rebuildStream()
	for _, i := range rowsOfLines(m, "one", "two", "three") {
		if kind, _ := m.rangeMark(i); kind != review.KindSuggestion {
			t.Fatalf("row %d took the reply's kind %q instead of the thread's", i, kind)
		}
	}
}
