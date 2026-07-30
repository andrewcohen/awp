package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/diff"
)

// hunkOf builds a hunk starting at the given line with the given line specs,
// where each spec is a type byte and the content is derived from it.
func hunkOf(start int, spec string, content string) diff.Hunk {
	lines := make([]diff.HunkLine, 0, len(spec))
	for _, c := range spec {
		lines = append(lines, diff.HunkLine{Type: byte(c), Content: content})
	}
	return diff.Hunk{OldStart: start, OldCount: len(spec), NewStart: start, NewCount: len(spec), Lines: lines}
}

// streamModel is a viewer over the given files, hunk pane focused.
func streamModel(t *testing.T, files ...diff.FileDiff) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 8)
	updated, _ := m.Update(diffLoadedMsg{files: files})
	got := updated.(Model)
	got.focus = FocusHunks
	return got
}

func twoFiles() []diff.FileDiff {
	return []diff.FileDiff{
		{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{hunkOf(1, "  ++", "aaa"), hunkOf(30, " -", "aaa")}},
		{NewPath: "b.go", Status: "A", Hunks: []diff.Hunk{hunkOf(1, "  ", "bbb")}},
	}
}

func press(m Model, s string) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return updated.(Model)
}

func pressTimes(m Model, s string, n int) Model {
	for i := 0; i < n; i++ {
		m = press(m, s)
	}
	return m
}

// ---- geometry ----

func TestBuildStreamRowLayout(t *testing.T) {
	idx := buildStream(twoFiles(), 80, false, nil)
	// file a: header + (hunk header + 4 lines) + (hunk header + 2 lines) = 9
	// spacer (1) + file b: header + hunk header + 2 lines = 4
	if got := len(idx.rows); got != 14 {
		t.Fatalf("expected 14 rows, got %d", got)
	}
	if idx.rows[0].kind != rowFileHeader {
		t.Fatalf("expected the stream to open with a file header, got %v", idx.rows[0].kind)
	}
	if len(idx.fileStart) != 2 {
		t.Fatalf("expected 2 file starts, got %d", len(idx.fileStart))
	}
	for i, start := range idx.fileStart {
		if idx.rows[start].kind != rowFileHeader || idx.rows[start].file != i {
			t.Fatalf("fileStart[%d]=%d does not point at file %d's header", i, start, i)
		}
	}
	// A spacer separates the files, and only the files.
	spacers := 0
	for _, r := range idx.rows {
		if r.kind == rowSpacer {
			spacers++
		}
	}
	if spacers != 1 {
		t.Fatalf("expected 1 spacer between 2 files, got %d", spacers)
	}
}

func TestBuildStreamIndexesEveryHunkAcrossFiles(t *testing.T) {
	idx := buildStream(twoFiles(), 80, false, nil)
	if len(idx.hunkStart) != 3 {
		t.Fatalf("expected 3 hunk headers across both files, got %d", len(idx.hunkStart))
	}
	for _, s := range idx.hunkStart {
		if idx.rows[s].kind != rowHunkHeader {
			t.Fatalf("hunkStart %d does not point at a hunk header", s)
		}
	}
}

// Line numbers are resolved during the build, so an added line carries no old
// number and a removed line no new one.
func TestBuildStreamAssignsLineNumbers(t *testing.T) {
	files := []diff.FileDiff{{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 10, NewStart: 20,
		Lines: []diff.HunkLine{
			{Type: ' ', Content: "ctx"},
			{Type: '-', Content: "gone"},
			{Type: '+', Content: "added"},
		},
	}}}}
	idx := buildStream(files, 80, false, nil)
	var lines []rowRef
	for _, r := range idx.rows {
		if r.kind == rowLine {
			lines = append(lines, r)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 line rows, got %d", len(lines))
	}
	if lines[0].oldNo != 10 || lines[0].newNo != 20 {
		t.Fatalf("context line: got old=%d new=%d", lines[0].oldNo, lines[0].newNo)
	}
	if lines[1].oldNo != 11 || lines[1].newNo != 0 {
		t.Fatalf("removed line should have no new number: old=%d new=%d", lines[1].oldNo, lines[1].newNo)
	}
	if lines[2].oldNo != 0 || lines[2].newNo != 21 {
		t.Fatalf("added line should have no old number: old=%d new=%d", lines[2].oldNo, lines[2].newNo)
	}
}

