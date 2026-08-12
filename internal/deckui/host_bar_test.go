package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// barText is the bar as read rather than as sent: SGR sequences are full of
// digits, and the badge is a count, so an assertion about numbers has to be made
// against the plain text.
func barText(m *Model, w int) string { return ansi.Strip(m.renderHostBar(w)) }

// waitingRows is a deck where two workspaces want you and one is working, so the
// badge has something to say. Waiting needs the unread mark as well as the
// status — see workspace.Classify.
func waitingRows() []Item {
	return []Item{
		{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp", Status: "waiting", Unread: true},
		{ProjectName: "proj", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/tmp", Status: "waiting", Unread: true},
		{ProjectName: "proj", WorkspaceName: "three", Path: "/tmp", RepoRoot: "/tmp", Status: "working"},
	}
}

// paneOn opens the agent pane on a deck holding the given rows.
func paneOn(t *testing.T, items []Item) (Model, *panePopover) {
	t.Helper()
	m := New(items, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 120, 40
	m.itemsAll = append([]Item(nil), items...)
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	return m, p
}

// TestTheBarSaysWhatNeedsYou. The deck is where the work is watched from and a
// pane is most of the time spent there, so the numbers that are the reason to go
// back to the row list were exactly the wrong thing to be visible only from it.
func TestTheBarSaysWhatNeedsYou(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	bar := barText(&m, 120)
	// Two waiting, one working — the badge is dots and numbers, so the numbers are
	// what a test can look for.
	if !strings.Contains(bar, "2") || !strings.Contains(bar, "1") {
		t.Errorf("the bar does not carry the attention badge: %q", bar)
	}
}

// TestTheBarSaysNothingWhenNothingNeedsYou, matching the deck's own title row:
// the zero state renders nothing at all rather than a phrasing of "no".
func TestTheBarSaysNothingWhenNothingNeedsYou(t *testing.T) {
	m, _ := paneOn(t, []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}})
	bar := barText(&m, 120)
	if strings.ContainsAny(bar, "0123456789") {
		t.Errorf("the bar invented a count with nothing to report: %q", bar)
	}
	if !strings.Contains(bar, "ws") {
		t.Errorf("the bar lost its label: %q", bar)
	}
}

// TestTheBarKeepsTheLeaveKeyAtEveryWidth. Three things want one row, and the
// order they are given up in is fixed: the label first, because the screen below
// already says what it is; then the badge, which is a thing leaving will show
// you; never the leave key, which is how you leave.
func TestTheBarKeepsTheLeaveKeyAtEveryWidth(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 40, 24, 16, 8, 4} {
		bar := m.renderHostBar(w)
		if strings.Contains(bar, "\n") {
			t.Errorf("at %d columns the bar wrapped: %q", w, bar)
		}
		if !strings.Contains(bar, PaneLeaveKey) {
			t.Errorf("at %d columns the bar dropped the leave key: %q", w, bar)
		}
	}
}

// TestTheBarFillsExactlyTheWidthItWasGiven. It is a row of the frame rather than
// a thing inside a box, so short is a hole the previous frame shows through and
// long pushes the pane below it out of alignment.
func TestTheBarFillsExactlyTheWidthItWasGiven(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 60, 40, 30, 12} {
		if got := lipgloss.Width(m.renderHostBar(w)); got != w {
			t.Errorf("at %d columns the bar rendered %d wide: %q", w, got, m.renderHostBar(w))
		}
	}
}

// TestTheBadgeFollowsTheDeckWhileAPaneIsOpen is the point of putting it up here:
// an agent finishing its turn behind the pane has to show up without leaving it.
func TestTheBadgeFollowsTheDeckWhileAPaneIsOpen(t *testing.T) {
	m, _ := paneOn(t, []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}})
	if before := barText(&m, 120); strings.ContainsAny(before, "123456789") {
		t.Fatalf("something already needs you: %q", before)
	}

	// What a refresh arriving behind the pane does.
	m.itemsAll = waitingRows()

	if after := barText(&m, 120); !strings.Contains(after, "2") {
		t.Errorf("a row that started waiting while the pane was open never showed up: %q", after)
	}
}

