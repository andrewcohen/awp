// Package zdeck is a proof of concept for the navigation flow awp is heading
// toward: the workspace list and a live pane on screen at the same time, with
// awp owning the layout and the PTY rather than negotiating with a
// multiplexer for them.
//
// It is deliberately a separate command from `awp deck`. The last attempt at
// this was built into the real deck and left it in a half-state while the
// design was still moving.
//
// What it demonstrates that a multiplexer cannot: a terminal beside a native
// panel. tmux can split terminals against terminals; it has no way to put an
// agent's pane next to awp's own diff viewer.
package zdeck

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
	"github.com/andrewcohen/awp/internal/zmx"
)

// ctx is the context for zmx calls made from the update loop. They are all
// short local command invocations.
func ctx() context.Context { return context.Background() }

// leaveKey returns the keyboard to the list from a focused pane.
//
// It has to be a key nothing inside the pane wants, because everything else
// belongs to the program: esc, q and ctrl+c are all meaningful to an agent.
// zdeck intercepts it before the pane sees it, so it means the same thing
// whether the pane is a zmx client or a directly spawned process.
const leaveKey = "ctrl+\\"

type focus int

const (
	focusList focus = iota
	focusPane
)

// pane is whatever is currently open beside the list.
type pane struct {
	kind    Kind
	item    Item
	session string // zmx session name, empty for ephemeral and native panes
	term    *vterm.Term
	w, h    int
}

// Model is the zdeck screen.
type Model struct {
	items  []Item
	cursor int
	width  int
	height int

	focus  focus
	pane   *pane
	gen    int
	status string
	zmx    zmx.Client

	styles styles
	keys   keyMap
	quit   bool
}

type keyMap struct {
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Close key.Binding
	Quit  key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:    key.NewBinding(key.WithKeys("k", "up")),
		Down:  key.NewBinding(key.WithKeys("j", "down")),
		Enter: key.NewBinding(key.WithKeys("tab", "enter")),
		Close: key.NewBinding(key.WithKeys("x")),
		Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c")),
	}
}

// New builds the model over a workspace list and a zmx client.
func New(items []Item, client zmx.Client) Model {
	return Model{
		items:  items,
		zmx:    client,
		styles: newStyles(),
		keys:   newKeyMap(),
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) selected() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return Item{}, false
	}
	return m.items[m.cursor], true
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.resizePane()

	case vterm.OutputMsg:
		if m.pane == nil || msg.Gen != m.pane.term.Gen() {
			return m, nil // a frame from a pane that has since closed
		}
		return m, m.pane.term.AwaitOutput()

	case vterm.ExitMsg:
		if m.pane == nil || msg.Gen != m.pane.term.Gen() {
			return m, nil
		}
		ended := m.pane.kind.Label
		m.closePane()
		m.status = ended + " ended"
		return m, nil

	case tea.PasteMsg:
		if m.focus == focusPane && m.pane != nil {
			m.pane.term.SendText(msg.Content)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// A focused pane owns the keyboard, except for the one key that gives it
	// back. Nothing else is intercepted — an agent needs esc and ctrl+c.
	if m.focus == focusPane && m.pane != nil {
		if msg.String() == leaveKey {
			m.focus = focusList
			return m, nil
		}
		m.pane.term.SendKey(msg)
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		m.closePane()
		m.quit = true
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.status = ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.status = ""
		}
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		if m.pane == nil {
			m.status = "nothing open — press " + kindKeyList() + " to open a pane"
			return m, nil
		}
		m.focus = focusPane
		m.status = ""
		return m, nil
	case key.Matches(msg, m.keys.Close):
		if m.pane == nil {
			return m, nil
		}
		closed := m.pane.kind
		m.closePane()
		if closed.Lifetime == LongLived {
			m.status = closed.Label + " closed — its session is still running"
		} else {
			m.status = closed.Label + " closed"
		}
		return m, nil
	}

	if k, ok := KindForKey(msg.String()); ok {
		return m.openPane(k)
	}
	return m, nil
}

// openPane replaces whatever is open with the given kind for the selected row.
func (m Model) openPane(k Kind) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok {
		m.status = "select a workspace first"
		return m, nil
	}
	if item.Virtual {
		m.status = item.WorkspaceName + " has no workspace yet"
		return m, nil
	}
	if k.Lifetime == Native {
		m.status = k.Label + ": not wired up yet"
		return m, nil
	}

	w, h := m.paneDims()
	if w <= 0 || h <= 0 {
		m.status = "the window is too small to open a pane"
		return m, nil
	}

	cmd, session, err := k.command(item, m.zmx)
	if err != nil {
		m.status = k.Label + ": " + err.Error()
		return m, nil
	}
	m.gen++
	term, err := vterm.Start(m.gen, w, h, cmd)
	if err != nil {
		m.status = k.Label + ": " + err.Error()
		return m, nil
	}

	m.closePane()
	m.pane = &pane{kind: k, item: item, session: session, term: term, w: w, h: h}
	m.focus = focusPane
	m.status = ""
	return m, tea.Batch(term.AwaitOutput(), term.AwaitExit())
}

// closePane tears down the current pane. A long-lived kind's session keeps
// running; that is what makes it long-lived.
func (m *Model) closePane() {
	if m.pane == nil {
		return
	}
	_ = m.pane.term.Close()
	m.pane = nil
	m.focus = focusList
}

// resizePane keeps the hosted process in step with the layout.
func (m Model) resizePane() tea.Cmd {
	if m.pane == nil {
		return nil
	}
	w, h := m.paneDims()
	if w <= 0 || h <= 0 {
		return nil
	}
	if err := m.pane.term.Resize(w, h); err == nil {
		m.pane.w, m.pane.h = w, h
	}
	return nil
}

func kindKeyList() string {
	keys := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		keys = append(keys, k.Key)
	}
	return strings.Join(keys, "/")
}

// Status is the current status line (used by tests).
func (m Model) Status() string { return m.status }

// Quitting reports whether the model has asked to exit (used by tests).
func (m Model) Quitting() bool { return m.quit }

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}
