package deckui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/workspace"
)

// The sidebar is a narrow strip down the left of a pane or a split, holding the
// workspaces that want you.
//
// It exists because the top row's badge says *how many* and not *which*: three
// yellow dots is enough to know something is waiting and not enough to decide
// whether to leave what you are in. Leaving to find out is the thing the row was
// added to avoid, and it costs the pane a repaint of whatever program is in it.
//
// It reads and does nothing. There is no cursor in it, so no key moves one, so
// the arrangement verbs behind ctrl+| still address two halves rather than three
// regions — `h` / `l` / `tab` mean what they meant. A row you want to act on is
// two keys away (ctrl+\ to the deck, and the row is under the cursor there), and
// a sidebar that took the keyboard would have to answer what focus means with a
// pane, a split half and a strip on screen at once. That is a bigger question
// than "which of these wants me", which is the one it was opened for.
//
// It is a property of the deck rather than of the arrangement (see
// paneArrangement, which remembers what programs were on screen): the answer to
// "do I want to see what is waiting" does not change when you switch panes, so
// it stays on until you turn it off.

// sidebarKey is the verb, in both ctrl+| menus. Capital `S` because `s` is
// already the window key for a shell, and the two live in the same menu.
const sidebarKey = "S"

// sidebarWidth is the strip's width, in columns.
//
// Fixed rather than a fraction of the terminal. What goes on a row is a glyph and
// `project/workspace`, which is the same number of columns whether the terminal is
// 120 wide or 400 — a fraction would spend a quarter of a wide screen on names
// that stopped needing the room twenty columns ago. Wide enough for a name of
// useful length beside its project; narrow enough that a 120-column terminal can
// still carry a pane beside it.
const sidebarWidth = 28

// sidebarChildMinW is the narrowest thing the sidebar will leave beside itself:
// one pane, with its chrome.
//
// Below this the strip is taking columns from a program that has none to give,
// which is the wrong trade — the pane is what you are working in and the sidebar
// is what you glanced at.
const sidebarChildMinW = paneMinW + paneChromeW

// showsSidebar reports whether the strip is on screen.
//
// Only over a hosted program. Over the row list the list *is* the attention view
// — every row the sidebar would carry is already there, in more detail and with a
// cursor on it — so a strip beside it would be the same answer twice, in the
// narrower of the two.
func (m *Model) showsSidebar() bool {
	return m.sidebar && m.hostsTerminal() && m.width-sidebarWidth >= sidebarChildMinW
}

// sidebarCols is what the strip costs the child, which is nothing when it is not
// up. One function so childBox and the renderer cannot disagree about where the
// child starts.
func (m *Model) sidebarCols() int {
	if m.showsSidebar() {
		return sidebarWidth
	}
	return 0
}

// toggleSidebar is what ctrl+| S does.
//
// A terminal too narrow refuses and says the width it wants, rather than setting
// a flag that renders nothing — a key that appears to do nothing reads as broken,
// and the flag would then surprise you by taking effect on the next resize.
func (m *Model) toggleSidebar() {
	if !m.sidebar && m.width-sidebarWidth < sidebarChildMinW {
		m.status = fmt.Sprintf("sidebar: this terminal is %d columns, %d needed for a strip beside a pane",
			m.width, sidebarWidth+sidebarChildMinW)
		return
	}
	m.sidebar = !m.sidebar
	m.status = ""
}

// sidebarBucket is one group of rows in the strip, in the order the top row's
// badge counts them: waiting first, because there you are the blocker.
type sidebarBucket struct {
	kind  workspace.Attention
	label string
}

// sidebarBuckets is the strip's shape. The same three buckets the badge counts,
// so a `● 3` up on the top row and three rows under "waiting" down here are
// visibly the same three.
var sidebarBuckets = []sidebarBucket{
	{workspace.AttentionWaiting, "waiting"},
	{workspace.AttentionWorking, "working"},
	{workspace.AttentionNotified, "unread"},
}

