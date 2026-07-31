package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

func commentModel(t *testing.T, files ...diff.FileDiff) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 14)
	m.focus = FocusHunks
	m.SaveComment = func(review.Comment) error { return nil }
	return loadWith(m, 1, files...)
}

func commentOn(path string, line int, text, body string) review.Comment {
	return review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: body, State: review.Open,
		Anchor: review.Anchor{Path: path, Side: review.SideNew, LineHint: line, Text: text},
	}
}

// commentBodyText is a comment's body rows with the gutter stripped — everything
// after the header. What the wrapping tests actually care about.
//
// Rendered as a non-closing message so the block's trailing pad row does not show
// up as a blank body line.
func commentBodyText(c review.Comment, width int) []string {
	var out []string
	past := false
	for _, r := range commentRows(c, width, false) {
		if r.header {
			past = true
			continue
		}
		if !past {
			continue // the leading pad row
		}
		_, text := splitGutter(r.text)
		out = append(out, text)
	}
	return out
}

// commentRowStrings is every row's raw text, for width assertions.
func commentRowStrings(c review.Comment, width int) []string {
	rows := commentRows(c, width, true)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.text)
	}
	return out
}

// rowsOfKind counts stream rows of a kind, for asserting placement.
func rowsOfKind(m Model, k rowKind) int {
	n := 0
	for _, r := range m.stream.rows {
		if r.kind == k {
			n++
		}
	}
	return n
}

func TestCommentRendersUnderItsAnchoredLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.SetComments([]review.Comment{commentOn("a.go", 2, "beta", "needs a guard")})

	if got := rowsOfKind(m, rowComment); got == 0 {
		t.Fatal("expected comment rows in the stream")
	}
	// The comment's rows must directly follow the line it anchors to.
	found := false
	for i, r := range m.stream.rows {
		if r.kind != rowLine || m.lineText(r) != "beta" {
			continue
		}
		if i+1 < len(m.stream.rows) && m.stream.rows[i+1].kind == rowComment {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the comment to sit immediately below its line")
	}
	if got := rowsOfKind(m, rowOrphanHeader); got != 0 {
		t.Fatal("a placed comment should not appear in the detached section")
	}
}

// A comment whose line moved must follow the content, not the old line number.
func TestCommentFollowsMovedLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.SetComments([]review.Comment{commentOn("a.go", 2, "beta", "note")})

	m = loadWith(m, 2, fileWith("a.go", 1, "inserted", "alpha", "beta", "gamma"))
	for i, r := range m.stream.rows {
		if r.kind == rowLine && m.lineText(r) == "beta" {
			if i+1 >= len(m.stream.rows) || m.stream.rows[i+1].kind != rowComment {
				t.Fatal("expected the comment to move with its line")
			}
			return
		}
	}
	t.Fatal("beta not found after reload")
}

// A comment that cannot be located is shown in a detached section rather than
// silently dropped.
func TestUnplaceableCommentBecomesDetached(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{commentOn("vanished.go", 9, "gone", "stale note")})

	if got := rowsOfKind(m, rowOrphanHeader); got != 1 {
		t.Fatalf("expected a detached section header, got %d", got)
	}
	if got := rowsOfKind(m, rowOrphan); got == 0 {
		t.Fatal("expected the comment to still be shown")
	}
	view := stripANSI(m.renderStreamPanel(80, 12))
	if !strings.Contains(view, "detached") {
		t.Fatalf("expected the detached section to be visible, got:\n%s", view)
	}
}

// Duplicate lines must not capture a comment by text alone — context breaks the
// tie, and an unbreakable tie falls back to the nearest line.
func TestCommentUsesContextToDisambiguateDuplicates(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "}", "middle", "}", "tail", "}"))
	c := commentOn("a.go", 3, "}", "the second brace")
	c.Anchor.ContextBefore = []string{"middle"}
	c.Anchor.ContextAfter = []string{"tail"}
	m.SetComments([]review.Comment{c})

	// Shift the file so the line hint is stale; context should still win.
	m = loadWith(m, 2, fileWith("a.go", 1, "added", "}", "middle", "}", "tail", "}"))
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		// Walk back to the line this comment is under.
		j := i - 1
		for j >= 0 && m.stream.rows[j].kind == rowComment {
			j--
		}
		if j < 0 {
			t.Fatal("comment row with no line above it")
		}
		if got := m.stream.rows[j].newNo; got != 4 {
			t.Fatalf("expected the comment on the brace at line 4 (context match), got line %d", got)
		}
		return
	}
	t.Fatal("no comment row found")
}

func TestAnchorAtCursorDescribesTheLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 10, "alpha", "beta", "gamma"))
	for cursorText(m) != "beta" {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached beta")
		}
	}
	a, ok := m.AnchorAtCursor()
	if !ok {
		t.Fatal("expected an anchor on a line row")
	}
	if a.Path != "a.go" || a.Text != "beta" || a.LineHint != 11 {
		t.Fatalf("unexpected anchor: %+v", a)
	}
	if a.Side != review.SideNew {
		t.Fatalf("expected a context line to anchor to the new side, got %q", a.Side)
	}
	if len(a.ContextBefore) == 0 || a.ContextBefore[len(a.ContextBefore)-1] != "alpha" {
		t.Fatalf("expected preceding context, got %+v", a.ContextBefore)
	}
	if len(a.ContextAfter) == 0 || a.ContextAfter[0] != "gamma" {
		t.Fatalf("expected following context, got %+v", a.ContextAfter)
	}
}

// A removed line exists only on the old side, so that is where it anchors.
func TestAnchorOnRemovedLineUsesOldSide(t *testing.T) {
	m := commentModel(t, diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 5, NewStart: 5,
		Lines: []diff.HunkLine{{Type: '-', Content: "deleted"}},
	}}})
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	a, ok := m.AnchorAtCursor()
	if !ok {
		t.Fatal("expected an anchor")
	}
	if a.Side != review.SideOld || a.LineHint != 5 {
		t.Fatalf("expected old side line 5, got %+v", a)
	}
}

func TestAnchorAtCursorRefusesNonLineRows(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.cursorRow = 0 // the file divider
	if _, ok := m.AnchorAtCursor(); ok {
		t.Fatal("expected no anchor on the file divider")
	}
}

