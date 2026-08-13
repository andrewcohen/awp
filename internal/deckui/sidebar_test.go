package deckui

import (
	"strings"
	"testing"

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

// TestTheSidebarSaysWhatWantsYou: the attention scope's rows, grouped under the
// scope's own words for why each is there. The strip and `P`'s attention scope
// answer one question, so they have to answer it the same way — grouping by agent
// state instead made the strip say "nothing waiting" beside a row list with a
// screenful in it.
func TestTheSidebarSaysWhatWantsYou(t *testing.T) {
	m, _ := sidebarPane(t)
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 20}))
	for _, want := range []string{"waiting on you", "ws", "finished a turn", "read-me"} {
		if !strings.Contains(strip, want) {
			t.Errorf("the strip does not mention %q:\n%s", want, strip)
		}
	}
}

// TestTheSidebarListsTheAttentionScope. The strip is not a second opinion about
// what wants you — it is the same list `P`'s attention scope shows, in the same
// order, so a row missing from one is a bug in both.
func TestTheSidebarListsTheAttentionScope(t *testing.T) {
	m, _ := sidebarPane(t)
	strip := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 40}))
	rows := m.attentionView().Items()
	if len(rows) == 0 {
		t.Fatal("the attention scope is empty, so this proves nothing")
	}
	for _, it := range rows {
		if !strings.Contains(strip, it.WorkspaceName) {
			t.Errorf("%s is in the attention scope and not on the strip:\n%s", it.WorkspaceName, strip)
		}
	}
}

// TestEveryGroupTheScopeCanProduceHasWords. A reason with no label heads a group
// with a blank line where its name goes, which reads as a rendering fault rather
// than as a group.
func TestEveryGroupTheScopeCanProduceHasWords(t *testing.T) {
	m, _ := sidebarPane(t)
	for _, r := range sidebarGroups {
		if sidebarGroupLabel(r) == "" {
			t.Errorf("reason %v heads a group with no words", r)
		}
		if m.sidebarGroupStyle(r).GetForeground() == nil {
			t.Errorf("reason %v heads a group with no colour", r)
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
	if !strings.Contains(frame, "waiting on you") {
		t.Errorf("the strip is not in the frame:\n%s", frame)
	}
}

// TestBothMenusOfferTheSidebar. It is one verb reached from either arrangement,
// and a menu that does not list it is a key nobody finds.
func TestBothMenusOfferTheSidebar(t *testing.T) {
	m, _ := sidebarPane(t)
	if hint := panePrefixHint(&m); !strings.Contains(hint, sidebarKey) {
		t.Errorf("a pane's menu does not offer the strip: %q", hint)
	}
	if hint := splitPrefixHint(&m); !strings.Contains(hint, sidebarKey) {
		t.Errorf("a split's menu does not offer the strip: %q", hint)
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

// TestAProjectIsNamedOnceNotOnEveryRow.
func TestAProjectIsNamedOnceNotOnEveryRow(t *testing.T) {
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
		{ProjectName: "alpha", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
		{ProjectName: "alpha", WorkspaceName: "three", Path: "/tmp", RepoRoot: "/r", Status: "waiting", Unread: true},
	}
	m := New(items, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40
	m.itemsAll = items

	out := ansi.Strip(m.renderSidebar(box{w: sidebarWidth, h: 24}))
	if got := strings.Count(out, "alpha"); got != 1 {
		t.Errorf("three rows of one project named it %d times, want once:\n%s", got, out)
	}
	// And every workspace is still listed under it.
	for _, name := range []string{"one", "two", "three"} {
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
			continue // a header or a project sub-row
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

// TestARowGetsMostOfTheStripForItsName. The point of the width: a PR row is a
// number and the head of a title, and the strip is worth having only if enough of
// the title survives to tell two PRs apart.
func TestARowGetsMostOfTheStripForItsName(t *testing.T) {
	room := sidebarWidth - 2*sidebarPadX - len(sidebarIndent) - 2 // bar, dot and its space
	if room < 28 {
		t.Errorf("a row has %d columns for its name; a PR number and a readable title need about 28", room)
	}
}
