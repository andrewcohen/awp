package deckui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Clicking a row on the strip to go to it.
//
// The strip was built to read and do nothing, and the argument for that was about
// the *keyboard*: a strip that took the keys would have to answer what focus means
// with a pane, a split half and a strip on screen at once, which is a bigger
// question than "which of these wants me". That argument is untouched here. A mouse
// does not have the problem — pointing at a thing is how you say which one you mean,
// and it says it without taking anything away from whatever holds the keys.
//
// So this is the cheap half of #350, and deliberately only the cheap half. What it
// adds is one gesture with an obvious target; the keyboard's version still needs a
// cursor, a selection treatment, and a story about focus.
//
// Going to a row means what `enter` on the row list means, and through the same
// function: openPaneOrArrangement, so a workspace whose last arrangement was a split
// comes back as that split rather than as a bare agent pane. A second way into a
// workspace that opened something different from the first would be the two ways
// disagreeing about what entering a workspace is.

// clickSidebarRow acts on a mouse event that landed in the strip's columns,
// reporting whether it consumed it.
//
// Every click inside the strip is consumed, whether or not it hit a row. The
// alternative is a press on a section header falling through to the child, where it
// arrives at a negative column and is refused — nothing visibly wrong, but the strip
// would be a region that sometimes belongs to the program beside it.
//
// The wheel is not claimed. Nothing in here scrolls (overflow is a count, see
// sidebarLines), so consuming it would make the wheel dead over these columns rather
// than merely ineffective.
func (m *Model) clickSidebarRow(msg tea.MouseMsg) (tea.Cmd, bool) {
	if !m.showsSidebar() {
		return nil, false
	}
	click, isClick := msg.(tea.MouseClickMsg)
	if !isClick || click.Button != tea.MouseLeft {
		return nil, false
	}
	if click.X < 0 || click.X >= m.sidebarWidth() {
		return nil, false
	}
	it, ok := m.sidebarRowAt(click.Y)
	if !ok {
		return nil, true
	}
	return m.goToSidebarRow(it), true
}

// sidebarRowAt is which workspace is drawn on screen row y, if any.
//
// The lines come from sidebarLines rather than from arithmetic over the row count:
// the strip's height is spent on headers, separators and possibly an overflow
// count, and a hit test that re-derived the pattern would be a second copy of the
// layout loop. y is a screen row, so the strip's own origin comes off it — the box's
// top, from childBox, plus the strip's padding.
func (m *Model) sidebarRowAt(y int) (Item, bool) {
	b := m.childBox()
	i := y - b.y - sidebarPadY
	lines := m.sidebarLines(box{w: m.sidebarWidth(), h: b.h})
	if i < 0 || i >= len(lines) || lines[i].item == nil {
		return Item{}, false
	}
	return *lines[i].item, true
}

// goToSidebarRow opens the workspace that was clicked.
//
// The cursor moves with it, so leaving the pane lands the row list on the workspace
// you were just in rather than wherever it was pointing before. That is the same
// courtesy the pane keys already extend, and without it the click would quietly
// desync the two surfaces: you would come back out of a pane to find the deck's
// selection somewhere else entirely.
//
// A virtual inbox row is refused rather than acted on. Enter on one of those starts a
// review or opens the new-workspace form — consequential things — and a click is a
// cheaper gesture than a keypress, made by pointing at a strip you were reading. It
// says where to do it instead.
func (m *Model) goToSidebarRow(it Item) tea.Cmd {
	if it.Virtual {
		m.status = "no workspace yet — open it from the deck (ctrl+\\, then enter)"
		return nil
	}
	if strings.TrimSpace(it.WorkspaceName) == "" {
		return nil
	}
	if _, blocked := m.blockIfSettingUp(it); blocked {
		return nil
	}
	m.focusRow(it)
	cmd, _ := m.openPaneOrArrangement(it, PaneKindAgent)
	return cmd
}

// focusRow puts the deck's cursor on a row, named rather than indexed.
//
// The strip renders the all scope and the row list may be on another — or filtered —
// so the row clicked need not be in the list the cursor indexes. A row that is not
// there leaves the cursor alone: it is already pointing at something real, and moving
// it to an arbitrary index would be worse than not moving it.
func (m *Model) focusRow(row Item) {
	for i, it := range m.items() {
		if sameRow(row, it) {
			m.cursor = i
			return
		}
	}
}
