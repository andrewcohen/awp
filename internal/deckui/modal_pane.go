package deckui

import (
	"fmt"
	"os/exec"
	"strings"

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

	case tea.MouseMsg:
		// The deck asks for mouse events only while a pane is up (see View),
		// so anything arriving here belongs to the hosted program.
		p.term.SendMouse(msg)
		return nil
	}
	return nil
}

// The popover's chrome is one row and the border, and no more. Every cell it
// takes is one the hosted program does not get, and unlike the deck's other
// overlays — which frame a fixed amount of awp's own text — a pane is showing
// someone else's full-screen program.
//
// So there is no padding, and the leave hint shares the header row with the
// label instead of costing two more rows of its own. The border stays: it is
// what says where the pane ends when its program does not fill it.
const (
	paneHeaderRows = 1
	paneChromeW    = borderCells
	paneChromeH    = borderCells + paneHeaderRows
	paneMinW       = 20
	paneMinH       = 5
)

// paneInsetX / paneInsetY are where the terminal starts inside the popover:
// past the left border, and past the top border and the header row.
const (
	paneInsetX = 1
	paneInsetY = 1 + paneHeaderRows
)

func paneDims(deckW, deckH int) (w, h int) { return deckW - paneChromeW, deckH - paneChromeH }

// paneBox is the popover's outer size for a terminal of w×h. renderPopover and
// screenCursor both derive from it rather than each doing the arithmetic, so
// the cursor cannot land somewhere the box isn't.
func paneBox(w, h int) (boxW, boxH int) {
	return w + paneChromeW, h + paneChromeH
}

// screenCursor is where the hosted program's cursor lands on the deck's own
// screen: the centred popover's origin, plus the chrome around the terminal,
// plus wherever the program put it.
//
// ok is false when there should be no cursor at all — the pane does not fit,
// the program has hidden its cursor, or it sits outside the terminal. A
// full-screen program like jjui hides the cursor and then leaves it wherever
// was convenient, so honouring that is what stops a blinking block appearing
// at an arbitrary spot on its screen.
//
// The box size is computed rather than measured so this does not have to
// render the popover a second time.
func (p *panePopover) screenCursor(deckW, deckH int) (x, y int, ok bool) {
	if !paneFits(deckW, deckH) {
		return 0, 0, false
	}
	w, h := paneDims(deckW, deckH)
	boxW, boxH := paneBox(w, h)
	originX, originY := (deckW-boxW)/2, (deckH-boxH)/2
	cx, cy, visible := p.term.Cursor()
	if !visible || cx < 0 || cy < 0 || cx >= w || cy >= h {
		return 0, 0, false
	}
	return originX + paneInsetX + cx, originY + paneInsetY + cy, true
}

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

	boxW, _ := paneBox(w, h)
	body := lipgloss.JoinVertical(lipgloss.Left, p.header(m, w), p.term.View())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Width(boxW).
		Render(body)
}

// header is the pane's one row of chrome: what you are looking at on the left,
// how to leave on the right. It doubles as the status line the hint used to
// have a row of its own for.
func (p *panePopover) header(m *Model, w int) string {
	hint := m.styles.PaneHint.Render(paneLeaveKey + " deck")
	label := m.styles.PaneTitle.Render(truncate(p.label, w-lipgloss.Width(hint)-1))
	gap := w - lipgloss.Width(label) - lipgloss.Width(hint)
	if gap < 1 {
		// Too narrow for both; the label is the one you can infer without.
		return hint
	}
	return label + strings.Repeat(" ", gap) + hint
}
