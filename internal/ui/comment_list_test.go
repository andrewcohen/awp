package ui

import (
	"fmt"
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

// longFileModel is a viewer over one file long enough that a row can be
// centred with room on both sides — the minimum-scroll behaviour and the
// centred one are indistinguishable on a diff that fits the pane.
func longFileModel(t *testing.T, lines int) Model {
	t.Helper()
	body := make([]string, 0, lines)
	for i := range lines {
		body = append(body, fmt.Sprintf("line %d", i))
	}
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 24)
	m.focus = FocusHunks
	return loadWith(m, 1, fileWith("a.go", 1, body...))
}

// A conversation reached from the index is the thing you asked to read, so it
// gets the middle of the pane. Scrolling it just barely into view puts its first
// line on the last row with the rest of it below the fold.
func TestSeekingTheIndexCentersTheConversation(t *testing.T) {
	m := longFileModel(t, 200)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 150, "line 149", "down here", review.AuthorHuman),
	})
	m.focus = FocusComments
	m.seekToComment(0)

	if m.stream.rows[m.cursorRow].kind != rowComment {
		t.Fatalf("expected the cursor on the comment, got %v", m.stream.rows[m.cursorRow].kind)
	}
	want := m.streamContentHeight() / 2
	if got := m.cursorRow - m.streamScroll; got != want {
		t.Fatalf("expected the comment %d rows down a %d-row pane, got %d",
			want, m.streamContentHeight(), got)
	}
}

// Centring is an aim, not a demand: there is nothing above the first rows to
// scroll away, so a conversation near the top sits where it falls rather than
// being dragged down to the middle over blank space.
func TestCenteringDoesNotScrollPastTheTop(t *testing.T) {
	m := longFileModel(t, 200)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "line 1", "up here", review.AuthorHuman),
	})
	m.focus = FocusComments
	m.seekToComment(0)

	if m.streamScroll != 0 {
		t.Fatalf("expected the stream still at the top, got scroll %d", m.streamScroll)
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

// Delete has to work from the index, which means the diff cursor must already be
// parked on the selected conversation the moment focus arrives — not only after
// the first j/k, since D acts through the cursor.
func TestTabbingIntoTheIndexParksTheCursorOnTheSelection(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "first", review.AuthorHuman),
		comment("c2", "pkg/b.go", 2, "epsilon", "second", review.AuthorHuman),
	})
	m.commentsCursor = 1
	m.cursorRow = 0 // deliberately elsewhere
	m.focus = FocusFiles
	m.cycleFocus(true)
	if m.focus != FocusComments {
		t.Fatalf("expected the index focused, got %v", m.focus)
	}
	got, ok := m.localCommentAtCursor()
	if !ok {
		t.Fatal("expected the cursor on a comment after tabbing into the index")
	}
	if got.ID != "c2" {
		t.Fatalf("expected the cursor on the selected conversation c2, got %q", got.ID)
	}
}

func TestDeleteFromTheIndexRemovesTheSelection(t *testing.T) {
	m := indexModel(t)
	var deleted []string
	m.DeleteComment = func(id string) error { deleted = append(deleted, id); return nil }
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "first", review.AuthorHuman),
		comment("c2", "pkg/b.go", 2, "epsilon", "second", review.AuthorHuman),
	})
	m.focus = FocusFiles
	m.cycleFocus(true) // into the index, on c1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m = updated.(Model)
	if len(deleted) != 1 || deleted[0] != "c1" {
		t.Fatalf("expected c1 deleted through the store, got %v", deleted)
	}
	if len(m.commentIndex) != 1 || m.commentIndex[0].id != "c2" {
		t.Fatalf("expected only c2 left in the index, got %+v", m.commentIndex)
	}
	// And the cursor followed to what took its place, rather than sitting on a row
	// that shifted underneath it.
	got, ok := m.localCommentAtCursor()
	if !ok || got.ID != "c2" {
		t.Fatalf("expected the cursor on c2 after the delete, got %+v (ok=%v)", got, ok)
	}
}

