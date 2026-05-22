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

// newWorkspaceForm is the deck-internal new-workspace dialog. It is a
// plain struct deliberately — not a tea.Model — because it composes
// into the deck's single tea.Program rather than running as a nested
// program. See doc.go for the architectural rationale.
//
// Field navigation, focus management, validation, and the prompt-in-
// $EDITOR shortcut all live inside huh.Form. The wrapper only
// translates huh's terminal states (Normal / Completed / Aborted) into
// the deck's newFormAction sentinel.
//
// The four *string/*bool pointers are heap-allocated by
// newNewWorkspaceForm and bound to huh's fields; mutating them through
// the pointer (e.g. from a test) updates the value the form reads on
// validate/submit too.
type newWorkspaceForm struct {
	form          *huh.Form
	workspaceVal  *string
	bookmarkVal   *string
	promptVal     *string
	confirmSubmit *bool
}

// newFormAction is the result of an Update tick on the form. The deck
// inspects it to decide whether to clear the flag or dispatch a create
// job.
type newFormAction int

const (
	newFormActionNone newFormAction = iota
	newFormActionCancel
	newFormActionSubmit
)

// newNewWorkspaceForm constructs the form. The returned tea.Cmd MUST be
// dispatched by the caller (typically launchNewForm) so huh activates
// its first group — without it, tab/enter no-op.
func newNewWorkspaceForm(initial NewWorkspaceInitial) (newWorkspaceForm, tea.Cmd) {
	workspaceVal := strings.TrimSpace(initial.Name)
	bookmarkVal := strings.TrimSpace(initial.Bookmark)
	var promptVal string
	confirmSubmit := true

	// huh.Text has built-in $EDITOR support; we rebind it from ctrl+e
	// (huh's default) to ctrl+g to match the deck's existing key for
	// "edit prompt in $EDITOR". Quit gets esc added so the deck's
	// standard "esc cancels modal" pattern works here too (default is
	// ctrl+c only).
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Text.Editor = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "edit in $EDITOR"))

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Workspace").
				Placeholder("workspace name (leave blank to derive from bookmark)").
				CharLimit(0).
				Value(&workspaceVal),
			huh.NewInput().
				Title("Bookmark").
				Placeholder("optional jj bookmark/revision to track").
				CharLimit(0).
				Value(&bookmarkVal),
			huh.NewText().
				Title("Prompt").
				Placeholder("optional agent prompt to run after creating workspace").
				CharLimit(0).
				Lines(4).
				ShowLineNumbers(false).
				ExternalEditor(true).
				Value(&promptVal),
			huh.NewConfirm().
				Title("Action").
				Affirmative("Submit").
				Negative("Cancel").
				Value(&confirmSubmit).
				Validate(func(submit bool) error {
					if !submit {
						return nil
					}
					if strings.TrimSpace(workspaceVal) == "" &&
						strings.TrimSpace(bookmarkVal) == "" {
						return errors.New("workspace name or bookmark is required")
					}
					return nil
				}),
		),
	).
		WithKeyMap(km).
		WithTheme(charm.HuhTheme()).
		WithShowHelp(true).
		WithShowErrors(true)

	f := newWorkspaceForm{
		form:          form,
		workspaceVal:  &workspaceVal,
		bookmarkVal:   &bookmarkVal,
		promptVal:     &promptVal,
		confirmSubmit: &confirmSubmit,
	}
	return f, form.Init()
}

// update routes a tea.Msg into the form. Returns the updated form, any
// tea.Cmd that should run, and a newFormAction telling the deck whether
// the form is done.
func (f newWorkspaceForm) update(msg tea.Msg) (newWorkspaceForm, tea.Cmd, newFormAction) {
	if f.form == nil {
		return f, nil, newFormActionNone
	}
	m, cmd := f.form.Update(msg)
	if updated, ok := m.(*huh.Form); ok {
		f.form = updated
	}
	switch f.form.State {
	case huh.StateAborted:
		return f, cmd, newFormActionCancel
	case huh.StateCompleted:
		if f.confirmSubmit != nil && *f.confirmSubmit {
			return f, cmd, newFormActionSubmit
		}
		return f, cmd, newFormActionCancel
	}
	return f, cmd, newFormActionNone
}

func (f newWorkspaceForm) request() NewWorkspaceRequest {
	r := NewWorkspaceRequest{}
	if f.workspaceVal != nil {
		r.Name = strings.TrimSpace(*f.workspaceVal)
	}
	if f.bookmarkVal != nil {
		r.Bookmark = strings.TrimSpace(*f.bookmarkVal)
	}
	if f.promptVal != nil {
		r.Prompt = strings.TrimSpace(*f.promptVal)
	}
	return r
}

// view renders the form inside our centered card. huh draws its own
// field chrome; we wrap it in the deck's card border so the modal feel
// matches the other deck overlays.
func (f newWorkspaceForm) view(width, height int) string {
	if f.form == nil {
		return ""
	}
	const cardWidth = 84
	theme := charm.DefaultTheme()
	card := theme.Card.Width(cardWidth)
	body := f.form.WithWidth(cardWidth - 4).View()
	rendered := card.Render(theme.Title.Render("New workspace") + "\n\n" + body)
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}
