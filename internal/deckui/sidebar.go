package deckui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/workspace"
)

// The sidebar is a narrow strip down the left of a pane or a split, holding every
// workspace, sectioned so the ones wanting you are the ones at the top.
//
// It exists because the top row's badge says *how many* and not *which*: three
// yellow dots is enough to know something is waiting and not enough to decide
// whether to leave what you are in. Leaving to find out is the thing the row was
// added to avoid, and it costs the pane a repaint of whatever program is in it.
//
// It reads and does nothing. There is no cursor in it, so no key moves one, so
// the arrangement verbs behind ctrl+b still address two halves rather than three
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

// sidebarKey is the verb, in both ctrl+b menus. Capital `S` because `s` is
// already the window key for a shell, and the two live in the same menu.
const sidebarKey = "S"

// sidebarWidth is the strip's width, in columns.
//
// Fixed rather than a fraction of the terminal. What goes on a row is a glyph and
// a name, which is the same number of columns whether the terminal is 120 wide or
// 400 — a fraction would spend a quarter of a wide screen on names that stopped
// needing the room twenty columns ago.
//
// 36 rather than the 28 it started at. A row's useful content is a PR number and
// the head of its title, and at 28 — less padding, bar and dot — that left about
// sixteen columns, which truncated `fix(lint): drop the dead branch` to `fix(l...`.
// The eight extra columns come off a pane that has plenty and hand the row back
// most of a subject line. Narrow enough that a 120-column terminal still carries a
// usable pane beside it.
const sidebarWidth = 36

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

// toggleSidebar is what ctrl+b S does.
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
	if m.saveSidebar != nil {
		if err := m.saveSidebar(m.sidebar); err != nil {
			// Same treatment the scope's saver gets: the strip is already on or off,
			// which is what you asked for, and the only thing lost is that it will not
			// be next time. Worth saying, not worth refusing.
			m.status = fmt.Sprintf("sidebar: %v", err)
		}
	}
}

// SidebarSaver records whether the attention strip should be up next time.
//
// A hook for the reason ScopeSaver is one: deckui is the UI and has no business
// knowing where ~/.awp is. What it saves is the intent, not showsSidebar — a
// terminal too narrow to fit the strip today must not erase the answer for a wider
// one tomorrow.
//
// Deliberately its own hook rather than a general "save the deck's preferences",
// which would hand deckui a preferences struct to know the shape of. Two settings
// was not enough to be worth that, and this comment used to say a third would be
// when to reconsider. A third arrived (SplitFracSaver, #359) and the answer held:
// each saver takes exactly the value its key stores and nothing else, while a
// preferences struct would make every save a read-modify-write of a shape deckui
// would have to know — and knowing that shape is the thing these hooks exist to
// avoid. Reconsider again if a setting ever has to be saved together with another.
type SidebarSaver func(bool) error

// WithSidebar opens the deck with the strip in the state it was left in.
func (m Model) WithSidebar(on bool) Model {
	m.sidebar = on
	return m
}

// WithSidebarSaver sets the hook called when the sidebar is toggled.
func (m Model) WithSidebarSaver(save SidebarSaver) Model {
	m.saveSidebar = save
	return m
}

// sidebarPadX / sidebarPadY are the strip's own inset, inside its width.
//
// The deck spends nothing on its own frame (see layout.go), and for the same
// reason: it is the outermost program in its terminal and an inset there buys a
// gap against the edge of the world. The strip is not on an edge — it butts
// against a pane's border — so its rows need a column of air on both sides or
// they read as touching the border, and a row of it at the top so the first
// header is not level with the pane's top corner.
const (
	sidebarPadX = 1
	sidebarPadY = 1
)

// sidebarView is the read model the strip renders from: every workspace, in the
// all scope's own order.
//
// Every workspace rather than the attention scope's, which is what it used to be.
// The strip now has an `idle` section, and a scope that filters idle rows out
// cannot fill it. What the strip is for moved with that: it was "which of the
// things wanting you is which", and it is now the whole list, sectioned so the
// ones wanting you are the ones at the top.
//
// Unfiltered: the same argument countAttention makes for the badge. What the strip
// shows cannot depend on a filter typed into a row list that is not even on screen
// from inside a pane.
func (m Model) sidebarView() deckdata.View {
	v := m.rm()
	v.Scope = deckdata.ScopeAll
	v.Filter = ""
	return v
}