func TestWrappedSegmentsIsArithmetic(t *testing.T) {
	cases := []struct {
		content string
		avail   int
		want    int
	}{
		{"short", 10, 1},
		{strings.Repeat("x", 10), 10, 1},
		{strings.Repeat("x", 11), 10, 2},
		{strings.Repeat("x", 30), 10, 3},
		{strings.Repeat("x", 31), 10, 4},
		{strings.Repeat("x", 30), 0, 1}, // no room: one row, truncated
	}
	for _, c := range cases {
		if got := wrappedSegments(c.content, c.avail); got != c.want {
			t.Fatalf("wrappedSegments(%d chars, avail %d) = %d, want %d", len(c.content), c.avail, got, c.want)
		}
	}
}

func TestSegmentTextSlicesByCell(t *testing.T) {
	content := "0123456789abcdefghij"
	if got := segmentText(content, 0, 10); got != "0123456789" {
		t.Fatalf("segment 0: got %q", got)
	}
	if got := segmentText(content, 1, 10); got != "abcdefghij" {
		t.Fatalf("segment 1: got %q", got)
	}
}

func TestWrapAddsRowsForLongLines(t *testing.T) {
	long := strings.Repeat("x", 400)
	files := []diff.FileDiff{{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{{Type: '+', Content: long}},
	}}}}
	unwrapped := buildStream(files, 80, false, nil)
	wrapped := buildStream(files, 80, true, nil)
	if len(unwrapped.rows) != 3 { // header + hunk header + 1 line
		t.Fatalf("expected 3 unwrapped rows, got %d", len(unwrapped.rows))
	}
	if len(wrapped.rows) <= len(unwrapped.rows) {
		t.Fatalf("expected wrapping to add rows, got %d vs %d", len(wrapped.rows), len(unwrapped.rows))
	}
}

// ---- navigation ----

// The point of the whole change: scrolling runs from one file into the next
// with no boundary to stop at.
func TestScrollRunsContinuouslyIntoTheNextFile(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	if m.filesCursor != 0 {
		t.Fatalf("expected to start in the first file, got %d", m.filesCursor)
	}
	// Scroll until the top row belongs to the second file.
	crossed := -1
	for i := 1; i <= len(m.stream.rows); i++ {
		m = press(m, "j")
		if m.filesCursor == 1 {
			crossed = i
			break
		}
	}
	if crossed < 0 {
		t.Fatal("expected continuous scrolling to reach the second file")
	}
	if crossed != m.stream.fileStart[1] {
		t.Fatalf("expected to enter file 1 at row %d, did so after %d presses", m.stream.fileStart[1], crossed)
	}
}

// cursorVisible is the invariant the viewport must maintain: wherever the
// cursor goes, it is on screen.
func cursorVisible(m Model) bool {
	return m.cursorRow >= m.streamScroll && m.cursorRow < m.streamScroll+m.streamContentHeight()
}

func TestCursorClampsAtBothEnds(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = pressTimes(m, "k", 3)
	if m.cursorRow != 0 {
		t.Fatalf("expected the cursor to clamp at 0, got %d", m.cursorRow)
	}
	if m.streamScroll != 0 {
		t.Fatalf("expected scroll to sit at 0 with the cursor there, got %d", m.streamScroll)
	}
	m = pressTimes(m, "j", len(m.stream.rows)+10)
	if want := len(m.stream.rows) - 1; m.cursorRow != want {
		t.Fatalf("expected the cursor to clamp at the last row %d, got %d", want, m.cursorRow)
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible at scroll %d (height %d)", m.cursorRow, m.streamScroll, m.streamContentHeight())
	}
}

