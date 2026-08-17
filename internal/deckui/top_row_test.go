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
func barText(m *Model, w int) string { return ansi.Strip(m.renderTopRow(w)) }

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

// TestTheBarKeepsTheLeaveKeyWhileItFits. Three things want one row, and the order
// they are given up in is fixed: the label first, because the screen below already
// says what it is; then the badge, which is a thing leaving will show you; last the
// leave key, which is how you leave.
//
// Last, not never. Below the width of the key itself there is no honest answer —
// the row cannot overrun, because a row one column over pushes every later line of
// the frame down by one. So what is left of the claim, now that the row spells no
// keys at all, is the half about width: it never wraps, at any width.
func TestTheBarNeverWrapsHoweverNarrow(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 40, 24, 16, 8, 4} {
		bar := m.renderTopRow(w)
		if strings.Contains(bar, "\n") {
			t.Errorf("at %d columns the bar wrapped: %q", w, bar)
		}
	}
}

// TestTheBarSpellsNoKeysOverAPane. The way out used to sit on the right of every
// frame for the whole time a pane was open — a beginner's card that never came
// down. It is on `?` and in the ctrl+b menu, which is where a key you have to
// look up belongs.
func TestTheBarSpellsNoKeysOverAPane(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	bar := barText(&m, 200)
	for _, key := range []string{PaneMenuKey, PaneLeaveKey, "menu", "deck"} {
		if strings.Contains(bar, key) {
			t.Errorf("the row still spells %q at you: %q", key, bar)
		}
	}
}

