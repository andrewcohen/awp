package deckui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// newWorkspaceForm is the deck-internal new-workspace dialog. It is a
// plain struct deliberately — not a tea.Model — because it composes
// into the deck's single tea.Program rather than running as a nested
// program. See doc.go for the architectural rationale.
type newWorkspaceForm struct {
	activeField    int // 0 workspace, 1 bookmark, 2 prompt, 3 actions
	actionIndex    int // 0 submit, 1 cancel
	workspaceInput textinput.Model
	bookmarkInput  textinput.Model
	promptInput    textarea.Model
	err            string
}

// newFormAction is the result of an Update tick on the form. The deck
// inspects it to decide whether to clear the flag, dispatch a create
// job, or run an editor exec.
type newFormAction int

const (
	newFormActionNone newFormAction = iota
	newFormActionCancel
	newFormActionSubmit
	newFormActionEditor
)

// promptEditedMsg carries the result of editing the prompt textarea in
// $EDITOR. Routed back into the form's update via the deck's
// Update-message dispatch so the editor exec can complete cleanly
// (tea.ExecProcess is the supported handoff for external commands).
type promptEditedMsg struct {
	value string
	err   error
}

var (
	newFormKeyMove    = key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "move"))
	newFormKeySubmit  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit / focus submit"))
	newFormKeyNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline (prompt)"))
	newFormKeyEditor  = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "prompt in $EDITOR"))
	newFormKeyCancel  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

func newNewWorkspaceForm(initial NewWorkspaceInitial) newWorkspaceForm {
	workspaceInput := textinput.New()
	workspaceInput.Placeholder = "workspace name (blank → random name off trunk, or derive from bookmark)"
	workspaceInput.SetValue(strings.TrimSpace(initial.Name))
	workspaceInput.Prompt = ""
	workspaceInput.CharLimit = 0
	workspaceInput.Width = 56

	bookmarkInput := textinput.New()
	bookmarkInput.Placeholder = "optional jj bookmark/revision to track"
	bookmarkInput.SetValue(strings.TrimSpace(initial.Bookmark))
	bookmarkInput.Prompt = ""
	bookmarkInput.CharLimit = 0
	bookmarkInput.Width = 56

	promptInput := textarea.New()
	promptInput.Placeholder = "optional agent prompt to run after creating workspace"
	promptInput.CharLimit = 0
	promptInput.Prompt = ""
	promptInput.SetWidth(56)
	promptInput.SetHeight(4)
	promptInput.ShowLineNumbers = false
	promptInput.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline"))
	promptInput.Blur()

	f := newWorkspaceForm{
		workspaceInput: workspaceInput,
		bookmarkInput:  bookmarkInput,
		promptInput:    promptInput,
	}
	f.setFocus(0)
	return f
}

// update routes a tea.Msg into the form. It returns the updated form,
// any tea.Cmd that should run (textinput blink, editor exec process),
// and a newFormAction telling the deck whether the form is done.
func (f newWorkspaceForm) update(msg tea.Msg) (newWorkspaceForm, tea.Cmd, newFormAction) {
	f.err = ""

	switch m := msg.(type) {
	case promptEditedMsg:
		if m.err != nil {
			f.err = m.err.Error()
			return f, nil, newFormActionNone
		}
		f.promptInput.SetValue(m.value)
		return f, nil, newFormActionNone
	case tea.KeyMsg:
		switch m.String() {
		case "ctrl+c", "esc":
			return f, nil, newFormActionCancel
		case "shift+tab":
			f.prevField()
			return f, nil, newFormActionNone
		case "tab":
			f.nextField()
			return f, nil, newFormActionNone
		case "ctrl+g":
			if f.activeField == 2 {
				cmd, err := editPromptInEditor(f.promptInput.Value())
				if err != nil {
					f.err = err.Error()
					return f, nil, newFormActionNone
				}
				return f, cmd, newFormActionEditor
			}
		case "enter":
			if f.activeField == 3 {
				if f.actionIndex == 1 {
					return f, nil, newFormActionCancel
				}
				return f, nil, newFormActionSubmit
			}
			f.setFocus(3)
			return f, nil, newFormActionNone
		case "up":
			if f.activeField == 3 {
				f.handleActionLeft()
				return f, nil, newFormActionNone
			}
		case "down":
			if f.activeField == 3 {
				f.handleActionRight()
				return f, nil, newFormActionNone
			}
		case "left", "h":
			if f.activeField == 3 {
				f.handleActionLeft()
				return f, nil, newFormActionNone
			}
		case "right", "l":
			if f.activeField == 3 {
				f.handleActionRight()
				return f, nil, newFormActionNone
			}
		}
	}

	var cmd tea.Cmd
	switch f.activeField {
	case 0:
		f.workspaceInput, cmd = f.workspaceInput.Update(msg)
	case 1:
		f.bookmarkInput, cmd = f.bookmarkInput.Update(msg)
	case 2:
		f.promptInput, cmd = f.promptInput.Update(msg)
	}
	return f, cmd, newFormActionNone
}

