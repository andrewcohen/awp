package deckui

import (
	"fmt"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// paneLeaveKey gives the keyboard back to the deck.
//
// It has to be a key nothing inside the pane wants, because everything else
// belongs to the program: esc, q and ctrl+c all mean something to an agent.
// ctrl+\ is normally SIGQUIT, which is exactly why nothing interactive binds
// it, and the deck reads it as a key because its own terminal is in raw mode.
const paneLeaveKey = "ctrl+\\"

// PaneBackend turns a workspace and a window kind into a process the deck can
// host on a pty it owns, instead of handing off to a tmux window.
//
// The deck's UI does not change when one is present — the same keys do the
// same conceptual thing. Only where the process lives changes, which is why
// this is an interface and not a fork of the deck.
type PaneBackend interface {
	// Open returns the command for the item's pane of this kind, sized w×h,
	// plus a func that undoes anything Open had to set up. kind is the same
	// string ActionOpenWindow uses: "agent", "editor", "vcs", or "" for a
	// shell.
	Open(item Item, kind string, w, h int) (cmd *exec.Cmd, restore func(), err error)
	// Describes reports whether this backend handles the kind. Anything it
	// declines falls through to the ordinary tmux-window path, so review
	// windows and the PR-description window keep working unchanged.
	Describes(kind string) bool
}

// panePopover is a hosted process shown in place of the deck body.
type panePopover struct {
	term    *vterm.Term
	label   string
	restore func()
	setW    int
	setH    int
}

// openPane hosts the given window kind for the selected row. It reports false
// when there is no backend for it, so the caller can fall back to tmux.
func (m *Model) openPane(item Item, kind string) (tea.Cmd, bool) {
	if m.panes == nil || !m.panes.Describes(kind) {
		return nil, false
	}
	if !paneFits(m.width, m.height) {
		m.status = fmt.Sprintf("this terminal is %dx%d, too small for a pane", m.width, m.height)
		return nil, true
	}

	w, h := paneDims(m.width, m.height)
	cmd, restore, err := m.panes.Open(item, kind, w, h)
	if err != nil {
		m.status = paneLabel(kind) + ": " + err.Error()
		return nil, true
	}
	m.paneGen++
	term, err := vterm.Start(m.paneGen, w, h, cmd)
	if err != nil {
		if restore != nil {
			restore()
		}
		m.status = paneLabel(kind) + ": " + err.Error()
		return nil, true
	}

	p := &panePopover{
		term:    term,
		label:   paneLabel(kind) + " · " + item.ProjectName + "/" + item.WorkspaceName,
		restore: restore,
		setW:    w,
		setH:    h,
	}
	m.active = p
	m.status = ""
	return tea.Batch(term.AwaitOutput(), term.AwaitExit()), true
}

func paneLabel(kind string) string {
	if kind == "" {
		return "shell"
	}
	return kind
}

func (p *panePopover) close(m *Model) {
	_ = p.term.Close()
	if p.restore != nil {
		p.restore()
		p.restore = nil
	}
	if m.active == p {
		m.active = nil
	}
}

func (p *panePopover) footerHelp() string { return "" }

func (p *panePopover) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case vterm.OutputMsg:
		if msg.Gen != p.term.Gen() {
			// A frame from a pane that has already closed. Painting it would
			// put the previous process's screen inside this one.
			return nil
		}
		return p.term.AwaitOutput()

	case vterm.ExitMsg:
		if msg.Gen != p.term.Gen() {
			return nil
		}
		p.close(m)
		return nil

	case tea.KeyPressMsg:
		if msg.String() == paneLeaveKey {
			p.close(m)
			return nil
		}
		p.term.SendKey(msg)
		return nil

	case tea.PasteMsg:
		p.term.SendText(msg.Content)
		return nil
	}
	return nil
}

// The popover's chrome, matching the deck's other overlays: a rounded border
// with Padding(1, 2), a title, and a hint.
const (
	paneChromeW = 6 // border 2 + horizontal padding 4
	paneChromeH = 8 // border 2 + vertical padding 2 + title, blank, blank, hint
	paneMinW    = 20
	paneMinH    = 5
)

func paneDims(deckW, deckH int) (w, h int) { return deckW - paneChromeW, deckH - paneChromeH }

func paneFits(deckW, deckH int) bool {
	w, h := paneDims(deckW, deckH)
	return w >= paneMinW && h >= paneMinH
}

func (p *panePopover) renderPopover(m *Model) string {
	w, h := paneDims(m.width, m.height)
	if w != p.setW || h != p.setH {
		// The deck was resized, so the pty and the emulator have to follow
		// together or the process lays out for one width while we render at
		// another.
		if err := p.term.Resize(w, h); err == nil {
			p.setW, p.setH = w, h
		}
	}

	title := m.styles.PaneTitle.Render(p.label)
	hint := m.styles.PaneHint.Render(paneLeaveKey + " deck · every other key goes to the pane")
	body := lipgloss.JoinVertical(lipgloss.Left, title, "", p.term.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(w + 4 + borderCells).
		Render(body)
}
