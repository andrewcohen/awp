package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Which half has the keyboard, said by the border.
//
// In a split exactly one half may look like the active one, and the border is the
// only chrome that belongs to the half rather than to what is inside it. The pane
// half has always had one; the diff half had none, so focus was carried entirely by
// the pane going muted — a screen that says "not here" beside a half that never says
// "here".

// borderHue is the SGR parameters the top border row of a rendered half is painted
// with — the whole escape, not a palette token, because what reaches the terminal is
// lipgloss's translation of the token and the two are not the same string ("8"
// leaves as `90`).
//
// Compared between halves rather than against a colour name for that reason: the
// invariant worth pinning is that the two halves agree about which hue means focus,
// and that focused and blurred differ. A test naming the escape would pass a change
// that painted the diff half's border in a hue of its own.
func borderHue(half string) string {
	top := strings.SplitN(half, "\n", 2)[0]
	seq, _, _ := strings.Cut(strings.TrimPrefix(top, "\x1b["), "m")
	return seq
}

// framed reports whether a rendered block wears one border around the whole of it.
//
// Counted rather than looked for, because the viewer draws rounded borders of its
// own — the file list and the diff body are each in one — so the corner glyph says
// nothing on its own. An outer frame contributes exactly one top-left corner to the
// first row; the viewer's own panes contribute one each and sit side by side.
func framed(block string) bool {
	top := ansi.Strip(strings.SplitN(block, "\n", 2)[0])
	return strings.Count(top, lipgloss.RoundedBorder().TopLeft) == 1
}

// halfBorders renders both halves of a split and returns their top border rows.
func halfBorders(t *testing.T, m *Model, s *splitModal) (left, right string) {
	t.Helper()
	lb, rb := s.boxes(m.childBox())
	return renderChild(m, s.left, lb.focus(!s.rightFocused)),
		renderChild(m, s.right, rb.focus(s.rightFocused))
}

// TestTheDiffHalfHasABorderThatFollowsFocus, which is the whole of #338. Both
// directions: the focused half's border is the accent, the other's is muted, and
// moving the keyboard swaps them.
func TestTheDiffHalfHasABorderThatFollowsFocus(t *testing.T) {
	m, s := splitWithDiff(t)
	if !s.rightFocused {
		t.Fatal("precondition: |c leaves the keyboard in the diff half")
	}

	blurredPane, focusedDiff := halfBorders(t, &m, s)
	if !framed(focusedDiff) {
		t.Fatalf("the diff half has no border of its own:\n%s", ansi.Strip(focusedDiff))
	}

	s.rightFocused = false
	focusedPane, blurredDiff := halfBorders(t, &m, s)

	// The diff half says something different depending on where the keys are, which
	// is the bug: before this it said nothing either way.
	if borderHue(focusedDiff) == borderHue(blurredDiff) {
		t.Errorf("the diff half's border does not follow focus (both %q)", borderHue(focusedDiff))
	}
	// And it says it the way the pane half does. Two vocabularies for one question is
	// worse than one of them being absent, since the halves are read side by side.
	if got, want := borderHue(focusedDiff), borderHue(focusedPane); got != want {
		t.Errorf("the focused diff half's border is %q, the focused pane's is %q", got, want)
	}
	if got, want := borderHue(blurredDiff), borderHue(blurredPane); got != want {
		t.Errorf("the blurred diff half's border is %q, the blurred pane's is %q", got, want)
	}
}

// TestAWholeScreenDiffHasNoBorder. Nothing else is on screen, so there is no
// question for a border to answer — and two rows and two columns of a diff are
// worth more than a frame around the only thing in the terminal.
func TestAWholeScreenDiffHasNoBorder(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope, int) (string, error) { return diffModalSample, nil })
	m, _ = pressKey(m, "c")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("active is %T, want the diff viewer", m.active)
	}
	body, _ := dm.view(&m, m.childBox())
	if framed(body) {
		t.Errorf("a whole-screen diff drew a border around itself:\n%s", ansi.Strip(body))
	}
}

// TestTheBorderedHalfStillFillsItsBox. The border's cells come out of the body
// rather than being added around it: a half that renders wider or narrower than its
// box shifts everything inside it when the composed halves are centred (#339).
func TestTheBorderedHalfStillFillsItsBox(t *testing.T) {
	m, s := splitWithDiff(t)
	b := m.childBox()
	_, rb := s.boxes(b)
	half := renderChild(&m, s.right, rb.focus(true))
	for i, line := range strings.Split(half, "\n") {
		if got := ansi.StringWidth(line); got != rb.w {
			t.Fatalf("row %d of the diff half is %d columns wide, want %d", i, got, rb.w)
		}
	}
	if got, want := lipgloss.Height(half), b.h; got != want {
		t.Errorf("the diff half is %d rows tall, want %d", got, want)
	}
}
