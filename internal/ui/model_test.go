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

// enter is deliberately not an open binding — `e` is the only one.
func TestModelEnterDoesNotOpenFile(t *testing.T) {
	opened := false
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(string, int) tea.Cmd {
		opened = true
		return nil
	})
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M", Hunks: []diff.Hunk{{NewStart: 5}}}}})
	_, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if opened {
		t.Fatal("expected enter not to open a file")
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
	m.bodyHeight = 16
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
	m.bodyHeight = 16
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
	m.bodyHeight = 16
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
	m.bodyHeight = 8
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

// singleHunkModel is a file with one hunk taller than the pane — the case
// that used to be unscrollable with j/k.
func singleHunkModel(t *testing.T, lineCount int) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.bodyHeight = 8
	lines := make([]diff.HunkLine, lineCount)
	for i := range lines {
		lines[i] = diff.HunkLine{Type: ' ', Content: "line"}
	}
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks:   []diff.Hunk{{OldStart: 1, NewStart: 1, Lines: lines}},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	return got
}

func TestJScrollsSingleLargeHunk(t *testing.T) {
	m := singleHunkModel(t, 20)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if got.hunkScroll != 1 {
		t.Fatalf("expected j to scroll one line, got hunkScroll=%d", got.hunkScroll)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := updated.(Model).hunkScroll; got != 0 {
		t.Fatalf("expected k to scroll back, got hunkScroll=%d", got)
	}
}

func TestKStopsAtTopOfHunkPane(t *testing.T) {
	m := singleHunkModel(t, 20)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := updated.(Model).hunkScroll; got != 0 {
		t.Fatalf("expected scroll to clamp at 0, got %d", got)
	}
}

// Scrolling past a hunk boundary must move the cursor with it, so the
// highlighted header and `e`'s target line match what's on screen.
func TestScrollSyncsHunkCursor(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	// A short pane, so 9 rows of content actually have room to scroll.
	m.bodyHeight = 4
	hunk := func(start int) diff.Hunk {
		return diff.Hunk{OldStart: start, NewStart: start, Lines: []diff.HunkLine{
			{Type: ' ', Content: "a"}, {Type: '+', Content: "b"},
		}}
	}
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks:   []diff.Hunk{hunk(1), hunk(10), hunk(20)},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	// Each hunk renders as 1 header + 2 lines = 3 rows, so row 3 is the
	// second hunk's header.
	for i := 0; i < 3; i++ {
		updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		got = updated.(Model)
	}
	if got.hunksCursor != 1 {
		t.Fatalf("expected the cursor to follow the scroll to hunk 1, got %d", got.hunksCursor)
	}
}

func TestJumpHunkMovesToNextHeader(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.bodyHeight = 4
	hunk := func(start int) diff.Hunk {
		return diff.Hunk{OldStart: start, NewStart: start, Lines: []diff.HunkLine{
			{Type: ' ', Content: "a"}, {Type: '+', Content: "b"},
		}}
	}
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks:   []diff.Hunk{hunk(1), hunk(10), hunk(20)},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	got = updated.(Model)
	if got.hunksCursor != 1 {
		t.Fatalf("expected } to move to hunk 1, got %d", got.hunksCursor)
	}
	if got.hunkScroll != 3 {
		t.Fatalf("expected } to put hunk 1's header on top (row 3), got %d", got.hunkScroll)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'{'}})
	got = updated.(Model)
	if got.hunksCursor != 0 || got.hunkScroll != 0 {
		t.Fatalf("expected { to return to hunk 0 at row 0, got cursor=%d scroll=%d", got.hunksCursor, got.hunkScroll)
	}
}

func TestJumpHunkStopsAtEnds(t *testing.T) {
	m := singleHunkModel(t, 20)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	got := updated.(Model)
	if got.hunksCursor != 0 || got.hunkScroll != 0 {
		t.Fatalf("expected } to be a no-op with one hunk, got cursor=%d scroll=%d", got.hunksCursor, got.hunkScroll)
	}
}

func TestRenderHunkLinesUsesMinimalLineNumberGutterWidth(t *testing.T) {
	h := diff.Hunk{
		OldStart: 1,
		NewStart: 1,
		Lines:    []diff.HunkLine{{Type: ' ', Content: "one"}, {Type: '+', Content: "two"}},
	}
	lines := renderHunkLines(h, 80, false)
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

// wrapModel is one file with a single long line, wider than the pane.
func wrapModel(t *testing.T) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 8)
	long := strings.Repeat("abcdefghij ", 80) // ~880 cols, wraps to many rows
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "foo.go",
		Status:  "M",
		Hunks: []diff.Hunk{{OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{
			{Type: ' ', Content: "short"},
			{Type: '+', Content: long},
		}}},
	}}})
	got := updated.(Model)
	got.focus = FocusHunks
	return got
}

func TestWrapOffTruncatesToOneRowPerLine(t *testing.T) {
	m := wrapModel(t)
	if m.wrap {
		t.Fatal("expected wrap off by default")
	}
	layout, ok := m.hunkLayout()
	if !ok {
		t.Fatal("expected a layout")
	}
	// 1 header + 2 lines.
	if got := len(layout.rows); got != 3 {
		t.Fatalf("expected 3 rows unwrapped, got %d", got)
	}
}

func TestWrapToggleExpandsRows(t *testing.T) {
	m := wrapModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got := updated.(Model)
	if !got.wrap {
		t.Fatal("expected w to enable wrap")
	}
	layout, ok := got.hunkLayout()
	if !ok {
		t.Fatal("expected a layout")
	}
	if len(layout.rows) <= 3 {
		t.Fatalf("expected the long line to wrap onto extra rows, got %d", len(layout.rows))
	}
	// Toggling back restores the compact geometry.
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	back := updated.(Model)
	layout, _ = back.hunkLayout()
	if len(layout.rows) != 3 {
		t.Fatalf("expected 3 rows after toggling wrap off, got %d", len(layout.rows))
	}
}

// With wrap on there is more content than pane, so scrolling must be able to
// reach the end — the bug this refactor prevents is a clamp computed from
// unwrapped row counts.
func TestWrapScrollReachesEnd(t *testing.T) {
	m := wrapModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got := updated.(Model)
	layout, _ := got.hunkLayout()
	want := max(0, len(layout.rows)-got.hunkContentHeight())
	if want == 0 {
		t.Fatal("fixture should produce more rows than the pane holds")
	}
	for i := 0; i < len(layout.rows)+5; i++ {
		updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		got = updated.(Model)
	}
	if got.hunkScroll != want {
		t.Fatalf("expected scroll to reach %d, stopped at %d", want, got.hunkScroll)
	}
}

// Continuation rows must not repeat the gutter — only the first row carries
// the line numbers and +/- marker.
func TestWrappedContinuationRowsAreIndented(t *testing.T) {
	h := diff.Hunk{OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{
		{Type: '+', Content: strings.Repeat("x", 200)},
	}}
	rows := renderHunkLines(h, 40, true)
	if len(rows) < 2 {
		t.Fatalf("expected the line to wrap, got %d rows", len(rows))
	}
	if plain := stripANSI(rows[0]); !strings.Contains(plain, "+") {
		t.Fatalf("expected the first row to carry the gutter, got %q", plain)
	}
	for i, r := range rows[1:] {
		plain := stripANSI(r)
		if strings.Contains(plain, "+") {
			t.Fatalf("continuation row %d repeated the gutter: %q", i+1, plain)
		}
		if !strings.HasPrefix(plain, " ") {
			t.Fatalf("continuation row %d not indented: %q", i+1, plain)
		}
	}
}
