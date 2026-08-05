package deckui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// deckIndent is a literal string standing in for a computed width, so the two
// can drift. They only stay equal because of this.
func TestDeckIndentMatchesTextCol(t *testing.T) {
	if len(deckIndent) != deckTextCol {
		t.Errorf("deckIndent is %d spaces, deckTextCol is %d — the title row would sit off the body column",
			len(deckIndent), deckTextCol)
	}
}

// colOfFirst returns the column the given text starts in, on the first
// rendered line that contains it, with ANSI stripped.
func colOfFirst(t *testing.T, out, want string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(ansi.Strip(line), want); i >= 0 {
			return i
		}
	}
	return -1
}

func TestTitleRowAlignsWithProjectHeader(t *testing.T) {
	// The title heads the list below it, so it has to sit in the same column
	// the list's own headers do. It used to render at col 0 while every
	// project header's name started at deckTextCol, which read as the heading
	// hanging off the panel's left edge.
	items := []Item{
		{ProjectName: "frontend", WorkspaceName: "dashboard", Status: "idle"},
		{ProjectName: "frontend", WorkspaceName: "feat", Status: "idle"},
	}
	m := New(items, nil)
	m.width, m.height = 100, 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	titleCol := colOfFirst(t, out, "awp deck")
	headerCol := colOfFirst(t, out, "frontend")
	if titleCol < 0 || headerCol < 0 {
		t.Fatalf("expected both a title and a 'frontend' header; title=%d header=%d", titleCol, headerCol)
	}
	if titleCol != headerCol {
		t.Errorf("title starts at col %d, project header name at col %d — must align", titleCol, headerCol)
	}
}

func TestTitleRowScopeLabelStaysAtTheRightEdge(t *testing.T) {
	// Indenting the title must not drag the scope label in with it: the label
	// is pinned to the panel's inner right edge, which is where the rows
	// themselves end.
	m := New([]Item{{ProjectName: "web", WorkspaceName: "default", Status: "idle"}}, nil)
	m.width, m.height = 100, 40
	(&m).clampDeckViewport()

	var titleLine string
	for _, line := range strings.Split(m.renderList(m.width), "\n") {
		if strings.Contains(ansi.Strip(line), "awp deck") {
			titleLine = line
			break
		}
	}
	if titleLine == "" {
		t.Fatal("no title line rendered")
	}
	// The panel pads 1 col on each side, so a full-width line is m.width wide
	// and the scope label's last cell is the second-to-last column.
	if got := lipgloss.Width(titleLine); got != m.width {
		t.Errorf("title line is %d cols, want %d (scope label pulled off the right edge)", got, m.width)
	}
	if !strings.HasSuffix(strings.TrimRight(ansi.Strip(titleLine), " "), scopeLabel(m.scope)) {
		t.Errorf("title line does not end with the scope label: %q", ansi.Strip(titleLine))
	}
}

func TestNoWorkspacesMessageAlignsWithTheTitle(t *testing.T) {
	m := New(nil, nil)
	m.width, m.height = 100, 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	titleCol := colOfFirst(t, out, "awp deck")
	msgCol := colOfFirst(t, out, "No workspaces found.")
	if titleCol < 0 || msgCol < 0 {
		t.Fatalf("expected a title and the empty-state message; title=%d msg=%d", titleCol, msgCol)
	}
	if titleCol != msgCol {
		t.Errorf("empty-state message at col %d, title at col %d — must align", msgCol, titleCol)
	}
}
