package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/review"
)

// composingModel is a viewer sitting on the publish screen at the given size.
func composingModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.PublishReview = func(string, string, bool) (string, error) { return "", nil }
	m.SetSize(width, height)
	m.beginPublish()
	if !m.publishing || m.publishStage != publishComposing {
		t.Fatal("fixture is wrong: the model is not on the composing screen")
	}
	return m
}

// panelBorderRows is what a bordered body panel adds outside the height its host
// budgeted. The normal two-pane body does the same, so the publish overlay
// matching it is the convention rather than an overflow.
const panelBorderRows = 2

// The composing screen fills the height it is given — no more, so the footer
// stays put, and no less, so the summary box is not a letterbox with dead space
// under it.
//
// Both halves need asserting and they need different assertions. Overflow shows
// up in the rendered height; *underfill* does not, because the overlay's
// lipgloss Height pads short content out to the budget — a box left at four rows
// renders exactly the right number of rows, all the spare ones blank and below
// the keys. So the second check is where the hint actually lands: pinned to the
// bottom means nothing came up short.
//
// publishSummaryChrome is a hand-maintained count of the rows around the text
// area, and this is the only thing that detects it drifting. Off by one it
// silently steals a row from the box or pushes the key hint off the bottom, and
// neither shows up until someone is typing a review body.
func TestThePublishScreenFillsItsBudget(t *testing.T) {
	for _, height := range []int{14, 20, 32, 50} {
		m := composingModel(t, 120, height)
		out := m.renderPublishOverlay(120, height)
		if got, want := lipgloss.Height(out), height+panelBorderRows; got != want {
			t.Errorf("at budget %d the screen rendered %d rows, want %d — publishSummaryChrome (%d) is wrong",
				height, got, want, publishSummaryChrome)
			continue
		}
		// The last row is the overlay's bottom border; the key hint sits directly on
		// it. Anything between the two is height the box did not take.
		rows := strings.Split(out, "\n")
		hint := rows[len(rows)-2]
		if !strings.Contains(hint, "esc cancel") {
			t.Errorf("at budget %d the row above the border is %q, not the key hint — the box left %d rows unused",
				height, strings.TrimSpace(hint), blankTail(rows))
		}
	}
}

// blankTail counts the empty content rows above the bottom border, which is how
// much height the summary box failed to take.
//
// The escapes have to come off first: a row of the overlay is border glyphs and
// colour codes around its content, so TrimSpace on the raw string finds the
// border and calls every row non-blank.
func blankTail(rows []string) int {
	n := 0
	for i := len(rows) - 2; i > 0 && rowIsBlank(rows[i]); i-- {
		n++
	}
	return n
}

func rowIsBlank(row string) bool {
	return strings.TrimSpace(strings.Trim(ansi.Strip(row), "│")) == ""
}

// The box grows with the terminal. The whole point of the screen is writing a
// review body, and a fixed four rows meant doing it through a letterbox however
// much room was going spare.
func TestTheSummaryBoxGrowsWithTheScreen(t *testing.T) {
	short := composingModel(t, 120, 20)
	tall := composingModel(t, 120, 50)
	if tall.summaryEditor.area.Height() <= short.summaryEditor.area.Height() {
		t.Fatalf("a taller screen gave the box %d rows, no better than %d on a short one",
			tall.summaryEditor.area.Height(), short.summaryEditor.area.Height())
	}
	// And it is meaningfully bigger than the stream's box, which is the size this
	// screen used to inherit.
	if tall.summaryEditor.area.Height() <= commentEditorHeight {
		t.Errorf("on a 50-row body the box is still %d rows, no better than the stream's %d",
			tall.summaryEditor.area.Height(), commentEditorHeight)
	}
}

