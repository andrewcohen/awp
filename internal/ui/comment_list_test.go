package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/review"
)

// indexModel is a viewer tall enough for the index pane to fit beside the file
// list, loaded with two files.
func indexModel(t *testing.T) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 24)
	m.focus = FocusHunks
	return loadWith(m, 1,
		fileWith("a.go", 1, "alpha", "beta", "gamma"),
		fileWith("pkg/b.go", 1, "delta", "epsilon"),
	)
}

func comment(id, path string, line int, text, body, author string) review.Comment {
	return review.Comment{
		ID: id, Author: author, Body: body, State: review.Open,
		Anchor: review.Anchor{Path: path, Side: review.SideNew, LineHint: line, Text: text},
	}
}

func TestCommentIndexListsOneEntryPerConversation(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "needs a guard", review.AuthorHuman),
		comment("c2", "pkg/b.go", 1, "delta", "and this one", review.AuthorHuman),
	})
	if got := len(m.commentIndex); got != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", got, m.commentIndex)
	}
	// Stream order, so the index reads top to bottom the way the diff does.
	if m.commentIndex[0].id != "c1" || m.commentIndex[1].id != "c2" {
		t.Fatalf("expected stream order, got %q then %q", m.commentIndex[0].id, m.commentIndex[1].id)
	}
	if m.commentIndex[0].row >= m.commentIndex[1].row {
		t.Fatal("entry rows must ascend with the stream")
	}
}

// A reply is not its own destination: jumping to it and jumping to the remark it
// answers are the same jump, so it folds into the parent as a count.
func TestCommentIndexFoldsRepliesIntoTheirParent(t *testing.T) {
	m := indexModel(t)
	reply := comment("c2", "a.go", 2, "beta", "done", "agent")
	reply.ReplyTo = "c1"
	second := comment("c3", "a.go", 2, "beta", "still not", review.AuthorHuman)
	second.ReplyTo = "c1"
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "needs a guard", review.AuthorHuman),
		reply, second,
	})
	if got := len(m.commentIndex); got != 1 {
		t.Fatalf("expected one entry for the conversation, got %d: %+v", got, m.commentIndex)
	}
	if got := m.commentIndex[0].replies; got != 2 {
		t.Fatalf("expected 2 replies counted, got %d", got)
	}
}

// A comment whose anchor no longer resolves must be listed, and listed as
// detached — the whole point of keeping it in the stream is that it stays
// reachable, which needs an index entry that admits its position is stale.
func TestCommentIndexMarksDetachedConversations(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("gone", "a.go", 2, "a line that is not in this diff", "orphaned", review.AuthorHuman),
	})
	if len(m.commentIndex) != 1 {
		t.Fatalf("expected the detached comment listed, got %+v", m.commentIndex)
	}
	if !m.commentIndex[0].detached {
		t.Fatal("expected the entry marked detached")
	}
	if !strings.Contains(entryLocation(m.commentIndex[0]), "⚠") {
		t.Fatalf("expected the row to show its anchor is gone, got %q", entryLocation(m.commentIndex[0]))
	}
}

// Selecting an entry seeks the diff, so the conversation is on screen by the
// time the keyboard gets there. Without this the index would name a location the
// diff wasn't showing.
func TestSeekingTheIndexMovesTheDiffCursor(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "first", review.AuthorHuman),
		comment("c2", "pkg/b.go", 2, "epsilon", "second", review.AuthorHuman),
	})
	m.focus = FocusComments
	m.seekToComment(1)
	if got := m.stream.rows[m.cursorRow]; got.kind != rowComment {
		t.Fatalf("expected the cursor on a comment row, got %v", got.kind)
	}
	c := m.stream.comments[m.stream.rows[m.cursorRow].comment]
	if c.ID != "c2" {
		t.Fatalf("expected the cursor on c2, got %q", c.ID)
	}
	// And the file list follows, so all three panes agree on where you are.
	if got := diffPathAt(m, m.filesCursor); got != "pkg/b.go" {
		t.Fatalf("expected the file list on pkg/b.go, got %q", got)
	}
}

func diffPathAt(m Model, i int) string {
	if i < 0 || i >= len(m.filtered) {
		return ""
	}
	return pathOf(m.filtered[i])
}

// j/k in the index are seeks, not just selection moves.
func TestIndexKeysSeek(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "first", review.AuthorHuman),
		comment("c2", "pkg/b.go", 2, "epsilon", "second", review.AuthorHuman),
	})
	m.focus = FocusComments
	before := m.cursorRow
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := updated.(Model)
	if got.commentsCursor != 1 {
		t.Fatalf("expected the index cursor to advance, got %d", got.commentsCursor)
	}
	if got.cursorRow == before {
		t.Fatal("expected j in the index to seek the diff, not only move the selection")
	}
}

