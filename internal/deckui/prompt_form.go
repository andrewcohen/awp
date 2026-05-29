package deckui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// promptForm is the deck-internal "send a prompt to the workspace's
// agent" dialog. Same plain-struct, single-tea.Program pattern as the
// new-workspace and rename forms (see new_workspace_form.go for the
// architectural rationale).
//
// Built on huh.Form (one Text field with built-in $EDITOR support,
// rebound to Ctrl+G to match the deck convention). The wrapper just
// translates huh's terminal states into promptFormAction.
type promptForm struct {
	target    Item
	form      *huh.Form
	promptVal *string
}

type promptFormAction int

const (
	promptFormActionNone promptFormAction = iota
	promptFormActionCancel
	promptFormActionSubmit
)

// newPromptForm constructs the form. The returned tea.Cmd MUST be
// dispatched by the caller so huh activates the input. initial
// prepopulates the prompt field (empty for a blank "send a prompt"
// dialog; non-empty when another flow — e.g. PR repair — hands the
// user a draft to review and edit before sending).
func newPromptForm(target Item, initial string) (promptForm, tea.Cmd) {
	promptVal := initial

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Text.Editor = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "edit in $EDITOR"))

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("Prompt").
				Description("Send this prompt to "+strings.TrimSpace(target.ProjectName)+" / "+strings.TrimSpace(target.WorkspaceName)).
				Placeholder("prompt to dispatch to the agent...").
				CharLimit(0).
				Lines(6).
				ShowLineNumbers(false).
				ExternalEditor(true).
				Value(&promptVal).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("prompt is required")
					}
					return nil
				}),
		),
	).
		WithKeyMap(km).
		WithTheme(charm.HuhTheme()).
		WithShowHelp(true).
		WithShowErrors(true)

	f := promptForm{
		target:    target,
		form:      form,
		promptVal: &promptVal,
	}
	return f, form.Init()
}

func (f promptForm) update(msg tea.Msg) (promptForm, tea.Cmd, promptFormAction) {
	if f.form == nil {
		return f, nil, promptFormActionNone
	}
	m, cmd := f.form.Update(msg)
	if updated, ok := m.(*huh.Form); ok {
		f.form = updated
	}
	switch f.form.State {
	case huh.StateAborted:
		return f, cmd, promptFormActionCancel
	case huh.StateCompleted:
		return f, cmd, promptFormActionSubmit
	}
	return f, cmd, promptFormActionNone
}

func (f promptForm) value() string {
	if f.promptVal == nil {
		return ""
	}
	return strings.TrimSpace(*f.promptVal)
}

func (f promptForm) view(width, height int) string {
	if f.form == nil {
		return ""
	}
	const cardWidth = 84
	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	body := f.form.WithWidth(cardWidth - 4).View()
	rendered := card.Render(theme.Title.Render("Send prompt to agent") + "\n\n" + body)
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}
