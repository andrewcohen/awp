package deckui

import (
	"os/exec"
	"testing"

	"github.com/andrewcohen/awp/internal/vterm"
)

// A pane is a client of a session, and the session obeys exactly one of them.
//
// zmx hands a session's leadership to a client at attach only when there is no
// leader, and otherwise moves it on the first keyboard input from another client —
// mouse reports do not count, being terminal chatter rather than someone typing. So
// a pane left running behind the one on screen keeps the session it was attached
// to, and everything the new pane says is ignored: its mouse events are dropped and
// the size it reports is refused, which is a pane you cannot click in and a split
// half that never reflows until you press a key.
//
// The deck's half of that bargain is not to hold two clients of the same session at
// once, and to let go of the old one *before* the new one attaches. See
// Model.closeArrangement.

// termLog records the terminals a deck opens, and whether every earlier one had
// already been closed when each was opened.
type termLog struct {
	terms       []*fakeTerm
	aloneAtOpen []bool
}

// opener is the Model.openTerm to install; it wraps the fake and takes the census.
func (l *termLog) opener(gen, w, h int, c *exec.Cmd, _ vterm.HostColors) (vterm.Hosted, error) {
	alone := true
	for _, prev := range l.terms {
		if !prev.isClosed() {
			alone = false
		}
	}
	l.aloneAtOpen = append(l.aloneAtOpen, alone)
	ft := newFakeTerm(gen, w, h, c)
	l.terms = append(l.terms, ft)
	return ft, nil
}

// TestOpeningAPaneClosesTheOneItReplacesFirst — the leadership invariant, in the
// gesture that broke it: going to another workspace from the strip.
//
// Opening used to install the new pane over the old one and drop it, leaving one
// live `zmx attach` per pane per deck run — each of them still leading a session
// the deck would open again later.
func TestOpeningAPaneClosesTheOneItReplacesFirst(t *testing.T) {
	var log termLog
	m := sidebarDeck(t)
	m.openTerm = log.opener

	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	first, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened: active is %T, status %q", m.active, m.status)
	}

	other := m.itemsAll[2]
	m.goToSidebarRow(other)
	second, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("going to %s opened %T, want a pane (status %q)", other.WorkspaceName, m.active, m.status)
	}
	t.Cleanup(func() { second.close(&m) })

	if second == first {
		t.Fatal("the deck stayed in the first pane, so this proves nothing")
	}
	if !fakeOf(t, first).isClosed() {
		t.Error("the pane that was replaced is still running its client")
	}
	if len(log.aloneAtOpen) != 2 {
		t.Fatalf("the deck opened %d terminals, want 2", len(log.aloneAtOpen))
	}
	if !log.aloneAtOpen[1] {
		t.Error("the second pane attached while the first was still holding its session")
	}
}

// TestOpeningAPaneClosesTheOneParkedBehindAModal.
//
// A verb pressed on the strip steps the arrangement aside into overlayHost rather
// than closing it, so it can come back when the question is answered. A pane opened
// from that modal instead of answering it leaves the parked one holding its session
// with nothing on the deck able to reach it again.
func TestOpeningAPaneClosesTheOneParkedBehindAModal(t *testing.T) {
	var log termLog
	m := sidebarDeck(t)
	m.openTerm = log.opener

	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	parked, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened: active is %T, status %q", m.active, m.status)
	}
	m.suspendForOverlay()
	if m.overlayHost != parked {
		t.Fatalf("the arrangement did not park: overlayHost is %T", m.overlayHost)
	}

	m.goToSidebarRow(m.itemsAll[2])
	if p, ok := m.active.(*panePopover); ok {
		t.Cleanup(func() { p.close(&m) })
	}
	if !fakeOf(t, parked).isClosed() {
		t.Error("the parked pane is still running its client")
	}
	if m.overlayHost != nil {
		t.Errorf("a closed pane is still parked as the overlay host (%T)", m.overlayHost)
	}
}
