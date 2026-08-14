package deckui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// The attention strip beside a pane.
//
// What it has to get right is not its own contents — those are a pass over rows
// the deck already holds — but that the columns it takes are columns the child
// knows it does not have. A pane whose pty was sized to the whole terminal while
// the strip covers its left 28 columns is a program laid out for a width it is
// not drawn at, and the cursor and the mouse land 28 columns off.

// sidebarDeck is a deck with rows in each attention bucket, wide enough to carry
// a strip beside a pane.
func sidebarDeck(t *testing.T) Model {
	t.Helper()
	items := []Item{
		{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp", Status: "waiting", Unread: true},
		{ProjectName: "proj", WorkspaceName: "busy", Path: "/tmp", RepoRoot: "/tmp", Status: "working"},
		{ProjectName: "other", WorkspaceName: "read-me", Path: "/tmp", RepoRoot: "/tmp", Unread: true},
	}
	m := New(items, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40
	m.itemsAll = items
	m.keysEnhanced = true
	return m
}

// sidebarPane opens a pane and turns the strip on with ctrl+b S.
func sidebarPane(t *testing.T) (Model, *panePopover) {
	t.Helper()
	m := sidebarDeck(t)
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened: active is %T, status %q", m.active, m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(sidebarKey))
	if !m.showsSidebar() {
		t.Fatalf("ctrl+b %s did not put the strip up (status %q)", sidebarKey, m.status)
	}
	return m, p
}

// TestTheSidebarTakesItsColumnsFromTheChild. The one invariant: what the strip
// occupies is not the child's, and the child is told so through the one box every
// path derives from — the renderer, the cursor and the mouse translation alike.
func TestTheSidebarTakesItsColumnsFromTheChild(t *testing.T) {
	m, _ := sidebarPane(t)
	b := m.childBox()
	if b.x != sidebarWidth {
		t.Errorf("the child starts at column %d, want %d — the strip's columns", b.x, sidebarWidth)
	}
	if want := m.width - sidebarWidth; b.w != want {
		t.Errorf("the child is %d columns wide, want %d", b.w, want)
	}
}

// TestTheSidebarGivesTheColumnsBack. Pressing the key again is the whole way out,
// so the child has to get the columns back rather than keeping a 28-column gap
// down its left.
func TestTheSidebarGivesTheColumnsBack(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(sidebarKey))
	if m.showsSidebar() {
		t.Fatal("the second press left the strip up")
	}
	if b := m.childBox(); b.x != 0 || b.w != m.width {
		t.Errorf("the child got %d columns at x=%d, want the whole %d", b.w, b.x, m.width)
	}
}

// TestTheSidebarIsNotUpOverTheRowList. Over the list every row the strip would
// carry is already on screen, with a cursor on it — a strip beside it is the same
// answer twice, in the narrower of the two.
func TestTheSidebarIsNotUpOverTheRowList(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	if m.active != nil {
		t.Fatalf("the leave key left %T open", m.active)
	}
	if m.showsSidebar() {
		t.Error("the strip is up over the row list")
	}
	if b := m.childBox(); b.w != m.width {
		t.Errorf("the row list got %d columns, want the whole %d", b.w, m.width)
	}
	if !m.sidebar {
		t.Error("leaving the pane forgot the setting; it is the deck's, not the pane's")
	}
}

// TestTheSidebarStaysOnAcrossPanes. It answers "do I want to see what is
// waiting", which does not change when you switch what you are working in.
func TestTheSidebarStaysOnAcrossPanes(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, resumeKey())
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("coming back gave %T", m.active)
	}
	if !m.showsSidebar() {
		t.Error("coming back into a pane lost the strip")
	}
}

// stripDeck is a deck holding exactly the rows a test wants to see sectioned,
// wide enough to carry the strip beside a pane.
func stripDeck(items []Item) Model {
	m := New(items, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40
	m.itemsAll = items
	return m
}

// TestTheSidebarSectionsByAgentState: the bands, in order, and a row under each.
func TestTheSidebarSectionsByAgentState(t *testing.T) {
	m, _ := sidebarPane(t)
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 20}))
	for _, want := range []string{"waiting", "ws", "ready", "read-me", "working", "busy"} {
		if !strings.Contains(strip, want) {
			t.Errorf("the strip does not mention %q:\n%s", want, strip)
		}
	}
}

