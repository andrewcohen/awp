package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// headerText is the header as read rather than as sent: SGR sequences are full of
// digits, and the badge is a count, so an assertion about numbers has to be made
// against the plain text.
func headerText(m *Model, p *panePopover, w int) string { return ansi.Strip(p.header(m, w)) }

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

// TestThePaneHeaderSaysWhatNeedsYou. The deck is where the work is watched from
// and a pane is most of the time spent there, so the numbers that are the reason
// to go back to the row list were exactly the wrong thing to be visible only from
// it.
func TestThePaneHeaderSaysWhatNeedsYou(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	header := headerText(&m, p, 120)
	// Two waiting, one working — the badge is dots and numbers, so the numbers are
	// what a test can look for.
	if !strings.Contains(header, "2") || !strings.Contains(header, "1") {
		t.Errorf("the pane header does not carry the attention badge: %q", header)
	}
}

// TestThePaneHeaderSaysNothingWhenNothingNeedsYou, matching the deck's own title
// row: the zero state renders nothing at all rather than a phrasing of "no".
func TestThePaneHeaderSaysNothingWhenNothingNeedsYou(t *testing.T) {
	m, p := paneOn(t, []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}})
	header := headerText(&m, p, 120)
	if strings.ContainsAny(header, "0123456789") {
		t.Errorf("the pane header invented a count with nothing to report: %q", header)
	}
	if !strings.Contains(header, "ws") {
		t.Errorf("the pane header lost its label: %q", header)
	}
}

// TestThePaneHeaderKeepsTheLeaveKeyAtEveryWidth. Three things want one row, and
// the order they are given up in is fixed: the label first, because the screen
// below already says what it is; then the badge, which is a thing leaving will
// show you; never the leave key, which is how you leave.
func TestThePaneHeaderKeepsTheLeaveKeyAtEveryWidth(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 40, 24, 16, 8, 4} {
		header := p.header(&m, w)
		if strings.Contains(header, "\n") {
			t.Errorf("at %d columns the header wrapped: %q", w, header)
		}
		if !strings.Contains(header, PaneLeaveKey) {
			t.Errorf("at %d columns the header dropped the leave key: %q", w, header)
		}
	}
}

// TestThePaneHeaderFitsTheWidthItWasGiven. It sits inside the pane's border, so a
// row one column too wide pushes the border out and the frame stops lining up.
func TestThePaneHeaderFitsTheWidthItWasGiven(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 60, 40, 30} {
		if got := lipgloss.Width(p.header(&m, w)); got > w {
			t.Errorf("at %d columns the header rendered %d wide: %q", w, got, p.header(&m, w))
		}
	}
}

// TestTheBadgeFollowsTheDeckWhileThePaneIsOpen is the point of the feature: an
// agent finishing its turn behind the pane has to show up without leaving it.
func TestTheBadgeFollowsTheDeckWhileThePaneIsOpen(t *testing.T) {
	m, p := paneOn(t, []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}})
	if before := headerText(&m, p, 120); strings.ContainsAny(before, "123456789") {
		t.Fatalf("something already needs you: %q", before)
	}

	// What a refresh arriving behind the pane does.
	m.itemsAll = waitingRows()

	if after := headerText(&m, p, 120); !strings.Contains(after, "2") {
		t.Errorf("a row that started waiting while the pane was open never showed up: %q", after)
	}
}
