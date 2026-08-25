package ui

import (
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// The wheel, and #340's rule: it scrolls what the pointer is over and leaves the
// keyboard where it was.

// wheelModel is a viewer with a comment, so the left column carries both panes —
// the file list over the comment index — and the routing has three targets rather
// than two.
func wheelModel(t *testing.T) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"), fileWith("b.go", 1, "gamma", "delta"))
	first := commentOn("a.go", 1, "alpha", "a finding")
	second := commentOn("b.go", 1, "gamma", "another finding")
	second.ID = "c2"
	m.SetComments([]review.Comment{first, second})
	m.SetSize(120, 20)
	// Two, so the index has somewhere to move to: with one entry every notch clamps
	// and a routing bug that scrolled the wrong pane would look like a pass.
	if len(m.commentIndex) < 2 {
		t.Fatalf("fixture is wrong: expected two comments in the index, got %d", len(m.commentIndex))
	}
	if len(m.filtered) < 2 {
		t.Fatalf("fixture is wrong: expected two files, got %d", len(m.filtered))
	}
	return m
}

// overStream is a point inside the diff pane, and overFiles / overComments points
// inside the two halves of the left column.
func (m Model) overStream() (int, int) {
	left, _ := m.paneWidthsFor(m.width)
	return left + 5, 3
}

func (m Model) overFiles() (int, int) { return 2, 2 }

func (m Model) overComments() (int, int) {
	height := max(minBodyHeight, m.bodyHeight)
	return 2, height - commentPaneHeight(len(m.commentIndex), m.hiddenThreads(), height) + 1
}

// TestTheWheelScrollsTheDiffWithoutMovingTheCursor. The whole of #340's rule in one
// assertion: the view moves, the cursor does not, and neither does the focus.
func TestTheWheelScrollsTheDiffWithoutMovingTheCursor(t *testing.T) {
	m := wheelModel(t)
	x, y := m.overStream()
	at, cursor := m.streamScroll, m.cursorRow

	m, took := m.WheelAt(x, y, false)
	if !took {
		t.Fatal("a notch over the diff was not taken")
	}
	if m.streamScroll != at+wheelRows {
		t.Errorf("stream scrolled to %d, want %d", m.streamScroll, at+wheelRows)
	}
	if m.cursorRow != cursor {
		t.Errorf("the wheel moved the cursor to row %d, from %d", m.cursorRow, cursor)
	}
	if m.focus != FocusHunks {
		t.Errorf("the wheel moved the focus to %v", m.focus)
	}
}

// TestAWheelUpAtTheTopOfTheDiffStaysThere. Bounded by the clamp, and still reported
// as taken — the event was the wheel's either way, and the alternative is falling
// through to something else.
func TestAWheelUpAtTheTopOfTheDiffStaysThere(t *testing.T) {
	m := wheelModel(t)
	m.streamScroll = 0
	x, y := m.overStream()

	m, took := m.WheelAt(x, y, true)
	if !took {
		t.Fatal("a notch over the diff was not taken")
	}
	if m.streamScroll != 0 {
		t.Errorf("scrolled to %d above the top of the stream", m.streamScroll)
	}
}

// TestTheWheelOverTheFileListMovesTheFileList, and not the keyboard: the diff keeps
// the keys while the pointer picks a different file to look at.
func TestTheWheelOverTheFileListMovesTheFileList(t *testing.T) {
	m := wheelModel(t)
	x, y := m.overFiles()

	m, took := m.WheelAt(x, y, false)
	if !took {
		t.Fatal("a notch over the file list was not taken")
	}
	if m.filesCursor != 1 {
		t.Errorf("file list is on %d, want 1", m.filesCursor)
	}
	if m.focus != FocusHunks {
		t.Errorf("pointing at the file list moved the keyboard to %v", m.focus)
	}
	m, _ = m.WheelAt(x, y, true)
	if m.filesCursor != 0 {
		t.Errorf("a notch back put the file list on %d, want 0", m.filesCursor)
	}
}

// TestTheWheelOverTheCommentIndexMovesTheCommentIndex. The point of the row
// arithmetic in wheelLeftColumn: the two panes share a column, so only y separates
// them, and getting it wrong scrolls the wrong one.
func TestTheWheelOverTheCommentIndexMovesTheCommentIndex(t *testing.T) {
	m := wheelModel(t)
	x, y := m.overComments()

	m, took := m.WheelAt(x, y, false)
	if !took {
		t.Fatal("a notch over the comment index was not taken")
	}
	if m.commentsCursor != 1 {
		t.Errorf("comment index is on %d, want 1", m.commentsCursor)
	}
	// The diff follows a selected conversation, file cursor included — that is what
	// seeking to one means, and it is what j/k in this pane already do. Only the
	// keyboard has to stay put.
	if m.focus != FocusHunks {
		t.Errorf("pointing at the comment index moved the keyboard to %v", m.focus)
	}
}

// TestTheWheelOverAHiddenColumnScrollsTheDiff. With `\` the stream is the whole
// body, so the columns the file list used to occupy are the diff's now.
func TestTheWheelOverAHiddenColumnScrollsTheDiff(t *testing.T) {
	m := wheelModel(t)
	m.hideLeft = true
	m.focus = FocusHunks
	at := m.streamScroll

	m, took := m.WheelAt(2, 2, false)
	if !took {
		t.Fatal("a notch in the left columns was not taken by the diff")
	}
	if m.streamScroll != at+wheelRows {
		t.Errorf("stream scrolled to %d, want %d", m.streamScroll, at+wheelRows)
	}
}

// TestTheWheelScrollsTheHelpReference. It stands in place of the panes rather than
// over them, and it is long enough that reaching the end of it is what a reader
// with a mouse is trying to do.
func TestTheWheelScrollsTheHelpReference(t *testing.T) {
	m := wheelModel(t)
	m = press(m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the reference")
	}
	m, took := m.WheelAt(m.width/2, 3, false)
	if !took {
		t.Fatal("a notch over the reference was not taken")
	}
	if m.helpVP.YOffset() == 0 {
		t.Error("the reference did not scroll")
	}
}

// TestTheWheelDoesNothingOverAPrompt. The publish and merge screens replace the
// panes, and neither has anything to scroll — so the notch is not taken rather than
// quietly moving a pane nobody can see.
func TestTheWheelDoesNothingOverAPrompt(t *testing.T) {
	m := wheelModel(t)
	m.publishing = true
	x, y := m.overStream()
	if _, took := m.WheelAt(x, y, false); took {
		t.Error("a notch over the publish prompt was taken")
	}
}