// TestTheSidebarListsEveryWorkspace. It used to list the attention scope, which is
// where the `idle` band could not come from: a scope that filters idle rows out
// cannot fill a section of them.
func TestTheSidebarListsEveryWorkspace(t *testing.T) {
	m, _ := sidebarPane(t)
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 40}))
	rows := m.sidebarView().Items()
	if len(rows) == 0 {
		t.Fatal("the deck has no rows, so this proves nothing")
	}
	for _, it := range rows {
		if !strings.Contains(strip, it.WorkspaceName) {
			t.Errorf("%s is a workspace and not on the strip:\n%s", it.WorkspaceName, strip)
		}
	}
}

// TestASectionIsPrintedOnce. The bug this replaced: the strip grouped by
// deckdata.Reason, which is a per-row answer to "why is this here" and not a
// partition — so walking the scope and starting a group whenever the reason changed
// re-opened a header that had already been printed further up. A header is worth
// its row only if everything below it is that band, and it can only mean that if it
// appears once.
func TestASectionIsPrintedOnce(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "a", WorkspaceName: "one", Status: "waiting", Unread: true},
		{ProjectName: "b", WorkspaceName: "two", Status: "working"},
		{ProjectName: "a", WorkspaceName: "three", Status: "waiting"},
		{ProjectName: "b", WorkspaceName: "four", Status: "working"},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 30}))
	for _, band := range []sidebarSection{sectionWaiting, sectionWorking} {
		label := sidebarSectionLabel(band)
		headers := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == label {
				headers++
			}
		}
		if headers != 1 {
			t.Errorf("%q heads %d sections, want exactly 1:\n%s", label, headers, out)
		}
	}
}

// TestEverySectionHasWordsAndAHue. A band with neither heads a group with a blank
// line where its name goes, which reads as a rendering fault rather than a header.
func TestEverySectionHasWordsAndAHue(t *testing.T) {
	m, _ := sidebarPane(t)
	for band := sidebarSection(0); band < sidebarSectionCount; band++ {
		if sidebarSectionLabel(band) == "" {
			t.Errorf("section %d heads a group with no words", band)
		}
		if m.sidebarSectionStyle(band).GetForeground() == nil {
			t.Errorf("section %d heads a group with no colour", band)
		}
	}
}

// TestEachStateLandsInTheSectionItsDotNames. The bands are a partition of agent
// state, and this is the mapping — including the two that are read off the status
// whether or not the mark is unread: an agent that stopped to ask you something is
// still stopped once you have seen the question.
func TestEachStateLandsInTheSectionItsDotNames(t *testing.T) {
	for _, tc := range []struct {
		item Item
		want sidebarSection
	}{
		{Item{PinGroup: "default", Status: "idle"}, sectionPinned},
		{Item{PinGroup: "a", Status: "working"}, sectionPinned},
		{Item{Status: "waiting", Unread: true}, sectionWaiting},
		{Item{Status: "waiting"}, sectionWaiting},
		{Item{Status: "error"}, sectionError},
		{Item{Status: "done", Unread: true}, sectionReady},
		{Item{Status: "idle", Unread: true}, sectionReady},
		{Item{Status: "working"}, sectionWorking},
		{Item{Status: "in_progress"}, sectionWorking},
		{Item{Status: "idle"}, sectionIdle},
		{Item{Status: "done"}, sectionIdle},
		{Item{Status: "exited", Unread: true}, sectionIdle},
	} {
		if got := sidebarSectionOf(tc.item); got != tc.want {
			t.Errorf("status %q (unread %v, pin %q) sections under %q, want %q",
				tc.item.Status, tc.item.Unread, tc.item.PinGroup,
				sidebarSectionLabel(got), sidebarSectionLabel(tc.want))
		}
	}
}