// The viewport follows the cursor rather than the reverse, so the cursor stays
// on screen through every kind of movement.
func TestViewportKeepsCursorVisible(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	for _, key := range []string{"j", "j", "j", "j", "j", "G", "g", "}", "}", "{"} {
		m = press(m, key)
		if !cursorVisible(m) {
			t.Fatalf("after %q: cursor %d not visible at scroll %d", key, m.cursorRow, m.streamScroll)
		}
	}
}

// The file list follows the cursor.
func TestFileCursorFollowsCursorRow(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.cursorRow = m.stream.fileStart[1]
	m.syncFileCursorToCursor()
	if m.filesCursor != 1 {
		t.Fatalf("expected the file cursor to follow the cursor row, got %d", m.filesCursor)
	}
}

// ...and moving the file list seeks the scroll.
func TestFileListSeeksTheStream(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.focus = FocusFiles
	m = press(m, "j")
	if m.filesCursor != 1 {
		t.Fatalf("expected the file cursor to advance, got %d", m.filesCursor)
	}
	if m.cursorRow != m.stream.fileStart[1] {
		t.Fatalf("expected to seek the cursor to row %d, got %d", m.stream.fileStart[1], m.cursorRow)
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible after seek at scroll %d", m.cursorRow, m.streamScroll)
	}
	m = press(m, "k")
	if m.filesCursor != 0 || m.cursorRow != m.stream.fileStart[0] {
		t.Fatalf("expected to seek back to the first file, got file=%d cursor=%d", m.filesCursor, m.cursorRow)
	}
}

func TestFileListSeekStopsAtEnds(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.focus = FocusFiles
	m = press(m, "k")
	if m.filesCursor != 0 {
		t.Fatalf("expected to stay on the first file, got %d", m.filesCursor)
	}
	m = pressTimes(m, "j", 5)
	if m.filesCursor != 1 {
		t.Fatalf("expected to stop on the last file, got %d", m.filesCursor)
	}
}

// Hunk hops are no longer confined to one file.
func TestJumpHunkCrossesFiles(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	starts := m.stream.hunkStart
	if len(starts) != 3 {
		t.Fatalf("expected 3 hunks, got %d", len(starts))
	}
	// From the top, } steps through every hunk in the change — the third one
	// lives in the second file.
	for i, want := range starts {
		m = press(m, "}")
		if m.cursorRow != want {
			t.Fatalf("press %d: expected cursor at row %d, got %d", i+1, want, m.cursorRow)
		}
	}
	if m.filesCursor != 1 {
		t.Fatalf("expected the file cursor to follow across the file, got %d", m.filesCursor)
	}
	m = press(m, "{")
	if m.cursorRow != starts[1] {
		t.Fatalf("expected { to go back to row %d, got %d", starts[1], m.cursorRow)
	}
}

func TestJumpHunkStopsAtEnds(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = press(m, "{")
	if m.cursorRow != 0 {
		t.Fatalf("expected { at the top to stay put, got %d", m.cursorRow)
	}
	m = pressTimes(m, "}", 10)
	last := m.cursorRow
	m = press(m, "}")
	if m.cursorRow != last {
		t.Fatalf("expected } past the last hunk to stay put, got %d", m.cursorRow)
	}
}

func TestGAndShiftGGoToStreamEnds(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = pressTimes(m, "j", 3)
	m = press(m, "g")
	if m.cursorRow != 0 || m.streamScroll != 0 {
		t.Fatalf("expected g to go to the top, got cursor=%d scroll=%d", m.cursorRow, m.streamScroll)
	}
	m = press(m, "G")
	if want := len(m.stream.rows) - 1; m.cursorRow != want {
		t.Fatalf("expected G to reach the last row %d, got %d", want, m.cursorRow)
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible after G at scroll %d", m.cursorRow, m.streamScroll)
	}
}

// ---- rendering ----

