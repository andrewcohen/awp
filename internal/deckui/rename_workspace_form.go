package deckui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// renameWorkspaceForm is the deck-internal rename dialog. Plain struct,
// not a tea.Model — see new_workspace_form.go for the architectural
// rationale (single tea.Program, no nested renderers).
type renameWorkspaceForm struct {
	target    Item
	nameInput textinput.Model
	err       string
}

type renameFormAction int

const (
	renameFormActionNone renameFormAction = iota
	renameFormActionCancel
	renameFormActionSubmit
)

var (
	renameFormKeySubmit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "rename"))
	renameFormKeyCancel = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

func newRenameWorkspaceForm(target Item) renameWorkspaceForm {
	input := textinput.New()
	input.Placeholder = "new workspace name"
	input.SetValue(target.WorkspaceName)
	input.Prompt = ""
	input.CharLimit = 0
	input.Width = 56
	input.Focus()
	// Cursor at end of the prefilled value so users can append/backspace
	// without retyping.
	input.SetCursor(len(target.WorkspaceName))
	return renameWorkspaceForm{
		target:    target,
		nameInput: input,
	}
}

func (f renameWorkspaceForm) update(msg tea.Msg) (renameWorkspaceForm, tea.Cmd, renameFormAction) {
	f.err = ""
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+c", "esc":
			return f, nil, renameFormActionCancel
		case "enter":
			candidate := strings.TrimSpace(f.nameInput.Value())
			if candidate == "" {
				f.err = "new workspace name is required"
				return f, nil, renameFormActionNone
			}
			if candidate == f.target.WorkspaceName {
				f.err = "new name is the same as the current name"
				return f, nil, renameFormActionNone
			}
			return f, nil, renameFormActionSubmit
		}
	}
	var cmd tea.Cmd
	f.nameInput, cmd = f.nameInput.Update(msg)
	return f, cmd, renameFormActionNone
}

func (f renameWorkspaceForm) value() string {
	return strings.TrimSpace(f.nameInput.Value())
}

func (f renameWorkspaceForm) view(width, height int) string {
	const cardWidth = 84
	const contentWidth = 74

	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	title := theme.Title.Render("Rename workspace")
	subtitle := theme.Subtitle.Render(formWrap("Renaming \""+f.target.WorkspaceName+"\". This updates the jj workspace, tmux session + window, and workspace state — the on-disk directory keeps its original path.", contentWidth))
	label := theme.Label.Render("New name:")

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	b.WriteString(label)
	b.WriteString("\n")
	b.WriteString(f.nameInput.View())

	if strings.TrimSpace(f.err) != "" {
		b.WriteString("\n\n")
		b.WriteString(theme.Error.Render(formWrap("Error: "+f.err, contentWidth)))
	}

	b.WriteString("\n\n")
	helpModel := charm.NewHelp()
	b.WriteString(helpModel.ShortHelpView([]key.Binding{
		renameFormKeySubmit, renameFormKeyCancel,
	}))

	rendered := card.Render(b.String())
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}