// The editor round trip: `c` opens, typing fills, enter saves through the sink.
func TestCommentGestureSavesThroughTheSink(t *testing.T) {
	var saved []review.Comment
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SaveComment = func(c review.Comment) error {
		saved = append(saved, c)
		return nil
	}
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	if !m.editing {
		t.Fatal("expected c to open the compose box")
	}
	for _, r := range "looks wrong" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editing {
		t.Fatal("expected enter to close the compose box")
	}
	if len(saved) != 1 || saved[0].Body != "looks wrong" {
		t.Fatalf("expected the comment to be saved, got %+v", saved)
	}
	if saved[0].Anchor.Path != "a.go" {
		t.Fatalf("expected the anchor to carry the path, got %+v", saved[0].Anchor)
	}
	if rowsOfKind(m, rowComment) == 0 {
		t.Fatal("expected the new comment to appear in the stream")
	}
}

func TestCommentGestureCancels(t *testing.T) {
	saved := 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.editing || saved != 0 {
		t.Fatalf("expected esc to discard, editing=%v saved=%d", m.editing, saved)
	}
}

// An empty body is a cancel, not an empty comment.
func TestEmptyCommentIsDiscarded(t *testing.T) {
	saved := 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(Model); got.editing || saved != 0 {
		t.Fatalf("expected an empty comment to be discarded, editing=%v saved=%d", got.editing, saved)
	}
}

// A failing store must surface the error rather than pretending it saved.
func TestSaveFailureIsReported(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { return errors.New("disk full") }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.statusErr || !strings.Contains(got.status, "disk full") {
		t.Fatalf("expected the save error in status, got %q err=%v", got.status, got.statusErr)
	}
	if rowsOfKind(got, rowComment) != 0 {
		t.Fatal("a comment that failed to save must not appear as saved")
	}
}

// While composing, keys belong to the editor — the host must not treat them as
// its own bindings.
func TestEditingClaimsTheKeyboard(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	if !m.Filtering() {
		t.Fatal("expected Filtering() to report that an input owns the keyboard")
	}
	before := m.cursorRow
	m = press(m, "j")
	if m.cursorRow != before {
		t.Fatal("expected j to type into the comment, not move the cursor")
	}
}

// Comment rows shift the offsets the file and hunk indexes point at, so those
// have to be remapped or seeking lands in the wrong place.
func TestCommentRowsRemapFileAndHunkOffsets(t *testing.T) {
	m := commentModel(t,
		fileWith("a.go", 1, "alpha", "beta"),
		fileWith("b.go", 1, "one", "two"),
	)
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "note")})

	for i, start := range m.stream.fileStart {
		if got := m.stream.rows[start].kind; got != rowFileHeader {
			t.Fatalf("fileStart[%d] points at %v, not a file header", i, got)
		}
	}
	for i, start := range m.stream.hunkStart {
		if got := m.stream.rows[start].kind; got != rowHunkHeader {
			t.Fatalf("hunkStart[%d] points at %v, not a hunk header", i, got)
		}
	}
}

// ---- reviewed / collapse ----

func TestReviewedFileCollapsesToItsDivider(t *testing.T) {
	m := commentModel(t,
		fileWith("a.go", 1, "alpha", "beta"),
		fileWith("b.go", 1, "one", "two"),
	)
	before := len(m.stream.rows)
	// Cursor into the first file, then mark it reviewed.
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "r")

	if len(m.stream.rows) >= before {
		t.Fatalf("expected collapsing to shrink the stream, %d → %d", before, len(m.stream.rows))
	}
	// a.go's divider survives; its body does not.
	sawDivider, sawLine := false, false
	for _, r := range m.stream.rows {
		if r.file < 0 || r.file >= len(m.filtered) || pathOf(m.filtered[r.file]) != "a.go" {
			continue
		}
		switch r.kind {
		case rowFileHeader:
			sawDivider = true
			if !r.collapsed {
				t.Fatal("expected the divider to be marked collapsed")
			}
		case rowLine:
			sawLine = true
		}
	}
	if !sawDivider || sawLine {
		t.Fatalf("expected divider only, got divider=%v line=%v", sawDivider, sawLine)
	}
}

// The collapsed divider must still report what it is hiding.
func TestCollapsedDividerSummarisesWhatIsHidden(t *testing.T) {
	m := commentModel(t, diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 1, NewStart: 1,
		Lines: []diff.HunkLine{{Type: '+', Content: "added"}, {Type: '-', Content: "gone"}, {Type: ' ', Content: "ctx"}},
	}}})
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "r")
	row := stripANSI(m.renderStreamRowAt(m.stream.fileStart[0], 90))
	for _, want := range []string{"reviewed", "1 hunk", "2 line"} {
		if !strings.Contains(row, want) {
			t.Fatalf("collapsed divider missing %q: %q", want, row)
		}
	}
}

func TestReviewedTogglesOff(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	full := len(m.stream.rows)
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "r")
	collapsed := len(m.stream.rows)
	m = press(m, "r")
	if len(m.stream.rows) != full {
		t.Fatalf("expected toggling back to restore the body: %d → %d → %d", full, collapsed, len(m.stream.rows))
	}
}

// The mark is keyed to content, so an edit after reviewing brings the file back.
// This is the failure that matters most: a change hidden behind a stale reviewed
// flag is a change nobody looked at.
func TestEditAfterReviewingResurfacesTheFile(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "r")
	if !m.isCollapsed("a.go") {
		t.Fatal("expected the file to be collapsed after review")
	}

	// The agent edits the file.
	m = loadWith(m, 2, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	if m.isCollapsed("a.go") {
		t.Fatal("expected an edit after reviewing to resurface the file")
	}
	sawLine := false
	for _, r := range m.stream.rows {
		if r.kind == rowLine {
			sawLine = true
		}
	}
	if !sawLine {
		t.Fatal("expected the file's body to be visible again")
	}
}

func TestMarkReviewedPersistsThroughTheSink(t *testing.T) {
	saved := map[string]string{}
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.MarkReviewed = func(path, hash string) error {
		if hash == "" {
			delete(saved, path)
		} else {
			saved[path] = hash
		}
		return nil
	}
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "r")
	if saved["a.go"] == "" {
		t.Fatalf("expected the mark to be persisted, got %+v", saved)
	}
	m = press(m, "r")
	if _, ok := saved["a.go"]; ok {
		t.Fatalf("expected un-reviewing to clear the mark, got %+v", saved)
	}
}