// Resizing the terminal mid-compose re-lays the box. Without this the screen
// keeps whatever size it opened at, so a pane that got taller leaves a gap and
// one that got shorter pushes the keys off.
func TestResizingWhileComposingRelaysTheBox(t *testing.T) {
	m := composingModel(t, 120, 20)
	before := m.summaryEditor.area.Height()
	m.SetSize(120, 44)
	if after := m.summaryEditor.area.Height(); after <= before {
		t.Fatalf("after growing the body to 44 the box is %d rows, was %d", after, before)
	}
	if got := lipgloss.Height(m.renderPublishOverlay(120, 44)); got != 44+panelBorderRows {
		t.Errorf("after the resize the screen renders %d rows, want %d", got, 44+panelBorderRows)
	}
}

// A terminal too short for the chrome still gets a usable box rather than a
// negative height — which textarea would clamp in its own way, or panic on.
func TestATinyScreenStillGetsABox(t *testing.T) {
	for _, height := range []int{0, 1, 5, 9} {
		if got := summaryAreaHeight(height); got < 3 {
			t.Errorf("a body of %d rows gave the box %d rows, want at least 3", height, got)
		}
	}
}

// The box wraps at the width its border is drawn at. It used to inherit the
// stream's hunk width — the right-hand pane — which is narrower than this
// screen, so the text stopped short of a border spanning the whole overlay.
func TestTheSummaryBoxUsesTheFullWidth(t *testing.T) {
	m := composingModel(t, 120, 30)
	// Measured against a box laid out at the overlay's width rather than against a
	// number: textarea.Width() reports the text column, which is one narrower than
	// the width it was set to because the prompt takes a cell.
	want := newCommentEditor(m.summaryEditor.anchor, publishOverlayInner(120))
	if got := m.summaryEditor.area.Width(); got != want.area.Width() {
		t.Errorf("the text area is %d wide, want %d (a box laid out at the overlay's width)", got, want.area.Width())
	}
	if m.summaryEditor.area.Width() <= editorAreaWidth(m.hunkWidth) {
		t.Errorf("the box is still sized to the stream's pane (%d), not the publish screen",
			m.summaryEditor.area.Width())
	}
	// And what is drawn agrees: every row of the box is as wide as its border, with
	// no strip of unused box down the right where the text stopped early.
	inner := publishOverlayInner(120)
	rows := strings.Split(m.summaryBoxView(inner), "\n")
	for i, r := range rows {
		if lipgloss.Width(r) != lipgloss.Width(rows[0]) {
			t.Fatalf("row %d of the box is %d wide, its border is %d", i, lipgloss.Width(r), lipgloss.Width(rows[0]))
		}
	}
	// Its border spans the overlay exactly: summaryBoxView sets a content width of
	// inner-2 and the border puts the two back.
	if got := lipgloss.Width(rows[0]); got != inner {
		t.Errorf("the box renders %d wide inside an overlay of %d", got, inner)
	}
}

// No cursorline band in the compose box. textarea paints one by default, which
// says nothing the blinking cursor is not already saying and paints a stripe
// across the box to say it — invisible in the stream's four rows, and the most
// obvious thing on the publish screen once the box fills the pane.
//
// Asserted on the style rather than on rendered output: lipgloss strips colour
// with no TTY, so a background fill leaves no trace in a test's string.
func TestTheComposeBoxHasNoCursorlineBand(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    commentEditor
	}{
		{"the stream's box", newCommentEditor(review.Anchor{Path: "a.go", LineHint: 1}, 80)},
		{"the publish summary", composingModel(t, 120, 30).summaryEditor},
	} {
		for _, s := range []struct {
			when  string
			style lipgloss.Style
		}{
			{"focused", tc.e.area.FocusedStyle.CursorLine},
			{"blurred", tc.e.area.BlurredStyle.CursorLine},
		} {
			if got := s.style.GetBackground(); got != lipgloss.TerminalColor(lipgloss.NoColor{}) {
				t.Errorf("%s (%s) paints a cursorline background: %v", tc.name, s.when, got)
			}
		}
	}
}
