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
	openFormKeySubmit  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit"))
	openFormKeyEditor  = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "prompt in $EDITOR"))
	openFormKeyCancel  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

type openRequest struct {
	Name     string
	Bookmark string
	Prompt   string
	Yes      bool
}

type openFormModel struct {
	workspaceOptions []string
	activeField      int
	actionIndex      int
	workspaceIndex   int
	workspaceInput   textinput.Model
	bookmarkInput    textinput.Model
	promptInput      textarea.Model
	cancel           bool
	err              string
}

type promptEditedMsg struct {
	value string
	err   error
}

func newOpenFormModel(initial openRequest, workspaces []string) openFormModel {
	workspaceInput := textinput.New()
	workspaceInput.Placeholder = "existing name or new workspace name"
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
	promptInput.SetValue(strings.TrimSpace(initial.Prompt))
	promptInput.Prompt = ""
	promptInput.SetWidth(56)
	promptInput.SetHeight(4)
	promptInput.ShowLineNumbers = false
	promptInput.Blur()

	m := openFormModel{
		workspaceOptions: append([]string(nil), workspaces...),
		workspaceIndex:   selectedWorkspaceIndex(workspaces, initial.Name),
		workspaceInput:   workspaceInput,
		bookmarkInput:    bookmarkInput,
		promptInput:      promptInput,
	}
	m.setFocus(0)
	return m
}

func runOpenWithCharm(initial openRequest, workspaces []string, in io.Reader, out io.Writer) (openRequest, error) {
	if charm.IsDumbTerminal() {
		return openRequest{}, errors.New("interactive open form not available in dumb terminal")
	}
	model := newOpenFormModel(initial, workspaces)
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
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
		case "up":
			if m.activeField == 0 {
				m.handleWorkspaceUp()
				return m, nil
			}
			if m.activeField == 3 {
				m.handleActionLeft()
				return m, nil
			}
		case "down":
			if m.activeField == 0 {
				m.handleWorkspaceDown()
				return m, nil
			}
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
		m.workspaceIndex = selectedWorkspaceIndex(m.filteredWorkspaceOptions(), m.workspaceInput.Value())
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

func (m *openFormModel) handleWorkspaceUp() {
	options := m.filteredWorkspaceOptions()
	if len(options) == 0 {
		return
	}
	if m.workspaceIndex <= 0 {
		m.workspaceIndex = 0
	} else {
		m.workspaceIndex--
	}
	m.workspaceInput.SetValue(options[m.workspaceIndex])
}

func (m *openFormModel) handleWorkspaceDown() {
	options := m.filteredWorkspaceOptions()
	if len(options) == 0 {
		return
	}
	if m.workspaceIndex < 0 {
		m.workspaceIndex = 0
	} else if m.workspaceIndex < len(options)-1 {
		m.workspaceIndex++
	}
	m.workspaceInput.SetValue(options[m.workspaceIndex])
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
	title := theme.Title.Render("Open workspace")
	subtitle := theme.Subtitle.Render(wrapText("Open an existing workspace or create a new one.", contentWidth))
	label := theme.Label
	focused := theme.Focused
	dim := theme.Dim
	hint := theme.Hint
	errorStyle := theme.Error
	chip := theme.Chip
	chipActive := theme.ChipActive
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

	filtered := m.filteredWorkspaceOptions()
	preview := m.previewText()

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(0, "Workspace"))
	b.WriteString("\n")
	b.WriteString(m.workspaceInput.View())
	if len(filtered) > 0 {
		b.WriteString("\n")
		b.WriteString(dim.Render("   Existing workspaces: "))
		for i, option := range filtered {
			if i > 0 {
				b.WriteString(" ")
			}
			if i == m.workspaceIndex && m.activeField == 0 {
				b.WriteString(chipActive.Render(option))
			} else {
				b.WriteString(chip.Render(option))
			}
		}
	} else if len(m.workspaceOptions) > 0 {
		b.WriteString("\n")
		b.WriteString(dim.Render("   Existing workspaces: "))
		b.WriteString(hint.Render("no existing workspace match"))
	}
	b.WriteString("\n")
	b.WriteString(hint.Render(indentWrapped(preview, "   ", contentWidth)))
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
		openFormKeyMove, openFormKeySubmit, openFormKeyEditor, openFormKeyCancel,
	}))
	return card.Render(b.String()) + "\n"
}

func (m openFormModel) filteredWorkspaceOptions() []string {
	query := strings.TrimSpace(m.workspaceInput.Value())
	if query == "" {
		return append([]string(nil), m.workspaceOptions...)
	}
	lowered := strings.ToLower(query)
	filtered := make([]string, 0, len(m.workspaceOptions))
	for _, option := range m.workspaceOptions {
		if strings.Contains(strings.ToLower(option), lowered) {
			filtered = append(filtered, option)
		}
	}
	return filtered
}

func (m openFormModel) previewText() string {
	request := m.currentRequest()
	name := request.Name
	bookmark := request.Bookmark
	prompt := request.Prompt
	if name == "" && bookmark != "" {
		name = bookmark
	}
	if name == "" {
		return "Enter a workspace name or bookmark to continue."
	}
	if selectedWorkspaceIndex(m.workspaceOptions, name) >= 0 {
		if prompt != "" {
			return fmt.Sprintf("Will open existing workspace %q. Prompt will not auto-run for existing workspaces.", name)
		}
		return fmt.Sprintf("Will open existing workspace %q.", name)
	}
	if prompt != "" {
		return fmt.Sprintf("Will create workspace %q and run the prompt in tmux after bootstrap.", name)
	}
	return fmt.Sprintf("Will create workspace %q if it does not already exist.", name)
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

func selectedWorkspaceIndex(options []string, name string) int {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return -1
	}
	for i, option := range options {
		if option == trimmed {
			return i
		}
	}
	return -1
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
