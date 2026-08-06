package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// Every bordered pane spans the width it is handed and stands two rows taller
// than the height it is handed — the border rows sit outside the caller's
// budget, which is what buildLeftColumn's arithmetic assumes when it splits the
// column between the file list and the comment index.
//
// This exists because the Charm v2 migration broke it and nothing noticed.
// lipgloss v2 counts border cells inside Style.Width/Height where v1 drew them
// outside, so `border.Width(width-2).Height(height)` quietly became two columns
// narrow — and the comment rows, built for the old inner width, wrapped instead
// of fitting. A wrapped row is an extra row, so the column grew past its budget
// and pushed the layout down. Nothing failed; the panes were simply wrong.
//
// Assert the geometry, not the content: content changes constantly and these
// numbers should not.
func TestBorderedPanesFillTheirBudget(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	// Bodies long enough that a pane two columns narrow would wrap them, which
	// is the failure this is guarding: the widths alone would still look right.
	m.comments = []review.Comment{
		{ID: "c1", Body: "first remark about this line", Anchor: review.Anchor{Path: "a.go", LineHint: 1}},
		{ID: "c2", Body: "second remark, a bit longer than the first", Anchor: review.Anchor{Path: "a.go", LineHint: 2}},
		{ID: "c3", Body: "third", Anchor: review.Anchor{Path: "a.go", LineHint: 3}},
	}
	m.SetSize(120, 40)

	const paneWidth = 30
	paneHeight := commentPaneHeight(len(m.commentIndex), m.hiddenThreads(), m.bodyHeight)
	if paneHeight <= 0 {
		t.Fatal("fixture is wrong: the comment pane is not visible")
	}

	// The stream panel is deliberately not here. Its rows are the diff's own,
	// which carry their own widths (a short file divider, a trailing rule), so
	// it does not render as a uniform block and asserting one would be
	// inventing a requirement it never met.
	for _, tc := range []struct {
		name   string
		out    string
		height int
	}{
		{"comment index", m.renderCommentList(paneWidth, paneHeight), paneHeight},
		{"file list", m.renderFileList(paneWidth, m.bodyHeight-2-paneHeight), m.bodyHeight - 2 - paneHeight},
		{"empty-diff placeholder", emptyModel(t).renderStreamPanel(paneWidth, 12), 12},
	} {
		rows := strings.Split(tc.out, "\n")
		if got, want := len(rows), tc.height+panelBorderRows; got != want {
			t.Errorf("%s: %d rows, want %d (a budget of %d plus its border)",
				tc.name, got, want, tc.height)
		}
		for i, r := range rows {
			if got := lipgloss.Width(r); got != paneWidth {
				t.Errorf("%s: row %d is %d wide, want %d", tc.name, i, got, paneWidth)
				break
			}
		}
	}
}

// emptyModel is a viewer with no files, which is the branch that draws the
// "No changes" placeholder rather than a stream.
func emptyModel(t *testing.T) Model {
	t.Helper()
	m := commentModel(t)
	m.SetSize(120, 40)
	return m
}

// The whole left column adds up: the file list and the comment index stacked
// come to the body height plus one border pair, because buildLeftColumn hands
// the file list `height-2-paneHeight` precisely so the two together fit.
func TestTheLeftColumnAddsUp(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.comments = []review.Comment{
		{ID: "c1", Body: "a remark", Anchor: review.Anchor{Path: "a.go", LineHint: 1}},
		{ID: "c2", Body: "another remark", Anchor: review.Anchor{Path: "a.go", LineHint: 2}},
	}
	m.SetSize(120, 40)
	col := m.renderLeftColumn(30, m.bodyHeight)
	if got, want := len(strings.Split(col, "\n")), m.bodyHeight+panelBorderRows; got != want {
		t.Errorf("the left column is %d rows against a body of %d, want %d",
			got, m.bodyHeight, want)
	}
}
