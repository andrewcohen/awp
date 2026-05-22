package deckui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// promptForm is the deck-internal "send a prompt to the workspace's
// agent" dialog. Same plain-struct, single-tea.Program pattern as the
// new-workspace and rename forms (see new_workspace_form.go for the
// architectural rationale).
type promptForm struct {
	target      Item
	promptInput textarea.Model
	err         string
}

type promptFormAction int

const (
	promptFormActionNone promptFormAction = iota
	promptFormActionCancel
	promptFormActionSubmit
	promptFormActionEditor
)

var (
	promptFormKeySubmit  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit"))
	promptFormKeyNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline"))
	promptFormKeyEditor  = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "prompt in $EDITOR"))
	promptFormKeyCancel  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

func newPromptForm(target Item) promptForm {
	pi := textarea.New()
	pi.Placeholder = "prompt to dispatch to the agent..."
	pi.CharLimit = 0
	pi.Prompt = ""
	pi.SetWidth(56)
	pi.SetHeight(6)
	pi.ShowLineNumbers = false
	pi.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline"))
	pi.Focus()
	return promptForm{
		target:      target,
		promptInput: pi,
	}
}

func (f promptForm) update(msg tea.Msg) (promptForm, tea.Cmd, promptFormAction) {
	f.err = ""
	switch m := msg.(type) {
	case promptEditedMsg:
		if m.err != nil {
			f.err = m.err.Error()
			return f, nil, promptFormActionNone
		}
		f.promptInput.SetValue(m.value)
		return f, nil, promptFormActionNone
	case tea.KeyMsg:
		switch m.String() {
		case "ctrl+c", "esc":
			return f, nil, promptFormActionCancel
		case "ctrl+g":
			cmd, err := editPromptInEditor(f.promptInput.Value())
			if err != nil {
				f.err = err.Error()
				return f, nil, promptFormActionNone
			}
			return f, cmd, promptFormActionEditor
		case "enter":
			if strings.TrimSpace(f.promptInput.Value()) == "" {
				f.err = "prompt is required"
				return f, nil, promptFormActionNone
			}
			return f, nil, promptFormActionSubmit
		}
	}
	var cmd tea.Cmd
	f.promptInput, cmd = f.promptInput.Update(msg)
	return f, cmd, promptFormActionNone
}

func (f promptForm) value() string {
	return strings.TrimSpace(f.promptInput.Value())
}

func (f promptForm) view(width, height int) string {
	const cardWidth = 84
	const contentWidth = 74

	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	title := theme.Title.Render("Send prompt to agent")

	// Make the target obvious — the dialog floats over the row list and
	// the user needs a positive confirmation of which workspace they're
	// about to dispatch to.
	project := strings.TrimSpace(f.target.ProjectName)
	if project == "" {
		project = "(unknown project)"
	}
	workspace := strings.TrimSpace(f.target.WorkspaceName)
	if workspace == "" {
		workspace = "(unknown workspace)"
	}
	label := theme.Label
	focused := theme.Focused
	dim := theme.Dim
	errorStyle := theme.Error
	hint := theme.Hint

	targetLine := label.Render("Target: ") + focused.Render(project+" / "+workspace)
	pathLine := ""
	if p := strings.TrimSpace(f.target.Path); p != "" {
		pathLine = dim.Render(p)
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(targetLine)
	if pathLine != "" {
		b.WriteString("\n")
		b.WriteString(pathLine)
	}
	b.WriteString("\n\n")
	b.WriteString(label.Render("Prompt:"))
	b.WriteString("\n")
	b.WriteString(f.promptInput.View())
	b.WriteString("\n")
	b.WriteString(hint.Render(formIndent("Prompt supports multiple lines. Press Ctrl+G to edit it in $EDITOR.", "   ", contentWidth)))

	if strings.TrimSpace(f.err) != "" {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(formWrap("Error: "+f.err, contentWidth)))
	}

	b.WriteString("\n\n")
	helpModel := charm.NewHelp()
	b.WriteString(helpModel.ShortHelpView([]key.Binding{
		promptFormKeySubmit, promptFormKeyNewline, promptFormKeyEditor, promptFormKeyCancel,
	}))

	rendered := card.Render(b.String())
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}