// A cursor inside a file that collapses must end up somewhere valid.
func TestCursorSurvivesCollapsing(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "a", "b", "c", "d", "e", "f"))
	m = press(m, "G")
	m = press(m, "r")
	if m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		t.Fatalf("cursor %d out of range for %d rows", m.cursorRow, len(m.stream.rows))
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible at scroll %d", m.cursorRow, m.streamScroll)
	}
}

// ---- remote threads ----

func remoteThread(id, path string, line int, resolved bool, body string) review.Thread {
	return review.Thread{
		ID: id, Path: path, Side: review.SideNew, Line: line, Resolved: resolved,
		Comments: []review.ThreadComment{{Author: "alice", Body: body}},
	}
}

func TestRemoteThreadsRenderInline(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 2, false, "this leaks")})

	view := stripANSI(m.renderStreamPanel(90, 12))
	if !strings.Contains(view, "this leaks") {
		t.Fatalf("expected the thread body inline, got:\n%s", view)
	}
	// It must be identifiable as already on GitHub, not as a local draft.
	if !strings.Contains(view, "github") {
		t.Fatalf("expected the thread labelled as remote, got:\n%s", view)
	}
	// Inline means placed under its line, not swept into the detached section —
	// GitHub gives a line number but no line text, so this is exactly the case
	// that used to orphan every thread.
	if rowsOfKind(m, rowOrphanHeader) != 0 {
		t.Fatal("expected the thread placed inline, not detached")
	}
	placedUnderLine := false
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		if i > 0 && m.stream.rows[i-1].kind == rowLine {
			placedUnderLine = true
		}
	}
	if !placedUnderLine {
		t.Fatal("expected the thread to sit directly below a diff line")
	}
}

// Resolved threads are settled conversation; showing them by default buries the
// ones still needing attention.
func TestResolvedThreadsHiddenUntilToggled(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetThreads([]review.Thread{
		remoteThread("T1", "a.go", 1, false, "open point"),
		remoteThread("T2", "a.go", 2, true, "settled point"),
	})
	view := stripANSI(m.renderStreamPanel(90, 14))
	if !strings.Contains(view, "open point") {
		t.Fatalf("expected the unresolved thread shown:\n%s", view)
	}
	if strings.Contains(view, "settled point") {
		t.Fatalf("expected the resolved thread hidden by default:\n%s", view)
	}

	m = press(m, "T") // → all
	view = stripANSI(m.renderStreamPanel(90, 14))
	if !strings.Contains(view, "settled point") {
		t.Fatalf("expected T to reveal resolved threads:\n%s", view)
	}

	m = press(m, "T") // → none
	view = stripANSI(m.renderStreamPanel(90, 14))
	if strings.Contains(view, "open point") || strings.Contains(view, "settled point") {
		t.Fatalf("expected T again to hide all threads:\n%s", view)
	}

	m = press(m, "T") // → unresolved again
	if m.threadVisibility != ThreadsUnresolved {
		t.Fatalf("expected the toggle to cycle back, got %v", m.threadVisibility)
	}
}

// Threads use the same relocation ladder as local comments, because their line
// numbers are GitHub's against a particular commit and drift the same way.
func TestRemoteThreadsRelocateWithContent(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	th := remoteThread("T1", "a.go", 2, false, "about beta")
	// Anchor text is what actually locates it; give it the line's content.
	m.SetThreads([]review.Thread{th})
	m.threads[0].Comments[0].Body = "about beta"
	m.rebuildStream()

	// A thread whose path is unknown must not vanish silently.
	m.SetThreads([]review.Thread{remoteThread("T9", "gone.go", 1, false, "orphan thread")})
	view := stripANSI(m.renderStreamPanel(90, 14))
	if !strings.Contains(view, "detached") || !strings.Contains(view, "orphan thread") {
		t.Fatalf("expected an unplaceable thread in the detached section:\n%s", view)
	}
}

// Local comments and remote threads keep separate vocabularies, so the UI cannot
// claim a draft was "resolved" or a thread "addressed".
func TestThreadStateIsPublishedNotOpen(t *testing.T) {
	c := threadAsComment(remoteThread("T1", "a.go", 3, false, "hi"))
	if c.State != review.Published {
		t.Fatalf("expected a remote thread to present as published, got %q", c.State)
	}
	if review.OpenCount([]review.Comment{c}) != 0 {
		t.Fatal("a remote thread must not count as a local finding awaiting triage")
	}
}

// Opening a diff should land on code, not on the file divider — otherwise the
// first thing `c` does is tell you to move.
func TestOpensWithCursorOnFirstLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	if got := m.stream.rows[m.cursorRow].kind; got != rowLine {
		t.Fatalf("expected the cursor on a diff line at open, got %v", got)
	}
	if _, ok := m.AnchorAtCursor(); !ok {
		t.Fatal("expected commenting to be possible immediately after opening")
	}
}

// A diff with no line content at all (rename-only) must not leave the cursor
// somewhere invalid.
func TestOpensSafelyWithNoLines(t *testing.T) {
	m := commentModel(t, diff.FileDiff{OldPath: "a.go", NewPath: "b.go", Status: "R"})
	if m.cursorRow < 0 || (len(m.stream.rows) > 0 && m.cursorRow >= len(m.stream.rows)) {
		t.Fatalf("cursor %d out of range for %d rows", m.cursorRow, len(m.stream.rows))
	}
}

// ---- edit / delete ----

