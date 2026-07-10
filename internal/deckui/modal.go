package deckui

import (
	tea "github.com/charmbracelet/bubbletea"
)

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
	// view returns the modal's body as (left, right) panes. right is ""
	// for single-column modals; the caller joins them.
	view(m *Model) (left, right string)
}

// popoverModal renders as a centered box over a blank canvas (confirms,
// small input prompts). View returns its render directly instead of
// composing a body + footer.
type popoverModal interface {
	modal
	renderPopover(m *Model) string
}