// TestTheBarIsInTheSameCellsWhicheverArrangementIsUp is the whole point of the
// unit. The badge and the leave key were once drawn by the pane, inside its
// border, and separately by the split, on the row above both halves — so the same
// three things sat in two different places depending on how many programs were on
// screen, and neither arrangement could be glanced at the way the other trained
// you to.
//
// Asserted through the deck's own frame, since where the bar ends up is the
// composition's answer rather than the bar's.
func TestTheBarIsInTheSameCellsWhicheverArrangementIsUp(t *testing.T) {
	lone, _ := paneOn(t, waitingRows())
	split, _ := openedSplit(t, "v")
	split.itemsAll = waitingRows()

	for _, tc := range []struct {
		what string
		m    *Model
	}{{"one pane", &lone}, {"a split", &split}} {
		row := ansi.Strip(strings.Split(tc.m.render(), "\n")[0])
		if lipgloss.Width(row) != tc.m.width {
			t.Errorf("with %s the bar is %d columns, the terminal is %d: %q",
				tc.what, lipgloss.Width(row), tc.m.width, row)
		}
		if !strings.Contains(row, PaneLeaveKey) {
			t.Errorf("with %s row 0 is not the bar: %q", tc.what, row)
		}
		if !strings.Contains(row, "2") {
			t.Errorf("with %s row 0 lost the badge: %q", tc.what, row)
		}
		if got := tc.m.childBox().y; got != hostBarRows {
			t.Errorf("with %s the child starts on row %d, want %d", tc.what, got, hostBarRows)
		}
	}
}

// TestTheBarNamesTheHalfTheKeysAreIn. The two halves are the same workspace, so
// listing both of them named the one thing that cannot tell them apart; the
// accent-vs-muted border already says which has the keys. What the bar adds is
// the kind, of the half you are actually typing at.
func TestTheBarNamesTheHalfTheKeysAreIn(t *testing.T) {
	m, s := openedSplit(t, "v")
	if _, ok := s.focused().(*panePopover); !ok {
		t.Skip("this split's focused half is not a pane")
	}
	want := s.focused().(*panePopover).label
	if bar := barText(&m, 200); !strings.Contains(bar, want) {
		t.Errorf("the bar does not name the focused half %q: %q", want, bar)
	}

	s.rightFocused = !s.rightFocused
	other, ok := s.focused().(*panePopover)
	if !ok {
		return
	}
	if bar := barText(&m, 200); !strings.Contains(bar, other.label) {
		t.Errorf("the bar did not follow the keys to %q: %q", other.label, bar)
	}
}

// TestAnArmedPrefixTakesTheWholeBar. The verb menu was painted over the split's
// bottom border in the first cut, for want of a row anything owned; this row is
// that row, so nothing is drawn over a border.
func TestAnArmedPrefixTakesTheWholeBar(t *testing.T) {
	m, s := openedSplit(t, "v")
	s.prefixArmed = true
	bar := barText(&m, 200)
	if !strings.Contains(bar, "zoom") {
		t.Errorf("an armed prefix does not show its verbs: %q", bar)
	}
	if strings.Contains(bar, "\n") {
		t.Errorf("the armed prefix wrapped off its row: %q", bar)
	}
}

// TestTheBarSaysNothingAboveAnOverlay. Help, the jobs overlay and the confirms
// are awp's own text in a box on a blank canvas — they have nothing to say about
// a workspace you are sitting inside, and a bar above one would be chrome around
// chrome.
func TestTheBarSaysNothingAboveAnOverlay(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	if !m.hostsBar() {
		t.Fatal("a pane does not get the bar")
	}
	p.close(&m)
	m.active = nil
	if m.hostsBar() {
		t.Error("the deck's own row list is being given a host bar as well")
	}
	if got := m.childBox().y; got != 0 {
		t.Errorf("the row list starts on row %d, want 0", got)
	}
}