// `c` on your own comment revises it rather than starting a second one beside it.
func TestIOnACommentEditsItInPlace(t *testing.T) {
	var updated []review.Comment
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.UpdateComment = func(c review.Comment) error {
		updated = append(updated, c)
		return nil
	}
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "original")})

	// Land on the comment row.
	for m.stream.rows[m.cursorRow].kind != rowComment {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached the comment row")
		}
	}
	m = press(m, "i")
	if !m.editing {
		t.Fatal("expected i on a comment to open it for editing")
	}
	if got := m.editor.area.Value(); got != "original" {
		t.Fatalf("expected the editor pre-filled, got %q", got)
	}
	if m.editor.editing != "c1" {
		t.Fatalf("expected the editor to carry the comment id, got %q", m.editor.editing)
	}

	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	updatedModel, _ = updatedModel.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updatedModel.(Model)
	if len(updated) != 1 || updated[0].ID != "c1" {
		t.Fatalf("expected an update carrying the id, got %+v", updated)
	}
	if len(m.comments) != 1 {
		t.Fatalf("expected editing not to add a comment, got %d", len(m.comments))
	}
	if m.comments[0].Body != "original!" {
		t.Fatalf("expected the body revised, got %q", m.comments[0].Body)
	}
}

func TestDDeletesTheCommentAtTheCursor(t *testing.T) {
	var deleted []string
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.DeleteComment = func(id string) error {
		deleted = append(deleted, id)
		return nil
	}
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "remove me")})
	for m.stream.rows[m.cursorRow].kind != rowComment {
		m = press(m, "j")
	}
	m = press(m, "D")
	if len(deleted) != 1 || deleted[0] != "c1" {
		t.Fatalf("expected the comment deleted, got %+v", deleted)
	}
	if len(m.comments) != 0 {
		t.Fatalf("expected the comment gone from the view, got %d", len(m.comments))
	}
	if rowsOfKind(m, rowComment) != 0 {
		t.Fatal("expected no comment rows after delete")
	}
	// Removing rows must not leave the cursor dangling.
	if m.cursorRow >= len(m.stream.rows) || !cursorVisible(m) {
		t.Fatalf("cursor %d invalid for %d rows", m.cursorRow, len(m.stream.rows))
	}
}

func TestDOnACodeLineDoesNothing(t *testing.T) {
	called := 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.DeleteComment = func(string) error { called++; return nil }
	m = press(m, "D")
	if called != 0 {
		t.Fatal("expected D on a code line to be a no-op")
	}
}

// Remote threads are GitHub's records: editing or deleting them from here would
// misrepresent what happened.
func TestRemoteThreadsCannotBeEditedOrDeleted(t *testing.T) {
	deleted, updated := 0, 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.DeleteComment = func(string) error { deleted++; return nil }
	m.UpdateComment = func(review.Comment) error { updated++; return nil }
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 1, false, "from github")})
	for m.stream.rows[m.cursorRow].kind != rowComment {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached the thread row")
		}
	}
	m = press(m, "D")
	m = press(m, "c")
	if deleted != 0 || updated != 0 {
		t.Fatalf("expected a remote thread to refuse edit/delete, got delete=%d update=%d", deleted, updated)
	}
	if m.editing {
		t.Fatal("expected no editor for a remote thread")
	}
}

func TestDeleteFailureIsReported(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.DeleteComment = func(string) error { return errors.New("read-only fs") }
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "x")})
	for m.stream.rows[m.cursorRow].kind != rowComment {
		m = press(m, "j")
	}
	m = press(m, "D")
	if !m.statusErr || !strings.Contains(m.status, "read-only fs") {
		t.Fatalf("expected the delete error reported, got %q", m.status)
	}
	if len(m.comments) != 1 {
		t.Fatal("a failed delete must not remove the comment from the view")
	}
}

// Comments read as blocks set into the diff, so every row of one spans the full
// pane width rather than trailing off after the text.
func TestCommentRowsSpanTheFullWidth(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "short\nand a rather longer second line")})

	const width = 70
	found := 0
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		found++
		row := m.renderStreamRowAt(i, width)
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("comment row %d spans %d columns, want %d (%q)", i, got, width, stripANSI(row))
		}
	}
	if found < 2 {
		t.Fatalf("expected a header row plus body rows, got %d", found)
	}
}

// On the cursor's row the cursorline wins over the comment fill: where the cursor
// is matters more than what kind of row it is, and the ▌ marker still says the latter.
func TestCursorlineWinsOnACommentRow(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "note")})
	target := -1
	for i, r := range m.stream.rows {
		if r.kind == rowComment {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("no comment row")
	}
	off := m.renderStreamRowAt(target, 60)
	m.cursorRow = target
	on := m.renderStreamRowAt(target, 60)
	if off == on {
		t.Fatal("expected the cursor row to render differently from a plain comment row")
	}
	if !strings.HasPrefix(stripANSI(on), selectionPrefixBar) {
		t.Fatalf("expected the cursor bar on a commented cursor row, got %q", stripANSI(on))
	}
}

// ---- reply threads in the stream ----

// A reply renders under its parent, not wherever its own anchor would resolve,
// so an exchange reads as one block.
func TestReplyRendersUnderItsParent(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	parent := commentOn("a.go", 1, "alpha", "this drops the error")
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "agreed, wrapping it", State: review.Open,
		ReplyTo: parent.ID,
		// Deliberately a different anchor: the reply must follow the parent.
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 2, Text: "beta"},
	}
	m.SetComments([]review.Comment{parent, reply})

	rows := m.stream.rows
	// Find the parent's row, then expect its reply immediately after.
	seen := []string{}
	for _, r := range rows {
		if r.kind != rowComment {
			continue
		}
		seen = append(seen, m.stream.comments[r.comment].ID)
	}
	if len(seen) == 0 {
		t.Fatal("expected comment rows")
	}
	first, second := "", ""
	for _, id := range seen {
		if first == "" {
			first = id
			continue
		}
		if id != first && second == "" {
			second = id
		}
	}
	if first != parent.ID || second != reply.ID {
		t.Fatalf("expected parent then reply, got %v", seen)
	}
}

// An orphaned reply must still be shown rather than dropped.
func TestOrphanedReplyIsShown(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{{
		ID: "r1", Author: "agent", Body: "dangling", State: review.Open, ReplyTo: "gone",
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 1, Text: "alpha"},
	}})
	view := stripANSI(m.renderStreamPanel(80, 12))
	if !strings.Contains(view, "dangling") {
		t.Fatalf("expected an orphaned reply to still be shown:\n%s", view)
	}
}

