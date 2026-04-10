package cli

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
)

type workspaceItem struct {
	name string
}

func (i workspaceItem) FilterValue() string { return i.name }
func (i workspaceItem) Title() string       { return i.name }
func (i workspaceItem) Description() string { return "" }

type pickerModel struct {
	list   list.Model
	choice string
	cancel bool
}

var pickerSelectKey = key.NewBinding(
	key.WithKeys("enter"),
	key.WithHelp("enter", "select"),
)

var pickerCancelKey = key.NewBinding(
	key.WithKeys("q", "esc"),
	key.WithHelp("q", "cancel"),
)

func newPickerModel(title string, options []string) pickerModel {
	items := make([]list.Item, 0, len(options))
	for _, option := range options {
		items = append(items, workspaceItem{name: option})
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.ShortHelpFunc = func() []key.Binding {
		return []key.Binding{pickerSelectKey, pickerCancelKey}
	}
	delegate.FullHelpFunc = func() [][]key.Binding {
		return [][]key.Binding{{pickerSelectKey, pickerCancelKey}}
	}

	l := list.New(items, delegate, 0, 0)
	l.Title = title
	l.SetStatusBarItemName("workspace", "workspaces")
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, &delegate)
	l.SetDelegate(delegate)

	return pickerModel{list: l}
}

func pickWorkspaceWithCharm(title string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no workspace options available")
	}
	if charm.IsDumbTerminal() {
		return "", errors.New("interactive picker not available in dumb terminal")
	}

	m := newPickerModel(title, options)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result, ok := final.(pickerModel)
	if !ok {
		return "", errors.New("unexpected picker state")
	}
	if result.cancel {
		return "", errors.New("selection cancelled")
	}
	if strings.TrimSpace(result.choice) == "" {
		return "", errors.New("no workspace selected")
	}
	return result.choice, nil
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		if m.list.FilterState() != list.Filtering {
			switch msg.String() {
			case "ctrl+c", "esc", "q":
				m.cancel = true
				return m, tea.Quit
			case "enter":
				selected, ok := m.list.SelectedItem().(workspaceItem)
				if !ok || strings.TrimSpace(selected.name) == "" {
					return m, nil
				}
				m.choice = selected.name
				return m, tea.Quit
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickerModel) View() string {
	return m.list.View()
}
