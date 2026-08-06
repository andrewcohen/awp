package deckui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// PRDescription is a pull request as its description page reads: what it is
// called, who wrote it, and what they said about it.
//
// Its own type rather than reusing the row cache's PRStatus. That one answers
// "does this row need attention" and is deliberately small enough to persist and
// refresh in bulk; this one is fetched for exactly one PR at the moment someone
// asks to read it, and carries the body — which is the whole point of it and far
// too large a thing to be caching per row.
type PRDescription struct {
	Number int
	Title  string
	Author string
	State  string
	URL    string
	Body   string
}

// PRDescriptionLoader fetches one PR's description. Installed by the CLI layer
// via WithPRDescriptionLoader, so the deck package never shells out to gh
// itself. Nil leaves `p d` unavailable, which the menu says rather than opening
// an empty box.
type PRDescriptionLoader func(item Item, number int) (PRDescription, error)

// prDescLoadedMsg is one finished fetch. It carries the number it was asked for
// so a reply that arrives after the reader has moved on and opened a different
// PR is discarded rather than painted over the one they are looking at.
type prDescLoadedMsg struct {
	number int
	desc   PRDescription
	err    error
}

// prDescModal is the `p d` overlay: a PR's title and body, scrollable, inside the
// deck.
//
// It used to be a tmux window running `gh pr view | less -R`, which meant reading
// the description cost a window, a context switch out of the deck, and a
// switch back. That is still the right answer when you want to keep it open
// beside something else, so it kept a key of its own (`p D`) — the difference
// between the two is whether the description is something you glance at or
// something you work next to, and each now has the shape that fits.
//
// Mirrors watchModal's viewport-in-a-box: same scroll bindings, same popover
// frame, so the two read as the same kind of thing.
type prDescModal struct {
	number int
	// label is the workspace this was opened from, kept for the header — the deck
	// row underneath is hidden by the popover, so without it the box says which PR
	// it is but not which of your workspaces it belongs to.
	label   string
	desc    PRDescription
	loading bool
	err     error
	vp      viewport.Model
}

func newPRDescModal(item Item, number int, load PRDescriptionLoader) (*prDescModal, tea.Cmd) {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k")),
		Down:         key.NewBinding(key.WithKeys("down", "j")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
	}
	pd := &prDescModal{
		number:  number,
		label:   item.ProjectName + "/" + item.WorkspaceName,
		loading: true,
		vp:      vp,
	}
	return pd, func() tea.Msg {
		desc, err := load(item, number)
		return prDescLoadedMsg{number: number, desc: desc, err: err}
	}
}

func (pd *prDescModal) footerHelp() string { return "" }

func (pd *prDescModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case prDescLoadedMsg:
		if msg.number != pd.number {
			// A fetch for a PR nobody is looking at any more.
			return nil
		}
		pd.loading = false
		pd.desc, pd.err = msg.desc, msg.err
		return nil
	case tea.KeyMsg:
		switch msg.String() {
		case "d", "esc", "q", "ctrl+c":
			m.active = nil
			return tea.ClearScreen
		}
		var cmd tea.Cmd
		pd.vp, cmd = pd.vp.Update(msg)
		return cmd
	}
	return nil
}

// prDescBody is what fills the scrollable pane, wrapped to width.
//
// Split out from the render so it is assertable: lipgloss strips colour with no
// TTY, so the only way to check that a failed fetch says so — rather than showing
// an empty box that reads as a PR with no description — is to look at the text.
func (pd *prDescModal) prDescBody(width int) string {
	switch {
	case pd.loading:
		return "loading…"
	case pd.err != nil:
		// The number, because the error from gh often does not carry it, and "no such
		// PR" is a different problem from "not logged in".
		return fmt.Sprintf("could not read #%d: %v", pd.number, pd.err)
	}
	body := strings.TrimSpace(pd.desc.Body)
	if body == "" {
		// Said out loud. An empty pane is indistinguishable from one that failed to
		// paint, and "this PR has no description" is itself worth knowing — it is the
		// thing you would go and write.
		return "no description on this PR."
	}
	// Markdown as written, wrapped and not rendered. A PR body is markdown and
	// glamour would format it, but that is a new dependency for this one pane —
	// left for whenever the review surface renders markdown properly (#68), which
	// will settle how it should look everywhere at once.
	if width < 1 {
		return body
	}
	return ansi.Wrap(strings.ReplaceAll(body, "\r\n", "\n"), width, "")
}

// prDescHeader is the pinned title above the scroll: which PR, what it is
// called, and whose it is.
func (pd *prDescModal) prDescHeader() string {
	strong := lipgloss.NewStyle().Foreground(lipgloss.Color(colStrong)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	info := lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo))

	num := info.Render(fmt.Sprintf("#%d", pd.number))
	if pd.loading || pd.err != nil {
		return num + "  " + muted.Render(pd.label)
	}
	line := num + "  " + strong.Render(pd.desc.Title)
	var meta []string
	if a := strings.TrimSpace(pd.desc.Author); a != "" {
		meta = append(meta, a)
	}
	if s := strings.TrimSpace(pd.desc.State); s != "" {
		meta = append(meta, strings.ToLower(s))
	}
	meta = append(meta, pd.label)
	return line + "\n" + muted.Render(strings.Join(meta, " · "))
}

func (pd *prDescModal) renderPopover(m *Model) string {
	boxWidth, innerWidth := helpBoxDims(m.width)
	pd.vp.Width = innerWidth
	vpHeight := m.height - 9
	if vpHeight < 3 {
		vpHeight = 3
	}
	pd.vp.Height = vpHeight
	pd.vp.SetContent(pd.prDescBody(innerWidth))

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
		Render("↑/↓ scroll · pgup/pgdn page · esc close · p D opens it in a tmux window")

	body := lipgloss.JoinVertical(lipgloss.Left, pd.prDescHeader(), "", pd.vp.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)
}