// bucketStyle is the hue a bucket's header wears: the colour its rows' status
// dots already wear, which is the colour the badge counts them in.
func (m Model) sidebarBucketStyle(kind workspace.Attention) lipgloss.Style {
	switch kind {
	case workspace.AttentionWaiting:
		return m.styles.Warning
	case workspace.AttentionWorking:
		return m.styles.Success
	case workspace.AttentionNotified, workspace.AttentionNone:
	}
	return m.styles.Muted
}

// renderSidebar draws the strip into the box it was given.
//
// Every workspace, not the current scope's rows — the same argument
// countAttention makes: what wants you cannot depend on which filter the row list
// happens to be set to, least of all from inside a pane where the filter is not
// even visible.
func (m Model) renderSidebar(b box) string {
	lines := make([]string, 0, b.h)
	byBucket := map[workspace.Attention][]Item{}
	for _, it := range m.mergedItemsAll() {
		if it.Optimistic {
			// A workspace still being created is a spinner on its own row and
			// nothing to act on yet — countAttention skips it for the same reason.
			continue
		}
		kind := workspace.Classify(it.Status, it.Unread)
		if kind == workspace.AttentionNone {
			continue
		}
		byBucket[kind] = append(byBucket[kind], it)
	}

	// The strip is what the deck's own row leaves, and its rows start on the same
	// text column the row list's do.
	for _, bucket := range sidebarBuckets {
		rows := byBucket[bucket.kind]
		if len(rows) == 0 {
			continue
		}
		lines = append(lines,
			m.sidebarBucketStyle(bucket.kind).Bold(true).Render(bucket.label+" "+strconv.Itoa(len(rows))))
		for _, it := range rows {
			lines = append(lines, m.sidebarRow(it))
		}
		lines = append(lines, "")
	}
	if len(lines) == 0 {
		lines = append(lines, m.styles.Muted.Render("nothing waiting"))
	}
	// Overflow is a count rather than a scroll: nothing can move a cursor in here,
	// so a viewport would be a scrollable region with no key that scrolls it. The
	// number is the honest thing to say instead of a list that silently stops.
	if len(lines) > b.h {
		hidden := len(lines) - b.h
		lines = lines[:max(0, b.h-1)]
		lines = append(lines, m.styles.Muted.Render("+"+strconv.Itoa(hidden+1)+" more"))
	}
	return lipgloss.NewStyle().Width(b.w).Height(b.h).Render(strings.Join(lines, "\n"))
}

// sidebarRow is one workspace: the status dot the row list would give it, its
// project, and its name.
//
// The workspace you are in wears a `┃` bar in Muted — the "pane the keyboard has
// left" tier of the selection treatment. It marks where you are without claiming
// to be the cursor, which is what the full-strength bar means and there is no
// cursor in here to mean it.
func (m Model) sidebarRow(it Item) string {
	bar := "  "
	if p := m.topRowSubject(); p != nil && p.project == it.ProjectName && p.workspace == it.WorkspaceName {
		bar = m.styles.Muted.Render("┃") + " "
	}
	glyph := statusGlyph(it.Status, false, it.Unread)
	// The project is a chip rather than a line of its own: a header per project
	// would spend a third of a short strip on names, where the rows it groups are
	// often one apiece.
	chip := m.styles.Muted.Render(it.ProjectName + "/")
	room := sidebarWidth - lipgloss.Width(bar) - lipgloss.Width(glyph) - lipgloss.Width(chip) - 1
	if room < sidebarNameMin {
		// No room for both. The workspace is the part that identifies the row.
		chip = ""
		room = sidebarWidth - lipgloss.Width(bar) - lipgloss.Width(glyph) - 1
	}
	return bar + glyph + " " + chip + truncate(it.WorkspaceName, max(1, room))
}

// sidebarNameMin is the shortest a workspace name is worth truncating to beside
// its project. Below it the chip has eaten the name it was labelling.
const sidebarNameMin = 8

// sidebarHint is how the ctrl+| menus name the key.
func sidebarHint(on bool) string {
	if on {
		return "S hide sidebar"
	}
	return "S sidebar"
}