// Deleting the last one takes the pane away, so focus has to leave with it.
func TestDeletingTheLastCommentFromTheIndexReleasesFocus(t *testing.T) {
	m := indexModel(t)
	m.DeleteComment = func(string) error { return nil }
	m.SetComments([]review.Comment{comment("c1", "a.go", 2, "beta", "only", review.AuthorHuman)})
	m.focus = FocusFiles
	m.cycleFocus(true)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m = updated.(Model)
	if len(m.commentIndex) != 0 {
		t.Fatalf("expected an empty index, got %+v", m.commentIndex)
	}
	if m.focus == FocusComments {
		t.Fatal("expected focus to leave the index once it emptied")
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
	if got := commentPaneHeight(0, 0, 24); got != 0 {
		t.Fatalf("no comments means no pane, got %d", got)
	}
	// Except when there is something to report: a change whose conversation is all
	// hidden must not look like a change nobody has commented on.
	if got := commentPaneHeight(0, 3, 24); got != 2 {
		t.Fatalf("expected a header-only pane for hidden threads, got %d", got)
	}
	if got := commentPaneHeight(50, 0, 24); got > 12 {
		t.Fatalf("expected at most half of 24 rows, got %d", got)
	}
	if got := commentPaneHeight(2, 0, 24); got != 3 {
		t.Fatalf("expected a header plus 2 entries, got %d", got)
	}
	// The tightest split that still works: a 2-row index (header + one entry)
	// over a 3-row file list. Cramped, but hiding the index instead would make a
	// comment unreachable on a short terminal.
	if got := commentPaneHeight(3, 0, 7); got != 2 {
		t.Fatalf("expected the minimum split in a 7-row column, got %d", got)
	}
	// One row shorter and it cannot split without starving the file list.
	if got := commentPaneHeight(3, 0, 6); got != 0 {
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

// An index row carries the same hue the conversation has in the diff, so the two
// views of one comment are recognisably the same thing. Selection overrides it:
// the app-wide marker has to read as the selection wherever it lands. lipgloss
// strips colour with no TTY, so the choice is asserted rather than the output.
func TestCommentEntryStylesFollowKindThenSelection(t *testing.T) {
	for _, k := range review.Kinds() {
		entryLoc, _ := commentEntryStyles(k, false, false)
		_, blockHead, _, _ := commentStyles(k, false)
		if entryLoc.GetForeground() != blockHead.GetForeground() {
			t.Fatalf("%q: index row and diff block disagree on hue", k)
		}
	}
	selLoc, selText := commentEntryStyles(review.KindSuggestion, true, false)
	otherSel, _ := commentEntryStyles(review.KindQuestion, true, false)
	if selLoc.GetForeground() != otherSel.GetForeground() {
		t.Fatal("a selected row must look the same whatever kind it is")
	}
	if selLoc.GetForeground() != styleSelected.GetForeground() || selText.GetForeground() != styleSelected.GetForeground() {
		t.Fatal("expected the selected row in the app-wide selection hue")
	}
	// Banded, the row keeps the selection hue and gains the cursorline
	// background — every style on the row has to carry it, or the band breaks
	// wherever one segment ends and the next begins.
	bandLoc, bandText := commentEntryStyles(review.KindSuggestion, true, true)
	if bandLoc.GetForeground() != styleSelected.GetForeground() {
		t.Fatal("a banded row must keep the selection hue")
	}
	for _, s := range []lipgloss.Style{bandLoc, bandText} {
		if s.GetBackground() != cursorlineBg {
			t.Fatalf("expected the cursorline background, got %v", s.GetBackground())
		}
	}
}

// cursorlineSeq is the escape run that turns the cursorline background on, so a
// test can ask whether a rendered row actually carries the band rather than
// inferring it from style values. Colour is forced on for this package's tests
// (see bench_test.go's init), which is what makes this observable at all.
func cursorlineSeq(t *testing.T) string {
	t.Helper()
	rendered := styleCursorFill.Render("@")
	at := strings.Index(rendered, "@")
	if at <= 0 {
		t.Fatalf("no background escape in %q — is the colour profile set?", rendered)
	}
	return rendered[:at]
}

// bandedRows counts how many rows of a rendered block carry the cursorline.
func bandedRows(t *testing.T, block string) int {
	t.Helper()
	seq := cursorlineSeq(t)
	n := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, seq) {
			n++
		}
	}
	return n
}

// The band says which selection the keys are driving. Two at once would leave
// that ambiguous, so only the pane holding the keyboard paints one — the rule
// the diff pane already followed, now that the left column has a band too.
func TestOnlyTheFocusedPanePaintsACursorline(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{
		comment("c1", "a.go", 2, "beta", "first", review.AuthorHuman),
		comment("c2", "pkg/b.go", 2, "epsilon", "second", review.AuthorHuman),
	})

	for _, tc := range []struct {
		focus Focus
		want  int
	}{
		{FocusFiles, 1},
		{FocusComments, 1},
		// The diff holds the keyboard, so neither list may claim it.
		{FocusHunks, 0},
	} {
		m.focus = tc.focus
		// The left column caches per (focus, selection, size) — drop it so each
		// case renders rather than reusing the previous focus's output.
		m.cache.drop()
		if got := bandedRows(t, m.renderLeftColumn(34, 20)); got != tc.want {
			t.Fatalf("focus %v: expected %d banded rows in the left column, got %d",
				tc.focus, tc.want, got)
		}
	}
}

