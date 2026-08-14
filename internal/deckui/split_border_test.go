package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Which half has the keyboard, said by the border.
//
// The diff half draws no border of its own — the viewer frames each of its panes, and
// a frame around those is a second line beside the first. What follows focus is the
// hue of the *viewer's* focused pane, which used to be the accent whatever the deck
// was doing: a split where the agent was being typed into still had a teal-bordered
// pane in the diff half claiming the keyboard.

// sgrFor is the escape a palette token actually leaves as.
//
// Rendered rather than written down, because the two are not the same string: the
// tokens are ANSI 16 indices and lipgloss emits `36` for "6" and `90` for "8". A test
// naming the escape would be pinning lipgloss's translation table rather than awp's
// choice of hue.
func sgrFor(token string) string {
	painted := lipgloss.NewStyle().Foreground(lipgloss.Color(token)).Render("x")
	seq, _, _ := strings.Cut(strings.TrimPrefix(painted, "\x1b["), "m")
	return seq
}

// paints reports whether the block sets this hue anywhere in it.
//
// Anywhere rather than on a particular glyph: which of the viewer's panes comes first
// depends on whether the left column is showing, so a test that read "the first
// border corner" was reading the file list in one layout and the diff body in
// another.
func paints(block, sgr string) bool {
	return strings.Contains(block, "\x1b["+sgr+"m")
}

// halves renders both halves of a split with the keyboard where the split says it is,
// and returns them in (focused, blurred) order.
func halves(t *testing.T, m *Model, s *splitModal) (focused, blurred string) {
	t.Helper()
	lb, rb := s.boxes(m.childBox())
	left := renderChild(m, s.left, lb.focus(!s.rightFocused))
	right := renderChild(m, s.right, rb.focus(s.rightFocused))
	if s.rightFocused {
		return right, left
	}
	return left, right
}

// TestTheDiffHalfDrawsNoBorderOfItsOwn. The viewer already frames its panes, so a
// border around the half is a second line against the first — which is what the first
// attempt at #338 did, and it was visible immediately.
func TestTheDiffHalfDrawsNoBorderOfItsOwn(t *testing.T) {
	m, s := splitWithDiff(t)
	dm, ok := s.right.(*diffModal)
	if !ok {
		t.Fatalf("the right half is %T", s.right)
	}
	_, rb := s.boxes(m.childBox())
	half := renderChild(&m, s.right, rb.focus(true))

	// The half against what the viewer alone drew into the same width: a frame shows up
	// as a box corner the viewer did not put there.
	inner := dm.inner.Body(rb.w-panelCols, rb.h+footerRows-diffModalChrome)
	corner := lipgloss.RoundedBorder().TopLeft
	got := strings.Count(firstRow(half), corner)
	want := strings.Count(firstRow(inner), corner)
	if got != want {
		t.Errorf("the diff half's first row has %d box corners, the viewer's own has %d:\n%s",
			got, want, firstRow(ansi.Strip(half)))
	}
}

func firstRow(block string) string { return strings.SplitN(ansi.Strip(block), "\n", 2)[0] }

// TestTheDiffHalfsBorderFollowsFocus, which is #338: the viewer's focused pane wears
// the accent only while the deck's keyboard is in that half.
func TestTheDiffHalfsBorderFollowsFocus(t *testing.T) {
	m, s := splitWithDiff(t)
	if !s.rightFocused {
		t.Fatal("precondition: |c leaves the keyboard in the diff half")
	}
	accent := sgrFor(colAccent)

	focusedDiff, _ := halves(t, &m, s)
	if !paints(focusedDiff, accent) {
		t.Error("the focused diff half paints nothing in the accent, so nothing says the keys are in it")
	}

	s.rightFocused = false
	_, blurredDiff := halves(t, &m, s)
	if paints(blurredDiff, accent) {
		t.Error("the blurred diff half still paints the accent — it goes on claiming the keyboard")
	}
}

// TestOnlyOneHalfClaimsTheKeyboard is the point of the exercise: before this, the diff
// half's focused pane and the pane half's border were both the accent at once, so two
// things on screen said the keys were in them.
func TestOnlyOneHalfClaimsTheKeyboard(t *testing.T) {
	m, s := splitWithDiff(t)
	accent := sgrFor(colAccent)
	for _, focusRight := range []bool{true, false} {
		s.rightFocused = focusRight
		focused, blurred := halves(t, &m, s)
		side := map[bool]string{true: "right", false: "left"}[focusRight]
		if !paints(focused, accent) {
			t.Errorf("with the keyboard on the %s, that half does not paint the accent", side)
		}
		if paints(blurred, accent) {
			t.Errorf("with the keyboard on the %s, the other half paints the accent too", side)
		}
	}
}

// TestAWholeScreenDiffIsNeverBlurred. HostBlurred is about being one of two halves, so
// a viewer that owns the terminal must not dim: there is nowhere else for the keyboard
// to be, and a diff whose every pane looks inactive reads as a dead screen.
func TestAWholeScreenDiffIsNeverBlurred(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope, int) (string, error) { return diffModalSample, nil })
	m, _ = pressKey(m, "c")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("active is %T, want the diff viewer", m.active)
	}
	full, _ := dm.view(&m, m.childBox())
	if dm.inner.HostBlurred {
		t.Error("a whole-screen diff was told the keyboard is somewhere else")
	}
	if !paints(full, sgrFor(colAccent)) {
		t.Error("a whole-screen diff draws no focused pane at all")
	}
}
