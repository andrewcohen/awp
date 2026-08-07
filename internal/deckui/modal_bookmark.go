package deckui

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// bookmarkPicker is the fuzzy bookmark picker shared by two flows,
// disambiguated by purpose: feeding a chosen bookmark back into the
// new-workspace form (bookmarkPurposeNewWorkspace / …StartFrom) or
// linking an existing workspace to a bookmark (bookmarkPurposeLinkExisting,
// the row-mode `B` key). It owns its list, loading state, purpose, and —
// for the link flow — the target row.
type bookmarkPicker struct {
	list       list.Model
	loading    bool
	purpose    bookmarkPurpose
	linkTarget Item
}

// newBookmarkPicker builds the picker in its loading state, titled for the
// flow, showing the placeholder until bookmarkFetcher returns a
// BookmarksDoneMsg.
func newBookmarkPicker(glyph string, purpose bookmarkPurpose, target Item) *bookmarkPicker {
	l := newBookmarkList()
	l.Title = bookmarkPickerTitle(purpose, target)
	l.SetShowStatusBar(false)
	l.SetItems([]list.Item{loadingItem{label: glyph + " loading bookmarks..."}})
	return &bookmarkPicker{list: l, loading: true, purpose: purpose, linkTarget: target}
}

// setBookmarks populates the list from a completed fetch.
func (p *bookmarkPicker) setBookmarks(names []string) {
	items := make([]list.Item, 0, len(names))
	for _, b := range names {
		items = append(items, bookmarkItem{name: b})
	}
	p.loading = false
	p.list.SetShowStatusBar(true)
	p.list.SetItems(items)
	p.list.ResetSelected()
	p.list.Title = bookmarkPickerTitle(p.purpose, p.linkTarget)
}

// tickLoading refreshes the animated spinner glyph while the fetch runs.
func (p *bookmarkPicker) tickLoading(glyph string) {
	p.list.SetItems([]list.Item{loadingItem{label: glyph + " loading bookmarks..."}})
}

// cancelToForm is the shared close path for the two cancel sites (during
// load and after load): clear the modal and, when the picker was opened
// from the new-workspace form's Start-from field, revert the form and
// re-enter it. Returns the command to run.
func (p *bookmarkPicker) cancelToForm(m *Model) tea.Cmd {
	fromForm := p.purpose == bookmarkPurposeNewWorkspaceStartFrom
	m.active = nil
	m.status = ""
	if fromForm {
		m.newWorkspaceMode = true
		m.newWorkspaceForm.RevertStartFrom()
		return nil
	}
	return nil
}

func (p *bookmarkPicker) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		return cmd
	}
	if p.loading {
		switch key.String() {
		case "esc", "ctrl+c":
			return p.cancelToForm(m)
		}
		return nil
	}
	// list.Model owns cursor + filter + paginator state. We only intercept
	// the keys whose default semantics don't match the picker UX:
	//   - enter: select the highlighted row directly, even while filtering
	//     (list's default would just commit the filter, forcing a
	//     double-enter to pick).
	//   - esc / ctrl+c while not filtering: close the picker (list's
	//     default is a no-op there; first esc still clears an active
	//     filter via list's own handling).
	filtering := p.list.FilterState() == list.Filtering
	switch key.String() {
	case "enter":
		if filtering {
			break
		}
		if it, ok := p.list.SelectedItem().(bookmarkItem); ok && strings.TrimSpace(it.name) != "" {
			updated, cmd := m.acceptBookmarkSelection(it.name, p.purpose, p.linkTarget)
			*m = updated.(Model)
			return cmd
		}
		return nil
	case "esc", "ctrl+c":
		if !filtering && p.list.FilterState() != list.FilterApplied {
			return p.cancelToForm(m)
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

func (p *bookmarkPicker) view(m *Model) (left, right string) {
	// JoinHorizontal between a short loading-state left pane and a tall
	// static right pane caused painting bleed during load, so the bookmark
	// picker is deliberately single-column (see the historical note that
	// used to live in View).
	return p.renderList(m, m.width), ""
}

func (p *bookmarkPicker) footerHelp() string {
	if p.loading {
		return ""
	}
	return p.list.Help.ShortHelpView(pickerShortHelp(p.list))
}

func (p *bookmarkPicker) renderList(m *Model, width int) string {
	// A single loadingItem stands in during the fetch so the list's chrome
	// keeps its shape and the panel doesn't resize under the user.
	return renderPickerPanel(m, &p.list, width)
}