// `c` on a comment replies to it — answering a remark is the common action, and
// revising your own wording is `i`.
func TestCOnACommentRepliesToIt(t *testing.T) {
	var replies []struct {
		parent string
		body   string
	}
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.ReplyComment = func(parentID string, c review.Comment) error {
		replies = append(replies, struct {
			parent string
			body   string
		}{parentID, c.Body})
		return nil
	}
	parent := commentOn("a.go", 1, "alpha", "this drops the error")
	parent.State = review.Sent
	m.SetComments([]review.Comment{parent})

	for m.stream.rows[m.cursorRow].kind != rowComment {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached the comment row")
		}
	}
	m = press(m, "c")
	if !m.editing {
		t.Fatal("expected c on a comment to open a reply box")
	}
	if m.editor.replyTo != parent.ID {
		t.Fatalf("expected the reply aimed at %q, got %q", parent.ID, m.editor.replyTo)
	}
	if got := m.editor.area.Value(); got != "" {
		t.Fatalf("expected an empty reply box, got %q", got)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: false})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if len(replies) != 1 || replies[0].parent != parent.ID || replies[0].body != "ok" {
		t.Fatalf("expected one reply to the parent, got %+v", replies)
	}
	// The view must agree with the store: a reply reopens its parent.
	for _, c := range m.comments {
		if c.ID == parent.ID && c.State != review.Open {
			t.Fatalf("expected the parent reopened, got %q", c.State)
		}
	}
}

// Replying to a reply threads under the conversation's top, not under the reply —
// one exchange, one thread.
func TestReplyingToAReplyThreadsUnderTheParent(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.ReplyComment = func(string, review.Comment) error { return nil }
	parent := commentOn("a.go", 1, "alpha", "top")
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "mid", State: review.Open, ReplyTo: parent.ID,
		Anchor: parent.Anchor,
	}
	m.SetComments([]review.Comment{parent, reply})

	// Land on the reply row (the second comment row).
	seen := 0
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		if m.stream.comments[r.comment].ID == reply.ID {
			m.cursorRow = i
			seen++
			break
		}
	}
	if seen == 0 {
		t.Fatal("never found the reply row")
	}
	m, _ = pressKeyUI(m, "c")
	if m.editor.replyTo != parent.ID {
		t.Fatalf("expected the reply aimed at the thread's top %q, got %q", parent.ID, m.editor.replyTo)
	}
}

func pressKeyUI(m Model, s string) (Model, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return updated.(Model), cmd
}

// A whole conversation shares one left bar at one indent.
//
// Replies used to step a space further right per level, which stair-stepped a
// long exchange across the pane and left a ragged left edge. The author label on
// each message already says where one ends and the next begins, so the nesting
// does not need to be spatial.
func TestThreadSharesOneBarWithoutIndenting(t *testing.T) {
	parent := commentRowStrings(commentOn("a.go", 1, "alpha", "top"), 60)
	reply := commentRowStrings(review.Comment{
		ID: "r1", Author: "agent", Body: "under", ReplyTo: "c1", State: review.Open,
	}, 60)

	indentOf := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
	// Every row of both messages sits at the same indent — the shared bar's.
	want := indentOf(parent[0])
	for _, rows := range [][]string{parent, reply} {
		for i, r := range rows {
			if got := indentOf(r); got != want {
				t.Fatalf("row %d sits at indent %d, expected the shared %d: %q", i, got, want, r)
			}
		}
	}
	if !strings.Contains(reply[1], "▌") {
		t.Fatalf("expected the same bar as the parent, got %q", reply[1])
	}
	if strings.Contains(reply[1], "↳") {
		t.Fatalf("expected no return marker, got %q", reply[1])
	}
}

// Every message opens with a bar-only row: top padding for the first, and the
// separator between messages after that. That blank row is what replaces the
// indent as the signal that a new message has started.
func TestEveryMessageOpensWithABarOnlyRow(t *testing.T) {
	for _, c := range []review.Comment{
		{ID: "c1", Author: review.AuthorHuman, Body: "top", State: review.Open},
		{ID: "r1", Author: "agent", Body: "under", ReplyTo: "c1", State: review.Open},
	} {
		rows := commentRows(c, 60, true)
		if len(rows) < 2 {
			t.Fatalf("%s: expected at least a pad row and a header, got %d", c.ID, len(rows))
		}
		if rows[0].header {
			t.Fatalf("%s: expected the first row to be padding, not the header", c.ID)
		}
		if _, text := splitGutter(rows[0].text); strings.TrimSpace(text) != "" {
			t.Fatalf("%s: expected a bar-only pad row, got %q", c.ID, rows[0].text)
		}
		if !rows[1].header {
			t.Fatalf("%s: expected the header directly after the pad row", c.ID)
		}
	}
}

// ---- live comment reload ----

// A reply filed while the view is open must appear without reopening it —
// otherwise watching an agent answer does not work, which is the point.
func TestCommentsReloadOnTheRefreshTick(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	parent := commentOn("a.go", 1, "alpha", "this drops the error")
	m.SetComments([]review.Comment{parent})

	stored := []review.Comment{parent, {
		ID: "r1", Author: "agent", Body: "agreed", State: review.Open,
		ReplyTo: parent.ID, Anchor: parent.Anchor,
	}}
	m.LoadComments = func() ([]review.Comment, error) { return stored, nil }

	if rowsOfKind(m, rowComment) != 4 { // pad + header + one body line + closing pad
		t.Fatalf("expected only the parent before the tick, got %d rows", rowsOfKind(m, rowComment))
	}
	updated, _ := m.Update(autoRefreshTickMsg{})
	m = updated.(Model)
	if len(m.comments) != 2 {
		t.Fatalf("expected the reply picked up on the tick, got %d", len(m.comments))
	}
	view := stripANSI(m.renderStreamPanel(80, 14))
	if !strings.Contains(view, "agreed") {
		t.Fatalf("expected the reply visible after the tick:\n%s", view)
	}
}

// The tick fires constantly, so an unchanged set must cost nothing and must not
// disturb the cursor.
func TestUnchangedCommentReloadLeavesTheViewAlone(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "a", "b", "c", "d", "e"))
	set := []review.Comment{commentOn("a.go", 1, "a", "note")}
	m.SetComments(set)
	m.LoadComments = func() ([]review.Comment, error) { return set, nil }
	m = pressTimes(m, "j", 3)
	cursor, scroll := m.cursorRow, m.streamScroll

	updated, _ := m.Update(autoRefreshTickMsg{})
	m = updated.(Model)
	if m.cursorRow != cursor || m.streamScroll != scroll {
		t.Fatalf("unchanged reload moved the view: cursor %d→%d scroll %d→%d",
			cursor, m.cursorRow, scroll, m.streamScroll)
	}
}