// TestTheBarFillsExactlyTheWidthItWasGiven. It is a row of the frame rather than
// a thing inside a box, so short is a hole the previous frame shows through and
// long pushes the pane below it out of alignment.
func TestTheBarFillsExactlyTheWidthItWasGiven(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, w := range []int{120, 80, 60, 40, 30, 12} {
		if got := lipgloss.Width(m.renderTopRow(w)); got != w {
			t.Errorf("at %d columns the bar rendered %d wide: %q", w, got, m.renderTopRow(w))
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
		// Named by the label rather than by the way out, which the row no longer
		// spells.
		if !strings.Contains(row, tc.m.topRowLabel()) {
			t.Errorf("with %s row 0 is not the bar: %q", tc.what, row)
		}
		if !strings.Contains(row, "2") {
			t.Errorf("with %s row 0 lost the badge: %q", tc.what, row)
		}
		if got := tc.m.childBox().y; got != topRowRows {
			t.Errorf("with %s the child starts on row %d, want %d", tc.what, got, topRowRows)
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

// TestAnArmedPrefixLeavesTheTopRowAlone, which is the half of #344 this row cares
// about. The menu took the whole row before that, so the attention badge and the
// name of what you were looking at went away for as long as you were reading the
// verbs — and the badge is the reason the row exists. The menu floats now, so the
// row says the same thing armed or not.
func TestAnArmedPrefixLeavesTheTopRowAlone(t *testing.T) {
	m, s := openedSplit(t, "v")
	m.itemsAll = waitingRows()
	quiet := barText(&m, 200)
	s.prefixArmed = true
	if armed := barText(&m, 200); armed != quiet {
		t.Errorf("arming the prefix changed the row:\n quiet: %q\n armed: %q", quiet, armed)
	}
}

// TestTheRowListWearsTheSameRow. The point of the row being the deck's: the three
// screens you move between constantly — the list, a pane, a split — put the badge
// in the same cell, so it can be glanced at rather than found.
//
// What differs is only what the row has to say. Over the list there is nothing to
// leave and no one workspace to report on, so the right end carries the scope
// instead of a leave key.
func TestTheRowListWearsTheSameRow(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	if !m.showsTopRow() {
		t.Fatal("a pane does not get the top row")
	}
	inPane := barText(&m, 120)

	p.close(&m)
	m.active = nil
	if !m.showsTopRow() {
		t.Fatal("the row list does not get the top row")
	}
	onList := barText(&m, 120)

	for _, tc := range []struct{ what, row string }{{"in a pane", inPane}, {"on the list", onList}} {
		if !strings.HasPrefix(tc.row, deckIndent) {
			t.Errorf("%s the row does not start on the deck's text column: %q", tc.what, tc.row)
		}
		if lipgloss.Width(tc.row) != 120 {
			t.Errorf("%s the row is %d columns, want 120", tc.what, lipgloss.Width(tc.row))
		}
	}
	// The badge is in the same cell on both, which is the whole claim.
	if a, b := strings.Index(inPane, "●"), strings.Index(onList, "●"); a != b {
		t.Errorf("the badge starts at %d in a pane and %d on the list", a, b)
	}
	if !strings.Contains(onList, "scope:") {
		t.Errorf("the list's row does not name the scope: %q", onList)
	}
	if strings.Contains(onList, PaneLeaveKey) {
		t.Errorf("the list's row offers a way out of a screen you are not in: %q", onList)
	}
}

// TestAnOverlayGetsNoTopRow. Help, the jobs overlay, the pickers and the diff
// viewer are modes you are inside of rather than looking out from — the diff has a
// whole status line of its own — so a row about which workspaces want you, above a
// screen whose subject is one file, is a row spent on the wrong question.
func TestAnOverlayGetsNoTopRow(t *testing.T) {
	m, p := paneOn(t, waitingRows())
	p.close(&m)
	m.active = newHelpModal()
	if m.showsTopRow() {
		t.Error("an overlay is being given the deck's top row")
	}
	if got := m.childBox().y; got != 0 {
		t.Errorf("an overlay starts on row %d, want 0 — it owns its whole canvas", got)
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
	rest := strings.ReplaceAll(bar, m.topRowLabel(), "")
	rest = strings.ReplaceAll(rest, m.topRowHint(), "")
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
	if !strings.Contains(m.renderTopRow(200), cluster) {
		t.Errorf("the bar does not show the row's glyph cluster %q", cluster)
	}
}

// TestTheBarNamesTheWorkspaceYouNamed. A display label is what you called the
// work, so the row that answers "where am I" says it rather than the directory
// slug. It comes after a `·` rather than a `/`, because a label is a sentence and
// `proj/the widget rewrite` claims a path that does not exist.
func TestTheBarNamesTheWorkspaceYouNamed(t *testing.T) {
	items := []Item{{
		ProjectName: "proj", WorkspaceName: "ws", DisplayName: "the widget rewrite",
		Bookmark: "andrew/ws", Path: "/tmp", RepoRoot: "/tmp",
	}}
	m, _ := paneOn(t, items)
	bar := barText(&m, 200)
	if !strings.Contains(bar, "proj · the widget rewrite") {
		t.Errorf("the bar does not carry the display label: %q", bar)
	}
	if strings.Contains(bar, "proj/") {
		t.Errorf("a labelled workspace should not be spelled as a path: %q", bar)
	}
}

// TestTheBarSpellsAnUnlabelledWorkspaceAsAPath, which is what it is: the project
// and the directory under it, unchanged by the label case above.
func TestTheBarSpellsAnUnlabelledWorkspaceAsAPath(t *testing.T) {
	items := []Item{{
		ProjectName: "proj", WorkspaceName: "ws", Bookmark: "andrew/widget",
		Path: "/tmp", RepoRoot: "/tmp",
	}}
	m, _ := paneOn(t, items)
	if bar := barText(&m, 200); !strings.Contains(bar, "proj/ws") {
		t.Errorf("the bar lost the workspace path: %q", bar)
	}
}

// TestTheBarShowsBackgroundWorkFromInsideAPane. A pane renders no status bar, so
// until the row carried them a background user action — ctrl+b x, an install —
// ran with nothing on screen to say so.
func TestTheBarShowsBackgroundWorkFromInsideAPane(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	m = m.startActivity("job:1", "install · ws", 0)
	if !strings.Contains(barText(&m, 120), "install · ws") {
		t.Errorf("the bar does not name the work in flight: %q", barText(&m, 120))
	}
}

// TestTheRowListDoesNotShowActivityTwice. The status bar below the list is
// already showing these chips; the same work named twice on one screen reads as
// two things happening.
func TestTheRowListDoesNotShowActivityTwice(t *testing.T) {
	m := New(waitingRows(), func(ActionRequest) error { return nil })
	m.width, m.height = 120, 40
	m = m.startActivity("job:1", "install · ws", 0)
	if strings.Contains(barText(&m, 120), "install · ws") {
		t.Errorf("the list's top row repeats the status bar's chips: %q", barText(&m, 120))
	}
}

// TestTheBarCountsTheActivityItCannotFit. Three jobs of "<action> · <workspace>"
// would leave no room for the label or the way out, so past two the row counts
// rather than spells.
func TestTheBarCountsTheActivityItCannotFit(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, id := range []string{"job:1", "job:2", "job:3", "job:4"} {
		m = m.startActivity(id, id+" · ws", 0)
	}
	bar := barText(&m, 200)
	if !strings.Contains(bar, "+2") {
		t.Errorf("the bar does not count the activity it left off: %q", bar)
	}
	if strings.Contains(bar, "job:3") {
		t.Errorf("the bar spelled more activity than it caps at: %q", bar)
	}
}

// TestTheLabelDoesNotMoveWhenTheStateDoes. Everything left of the label changes
// width on its own — the badge is recounted every frame, an activity chip
// appears the moment a background action starts — and packed after them the
// label slid sideways for reasons that had nothing to do with it.
func TestTheLabelDoesNotMoveWhenTheStateDoes(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	before := labelCol(&m, 200)
	if before < 0 {
		t.Fatal("the label is not on the row to begin with")
	}
	m = m.startActivity("job:1", "install · ws", 0)
	if after := labelCol(&m, 200); after != before {
		t.Errorf("the label moved from column %d to %d when a chip appeared", before, after)
	}
	m.itemsAll = nil // the badge and the PR state both go away
	if after := labelCol(&m, 200); after != before {
		t.Errorf("the label moved from column %d to %d when the state emptied", before, after)
	}
}

// labelCol is the column the label starts at, which is not the byte it starts at:
// the row is full of multi-byte glyphs and the label itself carries a `·`, so an
// index into the string moves when nothing on screen has.
func labelCol(m *Model, w int) int {
	bar, label := barText(m, w), m.topRowLabel()
	i := strings.Index(bar, label)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(bar[:i])
}

// TestTheLabelIsInTheMiddleOfTheRow, which is the anchor the test above is
// about: a fixed column, not merely a repeatable one.
func TestTheLabelIsInTheMiddleOfTheRow(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	start := labelCol(&m, 200)
	if start < 0 {
		t.Fatal("the label is not on the row")
	}
	middle := (200 - lipgloss.Width(m.topRowLabel())) / 2
	if start != middle {
		t.Errorf("the label starts at column %d, not the row's middle %d", start, middle)
	}
}

// TestTheLabelGivesWayToTheStateRatherThanOverlappingIt. On a narrow terminal
// the middle is already spoken for, so the label is clamped clear of the left
// side and truncated — a pane's title loses characters before the state loses a
// glyph.
func TestTheLabelGivesWayToTheStateRatherThanOverlappingIt(t *testing.T) {
	m, _ := paneOn(t, waitingRows())
	for _, w := range []int{40, 60, 80, 120, 200} {
		bar := barText(&m, w)
		if lipgloss.Width(bar) != w {
			t.Errorf("at %d columns the row is %d wide: %q", w, lipgloss.Width(bar), bar)
		}
		if strings.Contains(bar, "\n") {
			t.Errorf("at %d columns the row wrapped: %q", w, bar)
		}
	}
}
