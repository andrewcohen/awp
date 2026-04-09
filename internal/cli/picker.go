package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type pickerModel struct {
	title   string
	options []string
	cursor  int
	choice  string
	cancel  bool
}

func pickWorkspaceWithCharm(title string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no workspace options available")
	}
	if os.Getenv("TERM") == "dumb" {
		return "", errors.New("interactive picker not available in dumb terminal")
	}

	m := pickerModel{title: title, options: options}
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
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancel = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.choice = m.options[m.cursor]
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m pickerModel) View() string {
	var b strings.Builder
	b.WriteString(m.title)
	b.WriteString("\n\n")
	for i, option := range m.options {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, option)
	}
	b.WriteString("\n↑/↓ or j/k to move, enter to select, q/esc to cancel\n")
	return b.String()
}
