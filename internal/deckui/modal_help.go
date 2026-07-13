package deckui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpModal is the `?` help overlay: a bordered popover framing a
// scrollable viewport of the status/PR/activity legend + key bindings, so
// the full help is reachable even on a terminal too short to show it all
// at once. The content is derived from the keymap + palette; the modal
// owns only the scroll viewport.
type helpModal struct {
	vp    viewport.Model
	lastW int // inner width the content was last rendered at (-1 = unset)
}

func newHelpModal() *helpModal {
	vp := viewport.New(0, 0)
	// Scroll with arrows/j/k (line), pgup/pgdn (page), ctrl+u/ctrl+d
	// (half). Matches the jobs overlay's scroll feel.
	vp.KeyMap = viewport.KeyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k")),
		Down:         key.NewBinding(key.WithKeys("down", "j")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
	}
	return &helpModal{vp: vp, lastW: -1}
}

func (h *helpModal) update(m *Model, msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "?", "esc", "q", "enter":
		m.active = nil
		return nil
	}
	var cmd tea.Cmd
	h.vp, cmd = h.vp.Update(km)
	return cmd
}

func (h *helpModal) footerHelp() string { return "" }

func (h *helpModal) renderPopover(m *Model) string {
	boxWidth, innerWidth := helpBoxDims(m.width)
	// Rebuild the (static) content only when the inner width changes;
	// SetContent preserves the scroll offset otherwise.
	if innerWidth != h.lastW {
		h.vp.SetContent(helpColumns(innerWidth))
		h.lastW = innerWidth
	}
	h.vp.Width = innerWidth
	// Box chrome eats: border+padding (border 2 + vertical padding 2) plus
	// title (1) + blank (1) + blank (1) + hint (1) = 8 rows around the
	// viewport. Size the viewport to what's left so the box fits the height.
	vpHeight := m.height - 8
	if vpHeight < 3 {
		vpHeight = 3
	}
	h.vp.Height = vpHeight

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Render("awp deck — help")
	hintText := "↑/↓ scroll · pgup/pgdn page · esc close"
	if !h.vp.AtBottom() {
		hintText += " · ↓ more"
	}
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(hintText)

	body := lipgloss.JoinVertical(lipgloss.Left, title, "", h.vp.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)
}