// A store read failure must not interrupt a review; the next tick retries.
func TestCommentReloadFailureIsSilent(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	set := []review.Comment{commentOn("a.go", 1, "alpha", "note")}
	m.SetComments(set)
	m.LoadComments = func() ([]review.Comment, error) { return nil, errors.New("gone") }
	updated, _ := m.Update(autoRefreshTickMsg{})
	m = updated.(Model)
	if len(m.comments) != 1 {
		t.Fatalf("expected the existing comments kept on a read failure, got %d", len(m.comments))
	}
	if m.statusErr {
		t.Fatal("expected a background read failure not to raise an error status")
	}
}

// Picking up a new comment must not scroll the reader away from where they were.
func TestCommentReloadKeepsTheReadingPosition(t *testing.T) {
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, "line"+strconv.Itoa(i))
	}
	m := commentModel(t, fileWith("a.go", 1, lines...))
	m = pressTimes(m, "j", 20)
	before := cursorText(m)
	offset := m.cursorRow - m.streamScroll

	// A comment lands near the top, shifting every row below it.
	m.LoadComments = func() ([]review.Comment, error) {
		return []review.Comment{commentOn("a.go", 2, "line1", "up here")}, nil
	}
	updated, _ := m.Update(autoRefreshTickMsg{})
	m = updated.(Model)

	if got := cursorText(m); got != before {
		t.Fatalf("expected the cursor to stay on %q, got %q", before, got)
	}
	if got := m.cursorRow - m.streamScroll; got != offset {
		t.Fatalf("expected the screen position preserved, %d → %d", offset, got)
	}
}

// Hue says what the remark is asking for, not who wrote it — and it lands on the
// left bar and the header, never on the prose.
//
// It used to key off the author (yours one colour, the agent's another), which
// spent the only pre-attentive channel available on something a label already
// says. Authorship moved to the 🤖 marker on the body, freeing the hue for the
// distinction you cannot get from skimming: change wanted, answer wanted, or
// neither. Asserted on the style choice, since lipgloss strips colour with no TTY.
func TestColourFollowsKindNotAuthor(t *testing.T) {
	kinds := []review.Kind{review.KindComment, review.KindSuggestion, review.KindQuestion}
	seen := map[string]review.Kind{}
	var bodyHues []string
	for _, k := range kinds {
		bar, head, body, _ := commentStyles(k, false)
		hue := fmt.Sprint(head.GetForeground())
		if other, dup := seen[hue]; dup {
			t.Fatalf("%q and %q share a hue — the kinds must be distinguishable", k, other)
		}
		seen[hue] = k
		// The bar carries the same hue as the header: together they are the whole
		// colour signal.
		if bar.GetForeground() != head.GetForeground() {
			t.Fatalf("%q: the left bar must carry the kind's hue", k)
		}
		bodyHues = append(bodyHues, fmt.Sprint(body.GetForeground()))
	}
	// The prose is deliberately NOT tinted. Blue body text was hard to read
	// against the block's fill, and a coloured paragraph says nothing a coloured
	// edge does not — so every kind's body is the same readable colour.
	for i, hue := range bodyHues {
		if hue != bodyHues[0] {
			t.Fatalf("%q tints its body (%s) — the hue belongs on the bar and header only",
				kinds[i], hue)
		}
	}
	if bodyHues[0] == fmt.Sprint(styleCommentHead.GetForeground()) {
		t.Fatal("expected the body in a readable colour, not the comment kind's hue")
	}
	// An unset kind is a comment: records written before kinds existed have to
	// render as something, and the default is the one claiming the least.
	_, unsetHead, _, _ := commentStyles("", false)
	_, defaultHead, _, _ := commentStyles(review.KindComment, false)
	if unsetHead.GetForeground() != defaultHead.GetForeground() {
		t.Fatal("expected an unset kind to render as a plain comment")
	}
	// The cursorline changes the background, never the hue.
	_, cursorHead, _, _ := commentStyles(review.KindSuggestion, true)
	_, plainHead, _, _ := commentStyles(review.KindSuggestion, false)
	if cursorHead.GetForeground() != plainHead.GetForeground() {
		t.Fatal("expected the cursorline to change the background, not the hue")
	}
}

// tab cycles the kind while composing, and lands back where it started. The box
// owns every key while it is open, so tab is free here — it is the pane switch
// only out in the diff.
func TestTabCyclesTheKindInTheComposeBox(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	if got := m.editor.kind.OrDefault(); got != review.KindComment {
		t.Fatalf("expected a new box to start as a plain comment, got %q", got)
	}

	var order []review.Kind
	for i := 0; i < len(review.Kinds()); i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(Model)
		order = append(order, m.editor.kind)
	}
	if order[len(order)-1] != review.KindComment {
		t.Fatalf("expected the cycle to wrap back to comment, got %v", order)
	}
	if order[0] == review.KindComment {
		t.Fatalf("expected the first tab to change the kind, got %v", order)
	}

	// And the saved record carries whatever the box was showing.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	want := m.editor.kind
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(m.comments) != 1 {
		t.Fatalf("expected the comment saved, got %d", len(m.comments))
	}
	if got := m.comments[0].Kind; got != want {
		t.Fatalf("expected the saved kind %q, got %q", want, got)
	}
}

// Editing reopens the box on the kind the comment already has, so revising the
// wording does not silently reset it to the default.
func TestEditingKeepsTheExistingKind(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.UpdateComment = func(review.Comment) error { return nil }
	c := commentOn("a.go", 2, "beta", "needs a guard")
	c.Kind = review.KindSuggestion
	m.SetComments([]review.Comment{c})
	m.cursorRow = firstRowOfComment(m, c.ID)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = updated.(Model)
	if got := m.editor.kind; got != review.KindSuggestion {
		t.Fatalf("expected the box to open on the existing kind, got %q", got)
	}
}