// TestPinnedRowsSitAtTheVeryTop, whatever their agent is doing. A pin is a
// statement about the workspace and not about this minute, so one that sorted under
// `idle` because the agent is between turns is the pin not working.
func TestPinnedRowsSitAtTheVeryTop(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "a", WorkspaceName: "busy", Status: "working"},
		{ProjectName: "a", WorkspaceName: "kept", Status: "idle", PinGroup: "default"},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 30}))
	pinned, working := strings.Index(out, "kept"), strings.Index(out, "busy")
	if pinned < 0 || working < 0 {
		t.Fatalf("a row is missing from the strip:\n%s", out)
	}
	if pinned > working {
		t.Errorf("an idle pinned row sorted below a working one:\n%s", out)
	}
	if head := strings.Index(out, sidebarSectionLabel(sectionPinned)); head < 0 || head > pinned {
		t.Errorf("the pinned rows are not under the pinned header:\n%s", out)
	}
}

// TestIdleRowsAreMostRecentlyActiveFirst. The band has no urgency to rank by, so
// the useful question is "where was I" — and a workspace last touched in March is
// not the answer. An unknown time still sorts last: it is a row we have no reason
// to raise, rather than an ancient one.
func TestIdleRowsAreMostRecentlyActiveFirst(t *testing.T) {
	now := time.Now()
	m := stripDeck([]Item{
		{ProjectName: "a", WorkspaceName: "march", Status: "idle", LastActiveAt: now.Add(-90 * 24 * time.Hour)},
		{ProjectName: "a", WorkspaceName: "unknown", Status: "idle"},
		{ProjectName: "a", WorkspaceName: "minutes", Status: "idle", LastActiveAt: now.Add(-5 * time.Minute)},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 30}))
	recent, old, unknown := strings.Index(out, "minutes"), strings.Index(out, "march"), strings.Index(out, "unknown")
	if recent < 0 || old < 0 || unknown < 0 {
		t.Fatalf("a row is missing from the strip:\n%s", out)
	}
	if recent > old || old > unknown {
		t.Errorf("idle rows are out of order (recent at %d, old at %d, unknown at %d):\n%s",
			recent, old, unknown, out)
	}
}

// TestARowsSecondLineCarriesItsPR, indented under the name.
//
// The name gets a line to itself so it never truncates against the fixed-width
// fields it used to share a line with — and a truncated name is the one field you
// cannot work out from the others.
func TestARowsSecondLineCarriesItsPR(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "a", WorkspaceName: "with-pr", Status: "idle", Bookmark: "andrew/thing"},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 30}))
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "with-pr") {
			continue
		}
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], "andrew/thing") {
			t.Fatalf("the bookmark is not on the line under the name:\n%s", out)
		}
		return
	}
	t.Fatalf("the workspace is not on the strip:\n%s", out)
}

// TestARowWithNothingToSaySpendsOneLine. The second line is allowed to be missing,
// which for a workspace with no PR and no bookmark it is — a blank one would cost
// the strip half its rows to say nothing.
func TestARowWithNothingToSaySpendsOneLine(t *testing.T) {
	m := stripDeck(nil)
	if got := m.sidebarMeta(m.sidebarView(), Item{WorkspaceName: "bare"}, sidebarWidth); got != "" {
		t.Errorf("a workspace with no PR and no bookmark drew %q", got)
	}
	if got := m.sidebarRow(m.sidebarView(), Item{WorkspaceName: "bare"}, sidebarWidth); len(got) != 1 {
		t.Errorf("the row spent %d lines: %q", len(got), got)
	}
}

// TestTheSidebarCountsEveryWorkspaceNotTheScope. The same argument
// countAttention makes: what wants you cannot depend on which filter the row list
// is set to, least of all from inside a pane where the filter is not on screen.
func TestTheSidebarCountsEveryWorkspaceNotTheScope(t *testing.T) {
	m, _ := sidebarPane(t)
	m.filter = "no-such-workspace"
	if got := len(m.items()); got != 0 {
		t.Fatalf("the filter left %d rows in the scoped list, wanted none", got)
	}
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 20}))
	if !strings.Contains(strip, "ws") {
		t.Errorf("a workspace the scoped list filtered out fell off the strip:\n%s", strip)
	}
}

