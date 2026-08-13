package deckui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// What moves the keyboard between halves, and what does not.
//
// A click does: pointing at a half and pressing is how you say "this one", and
// that is what a mouse is for. A wheel does not. Scrolling is reading — you turn
// the wheel over the thing you want to look at, not the thing you want to type
// into — and a deck that moved the keyboard on the way past means a glance at the
// diff sends your next keystroke into it instead of the agent.
//
// Both still reach the half under the pointer. Where the event goes and what holds
// the keyboard are two questions, and answering them with one function is what tied
// them together.

// TestAClickMovesTheKeyboardToTheHalfItLandedIn.
func TestAClickMovesTheKeyboardToTheHalfItLandedIn(t *testing.T) {
	m, s := openedSplit(t, "v")
	b := m.childBox()
	left, right := s.boxes(b)

	s.rightFocused = false
	if _, cmd := m.Update(clickAt(right.x + right.w/2)); cmd != nil {
		_ = cmd
	}
	if !s.rightFocused {
		t.Error("a click in the right half did not move the keyboard to it")
	}
	if _, cmd := m.Update(clickAt(left.x + left.w/2)); cmd != nil {
		_ = cmd
	}
	if s.rightFocused {
		t.Error("a click in the left half did not move the keyboard back")
	}
}

// TestTheWheelDoesNotMoveTheKeyboard, over either half.
func TestTheWheelDoesNotMoveTheKeyboard(t *testing.T) {
	for _, over := range []string{"the other half", "its own half"} {
		t.Run(over, func(t *testing.T) {
			m, s := openedSplit(t, "v")
			b := m.childBox()
			left, right := s.boxes(b)
			s.rightFocused = false

			x := right.x + right.w/2
			if over == "its own half" {
				x = left.x + left.w/2
			}
			m.Update(wheelAt(x))
			if s.rightFocused {
				t.Error("the wheel moved the keyboard to the half it was over")
			}
		})
	}
}

// TestTheWheelStillReachesTheHalfItIsOver. Not moving the keyboard is not the same
// as doing nothing: the point of scrolling over a half is to scroll that half, so
// the event has to arrive even though the keys stay where they were.
func TestTheWheelStillReachesTheHalfItIsOver(t *testing.T) {
	m, s := openedSplit(t, "v")
	b := m.childBox()
	_, right := s.boxes(b)
	s.rightFocused = false

	p, ok := s.right.(*panePopover)
	if !ok {
		t.Fatalf("the right half is a %T", s.right)
	}
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", p.term)
	}
	f.askForMouse() // a program that never asked is sent nothing at all

	m.Update(wheelAt(right.x + right.w/2))
	if len(f.miceSeen()) == 0 {
		t.Error("the wheel over the right half never reached it")
	}
	if s.rightFocused {
		t.Error("delivering the wheel moved the keyboard after all")
	}
}

func wheelAt(x int) tea.MouseMsg {
	return tea.MouseWheelMsg{X: x, Y: 5, Button: tea.MouseWheelUp}
}