// A review remark is prose; clipping it at the pane edge hides the half that
// explains the point. It wraps instead.
func TestCommentBodyWrapsRatherThanTruncating(t *testing.T) {
	long := "this is a long remark that will not fit on one row of a narrow pane and therefore has to wrap"
	c := review.Comment{ID: "c1", Author: review.AuthorHuman, Body: long, State: review.Open}

	body := commentBodyText(c, 40)
	if len(body) < 2 {
		t.Fatalf("expected the body wrapped over several rows, got %d: %q", len(body), body)
	}
	// Nothing may be lost to the wrap.
	if joined := strings.Join(body, ""); !strings.Contains(joined, "therefore has to wrap") {
		t.Fatalf("expected the tail preserved, got %q", joined)
	}
	for i, r := range commentRowStrings(c, 40) {
		if lipgloss.Width(r) > 40 {
			t.Fatalf("row %d exceeds the width: %q", i, r)
		}
	}
}

// The row counter and the renderer must agree, or the stream's indices stop
// matching what is drawn.
func TestCommentRowCountMatchesRenderedRows(t *testing.T) {
	cases := []review.Comment{
		{ID: "a", Author: review.AuthorHuman, Body: "short", State: review.Open},
		{ID: "b", Author: "agent", Body: strings.Repeat("wrap me ", 30), State: review.Open, ReplyTo: "a"},
		{ID: "c", Author: review.AuthorHuman, Body: "one\ntwo\nthree", State: review.Sent},
	}
	// Both values of `last`: the closing message carries an extra pad row, so a
	// counter that ignored the flag would desync every row after the block.
	for _, width := range []int{30, 60, 120} {
		for _, c := range cases {
			for _, last := range []bool{false, true} {
				want := commentRowCount(c, width, last)
				got := len(commentLines(c, width, false, last))
				if want != got {
					t.Fatalf("width %d, comment %q, last=%v: counted %d rows, rendered %d",
						width, c.ID, last, want, got)
				}
			}
		}
	}
	// And the closing message is exactly one row taller.
	for _, c := range cases {
		if a, b := commentRowCount(c, 60, false), commentRowCount(c, 60, true); b != a+1 {
			t.Fatalf("comment %q: closing the block added %d rows, want 1", c.ID, b-a)
		}
	}
}

// A wrapped comment still occupies the rows the geometry allotted it.
func TestWrappedCommentOccupiesItsRowsInTheStream(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		commentOn("a.go", 1, "alpha", strings.Repeat("a long remark ", 20)),
	})
	rows := rowsOfKind(m, rowComment)
	if rows < 3 {
		t.Fatalf("expected a wrapped comment to occupy several rows, got %d", rows)
	}
	// Every allotted row must render something, or the geometry over-counted.
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		if got := stripANSI(m.renderStreamRowAt(i, 60)); strings.TrimSpace(got) == "" {
			t.Fatalf("row %d was allotted but renders empty", i)
		}
	}
}

// Prose breaks at spaces. Mid-word breaking is right for code, where reflowing at
// spaces misrepresents where a token ends, and wrong for a sentence.
func TestCommentBodyWordWraps(t *testing.T) {
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, State: review.Open,
		Body: "the quick brown fox jumps over the lazy dog and keeps running",
	}
	for i, text := range commentBodyText(c, 30) {
		if strings.TrimSpace(text) == "" {
			continue
		}
		// No row may start or end mid-word: every row's edges land on whole words.
		if strings.HasPrefix(text, " ") {
			t.Fatalf("row %d starts with padding: %q", i, text)
		}
		for _, word := range strings.Fields(text) {
			if !strings.Contains(c.Body, word) {
				t.Fatalf("row %d contains a broken word %q: %q", i, word, text)
			}
		}
	}
}

// A word longer than the line still has to break, or it overflows the pane.
func TestCommentWrapBreaksAnOverlongWord(t *testing.T) {
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, State: review.Open,
		Body: "see " + strings.Repeat("x", 120),
	}
	rows := commentRowStrings(c, 30)
	for i, r := range rows {
		if lipgloss.Width(r) > 30 {
			t.Fatalf("row %d overflows: %d cells (%q)", i, lipgloss.Width(r), r)
		}
	}
	if len(rows) < 5 {
		t.Fatalf("expected the long word broken across rows, got %d", len(rows))
	}
}

// Comments wrap whether or not code does — `w` governs code, not prose.
func TestCommentsWrapIndependentlyOfWrapMode(t *testing.T) {
	long := strings.Repeat("a wordy remark ", 12)
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", long)})
	wrapOff := rowsOfKind(m, rowComment)

	m = press(m, "w") // code wrap on
	if !m.wrap {
		t.Fatal("expected code wrap enabled")
	}
	if got := rowsOfKind(m, rowComment); got != wrapOff {
		t.Fatalf("expected comment rows unaffected by code wrap mode, %d → %d", wrapOff, got)
	}
	if wrapOff < 3 {
		t.Fatalf("expected the comment wrapped even with code wrap off, got %d rows", wrapOff)
	}
}

// A blank line inside a comment is a paragraph break and must survive wrapping.
func TestCommentPreservesBlankLines(t *testing.T) {
	c := review.Comment{
		ID: "c1", Author: review.AuthorHuman, State: review.Open,
		Body: "first para\n\nsecond para",
	}
	body := commentBodyText(c, 40)
	blank := 0
	for _, text := range body {
		if strings.TrimSpace(text) == "" {
			blank++
		}
	}
	if blank != 1 {
		t.Fatalf("expected the paragraph break preserved, got %d blank rows in %q", blank, body)
	}
}

// A conversation renders as one card with one left bar, so every message in it
// takes the thread's hue. A reply carrying its own kind would break that edge
// into two colours mid-block.
func TestRepliesTakeTheThreadsKind(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	parent := commentOn("a.go", 1, "alpha", "this drops the error")
	parent.Kind = review.KindSuggestion
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "agreed", State: review.Open,
		// Filed as a plain comment, which is what `awp review reply` defaults to.
		Kind: review.KindComment, ReplyTo: parent.ID, Anchor: parent.Anchor,
	}
	m.SetComments([]review.Comment{parent, reply})

	var kinds []review.Kind
	for _, c := range m.stream.comments {
		kinds = append(kinds, c.Kind)
	}
	if len(kinds) != 2 {
		t.Fatalf("expected the parent and its reply placed, got %d", len(kinds))
	}
	for i, k := range kinds {
		if k != review.KindSuggestion {
			t.Fatalf("message %d renders as %q; the whole thread should take the parent's kind", i, k)
		}
	}
	// The stored record is untouched — only the display copy is normalised.
	for _, c := range m.comments {
		if c.ID == "r1" && c.Kind != review.KindComment {
			t.Fatalf("expected the stored reply's own kind preserved, got %q", c.Kind)
		}
	}
}

