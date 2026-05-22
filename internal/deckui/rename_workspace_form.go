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

// renameWorkspaceForm is the deck-internal rename dialog. Plain struct,
// not a tea.Model — see new_workspace_form.go for the architectural
// rationale (single tea.Program, no nested renderers).
//
// The form is a one-field huh.Form: a single Input bound to the new
// workspace name. Validation rejects empty names and a name that
// matches the current one. huh owns Enter-to-submit, esc-to-cancel,
// and the validation-error rendering.
type renameWorkspaceForm struct {
	target  Item
	form    *huh.Form
	nameVal *string
}

type renameFormAction int

const (
	renameFormActionNone renameFormAction = iota
	renameFormActionCancel
	renameFormActionSubmit
)

// newRenameWorkspaceForm builds the rename form. The returned tea.Cmd
// MUST be dispatched by the caller so huh activates the input field —
// without it, enter/esc no-op.
func newRenameWorkspaceForm(target Item) (renameWorkspaceForm, tea.Cmd) {
	nameVal := target.WorkspaceName

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))

	current := target.WorkspaceName
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("New name").
				Description("Renames the jj workspace, tmux session + window, and workspace state — the on-disk directory keeps its original path.").
				Placeholder("new workspace name").
				CharLimit(0).
				Value(&nameVal).
				Validate(func(s string) error {
					trimmed := strings.TrimSpace(s)
					if trimmed == "" {
						return errors.New("new workspace name is required")
					}
					if trimmed == current {
						return errors.New("new name is the same as the current name")
					}
					return nil
				}),
		),
	).
		WithKeyMap(km).
		WithTheme(charm.HuhTheme()).
		WithShowHelp(true).
		WithShowErrors(true)

	f := renameWorkspaceForm{
		target:  target,
		form:    form,
		nameVal: &nameVal,
	}
	return f, form.Init()
}

func (f renameWorkspaceForm) update(msg tea.Msg) (renameWorkspaceForm, tea.Cmd, renameFormAction) {
	if f.form == nil {
		return f, nil, renameFormActionNone
	}
	m, cmd := f.form.Update(msg)
	if updated, ok := m.(*huh.Form); ok {
		f.form = updated
	}
	switch f.form.State {
	case huh.StateAborted:
		return f, cmd, renameFormActionCancel
	case huh.StateCompleted:
		return f, cmd, renameFormActionSubmit
	}
	return f, cmd, renameFormActionNone
}

func (f renameWorkspaceForm) value() string {
	if f.nameVal == nil {
		return ""
	}
	return strings.TrimSpace(*f.nameVal)
}

func (f renameWorkspaceForm) view(width, height int) string {
	if f.form == nil {
		return ""
	}
	const cardWidth = 84
	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	body := f.form.WithWidth(cardWidth - 4).View()
	rendered := card.Render(theme.Title.Render("Rename workspace") + "\n\n" + body)
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}