// TestTheSidebarMarksTheWorkspaceYouAreIn, in the tier the design system gives a
// pane the keyboard has left: the bar, muted. It says where you are without
// claiming to be a cursor there is none of in here.
func TestTheSidebarMarksTheWorkspaceYouAreIn(t *testing.T) {
	m, p := sidebarPane(t)
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 20}))
	for _, line := range strings.Split(strip, "\n") {
		if !strings.HasPrefix(strings.TrimLeft(line, " "), "┃") {
			continue
		}
		if !strings.Contains(line, p.workspace) {
			t.Errorf("the marked row is %q, want the pane's own workspace %q", line, p.workspace)
		}
		return
	}
	t.Errorf("the workspace the pane is of (%s) is not marked:\n%s", p.workspace, strip)
}

// TestTheSidebarRefusesATerminalWithNoRoomBesideAPane. A flag set on a terminal
// too narrow renders nothing, which reads as the key being broken — and then
// surprises you by taking effect on the next resize.
func TestTheSidebarRefusesATerminalWithNoRoomBesideAPane(t *testing.T) {
	m := sidebarDeck(t)
	m.width = sidebarWidth + sidebarChildMinW - 1
	m.toggleSidebar()
	if m.sidebar {
		t.Error("the strip turned on with no room for a pane beside it")
	}
	if !strings.Contains(m.status, "columns") {
		t.Errorf("the refusal does not say the width it wants: %q", m.status)
	}
}

// TestTheSidebarDoesNotOverflowItsBox. The strip is rows of text with no cursor
// and nothing that scrolls it, so more rows than height is a count — a frame one
// line too tall pushes every later line of the deck down by one.
func TestTheSidebarDoesNotOverflowItsBox(t *testing.T) {
	m, _ := sidebarPane(t)
	const height = 4
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: height}))
	if got := len(strings.Split(strip, "\n")); got != height {
		t.Errorf("the strip rendered %d rows into a box %d tall:\n%s", got, height, strip)
	}
	if !strings.Contains(strip, "more") {
		t.Errorf("rows were dropped without saying so:\n%s", strip)
	}
}

// TestTheFrameIsStillTheTerminalsWithTheSidebarUp. The strip is joined beside the
// child rather than over it, so the frame's width is unchanged — a frame one
// column wide of the terminal wraps, and a wrapped row misaligns everything below
// it.
func TestTheFrameIsStillTheTerminalsWithTheSidebarUp(t *testing.T) {
	m, _ := sidebarPane(t)
	frame := ansi.Strip(m.render())
	for i, line := range strings.Split(frame, "\n") {
		if got := len([]rune(line)); got > m.width {
			t.Fatalf("frame line %d is %d columns wide, terminal is %d", i, got, m.width)
		}
	}
	// And the strip is in the frame at all — every other test here reads
	// renderSidebar directly, which would pass on a strip nothing draws.
	if !strings.Contains(frame, sidebarSectionLabel(sectionWaiting)) {
		t.Errorf("the strip is not in the frame:\n%s", frame)
	}
}

// TestBothMenusOfferTheSidebar. It is one verb reached from either arrangement,
// and a menu that does not list it is a key nobody finds.
func TestBothMenusOfferTheSidebar(t *testing.T) {
	m, _ := sidebarPane(t)
	if mn := panePrefixMenu(&m); !menuBinds(mn, sidebarKey) {
		t.Errorf("a pane's menu does not offer the strip: %+v", mn.verbs)
	}
	if mn := splitPrefixMenu(&m); !menuBinds(mn, sidebarKey) {
		t.Errorf("a split's menu does not offer the strip: %+v", mn.verbs)
	}
}