// renderSidebar draws the strip into the box it was given.
func (m Model) renderSidebar(b box) string {
	inner := max(1, b.w-2*sidebarPadX)
	avail := max(1, b.h-2*sidebarPadY)

	v := m.sidebarView()
	groups := sidebarSections(v.Items())

	lines := make([]string, 0, 2*len(v.Items())+len(groups))
	for _, g := range groups {
		// A blank row above each group but the first. It costs a workspace the
		// strip could have listed, and it is worth it: the groups are what makes
		// the strip scannable rather than a list, and colour alone did not
		// separate them enough to find the one you were looking for.
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.sidebarSectionStyle(g.section).Render(
			truncate(sidebarSectionLabel(g.section), inner)))
		for _, it := range g.items {
			lines = append(lines, m.sidebarRow(v, it, inner)...)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, m.styles.Muted.Render("no workspaces"))
	}
	// Overflow is a count rather than a scroll: nothing can move a cursor in here,
	// so a viewport would be a scrollable region with no key that scrolls it. The
	// number is the honest thing to say instead of a list that silently stops.
	if len(lines) > avail {
		hidden := len(lines) - avail
		lines = lines[:max(0, avail-1)]
		lines = append(lines, m.styles.Muted.Render("+"+strconv.Itoa(hidden+1)+" more"))
	}
	return lipgloss.NewStyle().
		Width(b.w).Height(b.h).
		Padding(sidebarPadY, sidebarPadX).
		Render(strings.Join(lines, "\n"))
}

// sidebarSection is the band a row sits in on the strip.
//
// Agent state, not deckdata.Reason, which is what it was and what made the strip
// unreadable: a reason is a per-row answer to "why is this here", and several of
// them describe the same row from different angles, so walking the scope in its
// own order and starting a group whenever the reason changed re-opened a section
// that had already been printed further up. Sections have to be a partition — one
// row lands in exactly one of these — for a header to mean "everything below is
// this", which is the only reason a header is worth its row.
//
// Declaration order is render order.
type sidebarSection int

const (
	// sectionPinned is every row carrying a register, whatever its agent is doing.
	// Pinned is a statement about the workspace rather than about this minute, so
	// it outranks the state bands: you pinned it because you want it in front of
	// you, and a pin that sorted under `idle` because the agent happens to be
	// between turns is the pin not working.
	sectionPinned sidebarSection = iota
	sectionWaiting
	sectionError
	// sectionReady is the grey dot: an agent that finished, with the mark still
	// unread. It is a turn you have not looked at yet.
	sectionReady
	sectionWorking
	// sectionIdle is everything with nothing to say — no dot at all.
	sectionIdle
	sidebarSectionCount
)

// sidebarGroup is one section and the rows under it. Empty sections are dropped
// before this, so a group always has rows.
type sidebarGroup struct {
	section sidebarSection
	items   []Item
}

// sidebarSections partitions the rows into the strip's bands, in render order.
//
// Rows keep the scope's order inside a band, except idle, which is sorted most
// recently active first: the band has no urgency to rank by, so the useful
// question is "where was I", and a workspace last touched in March is not the one
// you want at the top of it. A zero LastActiveAt is unknown rather than ancient,
// but it still sorts last — an unknown row is the one we have no reason to raise.
func sidebarSections(rows []Item) []sidebarGroup {
	var byBand [sidebarSectionCount][]Item
	for _, it := range rows {
		band := sidebarSectionOf(it)
		byBand[band] = append(byBand[band], it)
	}
	idle := byBand[sectionIdle]
	sort.SliceStable(idle, func(i, j int) bool {
		return idle[i].LastActiveAt.After(idle[j].LastActiveAt)
	})

	groups := make([]sidebarGroup, 0, sidebarSectionCount)
	for band := sidebarSection(0); band < sidebarSectionCount; band++ {
		if len(byBand[band]) > 0 {
			groups = append(groups, sidebarGroup{section: band, items: byBand[band]})
		}
	}
	return groups
}

// sidebarSectionOf is which band one row belongs to.
//
// The order of the checks is the partition: pinned wins over everything, then the
// three states that mean something is up, then the grey dot, and what is left has
// nothing to report. `waiting` and `error` are read off the status whether or not
// the mark is unread — the dot is an attention signal and goes quiet once you have
// looked, but an agent that stopped to ask you something is still stopped, and
// filing it under `idle` because you have already seen the question once is the
// strip forgetting the thing it is for.
func sidebarSectionOf(it Item) sidebarSection {
	status := strings.ToLower(strings.TrimSpace(it.Status))
	switch {
	case strings.TrimSpace(it.PinGroup) != "":
		return sectionPinned
	case workspace.IsWorking(status):
		return sectionWorking
	case status == "waiting":
		return sectionWaiting
	case status == "error":
		return sectionError
	case statusGlyphVisible(status, it.Unread):
		return sectionReady
	}
	return sectionIdle
}