func TestStreamPanelRendersOnlyTheVisibleWindow(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	height := 5
	out := m.renderStreamPanel(60, height)
	// The border adds two rows around the content.
	if got := strings.Count(out, "\n") + 1; got != height+2 {
		t.Fatalf("expected %d rendered rows, got %d", height+2, got)
	}
}

func TestStreamFileHeaderShowsPath(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	row := stripANSI(m.renderStreamRow(m.stream.rows[m.stream.fileStart[1]], 60, false))
	if !strings.Contains(row, "b.go") {
		t.Fatalf("expected the file header to name the file, got %q", row)
	}
	if !strings.Contains(row, "1 hunk") {
		t.Fatalf("expected the file header to count hunks, got %q", row)
	}
}

// The divider has to be unmissable in a continuous scroll, so it spans the
// full pane width with the filename set into it.
func TestStreamFileHeaderDrawsARuleAcrossThePane(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	for _, width := range []int{40, 60, 100} {
		row := stripANSI(m.renderStreamRow(m.stream.rows[m.stream.fileStart[0]], width, false))
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("width %d: divider spans %d columns, want %d (%q)", width, got, width, row)
		}
		if !strings.HasPrefix(row, strings.Repeat(fileRuleGlyph, fileRuleLead)) {
			t.Fatalf("width %d: expected a rule lead-in, got %q", width, row)
		}
		if !strings.HasSuffix(row, fileRuleGlyph) {
			t.Fatalf("width %d: expected the rule to reach the right edge, got %q", width, row)
		}
		if !strings.Contains(row, "a.go") {
			t.Fatalf("width %d: filename lost in the rule: %q", width, row)
		}
	}
}

// A very long path must not push the rule past the pane edge.
func TestStreamFileHeaderTruncatesLongPaths(t *testing.T) {
	long := "internal/" + strings.Repeat("deeply/nested/", 8) + "file.go"
	m := streamModel(t, diff.FileDiff{NewPath: long, Status: "M", Hunks: []diff.Hunk{hunkOf(1, " ", "x")}})
	row := stripANSI(m.renderStreamRow(m.stream.rows[0], 50, false))
	if got := lipgloss.Width(row); got > 50 {
		t.Fatalf("divider overflowed: %d columns (%q)", got, row)
	}
}

// The file the cursor is in is styled differently, so scrolling tells you
// where you are without consulting the file list. Asserted on the style
// choice rather than rendered output — lipgloss strips colour under test.
func TestStreamFileHeaderMarksTheCurrentFile(t *testing.T) {
	rule, base := fileRuleStyles(false)
	curRule, curBase := fileRuleStyles(true)
	if rule.GetForeground() == curRule.GetForeground() {
		t.Fatal("expected the current file's rule to use a different hue")
	}
	if base.GetForeground() == curBase.GetForeground() {
		t.Fatal("expected the current file's name to use a different hue")
	}
	if !base.GetBold() || !curBase.GetBold() {
		t.Fatal("expected the filename to be bold in both states")
	}
}

// Only a line's first row carries the gutter; continuations are padded to the
// same width so the code column stays aligned.
func TestWrappedContinuationRowsOmitTheGutter(t *testing.T) {
	long := strings.Repeat("x", 300)
	m := streamModel(t, diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{{Type: '+', Content: long}},
	}}})
	m = press(m, "w")
	var segs []rowRef
	for _, r := range m.stream.rows {
		if r.kind == rowLine {
			segs = append(segs, r)
		}
	}
	if len(segs) < 2 {
		t.Fatalf("expected the line to wrap, got %d rows", len(segs))
	}
	first := stripANSI(m.renderStreamRow(segs[0], 60, false))
	cont := stripANSI(m.renderStreamRow(segs[1], 60, false))
	if !strings.Contains(first, "+") {
		t.Fatalf("expected the first row to carry the + marker, got %q", first)
	}
	if strings.Contains(cont, "+") {
		t.Fatalf("continuation row repeated the gutter: %q", cont)
	}
	if !strings.HasPrefix(cont, " ") {
		t.Fatalf("continuation row not padded: %q", cont)
	}
}

