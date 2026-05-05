package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/charm"
)

var (
	openFormKeyMove    = key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "move"))
	openFormKeySubmit  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit / focus submit"))
	openFormKeyNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline (prompt)"))
	openFormKeyEditor  = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "prompt in $EDITOR"))
	openFormKeyCancel  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

type openRequest struct {
	Name     string
	Bookmark string
	Prompt   string
	Yes      bool
	// NoSwitch suppresses the final tmux switch-client step. Used by
	// the async create-workspace job so the subprocess prepares the
	// workspace + agent + prompt without yanking the user's tmux
	// focus away from the deck.
	NoSwitch bool
}

type openFormModel struct {
	activeField    int
	actionIndex    int
	workspaceInput textinput.Model
	bookmarkInput  textinput.Model
	promptInput    textarea.Model
	cancel         bool
	err            string
}

type promptEditedMsg struct {
	value string
	err   error
}

func newOpenFormModel(initial openRequest, _ []string) openFormModel {
	workspaceInput := textinput.New()
	workspaceInput.Placeholder = "workspace name (leave blank to derive from bookmark)"
	workspaceInput.SetValue(strings.TrimSpace(initial.Name))
	workspaceInput.Prompt = ""
	workspaceInput.CharLimit = 0
	workspaceInput.Width = 56
	workspaceInput.Focus()

	bookmarkInput := textinput.New()
	bookmarkInput.Placeholder = "optional jj bookmark/revision to track"
	bookmarkInput.SetValue(strings.TrimSpace(initial.Bookmark))
	bookmarkInput.Prompt = ""
	bookmarkInput.CharLimit = 0
	bookmarkInput.Width = 56

	promptInput := textarea.New()
	promptInput.Placeholder = "optional agent prompt to run after creating workspace"
	promptInput.CharLimit = 0
	promptInput.SetValue(strings.TrimSpace(initial.Prompt))
	promptInput.Prompt = ""
	promptInput.SetWidth(56)
	promptInput.SetHeight(4)
	promptInput.ShowLineNumbers = false
	promptInput.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline"))
	promptInput.Blur()

	m := openFormModel{
		workspaceInput: workspaceInput,
		bookmarkInput:  bookmarkInput,
		promptInput:    promptInput,
	}
	m.setFocus(0)
	return m
}

func runOpenWithCharm(initial openRequest, workspaces []string, in io.Reader, out io.Writer) (openRequest, error) {
	if charm.IsDumbTerminal() {
		return openRequest{}, errors.New("interactive open form not available in dumb terminal")
	}
	model := newOpenFormModel(initial, workspaces)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return openRequest{}, err
	}
	result, ok := final.(openFormModel)
	if !ok {
		return openRequest{}, errors.New("unexpected open form state")
	}
	if result.cancel {
		return openRequest{}, errors.New("open cancelled")
	}
	request := result.currentRequest()
	if strings.TrimSpace(request.Name) == "" && strings.TrimSpace(request.Bookmark) == "" {
		return openRequest{}, errors.New("workspace open requires a workspace name or bookmark")
	}
	return request, nil
}

func (m openFormModel) Init() tea.Cmd { return textinput.Blink }