// sidebarSectionLabel is the header over a band.
func sidebarSectionLabel(s sidebarSection) string {
	switch s {
	case sectionPinned:
		return "pinned"
	case sectionWaiting:
		return "waiting"
	case sectionError:
		return "error"
	case sectionReady:
		return "ready"
	case sectionWorking:
		return "working"
	case sectionIdle, sidebarSectionCount:
	}
	return "idle"
}

// sidebarSectionStyle is the hue a header wears: the one its rows' status dots
// already wear, so the strip is read with the vocabulary the rest of the deck
// taught. Pinned is Accent — the structural hue the deck gives project headers,
// which is what a register is.
func (m Model) sidebarSectionStyle(s sidebarSection) lipgloss.Style {
	switch s {
	case sectionPinned:
		return m.styles.Accent.Bold(true)
	case sectionWaiting:
		return m.styles.Warning.Bold(true)
	case sectionError:
		return m.styles.Danger.Bold(true)
	case sectionWorking:
		return m.styles.Success.Bold(true)
	case sectionReady, sectionIdle, sidebarSectionCount:
	}
	return m.styles.Muted.Bold(true)
}

// sidebarRow is one workspace, over two lines:
//
//	● workspace-name
//	  ◐ ⇅ #412 andrew/thing
//
// Two lines rather than one because the strip is 36 columns and the row has two
// unrelated facts to carry — which workspace, and where its PR stands. On one line
// they compete: the number and the glyphs are fixed-width and go first, so the
// name is what truncates, and a truncated name is the one field you cannot work
// out from the others. Given a line of its own the name gets the whole strip, and
// the PR line under it is free to be missing, which for a workspace with no PR it
// is.
//
// The workspace you are in wears a `┃` bar in Muted — the "pane the keyboard has
// left" tier of the selection treatment. It marks where you are without claiming
// to be the cursor, which is what the full-strength bar means and there is no
// cursor in here to mean it.
func (m Model) sidebarRow(v deckdata.View, it Item, width int) []string {
	bar := "  "
	if p := m.topRowSubject(); p != nil && p.project == it.ProjectName && p.workspace == it.WorkspaceName {
		bar = m.styles.Muted.Render("┃") + " "
	}
	glyph := statusGlyph(it.Status, false, it.Unread)
	// Every row is the same shape — bar, dot, name — whatever the row is about.
	// Rows with a dot and rows without used to sit at different indents, so nothing
	// lined up vertically and the drift cost columns on both kinds.
	name := truncate(it.WorkspaceName, max(1, width-lipgloss.Width(bar)-lipgloss.Width(glyph)-1))
	lines := []string{bar + glyph + " " + name}
	if meta := m.sidebarMeta(v, it, max(1, width-len(sidebarIndent))); meta != "" {
		lines = append(lines, sidebarIndent+meta)
	}
	return lines
}

// sidebarMeta is a row's second line: the PR glyph cluster, the PR number, and the
// bookmark the workspace is on. "" when the row has none of the three, which is
// the second line not being drawn at all.
//
// prGlyphCluster, not a spelling of its own — the deck's other three surfaces
// render that cluster in that order, and the eye reads the positions, so a fourth
// surface assembling its own would be showing the same glyphs and meaning
// something else by them. Only the bookmark truncates: it is the field that varies
// in length, and the glyphs and the number are the fixed part they were budgeted
// against.
func (m Model) sidebarMeta(v deckdata.View, it Item, width int) string {
	segs := make([]string, 0, 3)
	if cluster := m.prGlyphCluster(it); cluster != "" {
		segs = append(segs, cluster)
	}
	number := ""
	if pr, ok := v.ResolvePRStatus(it); ok {
		number = "#" + strconv.Itoa(pr.Number)
	}
	if number != "" {
		segs = append(segs, m.styles.Info.Render(number))
	}
	if len(segs) == 0 && strings.TrimSpace(it.Bookmark) == "" {
		return ""
	}
	head := strings.Join(segs, " ")
	if bookmark := strings.TrimSpace(it.Bookmark); bookmark != "" {
		room := width - lipgloss.Width(head)
		if head != "" {
			room--
		}
		if bookmark = truncate(bookmark, max(1, room)); head != "" {
			head += " "
		}
		head += m.styles.Muted.Render(bookmark)
	}
	return head
}

// sidebarIndent is what a row's second line is inset by: the columns the first
// line spends on its selection bar, so the PR line reads as belonging to the name
// above rather than as a row of its own.
const sidebarIndent = "  "

// sidebarVerb is how the ctrl+b menus name the key.
//
// The description says which way the toggle goes, because with the strip already up
// "show or hide" leaves you to work out which one pressing it does.
func sidebarVerb(on bool) [2]string {
	if on {
		return [2]string{sidebarKey, "hide the attention sidebar"}
	}
	return [2]string{sidebarKey, "show the attention sidebar"}
}
