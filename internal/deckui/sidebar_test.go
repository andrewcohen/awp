package deckui

import (
	"sort"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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

// TestEveryRowSpendsExactlyTwoLines, a workspace with nothing to say included.
//
// The fixed cadence is what separates one row from the next. It replaced a
// variable-height row plus a blank row between rows, which spent between two and
// three lines per workspace and still had to be scanned for where one ended. Both
// lines carry text — see TestASecondLineAlwaysSaysSomething for why a blank one does
// not keep the cadence, whatever the line count says.
func TestEveryRowSpendsExactlyTwoLines(t *testing.T) {
	m := stripDeck(nil)
	v := m.sidebarView()
	for _, it := range []Item{
		{WorkspaceName: "bare"},
		{ProjectName: "awp", WorkspaceName: "default"},
		{WorkspaceName: "flaky-login-test", Bookmark: "andrew/login-retry"},
		{WorkspaceName: "docs-tidy", Bookmark: "andrew/docs-tidy"},
	} {
		row := m.sidebarRow(v, it, sidebarWidth)
		if len(row) != 2 {
			t.Errorf("%s spent %d lines, want 2: %q", it.WorkspaceName, len(row), row)
		}
	}
}

// TestTheRowCadenceIsWhatSeparatesRows: every name in a section is exactly two lines
// below the last, so a meta line can never be read as belonging to the name under it.
func TestTheRowCadenceIsWhatSeparatesRows(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "a", WorkspaceName: "one", Status: "waiting", Bookmark: "andrew/one-branch"},
		{ProjectName: "a", WorkspaceName: "two", Status: "waiting"}, // no meta: a blank second line
		{ProjectName: "a", WorkspaceName: "three", Status: "waiting", Bookmark: "andrew/three-branch"},
	})
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
	lines := strings.Split(out, "\n")
	at := func(name string) int {
		for i, l := range lines {
			if strings.Contains(l, name) {
				return i
			}
		}
		t.Fatalf("%s is missing from the strip:\n%s", name, out)
		return -1
	}
	// Sorted, because the rows render in the scope's own order rather than the order
	// the fixture lists them in.
	rows := []int{at("one"), at("two"), at("three")}
	sort.Ints(rows)
	for i := 1; i < len(rows); i++ {
		if rows[i]-rows[i-1] != 2 {
			t.Errorf("consecutive names sit %d lines apart, want 2 (%v):\n%s",
				rows[i]-rows[i-1], rows, out)
		}
	}
}