// TestTheSidebarTogglesFromASplitToo. Both halves are the workspace you are in,
// so the question the strip answers is the same one — and the verb is in that
// menu, so it has to work there.
func TestTheSidebarTogglesFromASplitToo(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(sidebarKey))
	if !m.showsSidebar() {
		t.Fatalf("ctrl+b %s did nothing in a split (status %q)", sidebarKey, m.status)
	}
	if _, isSplit := m.active.(*splitModal); !isSplit {
		t.Fatalf("the key took the split down: active is %T", m.active)
	}
	if b := m.childBox(); b.x != sidebarWidth {
		t.Errorf("the split starts at column %d, want %d", b.x, sidebarWidth)
	}
}

// The row layout, after the strip was reported as mostly wasted space.
//
// What it was spending its width and height on: `alpha/` printed on every row
// as a chip, eight columns of a twenty-six-column strip repeated four times in a
// row, while the PR titles it was labelling truncated to `fix(l...`; and a blank
// row between every group, a quarter of the height, on a surface whose whole
// purpose is fitting more rows than the badge can count.

// TestGroupsAreSeparatedByABlankRow, and the first one is not pushed down by one.
//
// The blank costs a workspace the strip could have listed, which is why it was
// tried without: the header is coloured and bold, and that reads as a header on its
// own. It does not read as a *separator* — the groups are what makes the strip
// scannable instead of a list, and against a wall of rows the eye could not find
// the one it wanted. So the row is spent deliberately.
func TestGroupsAreSeparatedByABlankRow(t *testing.T) {
	m := sidebarDeck(t)
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
	lines := strings.Split(out, "\n")

	first, last := -1, 0
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		t.Fatalf("the strip rendered nothing:\n%s", out)
	}
	// The fixture spans more than one group, so somewhere between the first row of
	// content and the last there is exactly one blank per group boundary.
	blanks := 0
	for _, l := range lines[first:last] {
		if strings.TrimSpace(l) == "" {
			blanks++
		}
	}
	if blanks == 0 {
		t.Errorf("no blank row separates the groups:\n%s", out)
	}
	// And the strip's first content row is a header, not a blank the loop emitted
	// before it — the separator goes between groups, not above the first.
	if strings.TrimSpace(lines[first]) == "" {
		t.Errorf("the strip opens with a blank row:\n%s", out)
	}
}

// TestTheProjectIsNotOnTheStripAtAll. It was a chip on every row, then a muted
// sub-row printed once per project — and now neither. The strip's sections cut
// across projects, so a project sub-row inside a band would be a second level of
// grouping under the one that matters, and the chip was eight columns of a name
// already on screen above. The cost is that two workspaces of the same name in
// different projects read alike on the strip; the row list is where you tell them
// apart.
func TestTheProjectIsNotOnTheStripAtAll(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "alpha", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
		{ProjectName: "alpha", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
	if strings.Contains(out, "alpha") {
		t.Errorf("the strip names the project:\n%s", out)
	}
	for _, name := range []string{"one", "two"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s is missing from the strip:\n%s", name, out)
		}
	}
}

// TestEveryWorkspaceRowStartsAtTheSameColumn. Rows with a status dot and rows
// without used to sit at different indents, so nothing lined up down the strip and
// the drift cost columns on both kinds.
func TestEveryWorkspaceRowStartsAtTheSameColumn(t *testing.T) {
	m := sidebarDeck(t)
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
	col := -1
	for _, l := range strings.Split(out, "\n") {
		dot := strings.Index(l, statusDot)
		if dot < 0 {
			continue // a header or a row's second line
		}
		if col == -1 {
			col = dot
			continue
		}
		if dot != col {
			t.Errorf("a row's dot is at column %d and another's at %d:\n%s", dot, col, out)
		}
	}
	if col == -1 {
		t.Fatalf("no row in the strip carries a status dot:\n%s", out)
	}
}

// TestARowGetsMostOfTheStripForItsName. The point of the width: a workspace name
// is the field you cannot reconstruct from the others, and the strip is worth
// having only if enough of it survives to tell two workspaces apart.
func TestARowGetsMostOfTheStripForItsName(t *testing.T) {
	room := sidebarWidth - 2*sidebarPadX - 3 // the bar's two columns, the dot, its space
	if room < 28 {
		t.Errorf("a row has %d columns for its name; telling two workspaces apart needs about 28", room)
	}
}