// ---- horizontal pan ----

func TestPanShiftsContentNotGutter(t *testing.T) {
	m := streamModel(t, diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{{Type: '+', Content: "0123456789abcdefghij"}},
	}}})
	var line rowRef
	for _, r := range m.stream.rows {
		if r.kind == rowLine {
			line = r
			break
		}
	}
	base := stripANSI(m.renderStreamRow(line, 60, false))
	m.hunkHScroll = 10
	panned := stripANSI(m.renderStreamRow(line, 60, false))
	if !strings.Contains(base, "0123456789") {
		t.Fatalf("unpanned row missing the line start: %q", base)
	}
	if strings.Contains(panned, "0123456789") {
		t.Fatalf("panned row should have dropped the first 10 columns: %q", panned)
	}
	if !strings.Contains(panned, "abcdefghij") {
		t.Fatalf("panned row missing the tail: %q", panned)
	}
	if base[:4] != panned[:4] {
		t.Fatalf("gutter moved: %q vs %q", base[:4], panned[:4])
	}
}

func longLineFile(name string) diff.FileDiff {
	return diff.FileDiff{NewPath: name, Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{{Type: '+', Content: strings.Repeat("abcdefghij", 40)}},
	}}}
}

func TestPanKeysMoveAndClamp(t *testing.T) {
	m := streamModel(t, longLineFile("a.go"))
	m = press(m, "l")
	if m.hunkHScroll != hScrollStep {
		t.Fatalf("expected l to pan by %d, got %d", hScrollStep, m.hunkHScroll)
	}
	m = press(m, "h")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected h to pan back, got %d", m.hunkHScroll)
	}
	m = press(m, "h")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected pan to clamp at 0, got %d", m.hunkHScroll)
	}
	m = press(m, "$")
	if want := 400 - minVisibleColumns; m.hunkHScroll != want {
		t.Fatalf("expected $ to pan to %d, got %d", want, m.hunkHScroll)
	}
	m = press(m, "0")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected 0 to return to the start, got %d", m.hunkHScroll)
	}
}

func TestPanNoOpsUnderWrap(t *testing.T) {
	m := streamModel(t, longLineFile("a.go"))
	m = press(m, "w")
	m = press(m, "l")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected l to no-op under wrap, got %d", m.hunkHScroll)
	}
}

func TestEnablingWrapResetsPan(t *testing.T) {
	m := streamModel(t, longLineFile("a.go"))
	m = press(m, "l")
	if m.hunkHScroll == 0 {
		t.Fatal("expected a pan to reset")
	}
	m = press(m, "w")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected enabling wrap to reset the pan, got %d", m.hunkHScroll)
	}
}

func TestSeekingToAFileResetsPan(t *testing.T) {
	m := streamModel(t, longLineFile("a.go"), longLineFile("b.go"))
	m = press(m, "l")
	if m.hunkHScroll == 0 {
		t.Fatal("expected a pan before seeking")
	}
	m.focus = FocusFiles
	m = press(m, "j")
	if m.hunkHScroll != 0 {
		t.Fatalf("expected seeking to reset the pan, got %d", m.hunkHScroll)
	}
}

// ---- editor jump ----

// ---- line cursor ----

func TestCursorMovesARowAtATime(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = press(m, "j")
	if m.cursorRow != 1 {
		t.Fatalf("expected j to move the cursor one row, got %d", m.cursorRow)
	}
	m = press(m, "k")
	if m.cursorRow != 0 {
		t.Fatalf("expected k to move back, got %d", m.cursorRow)
	}
}

// The cursor row carries the app-wide selection bar; every other row reserves
// the same columns so content stays aligned.
func TestCursorRowCarriesSelectionBar(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = pressTimes(m, "j", 2)
	cursor := stripANSI(m.renderStreamRowAt(m.cursorRow, 60))
	other := stripANSI(m.renderStreamRowAt(m.cursorRow+1, 60))
	if !strings.HasPrefix(cursor, selectionPrefixBar) {
		t.Fatalf("expected the cursor row to start with the bar, got %q", cursor)
	}
	if !strings.HasPrefix(other, selectionPrefixBlank) {
		t.Fatalf("expected other rows to reserve the bar width, got %q", other)
	}
}