func (m openFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.err = ""

	switch msg := msg.(type) {
	case promptEditedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.promptInput.SetValue(msg.value)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancel = true
			return m, tea.Quit
		case "shift+tab":
			m.prevField()
			return m, nil
		case "tab":
			m.nextField()
			return m, nil
		case "ctrl+g":
			if m.activeField == 2 {
				cmd, err := editTextInEditor(m.promptInput.Value())
				if err != nil {
					m.err = err.Error()
					return m, nil
				}
				return m, cmd
			}
		case "enter":
			if m.activeField == 3 {
				return m.handleSubmit()
			}
			m.setFocus(3)
			return m, nil
		case "up":
			if m.activeField == 3 {
				m.handleActionLeft()
				return m, nil
			}
		case "down":
			if m.activeField == 3 {
				m.handleActionRight()
				return m, nil
			}
		case "left", "h":
			if m.activeField == 3 {
				m.handleActionLeft()
				return m, nil
			}
		case "right", "l":
			if m.activeField == 3 {
				m.handleActionRight()
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	switch m.activeField {
	case 0:
		m.workspaceInput, cmd = m.workspaceInput.Update(msg)
	case 1:
		m.bookmarkInput, cmd = m.bookmarkInput.Update(msg)
	case 2:
		m.promptInput, cmd = m.promptInput.Update(msg)
	}
	return m, cmd
}

func (m *openFormModel) handleSubmit() (tea.Model, tea.Cmd) {
	if m.actionIndex == 1 {
		m.cancel = true
		return *m, tea.Quit
	}
	request := m.currentRequest()
	if strings.TrimSpace(request.Name) == "" && strings.TrimSpace(request.Bookmark) == "" {
		m.err = "workspace name or bookmark is required"
		return *m, nil
	}
	return *m, tea.Quit
}

func (m *openFormModel) handleActionLeft() {
	if m.actionIndex > 0 {
		m.actionIndex--
	}
}

func (m *openFormModel) handleActionRight() {
	if m.actionIndex < 1 {
		m.actionIndex++
	}
}

func (m *openFormModel) nextField() { m.setFocus((m.activeField + 1) % 4) }
func (m *openFormModel) prevField() { m.setFocus((m.activeField + 3) % 4) }

func (m *openFormModel) setFocus(field int) {
	m.activeField = field
	m.workspaceInput.Blur()
	m.bookmarkInput.Blur()
	m.promptInput.Blur()
	switch field {
	case 0:
		m.workspaceInput.Focus()
	case 1:
		m.bookmarkInput.Focus()
	case 2:
		m.promptInput.Focus()
	}
}

func (m openFormModel) currentRequest() openRequest {
	return openRequest{
		Name:     strings.TrimSpace(m.workspaceInput.Value()),
		Bookmark: strings.TrimSpace(m.bookmarkInput.Value()),
		Prompt:   strings.TrimSpace(m.promptInput.Value()),
	}
}

func (m openFormModel) View() string {
	const cardWidth = 84
	const contentWidth = 74

	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	title := theme.Title.Render("New workspace")
	subtitle := theme.Subtitle.Render(wrapText("Create a new workspace.", contentWidth))
	label := theme.Label
	focused := theme.Focused
	dim := theme.Dim
	hint := theme.Hint
	errorStyle := theme.Error
	fieldLabel := func(index int, name string) string {
		marker := "  "
		if m.activeField == index {
			marker = focused.Render("› ")
		}
		return marker + label.Render(name+":")
	}
	choice := func(selected bool, text string) string {
		if selected {
			return focused.Render("● " + text)
		}
		return dim.Render("○ " + text)
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(0, "Workspace"))
	b.WriteString("\n")
	b.WriteString(m.workspaceInput.View())
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(1, "Bookmark"))
	b.WriteString("\n")
	b.WriteString(m.bookmarkInput.View())
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(2, "Prompt"))
	b.WriteString("\n")
	b.WriteString(m.promptInput.View())
	b.WriteString("\n")
	b.WriteString(hint.Render(indentWrapped("Prompt supports multiple lines. Press Ctrl+G to edit it in $EDITOR.", "   ", contentWidth)))
	b.WriteString("\n\n")

	actionMarker := "  "
	if m.activeField == 3 {
		actionMarker = focused.Render("› ")
	}
	b.WriteString(actionMarker)
	b.WriteString(choice(m.activeField == 3 && m.actionIndex == 0, "Submit"))
	b.WriteString("   ")
	b.WriteString(choice(m.activeField == 3 && m.actionIndex == 1, "Cancel"))

	if strings.TrimSpace(m.err) != "" {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(wrapText("Error: "+m.err, contentWidth)))
	}

	b.WriteString("\n\n")
	helpModel := charm.NewHelp()
	b.WriteString(helpModel.ShortHelpView([]key.Binding{
		openFormKeyMove, openFormKeySubmit, openFormKeyNewline, openFormKeyEditor, openFormKeyCancel,
	}))
	return card.Render(b.String()) + "\n"
}

func wrapText(value string, width int) string {
	if width <= 0 {
		return value
	}
	lines := strings.Split(value, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		words := strings.Fields(line)
		current := words[0]
		for _, word := range words[1:] {
			if len([]rune(current))+1+len([]rune(word)) > width {
				wrapped = append(wrapped, current)
				current = word
				continue
			}
			current += " " + word
		}
		wrapped = append(wrapped, current)
	}
	return strings.Join(wrapped, "\n")
}

func indentWrapped(value string, indent string, width int) string {
	innerWidth := width - len([]rune(indent))
	if innerWidth <= 0 {
		innerWidth = width
	}
	wrapped := wrapText(value, innerWidth)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func editTextInEditor(initial string) (tea.Cmd, error) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return nil, errors.New("$EDITOR is not set")
	}
	file, err := os.CreateTemp("", "awp-open-prompt-*.md")
	if err != nil {
		return nil, fmt.Errorf("create temp file for editor: %w", err)
	}
	path := file.Name()
	defer file.Close()
	if _, err := file.WriteString(initial); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("write temp file for editor: %w", err)
	}
	cmd := exec.Command("sh", "-c", "exec \"$EDITOR\" \"$1\"", "sh", path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return promptEditedMsg{err: err}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return promptEditedMsg{err: fmt.Errorf("read edited prompt: %w", readErr)}
		}
		return promptEditedMsg{value: strings.TrimRight(string(data), "\n")}
	}), nil
}
