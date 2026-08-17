package deckui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
// **Colour marks structure here, not content.** The section headers keep their hues
// and so do the status dots: there are six headers on a screen and one dot per row,
// and between them they are the skeleton that says where you are in the list.
//
// A row's *second* line is muted throughout — PR glyphs, PR number, bookmark, project
// — where it used to carry the glyph cluster's own colours and a blue PR number. That
// is the line there is one of per row, so a hue on it is a hue repeated down the whole
// strip, and at 36 columns with two lines per row the repetition is what made the strip
// noisy rather than the headers. Emphasis spent everywhere is emphasis nowhere.
//
// So the rule for this file is narrower than "use the palette" and narrower than "mute
// it": chrome that appears a handful of times may carry a hue, and anything that
// appears once per row may not.
//
// **It is somewhere you can go.** It has a cursor, and every key that works on a
// deck row works on the row under it — the strip is not a second, weaker list with a
// subset of verbs but the deck's own row list in a narrow column.
//
// It was read-only first, and the thing that blocked the keyboard was focus: a
// pane, a split half and a strip on screen at once, and no obvious answer to which
// of them the keys belong to. What dissolved it was making the door a cycle rather
// than a mode. ctrl+\ already means "somewhere else"; it now means it three times —
// pane → sidebar → deck → pane — so there is no mode to be in that one press does
// not leave, and nothing new to learn. See sidebar_cursor.go, which is the whole of
// the keyboard's half.
//
// The arrangement verbs behind ctrl+b still address two halves rather than three
// regions: `h` / `l` / `tab` mean what they meant, and ctrl+b from the strip is
// forwarded to the arrangement, because the strip belongs to the deck rather than to
// what is beside it.
//
// It is a property of the deck rather than of the arrangement (see
// paneArrangement, which remembers what programs were on screen): the answer to
// "do I want to see what is waiting" does not change when you switch panes, so
// it stays on until you turn it off.

// sidebarKey is the verb, in both ctrl+b menus. Capital `S` because `s` is
// already the window key for a shell, and the two live in the same menu.
const sidebarKey = "S"

// sidebarDefaultWidth is the strip's width until you drag its edge, in columns.
//
// A column count rather than a fraction of the terminal, and that is still true now
// the number is yours to change. What goes on a row is a glyph and a name, which is
// the same number of columns whether the terminal is 120 wide or 400 — a fraction
// would spend a quarter of a wide screen on names that stopped needing the room
// twenty columns ago. So the drag records columns, and a wider terminal gives the
// extra room to the pane rather than to the strip.
//
// 36 rather than the 28 it started at. A row's useful content is a PR number and
// the head of its title, and at 28 — less padding, bar and dot — that left about
// sixteen columns, which truncated `fix(lint): drop the dead branch` to `fix(l...`.
// The eight extra columns come off a pane that has plenty and hand the row back
// most of a subject line. Narrow enough that a 120-column terminal still carries a
// usable pane beside it.
const sidebarDefaultWidth = 36

// sidebarMinWidth is the narrowest the strip may be dragged to.
//
// Below this it stops being a strip of rows and becomes a column of truncation: a
// row spends two columns on the status dot and two more on its padding, so at 20
// there are sixteen left for a name, which is about where `pr-2365-lantern-consumer`
// still says something. Narrower than that and every row reads the same.
//
// It is a floor on the drag rather than a floor on the strip existing — see
// sidebarWidth, which clamps a remembered width into what the terminal can spare.
const sidebarMinWidth = 20

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
	return m.sidebar && m.hostsTerminal() && m.sidebarFits()
}

// sidebarFits reports whether this terminal has room for the strip at all: the
// narrowest strip, beside the narrowest pane.
//
// Asked against sidebarMinWidth rather than against the width you dragged to, so a
// strip you widened on a big screen does not disappear on a small one — it comes
// back narrow. Only a terminal with no room for even the minimum refuses.
func (m *Model) sidebarFits() bool {
	return m.width-sidebarMinWidth >= sidebarChildMinW
}

