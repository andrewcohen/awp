package deckui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// box is the region of the screen a child has been given, in cells.
//
// It exists because a child that reads m.width / m.height has assumed it owns
// the terminal, and exactly one child can be right about that. Every modal used
// to: the picker asked the Model for its height, the pane asked for both, and
// the arithmetic that turned those into a list size or a pty size was spread
// across fifteen renderers. None of it was wrong, and none of it could be told
// "you get the left half".
//
// So the size travels as an argument, the way a repo directory does in
// internal/github — one spelling, in the position where it cannot be forgotten.
// The host decides (Model.childBox), the child is told.
//
// There is no origin here yet. A split needs one, to translate a mouse event
// into the coordinates of the half it landed in, but every box today starts at
// the screen's top-left, so an x and y would be two fields that are always zero
// and never read — which is how a field is wrong the first time something uses
// it. Origin lands with the split, where a test can put a click in the right
// half and watch where it comes out.
type box struct {
	w, h int
}

// fit is the width a popover that wants `want` columns actually gets: what it
// asked for, or the box, whichever is smaller.
//
// The fixed-width popovers (confirms, small inputs) each named a number — 60
// columns, plus the border — chosen against a whole terminal, which is a number
// no box can contradict as long as there is only ever one box. Passing them a
// half-width box and having them render 62 columns into it is exactly the class
// of bug this seam exists to make impossible, so the number becomes a maximum
// rather than a width. Nothing changes for a terminal wide enough to honour it,
// which today is nearly all of them.
func fit(want int, b box) int {
	if b.w > 0 && want > b.w {
		return b.w
	}
	return want
}

// stacked is whether this box is too narrow to carry a two-column picker.
//
// A property of the box rather than of the deck: in a split, whether the open
// picker can afford its help pane depends on the half it renders into, not on
// how wide the terminal happens to be.
func (b box) stacked() bool { return b.w > 0 && b.w < deckStackThreshold }

// childBox is the region the deck's current child gets: all of it.
//
// The one place that answer is written down, so the split has one function to
// change rather than fifteen call sites to find.
func (m *Model) childBox() box { return box{w: m.width, h: m.height} }

// renderPickerPanel is the body panel every list picker (bookmark, open,
// review) renders into: the shared panel inset, with the list sized to fill
// exactly what the deck's chrome leaves.
//
// One function rather than three identical copies because the three had
// already drifted apart in their comments while agreeing on the number, and a
// wrong number here does not fail — it leaves a band of dead rows above the
// footer, or clips the list's own pagination off the bottom. list.Model owns
// its title, status bar, paginator and help inside whatever height it is given.
func renderPickerPanel(m *Model, l *list.Model, b box) string {
	listWidth := b.w - panelCols
	if listWidth < 8 {
		listWidth = 8
	}
	listHeight := b.h - panelRows - footerRows
	if listHeight < 3 {
		listHeight = 3
	}
	l.SetSize(listWidth, listHeight)
	return m.styles.Panel.Width(b.w).Render(l.View())
}

// modal is one full-screen overlay the deck can show in place of the row
// list: a picker, a form, a confirmation, an overlay. At most one is
// active at a time (Model.active); a nil active slot means row mode.
//
// This replaces the deck's historical bag of per-mode bool flags
// (openMode, bookmarkMode, …) with a single slot plus a small interface,
// so adding a modal no longer means threading another flag through
// Update and View. Concrete modals are plain structs (never a nested
// tea.Program — see the package doc) that own their own sub-state; the
// consequential actions that touch the rest of the deck (dispatching a
// review, opening a project, reverting to a form) are performed against
// the *Model passed into update, keeping that logic where it belongs.
//
// Migration is incremental: modes are moved onto this slot one at a time.
// Until a mode is migrated it keeps its bool flag, and the flag dispatch
// runs when active == nil.
type modal interface {
	// update handles a message while this modal is active. Key messages
	// drive the modal's own bindings; other messages (filter matches,
	// cursor blink, async results routed here) are forwarded to whatever
	// bubble the modal wraps. It may mutate the model (including setting
	// m.active = nil to close itself) and returns any command to run. The
	// model's Update calls this before the legacy flag dispatch.
	update(m *Model, msg tea.Msg) tea.Cmd
	// footerHelp returns the status-bar right segment for this modal, or
	// "" to leave it blank (e.g. while loading, or for popovers that render
	// their own hints).
	footerHelp() string
}

// bodyModal renders full-width in place of the row list (pickers, menus).
// View composes its (left, right) panes into the deck body with the normal
// footer beneath.
type bodyModal interface {
	modal
	// view returns the modal's body as (left, right) panes, rendered into the
	// box it was given. right is "" for single-column modals; the caller joins
	// them.
	view(m *Model, b box) (left, right string)
}

// popoverModal renders as a centered box over a blank canvas (confirms,
// small input prompts). View returns its render directly instead of
// composing a body + footer.
type popoverModal interface {
	modal
	renderPopover(m *Model, b box) string
}