// The kind is named once per conversation, on the remark that opened it. A reply
// already renders in the thread's hue, so repeating the word on every message is
// noise.
func TestOnlyTheThreadsFirstMessageNamesTheKind(t *testing.T) {
	parent := review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: "top",
		Kind: review.KindSuggestion, State: review.Open,
	}
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "under", ReplyTo: "c1",
		Kind: review.KindSuggestion, State: review.Open,
	}
	head := func(c review.Comment) string {
		for _, r := range commentRows(c, 60, true) {
			if r.header {
				return r.text
			}
		}
		return ""
	}
	if got := head(parent); !strings.Contains(got, "suggestion") {
		t.Fatalf("expected the opening message to name its kind, got %q", got)
	}
	if got := head(reply); strings.Contains(got, "suggestion") {
		t.Fatalf("expected a reply not to repeat the kind, got %q", got)
	}
	// A plain comment claims nothing, so it goes unlabelled either way.
	plain := review.Comment{ID: "c2", Author: review.AuthorHuman, Body: "x", State: review.Open}
	if got := head(plain); strings.Contains(got, "comment") {
		t.Fatalf("expected the default kind unlabelled, got %q", got)
	}
}

// Only the last message of a conversation closes the block. Giving every message
// a trailing pad would put two blank rows between each pair — the previous
// message's close and the next one's open.
func TestOnlyTheLastMessageInAThreadClosesTheBlock(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	parent := commentOn("a.go", 1, "alpha", "top")
	m.SetComments([]review.Comment{parent, {
		ID: "r1", Author: "agent", Body: "under", State: review.Open,
		ReplyTo: parent.ID, Anchor: parent.Anchor,
	}})

	// Walk the block's rows and read out which are blank, so the shape is
	// asserted rather than the row count.
	var blanks []int
	block := 0
	for _, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		lines := commentLines(m.stream.comments[r.comment], m.hunkWidth, false, r.lastComment)
		if r.commentLine < len(lines) {
			if _, text := splitGutter(stripANSI(lines[r.commentLine])); strings.TrimSpace(text) == "" {
				blanks = append(blanks, block)
			}
		}
		block++
	}
	// Three blanks: the block's opening pad, the separator before the reply, and
	// the closing pad. Never two in a row.
	if len(blanks) != 3 {
		t.Fatalf("expected 3 blank rows in the block, got %d at %v", len(blanks), blanks)
	}
	for i := 1; i < len(blanks); i++ {
		if blanks[i] == blanks[i-1]+1 {
			t.Fatalf("two blank rows in a row at %d — only the last message closes the block", blanks[i])
		}
	}
	// And the closing pad is the block's final row.
	if blanks[len(blanks)-1] != block-1 {
		t.Fatalf("expected the last row of the block to be its closing pad, got row %d of %d", blanks[len(blanks)-1], block)
	}
}

// Deleting a conversation prunes its replies from the viewer's own set too, not
// just from the store. Dropping only the parent would leave the replies on screen
// until the next reload — and placeComments would show each as a conversation of
// its own in the meantime.
func TestDeletingAParentPrunesItsRepliesFromTheView(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	var deleted []string
	m.DeleteComment = func(id string) error { deleted = append(deleted, id); return nil }
	parent := commentOn("a.go", 1, "alpha", "needs a guard")
	m.SetComments([]review.Comment{
		parent,
		{ID: "r1", Author: "agent", Body: "fixed", State: review.Open, ReplyTo: parent.ID, Anchor: parent.Anchor},
		{ID: "r2", Author: review.AuthorHuman, Body: "thanks", State: review.Open, ReplyTo: parent.ID, Anchor: parent.Anchor},
		// A separate conversation that must survive.
		{ID: "other", Author: review.AuthorHuman, Body: "unrelated", State: review.Open,
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 2, Text: "beta"}},
	})

	m.cursorRow = firstRowOfComment(m, parent.ID)
	updated, _ := m.deleteCommentAtCursor()
	m = updated.(Model)

	// The store is told once, about the parent — the cascade is its job.
	if len(deleted) != 1 || deleted[0] != parent.ID {
		t.Fatalf("expected one delete for the parent, got %v", deleted)
	}
	if len(m.comments) != 1 || m.comments[0].ID != "other" {
		t.Fatalf("expected only the unrelated comment left in the view, got %+v", m.comments)
	}
	// And no reply lingers in the stream as its own conversation.
	for _, c := range m.stream.comments {
		if c.ReplyTo != "" {
			t.Fatalf("a reply survived in the stream: %+v", c)
		}
	}
	// The index agrees.
	if len(m.commentIndex) != 1 || m.commentIndex[0].id != "other" {
		t.Fatalf("expected one entry left in the index, got %+v", m.commentIndex)
	}
	// A cascade that took more than what was pointed at has to say so.
	if !strings.Contains(m.status, "2 replies") {
		t.Fatalf("expected the status to report the replies taken, got %q", m.status)
	}
}

// Deleting a reply takes only that reply; its conversation stays.
func TestDeletingAReplyKeepsTheConversation(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.DeleteComment = func(string) error { return nil }
	parent := commentOn("a.go", 1, "alpha", "top")
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "answer", State: review.Open,
		ReplyTo: parent.ID, Anchor: parent.Anchor,
	}
	m.SetComments([]review.Comment{parent, reply})

	m.cursorRow = firstRowOfComment(m, reply.ID)
	updated, _ := m.deleteCommentAtCursor()
	m = updated.(Model)
	if len(m.comments) != 1 || m.comments[0].ID != parent.ID {
		t.Fatalf("expected the parent to survive, got %+v", m.comments)
	}
	if strings.Contains(m.status, "repl") {
		t.Fatalf("expected no cascade reported for a leaf reply, got %q", m.status)
	}
}