// The index joins the tab rotation only when it is on screen; tabbing into a
// pane that isn't rendered would strand the keyboard with no visible cursor.
func TestTabSkipsTheIndexWhenItIsNotShown(t *testing.T) {
	m := indexModel(t) // no comments
	m.focus = FocusFiles
	m.cycleFocus(true)
	if m.focus != FocusHunks {
		t.Fatalf("expected files → diff with no comments, got %v", m.focus)
	}

	m.SetComments([]review.Comment{comment("c1", "a.go", 2, "beta", "x", review.AuthorHuman)})
	m.focus = FocusFiles
	m.cycleFocus(true)
	if m.focus != FocusComments {
		t.Fatalf("expected files → comments once there is an index, got %v", m.focus)
	}
	m.cycleFocus(true)
	if m.focus != FocusHunks {
		t.Fatalf("expected comments → diff, got %v", m.focus)
	}
	// And backwards.
	m.cycleFocus(false)
	if m.focus != FocusComments {
		t.Fatalf("expected shift+tab to reverse, got %v", m.focus)
	}
}

// Focus must not be left on a pane that has gone away — deleting the last
// comment, or filtering its file out, removes the index from under it.
func TestFocusLeavesTheIndexWhenItEmpties(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{comment("c1", "a.go", 2, "beta", "x", review.AuthorHuman)})
	m.focus = FocusComments
	m.SetComments(nil)
	if m.focus == FocusComments {
		t.Fatal("expected focus to leave the index when it emptied")
	}
}

// The index takes at most half the column and never shortens the file list past
// usability — the file list is the primary index.
func TestCommentPaneHeightIsBounded(t *testing.T) {
	if got := commentPaneHeight(0, 24); got != 0 {
		t.Fatalf("no comments means no pane, got %d", got)
	}
	if got := commentPaneHeight(50, 24); got > 12 {
		t.Fatalf("expected at most half of 24 rows, got %d", got)
	}
	if got := commentPaneHeight(2, 24); got != 3 {
		t.Fatalf("expected a header plus 2 entries, got %d", got)
	}
	// The tightest split that still works: a 2-row index (header + one entry)
	// over a 3-row file list. Cramped, but hiding the index instead would make a
	// comment unreachable on a short terminal.
	if got := commentPaneHeight(3, 7); got != 2 {
		t.Fatalf("expected the minimum split in a 7-row column, got %d", got)
	}
	// One row shorter and it cannot split without starving the file list.
	if got := commentPaneHeight(3, 6); got != 0 {
		t.Fatalf("expected no pane in a 6-row column, got %d", got)
	}
}

// The two stacked panes must leave the left column exactly as tall as the hunk
// pane beside it, or JoinHorizontal pads the shorter side and the body grows.
func TestLeftColumnKeepsTheSameHeightWithTheIndex(t *testing.T) {
	m := indexModel(t)
	const h = 20
	without := strings.Count(m.renderLeftColumn(40, h), "\n")
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "x", review.AuthorHuman),
		comment("c2", "pkg/b.go", 1, "delta", "y", review.AuthorHuman),
	})
	if len(m.commentIndex) == 0 {
		t.Fatal("fixture is wrong: expected an index to render")
	}
	with := strings.Count(m.renderLeftColumn(40, h), "\n")
	if with != without {
		t.Fatalf("left column is %d rows with the index and %d without", with+1, without+1)
	}
	if got := strings.Count(m.renderStreamPanel(80, h), "\n"); got != with {
		t.Fatalf("left column (%d) and hunk pane (%d) disagree on height", with+1, got+1)
	}
}

// Selection wins over authorship: the app-wide marker has to read as the
// selection wherever it lands. lipgloss strips colour with no TTY, so the choice
// is asserted rather than the rendered output.
func TestCommentEntryStylesPutSelectionFirst(t *testing.T) {
	mineLoc, _ := commentEntryStyles(true, false)
	theirsLoc, _ := commentEntryStyles(false, false)
	if mineLoc.GetForeground() == theirsLoc.GetForeground() {
		t.Fatal("expected your conversations to take a different hue from everyone else's")
	}
	selLoc, selText := commentEntryStyles(true, true)
	otherSel, _ := commentEntryStyles(false, true)
	if selLoc.GetForeground() != otherSel.GetForeground() {
		t.Fatal("a selected row must look the same whoever wrote it")
	}
	if selLoc.GetForeground() != styleSelected.GetForeground() || selText.GetForeground() != styleSelected.GetForeground() {
		t.Fatal("expected the selected row in the app-wide selection hue")
	}
}

// An index row must fit its pane: a deep path plus a long remark cannot push it
// past the column and corrupt the layout.
func TestCommentEntryRowFitsItsWidth(t *testing.T) {
	e := commentEntry{
		path:    "internal/some/deep/package/with/a/long/name.go",
		line:    12345,
		summary: strings.Repeat("very long remark ", 20),
		author:  review.AuthorHuman,
		replies: 3,
	}
	for _, width := range []int{12, 24, 40} {
		for _, selected := range []bool{false, true} {
			if got := lipgloss.Width(renderCommentEntry(e, width, selected)); got > width {
				t.Fatalf("width %d selected=%v: row is %d cells", width, selected, got)
			}
		}
	}
}

// The summary skips leading blank lines — a comment written in $EDITOR often
// starts with one, and an index full of empty summaries is useless.
func TestFirstLineSkipsBlanks(t *testing.T) {
	if got := firstLine("\n\n  the point  \nmore"); got != "the point" {
		t.Fatalf("got %q", got)
	}
	if got := firstLine("   \n\t\n"); got != "" {
		t.Fatalf("expected empty for a blank body, got %q", got)
	}
}