// A band that stops where the text does reads as highlighted text, not as a
// cursorline. It has to span the pane, and never past it.
func TestTheBandSpansThePaneWidth(t *testing.T) {
	m := indexModel(t)
	m.SetComments([]review.Comment{comment("c1", "a.go", 2, "beta", "short", review.AuthorHuman)})
	inner := 30

	file := bandRow(m.renderFileRow(m.filtered[0], fileTreeRows(m.filtered)[0], inner-2, true, true), inner)
	if got := lipgloss.Width(file); got != inner {
		t.Fatalf("banded file row is %d cells, want %d", got, inner)
	}
	entry := bandRow(renderCommentEntry(m.commentIndex[0], inner-2, true, true), inner)
	if got := lipgloss.Width(entry); got != inner {
		t.Fatalf("banded index row is %d cells, want %d", got, inner)
	}
}

// An index row must fit its pane: a deep path plus a long remark cannot push it
// past the column and corrupt the layout.
func TestCommentEntryRowFitsItsWidth(t *testing.T) {
	e := commentEntry{
		path:    "internal/some/deep/package/with/a/long/name.go",
		lines:   "12345-12360",
		summary: strings.Repeat("very long remark ", 20),
		author:  review.AuthorHuman,
		replies: 3,
	}
	for _, width := range []int{12, 24, 40} {
		for _, selected := range []bool{false, true} {
			for _, band := range []bool{false, true} {
				got := lipgloss.Width(renderCommentEntry(e, width, selected, band))
				if got > width {
					t.Fatalf("width %d selected=%v band=%v: row is %d cells",
						width, selected, band, got)
				}
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

// A robot's words are marked wherever they appear, so an agent's finding is never
// mistaken for something the reviewer wrote.
func TestRobotCommentsAreMarked(t *testing.T) {
	agent := review.Comment{ID: "a1", Author: "agent", Body: "nil deref here", State: review.Open}
	body := commentBodyText(agent, 60)
	if len(body) == 0 || !strings.Contains(body[0], review.RobotMarker) {
		t.Fatalf("expected the robot marker on an agent's body, got %q", body)
	}
	mine := review.Comment{ID: "h1", Author: review.AuthorHuman, Body: "nil deref here", State: review.Open}
	for _, text := range commentBodyText(mine, 60) {
		if strings.Contains(text, review.RobotMarker) {
			t.Fatalf("expected no marker on your own comment, got %q", text)
		}
	}
}

// A mirrored GitHub thread's synthetic author is not AuthorHuman, so ByRobot
// alone would stamp other people's comments as an agent's. Those are real
// people's words on GitHub and must stay unmarked.
func TestMirroredGitHubThreadsAreNotMarkedAsRobots(t *testing.T) {
	c := Model{}.threadAsComments(review.Thread{
		ID: "T1", Path: "a.go", Line: 3,
		Comments: []review.ThreadComment{{Author: "someone", Body: "why here?"}},
	})[0]
	if robotAuthored(c) {
		t.Fatal("a mirrored GitHub thread must not count as robot-authored")
	}
	for _, text := range commentBodyText(c, 60) {
		if strings.Contains(text, review.RobotMarker) {
			t.Fatalf("expected no marker on a mirrored thread, got %q", text)
		}
	}
}

// The marker also shows in the index, so scanning the list tells you which
// conversations an agent started.
func TestIndexSummaryCarriesTheRobotMarker(t *testing.T) {
	got := entrySummary(review.Comment{ID: "a1", Author: "agent", Body: "nil deref"})
	if !strings.HasPrefix(got, review.RobotMarker) {
		t.Fatalf("expected the marker in the index summary, got %q", got)
	}
	mine := entrySummary(review.Comment{ID: "h1", Author: review.AuthorHuman, Body: "nil deref"})
	if strings.Contains(mine, review.RobotMarker) {
		t.Fatalf("expected no marker for your own comment, got %q", mine)
	}
	// An empty body must not become a bare marker with nothing after it.
	if got := entrySummary(review.Comment{ID: "a2", Author: "agent", Body: "  \n "}); got != "" {
		t.Fatalf("expected an empty summary to stay empty, got %q", got)
	}
}
