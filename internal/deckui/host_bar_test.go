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

// prRow is the workspace paneOn opens, carrying a PR whose CI is failing and a
// dev loop three units in with a gate red.
func prRow() []Item {
	return []Item{{
		ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp",
		PRNumber: 412,
		DevLoop: &DevLoopSummary{
			Done: 3, Total: 7,
			Gates: map[string]string{"gofmt": "pass", "vet": "pass", "test": "fail"},
		},
	}}
}

// TestTheBarSaysWhetherTheThingOnScreenIsBroken. The badge says something wants
// you somewhere; this says whether what you are looking at has fallen over —
// which from inside a pane you would otherwise leave to find out.
func TestTheBarSaysWhetherTheThingOnScreenIsBroken(t *testing.T) {
	m, _ := paneOn(t, prRow())
	bar := barText(&m, 200)
	if !strings.Contains(bar, "#412") {
		t.Errorf("the bar does not name the workspace's PR: %q", bar)
	}
	if !strings.Contains(bar, "3/7") {
		t.Errorf("the bar does not carry the dev loop's progress: %q", bar)
	}
	if !strings.Contains(bar, gateGlyphFail+"1") {
		t.Errorf("the bar does not report the one failing gate: %q", bar)
	}
	// The gate digest reports the worst result, so a red gate must not be
	// reported as two passes.
	if strings.Contains(bar, gateGlyphPass) {
		t.Errorf("the bar reports a pass while a gate is failing: %q", bar)
	}
}

// TestTheBarUsesNoWordsForState. The rule the badge already followed, applied to
// everything else the row reports: a coloured glyph and a number, so the row is
// glanced at rather than parsed. The label and the leave key are the only text.
func TestTheBarUsesNoWordsForState(t *testing.T) {
	m, _ := paneOn(t, prRow())
	bar := barText(&m, 200)
	// The label and hint are the allowed text; nothing else may spell a state.
	rest := strings.ReplaceAll(bar, m.hostBarLabel(), "")
	rest = strings.ReplaceAll(rest, m.hostBarHint(), "")
	for _, word := range []string{"pass", "fail", "pending", "gates", "waiting", "working", "PR", "unread", "columns"} {
		if strings.Contains(strings.ToLower(rest), strings.ToLower(word)) {
			t.Errorf("the bar spells state as the word %q: %q", word, rest)
		}
	}
}

// TestTheBarSaysNothingAboutAWorkspaceItCannotFind. A pane outlives a delete, and
// a refresh can land between the two — so the row it is of may be gone. Saying
// less is the only honest answer; the alternative is a zero PR and an empty loop
// rendered as facts.
func TestTheBarSaysNothingAboutAWorkspaceItCannotFind(t *testing.T) {
	m, _ := paneOn(t, prRow())
	if !strings.Contains(barText(&m, 200), "#412") {
		t.Fatal("the PR was never on the bar to begin with")
	}
	m.itemsAll = nil
	bar := barText(&m, 200)
	if strings.Contains(bar, "#") || strings.Contains(bar, "/7") {
		t.Errorf("the bar still reports a workspace that is gone: %q", bar)
	}
}

// TestTheBarAndTheRowSpellAPRTheSameWay. Two surfaces showing the same PR have to
// show it with the same glyphs in the same order, or the second one is a new
// vocabulary to learn — which is why prGlyphCluster is one function.
func TestTheBarAndTheRowSpellAPRTheSameWay(t *testing.T) {
	m, _ := paneOn(t, prRow())
	item := m.mergedItemsAll()[0]
	cluster := m.prGlyphCluster(item)
	if cluster == "" {
		t.Skip("this fixture's PR has no cached status, so it has earned no glyphs")
	}
	if !strings.Contains(m.renderHostBar(200), cluster) {
		t.Errorf("the bar does not show the row's glyph cluster %q", cluster)
	}
}
