package deckui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Opening the deck where you left it.
//
// The two halves that have to hold together: the arrangement is written down on every
// change (not at exit, which a killed deck never reaches), and it is acted on at the
// first moment a pane can be given a size.

// sizeMsg is the terminal telling the deck how big it is, which is the moment a resume
// becomes possible.
func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// resumeDeck is a deck told to open into an arrangement, before any size has arrived.
func resumeDeck(t *testing.T, a Arrangement) Model {
	t.Helper()
	m := sidebarDeck(t)
	m.width, m.height = 0, 0
	return m.WithArrangement(a)
}

// TestTheDeckOpensIntoTheRememberedPane. The whole point: the row list becomes the way
// out rather than the way in.
func TestTheDeckOpensIntoTheRememberedPane(t *testing.T) {
	m := resumeDeck(t, Arrangement{Project: "proj", Workspace: "busy", Kind: PaneKindAgent})
	next, _ := m.Update(sizeMsg(200, 40))
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("the deck opened on %T, want a pane (status %q)", m.active, m.status)
	}
	if p.workspace != "busy" {
		t.Errorf("it opened %q, want busy", p.workspace)
	}
	t.Cleanup(func() { p.close(&m) })
}

// TestTheCursorLandsOnTheResumedRow, so leaving the pane puts you on the row it was.
// reopenPane already does this for ctrl+\; resuming has to inherit it or the first
// ctrl+\ of a session drops you somewhere unrelated.
func TestTheCursorLandsOnTheResumedRow(t *testing.T) {
	m := resumeDeck(t, Arrangement{Project: "proj", Workspace: "busy", Kind: PaneKindAgent})
	next, _ := m.Update(sizeMsg(200, 40))
	m = next.(Model)
	if p, ok := m.active.(*panePopover); ok {
		t.Cleanup(func() { p.close(&m) })
	}
	it, ok := m.selected()
	if !ok {
		t.Fatal("nothing is selected after the resume")
	}
	if it.WorkspaceName != "busy" {
		t.Errorf("the cursor is on %q, want busy", it.WorkspaceName)
	}
}

// TestNothingRememberedOpensTheRowList — every deck before this one, and the first run
// of this one.
func TestNothingRememberedOpensTheRowList(t *testing.T) {
	m := resumeDeck(t, Arrangement{})
	next, _ := m.Update(sizeMsg(200, 40))
	if m = next.(Model); m.active != nil {
		t.Errorf("a deck with nothing remembered opened %T", m.active)
	}
}

// TestAResumeHappensOnceAndNotOnEveryResize.
//
// A resize is a WindowSizeMsg too. Left armed, the flag would drag you back into the
// pane every time the terminal changed shape — including the repaint that follows
// leaving the pane on purpose, which would make ctrl+\ appear not to work.
func TestAResumeHappensOnceAndNotOnEveryResize(t *testing.T) {
	m := resumeDeck(t, Arrangement{Project: "proj", Workspace: "busy", Kind: PaneKindAgent})
	next, _ := m.Update(sizeMsg(200, 40))
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("the resume did not open a pane: %T", m.active)
	}
	p.close(&m)
	if m.active != nil {
		t.Fatalf("closing the pane left %T", m.active)
	}
	next, _ = m.Update(sizeMsg(180, 38))
	if m = next.(Model); m.active != nil {
		t.Errorf("a later resize re-opened %T", m.active)
	}
}

// TestAResumeOfAWorkspaceThatIsGoneOpensTheRowList, and says so. This is the escape
// hatch: a deck that would not open because the thing it wanted to resume had been
// deleted is a deck you would have to repair a state file to use.
func TestAResumeOfAWorkspaceThatIsGoneOpensTheRowList(t *testing.T) {
	m := resumeDeck(t, Arrangement{Project: "proj", Workspace: "deleted-last-week", Kind: PaneKindAgent})
	next, _ := m.Update(sizeMsg(200, 40))
	m = next.(Model)
	if m.active != nil {
		t.Errorf("a deleted workspace opened %T", m.active)
	}
	if m.status == "" {
		t.Error("the deck resumed nothing and said nothing about it")
	}
}

// TestOpeningAPaneRecordsTheArrangement, so the next deck has something to open into.
// On every change rather than at exit: a deck is killed, or its terminal closes, and a
// save at exit is a save that does not happen on the occasions you most wanted it.
func TestOpeningAPaneRecordsTheArrangement(t *testing.T) {
	var saved []Arrangement
	m := sidebarDeck(t)
	m = m.WithArrangementSaver(func(a Arrangement) error {
		saved = append(saved, a)
		return nil
	})
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if p, ok := m.active.(*panePopover); ok {
		t.Cleanup(func() { p.close(&m) })
	}
	if len(saved) == 0 {
		t.Fatal("opening a pane recorded nothing")
	}
	last := saved[len(saved)-1]
	if !last.Set() {
		t.Errorf("the recorded arrangement names no workspace: %+v", last)
	}
	if last.Kind != PaneKindAgent {
		t.Errorf("it recorded kind %q, want the agent's", last.Kind)
	}
}

// TestAnArrangementSaverIsOptional — the mini-deck and every test deck have none, the
// same way they have no ScopeSaver.
func TestAnArrangementSaverIsOptional(t *testing.T) {
	m := sidebarDeck(t)
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent) // must not panic
	m = next.(Model)
	if p, ok := m.active.(*panePopover); ok {
		t.Cleanup(func() { p.close(&m) })
	}
}

// TestAnArrangementRoundTripsThroughItsStoredForm. The two conversions are a seam
// between a UI value and a file format, and a field dropped on the way through would
// come back as a subtly different arrangement — a split remembered as one pane, or a
// divider snapped back to even.
func TestAnArrangementRoundTripsThroughItsStoredForm(t *testing.T) {
	want := Arrangement{
		Project:   "thicket",
		Workspace: "analytics-probe",
		Kind:      PaneKindAgent,
		RightKind: SplitKindDiff,
		Split:     true,
		LeftFrac:  0.62,
	}
	if got := paneArrangementFrom(want).exported(); got != want {
		t.Errorf("round trip gave %+v, want %+v", got, want)
	}
}

// TestAShellHalfSurvivesTheRoundTrip. The shell's kind is the empty string, so a
// reader inferring "no split" from an emptiness test would make `|s` the one
// arrangement the deck could not remember — the trap paneArrangement.hasRight exists
// for, restated here because a file format is a second place to get it wrong.
func TestAShellHalfSurvivesTheRoundTrip(t *testing.T) {
	want := Arrangement{Workspace: "ws", Kind: PaneKindAgent, RightKind: "", Split: true}
	got := paneArrangementFrom(want).exported()
	if !got.Split {
		t.Errorf("a split with a shell half came back as one pane: %+v", got)
	}
}
