package deckui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/deckdata"
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
// is not enough to be worth that; a third is when to reconsider.
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

// attentionView is the read model the strip renders from: the attention scope's rows, in the scope's
// own order, grouped under the reason each of them is there for.
//
// The scope rather than a tally of agent states. The badge on the top row counts
// three things — waiting, working, unread — because it is three numbers wide, and
// the strip inherited that grouping when it was written. But the scope is the
// deck's actual answer to "what wants you", and it is wider: a PR whose review is
// requested, one whose CI has gone red, one approved and waiting to merge, a
// workspace you were in ten minutes ago. Grouping by agent state meant the strip
// said "nothing waiting" on a deck whose row list had a screenful — the strip and
// the `P` scope beside it disagreeing about the one question they both answer.
//
// Unfiltered, and over every workspace rather than the visible rows: the same
// argument countAttention makes for the badge. What wants you cannot depend on a
// filter typed into a list that is not even on screen from in here.
func (m Model) attentionView() deckdata.View {
	v := m.rm()
	v.Scope = deckdata.ScopeAttention
	v.Filter = ""
	return v
}

// renderSidebar draws the strip into the box it was given.
func (m Model) renderSidebar(b box) string {
	inner := max(1, b.w-2*sidebarPadX)
	avail := max(1, b.h-2*sidebarPadY)

	v := m.attentionView()
	rows := v.Items()

	lines := make([]string, 0, len(rows)+len(sidebarGroups))
	// A header whenever the reason changes, which needs no grouping pass of its
	// own: the scope is already ordered so that rows sharing a band are adjacent,
	// and walking it in its own order is what keeps the strip and the row list
	// listing the same workspaces in the same sequence.
	last := deckdata.ReasonNone
	// The project is a sub-row under the group, printed only when it changes,
	// rather than a chip repeated on every row. It was a chip, and the strip spent
	// eight of its twenty-six columns printing `alpha/` four times in a row while
	// the PR titles it was labelling truncated to `fix(l...` — three characters of
	// signal beneath a name already on screen above. Said once, the columns go back
	// to the part that differs.
	project := ""
	for _, it := range rows {
		reason := v.Wants(it)
		if reason != last {
			// A blank row above each group but the first. It costs a workspace the
			// strip could have listed, and it is worth it: the groups are what makes
			// the strip scannable rather than a list, and colour alone did not
			// separate them enough to find the one you were looking for.
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.sidebarGroupStyle(reason).Render(
				truncate(sidebarGroupLabel(reason), inner)))
			last, project = reason, ""
		}
		if it.ProjectName != project {
			project = it.ProjectName
			lines = append(lines, sidebarIndent+m.styles.Muted.Render(
				truncate(project, max(1, inner-len(sidebarIndent)))))
		}
		lines = append(lines, m.sidebarRow(it, v.DisplayLabel(it), inner))
	}
	if len(lines) == 0 {
		lines = append(lines, m.styles.Muted.Render("nothing waiting"))
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

// sidebarGroups is every reason the strip can head a group with, in the order the
// scope puts them. Only its length is used — to size the line slice — but the
// list is what a test can walk to check each one has a label and a hue.
var sidebarGroups = []deckdata.Reason{
	deckdata.ReasonWorking,
	deckdata.ReasonWaiting,
	deckdata.ReasonReReviewRequested,
	deckdata.ReasonReviewRequested,
	deckdata.ReasonNotified,
	deckdata.ReasonPRNeedsAction,
	deckdata.ReasonPRReadyToMerge,
	deckdata.ReasonRecent,
}

// sidebarGroupLabel is the header for a group of rows: the scope's own words for
// why they are there.
//
// Reason.String, so the strip says what the row list's meta line says rather than
// inventing a second vocabulary for the same fact. ReasonRecent's real words are a
// duration, which belongs to a row and not to a group of them, so the group says
// what they have in common.
func sidebarGroupLabel(r deckdata.Reason) string {
	if r == deckdata.ReasonRecent {
		return "recently active"
	}
	if s := r.String(); s != "" {
		return s
	}
	return "other"
}

// sidebarGroupStyle is the hue a group's header wears — the colour its rows'
// status dots and the badge's dots already wear, so the strip is read with the
// vocabulary the rest of the deck taught.
func (m Model) sidebarGroupStyle(r deckdata.Reason) lipgloss.Style {
	switch r {
	case deckdata.ReasonWorking:
		return m.styles.Success.Bold(true)
	case deckdata.ReasonWaiting, deckdata.ReasonReReviewRequested, deckdata.ReasonReviewRequested:
		return m.styles.Warning.Bold(true)
	case deckdata.ReasonPRNeedsAction:
		return m.styles.Danger.Bold(true)
	case deckdata.ReasonPRReadyToMerge:
		return m.styles.Success.Bold(true)
	case deckdata.ReasonNotified, deckdata.ReasonRecent, deckdata.ReasonNone:
	}
	return m.styles.Muted.Bold(true)
}

// sidebarRow is one workspace: the status dot the row list would give it, its
// project, and its name.
//
// The workspace you are in wears a `┃` bar in Muted — the "pane the keyboard has
// left" tier of the selection treatment. It marks where you are without claiming
// to be the cursor, which is what the full-strength bar means and there is no
// cursor in here to mean it.
func (m Model) sidebarRow(it Item, label string, width int) string {
	bar := "  "
	if p := m.topRowSubject(); p != nil && p.project == it.ProjectName && p.workspace == it.WorkspaceName {
		bar = m.styles.Muted.Render("┃") + " "
	}
	glyph := statusGlyph(it.Status, false, it.Unread)
	// Every row is the same shape — bar, dot, name — whatever the row is about.
	// Rows with a dot and rows without used to sit at different indents, so nothing
	// lined up vertically and the drift cost columns on both kinds.
	room := width - lipgloss.Width(bar) - lipgloss.Width(glyph) - 1
	return bar + glyph + " " + truncate(label, max(1, room))
}

// sidebarIndent is what a project sub-row is inset by: the columns a workspace row
// spends on its selection bar, so the name above lines up with the names below it
// rather than starting in the bar's channel.
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
