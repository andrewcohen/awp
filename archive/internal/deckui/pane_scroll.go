package deckui

import (
	tea "charm.land/bubbletea/v2"
)

// Scrolling back through a pane's history with the wheel.
//
// The emulator has been keeping the scrollback all along — see vterm.Hosted's
// ScrollBy — and until it was asked for, none of it was reachable: a pane rendered
// its viewport and the viewport was the tail. So a shell whose output had run past
// the window had simply lost the part above it, as far as awp was concerned.
//
// This is the same gate the selection uses. A program that asked for the mouse
// gets the wheel, because a wheel event is one of the things it asked for and it
// has its own idea of what scrolling means — an agent's transcript, nvim's buffer,
// a pager's file. What is left is the pane whose program ignores the mouse, where
// nothing else is going to answer.

// paneWheelRows is how far one notch of the wheel moves the view.
//
// Three rows, which is what a terminal emulator's own default is, and the reason
// is legibility rather than speed: a single row per notch makes a long scroll a
// wrist exercise, and a screenful per notch means every notch replaces the entire
// screen, so there is no overlap to read continuity from.
const paneWheelRows = 3

// scrollMouse moves the pane's view for a wheel event, reporting whether it took
// it.
//
// Bounded by the terminal, not here: ScrollBy clamps to the history that exists,
// so a wheel-up at the top of the scrollback and a wheel-down on the live tail
// are both no-ops. Reporting them as taken anyway is deliberate — the event was
// still the wheel's, and the alternative is falling through to a program that
// asked for no mouse events at all.
func (p *panePopover) scrollMouse(msg tea.MouseMsg) bool {
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok {
		return false
	}
	// Over the pane's own cells, so a wheel event on the border or in another
	// region is not this pane's to consume.
	if _, inside := paneMouse(msg, p.lastBox); !inside {
		return false
	}
	switch wheel.Button {
	case tea.MouseWheelUp:
		p.term.ScrollBy(-paneWheelRows)
	case tea.MouseWheelDown:
		p.term.ScrollBy(paneWheelRows)
	default:
		// A horizontal wheel. A terminal has no columns to scroll — its rows are
		// as wide as it is — so there is nothing to do with one.
		return false
	}
	// The selection's rows are rows of the view, and the view has just moved out
	// from under them: keeping the highlight would mark whatever text scrolled
	// into those rows instead of what was picked out.
	p.clearSelection()
	return true
}

// snapToTail puts the pane back on the live tail, which is what typing means.
//
// A key press is aimed at the program, and the program answers on the bottom row
// — so reading history is over the moment you type, and a pane that stayed put
// would swallow the response into rows above the view. Real terminals do the same
// thing for the same reason.
//
// New output does not snap. That is also what a terminal does, and it is what
// makes reading back through a chatty process possible at all: output arriving
// while you are up in the history would otherwise yank the view away mid-sentence.
func (p *panePopover) snapToTail() { p.term.ScrollToBottom() }

// paneBehind is how far back the pane is showing, and whether it is behind at all.
//
// Asked of the terminal every frame rather than tracked here, because the answer
// changes without awp doing anything: output arriving pushes rows into the history
// above a view that has not moved. libghostty's header says as much — there is no
// change notification for scroll state, so a caller polls.
func (p *panePopover) paneBehind() (rows int, behind bool) {
	above, atBottom := p.term.Scrollback()
	if atBottom {
		return 0, false
	}
	return above, true
}