// sidebarWidth is how many columns the strip actually gets: what you dragged it
// to, or the default, clamped to what this terminal can spare.
//
// The clamp is here rather than at the drag because the terminal can change size
// after the drag. A width is remembered as the number you chose, and every screen
// it is shown on answers for itself how much of that it can honour — the same
// argument SaveDeckSplit makes for not validating a fraction on the way in.
func (m *Model) sidebarWidth() int {
	want := m.sidebarW
	if want <= 0 {
		want = sidebarDefaultWidth
	}
	room := max(m.width-sidebarChildMinW, sidebarMinWidth)
	return min(max(want, sidebarMinWidth), room)
}

// sidebarCols is what the strip costs the child, which is nothing when it is not
// up. One function so childBox and the renderer cannot disagree about where the
// child starts.
func (m *Model) sidebarCols() int {
	if m.showsSidebar() {
		return m.sidebarWidth()
	}
	return 0
}

// toggleSidebar is what ctrl+b S does.
//
// A terminal too narrow refuses and says the width it wants, rather than setting
// a flag that renders nothing — a key that appears to do nothing reads as broken,
// and the flag would then surprise you by taking effect on the next resize.
func (m *Model) toggleSidebar() {
	if !m.sidebar && !m.sidebarFits() {
		m.status = fmt.Sprintf("sidebar: this terminal is %d columns, %d needed for a strip beside a pane",
			m.width, sidebarMinWidth+sidebarChildMinW)
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

// sidebarLine is one rendered line of the strip and the row it belongs to.
//
// The pairing exists because a click arrives as a screen row and has to come back
// as a workspace, and the only thing that knows which line is which is the loop that
// laid them out. It used to return bare strings, so there was nowhere to ask — see
// sidebarRowAt, which walks these.
//
// item is nil for a line that is not a row: a section header, a separator, the
// overflow count. Those are the strip's own furniture and clicking one means nothing.
type sidebarLine struct {
	text string
	item *Item
}

// renderSidebar draws the strip into the box it was given.
func (m Model) renderSidebar(b box) string {
	lines := m.sidebarLines(b)
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.text
	}
	// Vertical padding only. The horizontal inset moved into the lines themselves
	// (see sidebarGutter) because a banded row has to be able to own it: a band that
	// stops at the padding reads as a highlight of the text rather than of the row,
	// and padding applied out here is outside every line's own style by construction.
	return lipgloss.NewStyle().
		Width(b.w).Height(b.h).
		Padding(sidebarPadY, 0).
		Render(strings.Join(texts, "\n"))
}

// sidebarGutter is the strip's horizontal inset, as a string.
//
// A row's own style paints it — plain for an ordinary row, the band for the one you
// are in — so the band reaches both edges of the strip rather than floating inside
// it. The inset is still the strip's (the argument for it is in sidebarPadX: the strip
// butts against a pane's border, so its text needs a column of air on each side); what
// changed is who draws those columns.
var sidebarGutter = strings.Repeat(" ", sidebarPadX)

// The cursor wears no `┃` bar here, which is the one place the strip departs from the
// design system's selection treatment.
//
// The bar costs a column ahead of the status dot on *every* line — a header that
// skipped the slot would sit two columns left of the names under it — so the whole
// strip pays two of its 36 columns for a mark that only one row at a time wears. On a
// list where names are already truncating, that is the wrong trade: the bar's job is
// to be findable at a glance down a wide screen, and this list is narrow enough that
// a hue does it alone.
//
// So the cursor is the name in `Warning` + bold, which is the other half of the same
// treatment, and the band still means "the workspace you are in". Two marks, two
// facts, no columns.
//
// sidebarHasCursor reports whether the strip's cursor is on a row at all.
//
// The cursor is held as the row itself rather than as an index, because the strip's
// order is not the row list's — it is scope-all, unfiltered, and partitioned into
// bands — so an index shared between the two would name a different workspace on
// each. An identity survives the poll that re-bands a row, which is the same
// argument keepCursorOn makes about the row list.
//
// A virtual row has no workspace name and is still a row, so it answers on Virtual.
func (m Model) sidebarHasCursor() bool {
	return m.sidebarCursor.Virtual || strings.TrimSpace(m.sidebarCursor.WorkspaceName) != ""
}

// sidebarOnCursor reports whether this row is the one the strip's keys point at.
func (m Model) sidebarOnCursor(it Item) bool {
	return m.sidebarHasCursor() && sameRow(m.sidebarCursor, it)
}

// sidebarLines lays the strip out: every line it will draw, each tied to the row it
// came from.
//
// Split from renderSidebar so the layout has exactly one author. A hit test that
// re-derived which line is which — counting two lines per row plus a header plus the
// separators — would be a second implementation of this loop, agreeing with it until
// one of them changed.
func (m Model) sidebarLines(b box) []sidebarLine {
	inner := max(1, b.w-2*sidebarPadX)
	avail := max(1, b.h-2*sidebarPadY)

	v := m.sidebarView()
	// The clock is read here rather than held on the Model: nothing else in the strip
	// needs one, and a field would be a second thing to keep wound. sidebarSections
	// takes it as an argument so a test can say which moment it means.
	groups := sidebarSections(v.Items(), m.pinGroupAliases, time.Now())

	lines := make([]sidebarLine, 0, 2*len(v.Items())+len(groups))
	// Furniture — headers, separators, the overflow count — takes the inset as plain
	// spaces. Only a row's own lines paint it, so only a row's band can reach the
	// strip's edges. See sidebarGutter.
	text := func(s string) {
		if s != "" {
			s = sidebarGutter + s
		}
		lines = append(lines, sidebarLine{text: s})
	}
	for _, g := range groups {
		// A blank row above each group but the first. It costs a workspace the
		// strip could have listed, and it is worth it: the groups are what makes
		// the strip scannable rather than a list, and colour alone did not
		// separate them enough to find the one you were looking for.
		if len(lines) > 0 {
			text("")
		}
		text(m.sidebarSectionStyle(g.section).Render(
			truncate(sidebarGroupLabel(g, m.pinGroupAliases), inner)))
		for i, it := range g.items {
			// And a blank row between rows, so a row is a block with air around it
			// rather than one stripe in a wall of text. A row is two lines, and the
			// cadence alone — name, detail, name, detail — asks the eye to count in
			// order to know which lines belong together; the gap says it without
			// counting.
			//
			// It costs a third of the strip's height, which is the trade. There is no
			// cheaper separator: half a row is not addressable, since a cell is the
			// smallest unit there is vertically, and the trick that would have cost no
			// row at all — an underline on the row's last line, drawn at the bottom edge
			// of the cell — does not survive coloured content. Every inner lipgloss
			// style ends in a full SGR reset, which cancels the underline mid-line, so
			// the rule breaks at each coloured segment; via lipgloss's UnderlineSpaces
			// it is worse, rewriting the line cell by cell and underlining the escape
			// sequences as literal text.
			if i > 0 {
				text("")
			}
			// Both of a row's lines carry the row, so clicking either the name or the
			// detail under it means the same workspace. The row is one thing on screen
			// and the eye reads it as one; a click that only counted on the first line
			// would be a target half the size of the thing it looks like.
			row := it
			for _, l := range m.sidebarRow(v, it, b.w) {
				lines = append(lines, sidebarLine{text: l, item: &row})
			}
		}
	}
	if len(lines) == 0 {
		text(m.styles.Muted.Render("no workspaces"))
	}
	if len(lines) <= avail {
		return lines
	}
	// The strip scrolls now, because the cursor can walk off the bottom of it. What
	// it scrolls by is the cursor and nothing else: there is no wheel over these
	// columns (see clickSidebarRow) and no key that scrolls without moving, so the
	// window is derived from where the cursor is rather than remembered.
	//
	// That is why this is not a viewport. A viewport's substance is a scroll position
	// it holds between frames, and a held position here would be a second thing that
	// can be wrong about the same fact — a strip showing rows 20-40 with the cursor on
	// row 3. The window is a pure function of the lines, the height and the cursor, so
	// the two cannot disagree.
	top := sidebarScrollTop(lines, avail, m.sidebarCursorLine(lines))
	shown := lines[top:min(top+avail, len(lines))]
	// A count of what is below, in the last row of the window rather than in addition
	// to it — the honest thing to say instead of a list that silently stops. Only
	// below: a strip scrolled down is a strip whose top the cursor came through, so
	// what is above it is not news.
	if hidden := len(lines) - (top + len(shown)); hidden > 0 {
		shown = append(shown[:len(shown)-1:len(shown)-1],
			sidebarLine{text: sidebarGutter +
				m.styles.Muted.Render("+"+strconv.Itoa(hidden+1)+" more")})
	}
	return shown
}

// sidebarCursorLine is which line of the strip the cursor's row starts on, or -1
// when the cursor is not on the strip at all.
func (m Model) sidebarCursorLine(lines []sidebarLine) int {
	if !m.sidebarHasCursor() {
		return -1
	}
	for i, l := range lines {
		if l.item != nil && sameRow(*l.item, m.sidebarCursor) {
			return i
		}
	}
	return -1
}

// sidebarScrollTop is the first line of the window: far enough down that the
// cursor's row is inside it, and no further.
//
// A row is two lines and it scrolls as one — a window cut between a name and its
// meta line shows half a row, which the fixed two-line cadence exists to make
// impossible to misread.
//
// With no cursor the top is the top. That is the strip as it was before #350 and as
// it still is while the pane holds the keyboard: a glance at what is waiting, which
// is sorted so that what wants you is what is on screen.
func sidebarScrollTop(lines []sidebarLine, avail, cursor int) int {
	if cursor < 0 || cursor < avail {
		return 0
	}
	// The cursor's row, both of its lines, against the bottom of the window.
	return min(cursor+2-avail, max(0, len(lines)-avail))
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
	//
	// It is also the one band that renders as more than one group: it splits by
	// register, so a named register is its own section rather than a row under one
	// flat `pinned` word. See sidebarPinnedGroups for which registers get a header
	// and which fold onto their rows.
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
	// register is the pin register this group is for, on a sectionPinned group
	// whose register has been named. "" everywhere else — on every other band,
	// and on the folded pinned group that collects the unnamed registers.
	register string
	items    []Item
}

// sidebarSections partitions the rows into the strip's bands, in render order.
//
// Rows keep the scope's order inside a band, except idle, which is ranked by how
// recently it was touched — coarsely, in the buckets idleRecency names, and by label
// inside a bucket.
//
// Coarsely, and that is the whole point. It used to sort on LastActiveAt directly,
// which is a live timestamp: opening a workspace clears its unread mark, clearing the
// mark calls Entry.Touch(now), and Touch writes the field this sorts on. So clicking a
// row on the strip sent it to the top of its band and shifted every row below it down
// — the list reshuffling under your own hand, on the surface whose whole job is to be
// glanced at while you work somewhere else.
//
// This is the same cure #284 applied to the attention scope, where an agent's lifecycle
// kept moving its row: replace the continuous value with a band, and rank by the band.
// A Touch now moves a row only when it crosses a boundary — at most once, and when it
// does the move means something.
//
// What is given up is fine-grained "where was I" ordering. That was worth having on the
// band which by definition wants nothing from you, and it was not worth the strip
// moving while you read it.
// The pinned band is the one band that sub-partitions, into a group per register.
// See sidebarPinnedGroups for which registers get a group of their own.
func sidebarSections(rows []Item, aliases map[string]string, now time.Time) []sidebarGroup {
	var byBand [sidebarSectionCount][]Item
	for _, it := range rows {
		band := sidebarSectionOf(it)
		byBand[band] = append(byBand[band], it)
	}
	idle := byBand[sectionIdle]
	sort.SliceStable(idle, func(i, j int) bool {
		a, b := idleRecency(idle[i].LastActiveAt, now), idleRecency(idle[j].LastActiveAt, now)
		if a != b {
			return a < b
		}
		// Inside a bucket, the label — which is what you are reading, so the order
		// matches the text. Any stable key would do; the requirement is only that it
		// cannot change when an agent does something, which is what the timestamp
		// could not promise.
		return sidebarLabel(idle[i]) < sidebarLabel(idle[j])
	})

	groups := make([]sidebarGroup, 0, sidebarSectionCount)
	for band := sidebarSection(0); band < sidebarSectionCount; band++ {
		switch {
		case len(byBand[band]) == 0:
		case band == sectionPinned:
			groups = append(groups, sidebarPinnedGroups(byBand[band], aliases)...)
		default:
			groups = append(groups, sidebarGroup{section: band, items: byBand[band]})
		}
	}
	return groups
}

// sidebarPinnedGroups splits the pinned band into one group per register.
//
// A flat `pinned` header was the whole band under one word, which threw away the
// grouping registers exist to make — at the surface you are most likely to be
// reading it off, since the strip is what is in front of you while you work
// somewhere else. The row list has sectioned pins by register since they landed;
// this is the strip catching up.
//
// It does not catch up exactly, and the two divergences are both the strip's width.
// A header costs two rows here (itself, plus the blank sidebarLines puts above every
// group), against three for a workspace, so on 36 columns a header has to be worth a
// third of a row to print:
//
//   - A named register always gets one, showing its alias. The user typed that name
//     to create this exact grouping, so it is content rather than chrome, and a
//     one-member named register still earns its rows — when it gains a second member
//     the section is already there rather than the strip reshaping around it.
//   - An unnamed register does not. They fold together into one trailing `pinned`
//     group, each row carrying its register letter as a chip on its meta line
//     instead (see sidebarMeta). A bare letter is four columns of information and
//     does not justify two rows of a strip this narrow.
//
// So the strip never shows a bare-letter header, and it orders every named register
// ahead of the folded pile — where the row list interleaves them and puts the default
// register first. The row list has the width to spend and keeps its own behaviour.
//
// Folding onto the row is only for the letter. It was considered for a single-member
// *named* register too and is wrong: an alias runs 8–15 characters, which on the name
// line truncates the workspace name — the one field sidebarRow exists to protect —
// and on the muted meta line loses its hue and reads as a branch.
func sidebarPinnedGroups(rows []Item, aliases map[string]string) []sidebarGroup {
	named := map[string][]Item{}
	var order []string
	var folded []Item
	for _, it := range rows {
		reg := strings.TrimSpace(it.PinGroup)
		// A named default register is a named register: the user overrode the word
		// "pinned" with something, and that something is what the header should say.
		if strings.TrimSpace(aliases[reg]) == "" {
			folded = append(folded, it)
			continue
		}
		if _, seen := named[reg]; !seen {
			order = append(order, reg)
		}
		named[reg] = append(named[reg], it)
	}
	// The row list's own register order, so the two surfaces agree on which named
	// register comes first even though they disagree about the unnamed ones.
	sort.SliceStable(order, func(i, j int) bool {
		return deckdata.PinGroupSortKey(aliases, order[i]) < deckdata.PinGroupSortKey(aliases, order[j])
	})

	groups := make([]sidebarGroup, 0, len(order)+1)
	for _, reg := range order {
		groups = append(groups, sidebarGroup{section: sectionPinned, register: reg, items: named[reg]})
	}
	if len(folded) > 0 {
		groups = append(groups, sidebarGroup{section: sectionPinned, items: folded})
	}
	return groups
}

// sidebarGroupLabel is the header over a group: a named register's alias on the
// pinned groups that have one, and the band's own word everywhere else — including
// the folded pinned group, which is the one that still says "pinned".
func sidebarGroupLabel(g sidebarGroup, aliases map[string]string) string {
	if g.section == sectionPinned && g.register != "" {
		return deckdata.PinGroupLabel(aliases, g.register)
	}
	return sidebarSectionLabel(g.section)
}

// idleRecency is the bucket a row's last activity falls in, lower being more recent.
//
// Four spans and an unknown. The spans are deliberately wide: what the idle band is
// answering is "roughly where was I", and a boundary a workspace crosses once an hour
// is a row that moves once an hour rather than every time you touch it.
//
// A zero time is unknown rather than ancient, and sorts last — which is the same
// treatment deckdata.attention gives it, and for the same reason: an entry written
// before the field existed is one we have no opinion about, and no opinion must not
// read as "stale since the epoch".
func idleRecency(at, now time.Time) int {
	if at.IsZero() {
		return idleUnknown
	}
	switch d := now.Sub(at); {
	case d < time.Hour:
		return 0
	case d < 24*time.Hour:
		return 1
	case d < 7*24*time.Hour:
		return 2
	default:
		return 3
	}
}

// idleUnknown is the bucket for a row with no recorded activity: last, behind every
// row we know something about.
const idleUnknown = 4

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
//
// The headers keep their hues, and so do the dots. They are the strip's skeleton:
// six of them on a screen, one per band, marking where you are in the list — which is
// the opposite of the per-row repetition that made the strip noisy. What came off is
// the second line of every row. See sidebarMeta.
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
// out from the others. Given a line of its own the name gets the whole strip.
//
// Always two, even for a workspace with no PR and no bookmark, whose second line is
// blank. That fixed cadence is what separates one row from the next, and it does the
// job a blank row between rows was doing for a row's worth less: every odd line down
// the strip is a name and every even one is its detail, so a meta line cannot be
// read as belonging to the name below it. A variable-height row plus a separator
// spent between two and three rows per workspace and still had to be scanned for
// where one ended; this spends exactly two, and the blank second line of a bare row
// is the separator, rather than being in addition to one.
//
// Nothing on a row is indented from its section header: the dot sits in the
// header's own first column, so a name starts two columns in and the header's
// letters and the strip's names read as one left edge rather than two. The strip
// is 36 columns and there is only one level of structure in it — a section and its
// rows — so an indent has nothing to say that the coloured bold header does not.
//
// That edge survived #350's cursor, which is why the cursor has no bar: the `┃` every
// other list in the deck wears needs a column ahead of the dot on every line, and the
// two it costs come off names that are already truncating at 36 columns. The cursor is
// the name's own hue instead — see the note above sidebarHasCursor.
//
// The workspace you are in is marked by a band behind both its lines instead. It used
// to be the name in Strong, which on a strip where every other label is already at
// the terminal default was very nearly invisible: bright white against white is a
// difference you have to look for, and this mark exists to be found without looking.
//
// A band is also the mark that survives a cursor arriving. "Which workspace am I in"
// and "which row do the keys point at" are two facts, they are usually about
// different rows, and each needs its own mark — so the band takes the background and
// leaves the bar and the selection hue for the cursor.
// width is the strip's whole width, gutters included: a row owns them, so its band
// reaches the strip's edges rather than stopping a column short on each side. Its
// text is laid out inside them.
func (m Model) sidebarRow(v deckdata.View, it Item, width int) []string {
	band := m.sidebarBand(it)
	gutter := band.Render(sidebarGutter)
	inner := max(1, width-2*sidebarPadX)
	glyph := statusGlyphOn(band, it.Status, false, it.Unread)
	// The cursor is the name's own weight and hue — no bar, no column. See the note
	// above sidebarHasCursor for why the strip declines the `┃` every other list wears.
	//
	// Only while the strip has the keyboard. That is the tier the design system gives
	// a pane the keyboard has left, spent here on the mark itself rather than on a
	// dimmer version of it: the row the keys will come back to is still the row you
	// are in, which the band is already saying.
	nameStyle := band
	if m.sidebarOnCursor(it) && m.sidebarFocus {
		nameStyle = band.Foreground(lipgloss.Color(colWarning)).Bold(true)
	}
	// Every row is the same shape — dot, space, name — whatever the row is about.
	// Rows with a dot and rows without used to sit at different indents, so nothing
	// lined up vertically and the drift cost columns on both kinds.
	name := truncate(sidebarLabel(it), max(1, inner-lipgloss.Width(glyph)-1))
	meta := m.sidebarMeta(band, v, it, max(1, inner-len(sidebarIndent)))
	return []string{
		bandFill(band, gutter+glyph+" "+nameStyle.Render(name), width),
		bandFill(band, gutter+band.Render(sidebarIndent)+meta, width),
	}
}

// sidebarBand is the base style a row's segments are built from: the band when this
// is the workspace you are in, and the plain style otherwise.
//
// Every segment of a banded row has to carry the background itself. An enclosing
// style cannot do it, because each inner lipgloss style ends in a full SGR reset —
// which takes the enclosing background with it from the first coloured segment
// onwards. The diff viewer hit this first and answered it the same way (see
// cursorlineBg and paintCode in internal/ui), and it is the same mechanism that stops
// an underline being used as a row separator here.
func (m Model) sidebarBand(it Item) lipgloss.Style {
	if p := m.topRowSubject(); p != nil && p.project == it.ProjectName && p.workspace == it.WorkspaceName {
		return m.styles.ActiveRow
	}
	return m.styles.Label
}

// bandFill pads a line out to the strip's width in the band's own style, so the band
// reaches the right edge instead of stopping where the text does.
//
// Explicitly rather than by wrapping the line in band.Width(width): the wrapper's
// background would be cancelled by the first inner reset, which is the whole reason
// each segment carries its own. Padding a plain row costs a few trailing spaces and
// keeps one code path.
func bandFill(band lipgloss.Style, line string, width int) string {
	if pad := width - lipgloss.Width(line); pad > 0 {
		return line + band.Render(strings.Repeat(" ", pad))
	}
	return line
}

// sidebarLabel is the name a row goes by.
//
// Two corrections to the raw workspace name, both of which the strip's own screen
// forced:
//
// A workspace called `default` is the repo root's, and the name says nothing —
// six projects each with one render as six rows called `default`. The project is
// its identity, so that is what it is called. The row list reached the same
// conclusion by a different route: collapsedProjects folds a lone `default` row
// into its project header because "the project name stands in for the workspace
// label".
//
// And a `pr-128-…` prefix goes, because the number is on the line below. The name
// carried it, the meta line carried it, and the strip is 36 columns wide — it was
// spending eight of them on a field it then repeated.
// A display label wins over all of it, and over the two rules below.
//
// Both of those rules are guesses at what a name is trying to say — that `default`
// means the project, that a `pr-128-` prefix is noise the line below already carries.
// A label is not a guess: someone said what this row is. Overriding it with either
// rule would mean the strip and the row list calling the same workspace two things,
// which is worse than either name alone.
//
// The row list's DisplayLabel prefers a PR title when there is no label; this
// deliberately does not. The strip is 36 columns and its second line already carries
// the PR number, so a title here would spend the width twice — see the note above
// about the `pr-` prefix, which is the same argument.
func sidebarLabel(it Item) string {
	if label := strings.TrimSpace(it.DisplayName); label != "" {
		return label
	}
	name := strings.TrimSpace(it.WorkspaceName)
	if name == "default" {
		if p := strings.TrimSpace(it.ProjectName); p != "" {
			return p
		}
	}
	return strings.TrimPrefix(name, prWorkspacePrefix(name))
}

// prWorkspacePrefix is the `pr-<digits>-` head of a workspace name, or "" when the
// name does not start with one. Matched rather than split on the first `-`, so a
// workspace genuinely called `pr-review-notes` keeps its name.
func prWorkspacePrefix(name string) string {
	rest, ok := strings.CutPrefix(name, "pr-")
	if !ok {
		return ""
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(rest) || rest[digits] != '-' {
		return ""
	}
	return name[:len("pr-")+digits+1]
}

// sidebarMeta is a row's second line: the PR glyph cluster, the PR number, and the
// bookmark the workspace is on. Never empty — see below.
//
// prGlyphCluster, not a spelling of its own — the deck's other three surfaces render
// that cluster in that order, and the eye reads the positions, so a fourth surface
// assembling its own would be showing the same glyphs and meaning something else by
// them.
//
// **The line always says something.** A blank second line kept the two-line cadence
// in the line count and lost it on screen: what the eye reads as a row is a block of
// text, so a name with nothing under it reads as a one-line row and the rhythm the
// cadence was for is gone.
//
// Where a row has no PR and no bookmark left to show, the line falls back to
// sidebarOtherIdent — whichever of the project and the workspace name the line above
// is not already using. A repo-root row goes by its project, so its second line is
// the word `default`: the pair reads as which repo over which workspace in it. Every
// other row goes by its workspace name, so its second line is the project — which is
// the one fact the strip otherwise drops, and the reason two same-named workspaces in
// different projects were indistinguishable here.
//
// The bookmark is dropped when its last segment is already the row's name — on a real
// deck most rows, since a workspace named after its branch put
// `andrew/refactor-parser` under `refactor-parser`, one string twice, line after line.
// Only the bookmark truncates: it is the field that varies in length, and the glyphs
// and the number are the fixed part it was budgeted against.
func (m Model) sidebarMeta(band lipgloss.Style, v deckdata.View, it Item, width int) string {
	muted := band.Foreground(lipgloss.Color(colMuted))
	segs := make([]string, 0, 3)
	if cluster := m.prGlyphCluster(it); cluster != "" {
		// Stripped of its own colours and re-rendered muted. prGlyphCluster hands back
		// a pre-coloured cluster because its other three call sites want the hues, and
		// the shape of the cluster is the part worth sharing — which glyphs, in which
		// order. The strip takes the shape and declines the palette.
		segs = append(segs, muted.Render(ansi.Strip(cluster)))
	}
	if pr, ok := v.ResolvePRStatus(it); ok {
		segs = append(segs, muted.Render("#"+strconv.Itoa(pr.Number)))
	}
	head := strings.Join(segs, band.Render(" "))

	tail := sidebarBookmark(it)
	if tail == "" && head == "" {
		tail = sidebarOtherIdent(it)
	}
	// The register chip, for a row sidebarPinnedGroups folded rather than gave a
	// header to. It goes first because it is the row's section — the thing a header
	// would have said above it — and muted like the rest of the line: it appears once
	// per row, which is the frequency the design system reserves the palette against.
	//
	// It is prepended here rather than joined into segs above so it does not count as
	// content when the identity fallback is chosen. It did, at first, and that dropped
	// the project from exactly the rows carrying a chip — leaving a second line reading
	// only `[q]`, which is the one thing sidebarOtherIdent exists to prevent: a chip is
	// not an identity, and a row that has one still owes you which project it is in.
	chip := ""
	if reg := sidebarFoldedRegister(it, m.pinGroupAliases); reg != "" {
		chip = muted.Render("[" + reg + "]")
	}
	if tail != "" {
		room := width - lipgloss.Width(head) - lipgloss.Width(chip)
		if head != "" {
			room--
			head += band.Render(" ")
		}
		head += muted.Render(truncate(tail, max(1, room)))
	}
	if chip != "" && head != "" {
		return chip + band.Render(" ") + head
	}
	return chip + head
}

// sidebarFoldedRegister is the register letter to print on a row's meta line, or ""
// for a row that needs none.
//
// Exactly the rows sidebarPinnedGroups folds: pinned, with no alias on their
// register. The default register folds without a chip — its group header already
// says "pinned", which is the whole of what "default" means, and `[default]` on
// every row of it would be eight columns saying it again.
func sidebarFoldedRegister(it Item, aliases map[string]string) string {
	reg := strings.TrimSpace(it.PinGroup)
	if reg == "" || reg == deckdata.PinGroupDefault {
		return ""
	}
	if strings.TrimSpace(aliases[reg]) != "" {
		return "" // has a header of its own
	}
	return reg
}

// sidebarOtherIdent is the half of a row's identity its name line is not spending:
// the workspace name where the row goes by its project, and the project where it goes
// by its workspace name. Between them a row is always identified, and neither line
// ever repeats the other.
// A row with no project falls back to its own name rather than to nothing: the line
// has to say something, and repeating the name is the last resort that always exists.
func sidebarOtherIdent(it Item) string {
	project, name := strings.TrimSpace(it.ProjectName), strings.TrimSpace(it.WorkspaceName)
	if project == "" || sidebarLabel(it) == project {
		return name
	}
	return project
}

// sidebarBookmark is the bookmark worth printing under a row, and "" when it only
// restates the name the row already wears — see sidebarMeta, which then falls back to
// the row's other identifier rather than leave the line empty.
func sidebarBookmark(it Item) string {
	bookmark := strings.TrimSpace(it.Bookmark)
	tail := bookmark
	if i := strings.LastIndexByte(bookmark, '/'); i >= 0 {
		tail = bookmark[i+1:]
	}
	if tail == sidebarLabel(it) {
		return ""
	}
	return bookmark
}

// sidebarIndent is what a row's second line is inset by: exactly the columns the
// first line spends before its name — the status dot and its space — so the two
// lines start in the same column.
//
// The indents were once header 0, name 4, meta 2: three of them for two levels of
// structure. The eye reads an indent as structure, so a meta line at neither its
// name's column nor its header's was claiming a level that does not exist.
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