// Reserving the prefix narrows content width; the wrap geometry must agree with
// it or wrapped rows won't line up with what's rendered.
func TestWrapWidthAccountsForTheCursorPrefix(t *testing.T) {
	m := streamModel(t, longLineFile("a.go"))
	_, right := paneWidths(120)
	if want := right - 4 - lipgloss.Width(selectionPrefixBlank); m.hunkWidth != want {
		t.Fatalf("expected hunkWidth %d to reserve the prefix, got %d", want, m.hunkWidth)
	}
}

// Paging moves the cursor, not just the viewport.
func TestPagingMovesTheCursor(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if m.cursorRow == 0 {
		t.Fatal("expected ctrl+d to move the cursor")
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible after paging at scroll %d", m.cursorRow, m.streamScroll)
	}
}

// A rebuild (resize, wrap toggle, reload) must not leave the cursor past the
// end of the new geometry.
func TestCursorSurvivesRebuild(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = press(m, "G")
	m.SetSize(60, 6)
	if m.cursorRow > len(m.stream.rows)-1 {
		t.Fatalf("cursor %d past the end of %d rows", m.cursorRow, len(m.stream.rows))
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible after resize at scroll %d", m.cursorRow, m.streamScroll)
	}
}

// The editor jump follows the cursor, not the top of the viewport.
func TestOpenAtCursorFollowsTheCursorRow(t *testing.T) {
	var gotLine int
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(_ string, line int) tea.Cmd {
		gotLine = line
		return nil
	})
	m.SetSize(120, 20) // tall enough that scroll stays 0 while the cursor moves
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{
		NewPath: "a.go", Status: "M",
		Hunks: []diff.Hunk{{OldStart: 40, NewStart: 40, Lines: []diff.HunkLine{
			{Type: ' ', Content: "ctx"},
			{Type: '+', Content: "added"},
		}}},
	}}})
	m = updated.(Model)
	m.focus = FocusHunks
	// Rows: divider, hunk header, ctx (40), added (41).
	m = pressTimes(m, "j", 3)
	if m.streamScroll != 0 {
		t.Fatalf("fixture should not have scrolled, got %d", m.streamScroll)
	}
	m = press(m, "e")
	if gotLine != 41 {
		t.Fatalf("expected to open at the cursor's line 41, got %d", gotLine)
	}
}

// The cursorline spans the full pane width, vim-style, rather than only
// highlighting the text on the row.
func TestCursorlineSpansTheFullWidth(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = pressTimes(m, "j", 2) // land on a diff line, not the divider
	if m.stream.rows[m.cursorRow].kind != rowLine {
		t.Fatalf("fixture should put the cursor on a line row, got %v", m.stream.rows[m.cursorRow].kind)
	}
	const width = 60
	row := m.renderStreamRowAt(m.cursorRow, width)
	if got := lipgloss.Width(row); got != width {
		t.Fatalf("cursorline spans %d columns, want %d", got, width)
	}
	// A non-cursor row is not padded out.
	other := m.renderStreamRowAt(m.cursorRow+1, width)
	if lipgloss.Width(other) == width {
		t.Fatalf("non-cursor rows should not be padded to full width: %q", stripANSI(other))
	}
}

// Moving the cursor moves the cursorline with it.
func TestCursorlineFollowsTheCursor(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m = pressTimes(m, "j", 2)
	first := m.cursorRow
	wide := m.renderStreamRowAt(first, 60)
	m = press(m, "j")
	if m.cursorRow == first {
		t.Fatal("expected the cursor to move")
	}
	if narrow := m.renderStreamRowAt(first, 60); narrow == wide {
		t.Fatal("expected the previous row to stop being the cursorline")
	}
}