func (f *newWorkspaceForm) handleActionLeft() {
	if f.actionIndex > 0 {
		f.actionIndex--
	}
}

func (f *newWorkspaceForm) handleActionRight() {
	if f.actionIndex < 1 {
		f.actionIndex++
	}
}

func (f *newWorkspaceForm) nextField() { f.setFocus((f.activeField + 1) % 4) }
func (f *newWorkspaceForm) prevField() { f.setFocus((f.activeField + 3) % 4) }

func (f *newWorkspaceForm) setFocus(field int) {
	f.activeField = field
	f.workspaceInput.Blur()
	f.bookmarkInput.Blur()
	f.promptInput.Blur()
	switch field {
	case 0:
		f.workspaceInput.Focus()
	case 1:
		f.bookmarkInput.Focus()
	case 2:
		f.promptInput.Focus()
	}
}

func (f newWorkspaceForm) request() NewWorkspaceRequest {
	return NewWorkspaceRequest{
		Name:     strings.TrimSpace(f.workspaceInput.Value()),
		Bookmark: strings.TrimSpace(f.bookmarkInput.Value()),
		Prompt:   strings.TrimSpace(f.promptInput.Value()),
	}
}

// view renders the form. width and height are the deck's viewport
// dimensions. The card is centered with lipgloss.Place so cells outside
// the card overwrite whatever the row list had drawn. Because we share
// the deck's renderer (one tea.Program), no alt-screen bleed.
func (f newWorkspaceForm) view(width, height int) string {
	const cardWidth = 84
	const contentWidth = 74

	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	title := theme.Title.Render("New workspace")
	subtitle := theme.Subtitle.Render(formWrap("Create a new workspace.", contentWidth))
	label := theme.Label
	focused := theme.Focused
	dim := theme.Dim
	hint := theme.Hint
	errorStyle := theme.Error
	fieldLabel := func(index int, name string) string {
		marker := "  "
		if f.activeField == index {
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
	b.WriteString(f.workspaceInput.View())
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(1, "Bookmark"))
	b.WriteString("\n")
	b.WriteString(f.bookmarkInput.View())
	b.WriteString("\n\n")
	b.WriteString(fieldLabel(2, "Prompt"))
	b.WriteString("\n")
	b.WriteString(f.promptInput.View())
	b.WriteString("\n")
	b.WriteString(hint.Render(formIndent("Prompt supports multiple lines. Press Ctrl+G to edit it in $EDITOR.", "   ", contentWidth)))
	b.WriteString("\n\n")

	actionMarker := "  "
	if f.activeField == 3 {
		actionMarker = focused.Render("› ")
	}
	b.WriteString(actionMarker)
	b.WriteString(choice(f.activeField == 3 && f.actionIndex == 0, "Submit"))
	b.WriteString("   ")
	b.WriteString(choice(f.activeField == 3 && f.actionIndex == 1, "Cancel"))

	if strings.TrimSpace(f.err) != "" {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(formWrap("Error: "+f.err, contentWidth)))
	}

	b.WriteString("\n\n")
	helpModel := charm.NewHelp()
	b.WriteString(helpModel.ShortHelpView([]key.Binding{
		newFormKeyMove, newFormKeySubmit, newFormKeyNewline, newFormKeyEditor, newFormKeyCancel,
	}))

	rendered := card.Render(b.String())
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}

// formWrap soft-wraps a string at width, splitting on whitespace. Local
// to deckui so we don't import the cli package's sibling helper.
func formWrap(value string, width int) string {
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

func formIndent(value, indent string, width int) string {
	innerWidth := width - len([]rune(indent))
	if innerWidth <= 0 {
		innerWidth = width
	}
	wrapped := formWrap(value, innerWidth)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

// editPromptInEditor returns a tea.Cmd that suspends the deck's
// terminal, opens $EDITOR on a temp file seeded with `initial`, and
// emits a promptEditedMsg with the saved contents. tea.ExecProcess is
// the supported handoff for external commands — different from
// nesting another tea.Program, which causes alt-screen bleed.
func editPromptInEditor(initial string) (tea.Cmd, error) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return nil, errors.New("$EDITOR is not set")
	}
	file, err := os.CreateTemp("", "awp-deck-prompt-*.md")
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