// TestASecondLineAlwaysSaysSomething, and never says the line above again.
//
// A blank second line kept the cadence in the line count and lost it on screen: what
// the eye reads as a row is a block of text, so a name with nothing under it reads as
// a one-line row. With nothing else to print the line carries the half of the row's
// identity the name line is not using — the workspace name where the row goes by its
// project, the project where it goes by its name.
func TestASecondLineAlwaysSaysSomething(t *testing.T) {
	m := stripDeck(nil)
	v := m.sidebarView()
	for _, tc := range []struct {
		item Item
		want string
	}{
		// A repo-root row goes by its project, so the line is which workspace.
		{Item{ProjectName: "awp", WorkspaceName: "default"}, "default"},
		// Any other row goes by its name, so the line is which project — the one fact
		// the strip otherwise drops entirely.
		{Item{ProjectName: "beta", WorkspaceName: "bump-deps"}, "beta"},
		// A bookmark that only restates the name loses to that, not to a blank.
		{Item{ProjectName: "awp", WorkspaceName: "docs-tidy", Bookmark: "andrew/docs-tidy"}, "awp"},
		// One that says something new wins.
		{Item{ProjectName: "awp", WorkspaceName: "flaky-login-test", Bookmark: "andrew/login-retry"}, "andrew/login-retry"},
		// And with no project at all, the name is the last resort that always exists.
		{Item{WorkspaceName: "bare"}, "bare"},
	} {
		got := ansi.Strip(m.sidebarMeta(v, tc.item, sidebarWidth))
		if got != tc.want {
			t.Errorf("%s: second line %q, want %q", tc.item.WorkspaceName, got, tc.want)
		}
	}
	// And no rendered row has an empty second line.
	deck := stripDeck([]Item{
		{ProjectName: "awp", WorkspaceName: "default", Status: "idle"},
		{ProjectName: "b", WorkspaceName: "bare", Status: "waiting"},
	})
	dv := deck.sidebarView()
	for _, it := range dv.Items() {
		row := deck.sidebarRow(dv, it, sidebarWidth)
		if strings.TrimSpace(ansi.Strip(row[1])) == "" {
			t.Errorf("%s drew an empty second line", it.WorkspaceName)
		}
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

// TestTheSidebarMarksTheWorkspaceYouAreIn — its name in Strong, and no other row's.
//
// It was a muted `┃`, the tier the design system gives a pane the keyboard has left,
// and the bar needed a column ahead of the status dot. That column was the indent
// that made the strip's rows sit off the left edge its headers set, so the marker
// moved into the name's own weight. #350 puts a real cursor in here and a cursor does
// earn the bar back — the selection treatment is `┃ ` plus Warning — which is a
// different claim from "you are here".
func TestTheSidebarMarksTheWorkspaceYouAreIn(t *testing.T) {
	m, p := sidebarPane(t)
	v := m.sidebarView()
	rows := v.Items()
	if len(rows) < 2 {
		t.Fatalf("need two rows to tell a marked one from an unmarked one, got %d", len(rows))
	}
	marked := 0
	for _, it := range rows {
		line := m.sidebarRow(v, it, sidebarWidth)[0]
		isMine := it.ProjectName == p.project && it.WorkspaceName == p.workspace
		strong := strings.Contains(line, m.styles.Strong.Render(sidebarLabel(it)))
		if strong != isMine {
			t.Errorf("%s: marked %v, want %v (the pane is of %s/%s)",
				it.WorkspaceName, strong, isMine, p.project, p.workspace)
		}
		if strong {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("%d rows are marked, want exactly the one the pane is of", marked)
	}
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
// What it was spending its width and height on: `beta/` printed on every row
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

// TestARowGoesByItsName, never by the project it is in.
//
// The project was a chip on every row, then a muted sub-row printed once per project,
// and it is now on neither: the sections cut across projects, so a project sub-row
// inside a band would be a second level of grouping under the one that matters. It
// survives only as the second line's last-resort content, which is a row saying "and
// this one is in beta" rather than the project labelling the row. A `default`
// workspace is the one row that goes by its project — see below.
func TestARowGoesByItsName(t *testing.T) {
	items := []Item{
		{ProjectName: "beta", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
		{ProjectName: "beta", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
	}
	m := stripDeck(items)
	v := m.sidebarView()
	for _, it := range v.Items() {
		row := m.sidebarRow(v, it, sidebarWidth)
		if name := ansi.Strip(row[0]); strings.Contains(name, it.ProjectName) {
			t.Errorf("the name line names the project: %q", name)
		}
	}
	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
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
	room := sidebarWidth - 2*sidebarPadX - 2 // the status dot and its space
	if room < 28 {
		t.Errorf("a row has %d columns for its name; telling two workspaces apart needs about 28", room)
	}
}

// The three things that made the first version of the strip unreadable on a real
// deck, each of which was the strip repeating itself.

// TestADefaultWorkspaceGoesByItsProject. Six projects each with only a repo-root
// workspace rendered as six rows called `default`, and the name says nothing — the
// project is the identity. collapsedProjects reaches the same conclusion for the row
// list by a different route.
//
// `default` still appears, on the second line, where it is the one thing that line
// has to say: the pair is the project (which repo) over the workspace (which one in
// it). What must not happen is `default` being the row's *name*.
func TestADefaultWorkspaceGoesByItsProject(t *testing.T) {
	m := stripDeck([]Item{
		{ProjectName: "awp", WorkspaceName: "default", Status: "idle"},
		{ProjectName: "beta", WorkspaceName: "default", Status: "idle"},
	})
	v := m.sidebarView()
	rows := v.Items()
	if len(rows) != 2 {
		t.Fatalf("the deck has %d rows, want 2", len(rows))
	}
	for _, it := range rows {
		row := m.sidebarRow(v, it, sidebarWidth)
		name, meta := ansi.Strip(row[0]), strings.TrimSpace(ansi.Strip(row[1]))
		if !strings.Contains(name, it.ProjectName) {
			t.Errorf("the name line is %q, want the project %q", name, it.ProjectName)
		}
		if strings.Contains(name, "default") {
			t.Errorf("the name line still calls the row `default`: %q", name)
		}
		if meta != "default" {
			t.Errorf("the second line is %q, want `default`", meta)
		}
	}
}

// TestAPRWorkspaceDropsTheNumberFromItsName, because the number is on the line
// below it. `pr-128-refactor-parser` spent eight of the strip's thirty-six
// columns on a field the next line then repeated.
func TestAPRWorkspaceDropsTheNumberFromItsName(t *testing.T) {
	if got := sidebarLabel(Item{WorkspaceName: "pr-128-refactor-parser"}); got != "refactor-parser" {
		t.Errorf("the label is %q, want the name without its pr- prefix", got)
	}
	// And a workspace genuinely called pr-something keeps its name: the prefix is
	// matched, not split off at the first dash.
	for _, name := range []string{"pr-review-notes", "pr-", "pr-128", "prod-fix"} {
		if got := sidebarLabel(Item{WorkspaceName: name}); got != name {
			t.Errorf("sidebarLabel(%q) = %q, want it left alone", name, got)
		}
	}
}

// TestTheBookmarkIsDroppedWhenItIsTheNameAgain, which on a real deck is most rows:
// a workspace named after its branch put `andrew/refactor-parser` under
// `refactor-parser`, line after line.
func TestTheBookmarkIsDroppedWhenItIsTheNameAgain(t *testing.T) {
	for _, tc := range []struct {
		item Item
		want string
	}{
		{Item{WorkspaceName: "docs-tidy", Bookmark: "andrew/docs-tidy"}, ""},
		{Item{WorkspaceName: "docs-tidy", Bookmark: "docs-tidy"}, ""},
		{Item{WorkspaceName: "pr-128-refactor", Bookmark: "andrew/refactor"}, ""},
		{Item{WorkspaceName: "flaky-login-test", Bookmark: "andrew/login-retry"}, "andrew/login-retry"},
		{Item{WorkspaceName: "default", ProjectName: "awp", Bookmark: "andrew/awp"}, ""},
	} {
		if got := sidebarBookmark(tc.item); got != tc.want {
			t.Errorf("%s on %q: bookmark %q, want %q",
				tc.item.WorkspaceName, tc.item.Bookmark, got, tc.want)
		}
	}
}

// TestARowsTwoLinesStartAtTheSameColumn. The indents were header 0, name 4, meta 2 —
// three indents for two levels, so the meta line claimed a level of its own that
// does not exist. An indent is read as structure.
func TestARowsTwoLinesStartAtTheSameColumn(t *testing.T) {
	m := stripDeck(nil)
	row := m.sidebarRow(m.sidebarView(),
		Item{WorkspaceName: "flaky-login-test", Status: "working", Bookmark: "andrew/login-retry"}, sidebarWidth)
	if len(row) != 2 {
		t.Fatalf("the row spent %d lines, want 2: %q", len(row), row)
	}
	name, meta := ansi.Strip(row[0]), ansi.Strip(row[1])
	// Display columns, not byte offsets — the status dot is three bytes wide and one
	// column, which is the whole reason the two lines look misaligned or not.
	nameCol := lipgloss.Width(name[:strings.Index(name, "flaky-login-test")])
	metaCol := lipgloss.Width(meta[:len(meta)-len(strings.TrimLeft(meta, " "))])
	if nameCol != metaCol {
		t.Errorf("the name starts at column %d and its meta line at %d:\n%q\n%q",
			nameCol, metaCol, name, meta)
	}
}
