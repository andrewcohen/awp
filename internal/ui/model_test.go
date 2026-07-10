package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/diff"
)

const sampleDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,2 @@
-old
+new
`

func TestModelInitReturnsCmd(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("expected init cmd")
	}
}

func TestModelRefreshKey(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected refresh command")
	}
}

func TestModelFilterMode(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M"}}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.focus != FocusFilter {
		t.Fatalf("expected filter focus, got %v", got.focus)
	}
	if !strings.Contains(got.filterInput.Value(), "f") {
		t.Fatalf("expected filter input to update, got %q", got.filterInput.Value())
	}
}

func TestModelOpenCurrentFile(t *testing.T) {
	openedPath := ""
	openedLine := 0
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(path string, line int) tea.Cmd {
		openedPath = path
		openedLine = line
		return nil
	})
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M", Hunks: []diff.Hunk{{NewStart: 5}}}}})
	_, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if openedPath == "" || openedLine != 5 {
		t.Fatalf("unexpected open: %q:%d", openedPath, openedLine)
	}
}

func TestModelEAlsoOpensCurrentFile(t *testing.T) {
	openedPath := ""
	openedLine := 0
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(path string, line int) tea.Cmd {
		openedPath = path
		openedLine = line
		return nil
	})
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M", Hunks: []diff.Hunk{{NewStart: 7}}}}})
	_, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if openedPath == "" || openedLine != 7 {
		t.Fatalf("unexpected open via e: %q:%d", openedPath, openedLine)
	}
}

func TestModelErrorStatus(t *testing.T) {
	m := New("/repo", func() (string, error) { return "", errors.New("boom") }, nil)
	updated, _ := m.Update(diffLoadedMsg{err: errors.New("boom")})
	got := updated.(Model)
	if !got.statusErr || !strings.Contains(got.status, "boom") {
		t.Fatalf("unexpected status: %q err=%t", got.status, got.statusErr)
	}
}

func TestScheduleRefreshDisabledWhenZero(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.RefreshInterval = 0
	updated, cmd := m.Update(diffLoadedMsg{})
	_ = updated
	if cmd != nil {
		t.Fatal("expected no refresh scheduling when disabled")
	}
}

func TestHAndLMoveBetweenPanels(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	got := updated.(Model)
	if got.focus != FocusHunks {
		t.Fatalf("expected hunk focus after l, got %v", got.focus)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got = updated.(Model)
	if got.focus != FocusFiles {
		t.Fatalf("expected file focus after h, got %v", got.focus)
	}
}

func TestFilterFooterIsStableHeight(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.width = 100
	m.height = 20
	base := m.renderFooter()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	withFilter := updated.(Model).renderFooter()
	if len(strings.Split(base, "\n")) != len(strings.Split(withFilter, "\n")) {
		t.Fatalf("expected stable footer height, got %d vs %d", len(strings.Split(base, "\n")), len(strings.Split(withFilter, "\n")))
	}
	if !strings.Contains(withFilter, "Filter files:") {
		t.Fatalf("expected filter prompt in footer, got %q", withFilter)
	}
}

func TestDefaultRefreshIntervalSet(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if m.RefreshInterval != DefaultRefreshInterval {
		t.Fatalf("got %v want %v", m.RefreshInterval, DefaultRefreshInterval)
	}
	if m.RefreshInterval != 0 {
		t.Fatalf("expected auto-refresh disabled by default, got %v", m.RefreshInterval)
	}
}

func TestCtrlDPagesFileList(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.height = 20
	files := []diff.FileDiff{
		{NewPath: "a.go", Status: "M"},
		{NewPath: "b.go", Status: "M"},
		{NewPath: "c.go", Status: "M"},
		{NewPath: "d.go", Status: "M"},
		{NewPath: "e.go", Status: "M"},
		{NewPath: "f.go", Status: "M"},
		{NewPath: "g.go", Status: "M"},
		{NewPath: "h.go", Status: "M"},
	}
	updated, _ := m.Update(diffLoadedMsg{files: files})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(Model)
	if got.filesCursor != 7 {
		t.Fatalf("expected files cursor to page to 7, got %d", got.filesCursor)
	}
}

func TestCtrlUPagesHunks(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.height = 20
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks:   []diff.Hunk{{NewStart: 1, Lines: []diff.HunkLine{{Type: ' ', Content: "a"}}}, {NewStart: 2, Lines: []diff.HunkLine{{Type: ' ', Content: "b"}}}, {NewStart: 3, Lines: []diff.HunkLine{{Type: ' ', Content: "c"}}}, {NewStart: 4, Lines: []diff.HunkLine{{Type: ' ', Content: "d"}}}, {NewStart: 5, Lines: []diff.HunkLine{{Type: ' ', Content: "e"}}}, {NewStart: 6, Lines: []diff.HunkLine{{Type: ' ', Content: "f"}}}, {NewStart: 7, Lines: []diff.HunkLine{{Type: ' ', Content: "g"}}}, {NewStart: 8, Lines: []diff.HunkLine{{Type: ' ', Content: "h"}}}},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	got.hunkScroll = 7
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	got = updated.(Model)
	if got.hunkScroll != 0 {
		t.Fatalf("expected hunk scroll to page to 0, got %d", got.hunkScroll)
	}
}

func TestCtrlDScrollsSingleLargeHunk(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.height = 12
	lines := make([]diff.HunkLine, 12)
	for i := range lines {
		lines[i] = diff.HunkLine{Type: ' ', Content: "line"}
	}
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks:   []diff.Hunk{{NewStart: 1, Lines: lines}},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got = updated.(Model)
	if got.hunkScroll == 0 {
		t.Fatal("expected ctrl+d to scroll a single large hunk")
	}
}

func TestRenderHunkLinesUsesMinimalLineNumberGutterWidth(t *testing.T) {
	h := diff.Hunk{
		OldStart: 1,
		NewStart: 1,
		Lines:    []diff.HunkLine{{Type: ' ', Content: "one"}, {Type: '+', Content: "two"}},
	}
	lines := renderHunkLines(h, 80)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rendered lines, got %d", len(lines))
	}

	plain := stripANSI(lines[0])
	if strings.HasPrefix(plain, "   1") {
		t.Fatalf("expected compact gutter, got %q", plain)
	}
	if !strings.HasPrefix(plain, "1 1 │ ") {
		t.Fatalf("expected minimal-width line numbers, got %q", plain)
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
