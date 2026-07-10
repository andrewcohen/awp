package deckui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// helpModal is the `?` help overlay: a centered popover listing key
// bindings and the status/PR/activity legend. It holds no state — the
// content is derived from the keymap and palette at render time — so it's
// a stateless popoverModal.
type helpModal struct{}

func (helpModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "?", "esc", "q", "enter":
		m.active = nil
	}
	return nil
}

func (helpModal) footerHelp() string { return "" }

func (helpModal) renderPopover(m *Model) string { return m.renderHelp(m.width) }
