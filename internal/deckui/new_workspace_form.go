package deckui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/workspace"
)

// newWorkspaceForm is the deck-internal new-workspace dialog, also driven
// by the CLI open flow. It's a plain struct (not a tea.Model) so it can
// compose into a parent program instead of running nested.
//
// Layout (top to bottom):
//
//   - Workspace name (input)
//   - Start from (custom 2-option field: trunk / pick…)
//   - Prompt (textarea)
//   - Bookmark name (input, auto-populates from workspace name + prefix)
//   - Submit / Cancel
//
// The Start-from field's first option label is the resolved trunk
// bookmark (defaults to "main" if not provided). The second option is a
// sentinel that triggers the caller's bookmark picker; once picked, the
// option's label flips to the chosen bookmark name.
//
// The Bookmark-name field auto-populates as the user types the workspace
// name — until the user explicitly edits the bookmark field, at which
// point it stops auto-syncing. Blank = no bookmark created.
type newWorkspaceForm struct {
	form           *huh.Form
	workspaceVal   *string
	startFromVal   *string
	promptVal      *string
	bookmarkVal    *string
	confirmSubmit  *bool
	bookmarkPrefix string
	pickedBookmark *string
	startFromField *startFromField
	bookmarkInput  *huh.Input // for forcing a textinput resync after external mutation
	trunkName      string

	// Auto-population bookkeeping for the Bookmark-name field.
	// bookmarkDirty flips true the moment the user types into the
	// bookmark field with a value that's neither the previous auto value
	// nor the current auto value. Once dirty, we stop auto-syncing.
	prevWorkspace string
	prevBookmark  string
	lastAutoBook  string
	bookmarkDirty bool
}

// Start-from sentinel for the "pick a bookmark…" branch. The trunk
// option's stored value is the trunk bookmark name itself (resolved at
// form construction), not a sentinel.
const startFromPick = "__pick__"

// newFormAction is the result of an Update tick on the form. The caller
// inspects it to decide what to do next: clear the modal, dispatch a
// create job, or open the bookmark picker.
type newFormAction int

const (
	newFormActionNone newFormAction = iota
	newFormActionCancel
	newFormActionSubmit
	newFormActionOpenPicker
)

// newNewWorkspaceForm constructs the form. The returned tea.Cmd MUST be
// dispatched by the caller so huh activates its first group — without
// it, tab/enter no-op.
//
//   - bookmarkPrefix: drives the auto-populated default for the
//     Bookmark-name field ("<prefix>/<sanitized-workspace>"). Pass ""
//     to leave the field blank by default.
//   - trunkName: the bookmark to show as the Start-from default
//     (typically the repo's `trunk()` revset bookmark). Falls back to
//     "main" if blank.
func newNewWorkspaceForm(initial NewWorkspaceInitial, bookmarkPrefix, trunkName string) (newWorkspaceForm, tea.Cmd) {
	workspaceVal := strings.TrimSpace(initial.Name)
	pickedBookmark := strings.TrimSpace(initial.Bookmark)
	prefix := strings.TrimSpace(bookmarkPrefix)

	trunk := strings.TrimSpace(trunkName)
	if trunk == "" {
		trunk = "main"
	}

	startFromVal := trunk
	if pickedBookmark != "" && pickedBookmark != trunk {
		startFromVal = startFromPick
	} else {
		pickedBookmark = ""
	}

	var promptVal string
	confirmSubmit := true

	autoBook := computeAutoBookmark(prefix, workspaceVal)
	bookmarkVal := autoBook

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Text.Editor = key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "edit in $EDITOR"))

	// Hand-rolled vertical radio — see start_from_field.go for the
	// rationale (huh.Select's viewport adds scroll behavior we can't
	// fully suppress for a tiny static list).
	startFromField := newStartFromField(
		"Start from",
		"base the new workspace's @ on this revision",
		&startFromVal,
		startFromOptions(trunk, pickedBookmark),
	)

	// Kept as a separate variable so syncBookmarkAuto can force a
	// textinput resync via .Value() after externally mutating
	// *bookmarkVal — huh.Input's render reads from its internal
	// textinput, not the bound pointer, so the pointer-only write isn't
	// enough on its own.
	bookmarkInput := huh.NewInput().
		Title("Bookmark name").
		Description("the bookmark to create on the new workspace's first commit").
		Placeholder(bookmarkPlaceholder(prefix)).
		CharLimit(0).
		Value(&bookmarkVal)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Workspace").
				Placeholder("workspace name").
				CharLimit(0).
				Value(&workspaceVal),
			startFromField,
			huh.NewText().
				Title("Prompt").
				Placeholder("optional agent prompt to run after creating workspace").
				CharLimit(0).
				Lines(4).
				ShowLineNumbers(false).
				ExternalEditor(true).
				Value(&promptVal),
			bookmarkInput,
			huh.NewConfirm().
				Affirmative("Submit").
				Negative("Cancel").
				Value(&confirmSubmit).
				Validate(func(submit bool) error {
					if !submit {
						return nil
					}
					if strings.TrimSpace(workspaceVal) == "" {
						return errors.New("workspace name is required")
					}
					if startFromVal == startFromPick && strings.TrimSpace(pickedBookmark) == "" {
						return errors.New("pick a bookmark or switch to main")
					}
					if strings.TrimSpace(bookmarkVal) == "" {
						return errors.New("bookmark name is required")
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
		form:           form,
		workspaceVal:   &workspaceVal,
		startFromVal:   &startFromVal,
		promptVal:      &promptVal,
		bookmarkVal:    &bookmarkVal,
		confirmSubmit:  &confirmSubmit,
		bookmarkPrefix: prefix,
		pickedBookmark: &pickedBookmark,
		startFromField: startFromField,
		bookmarkInput:  bookmarkInput,
		trunkName:      trunk,
		prevWorkspace:  workspaceVal,
		prevBookmark:   bookmarkVal,
		lastAutoBook:   autoBook,
	}
	return f, form.Init()
}

// startFromOptions builds the two-option list for the Start-from field.
// The first option uses the resolved trunk name; the second flips
// between "pick a bookmark…" (no pick yet) and the picked bookmark name.
func startFromOptions(trunk, picked string) []startFromOption {
	pickLabel := "pick a bookmark…"
	if p := strings.TrimSpace(picked); p != "" {
		pickLabel = p
	}
	return []startFromOption{
		{Label: trunk, Value: trunk},
		{Label: pickLabel, Value: startFromPick},
	}
}

// bookmarkPlaceholder returns the format hint shown when the Bookmark
// name field is empty. With a prefix configured it reads "<prefix>/…"
// so the user sees the shape they'll get; without a prefix it's a
// generic example.
func bookmarkPlaceholder(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "e.g. feat-x (blank to skip)"
	}
	return strings.TrimRight(prefix, "/") + "/…"
}

// computeAutoBookmark derives the default "Bookmark name" value from
// the workspace name and configured prefix. Empty when either input is
// blank or normalization fails — meaning "no auto-bookmark."
func computeAutoBookmark(prefix, workspaceName string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		return ""
	}
	normalized, err := workspace.NormalizeName(name)
	if err != nil {
		return ""
	}
	return strings.TrimRight(prefix, "/") + "/" + normalized
}

// update routes a tea.Msg into the form. Returns the updated form, any
// tea.Cmd that should run, and a newFormAction telling the caller what
// to do next.
//
// After each huh.Form update we run the bookmark auto-sync: if the user
// hasn't manually touched the Bookmark-name field, regenerate its value
// from the current workspace name. The dirty flag flips the moment the
// user's typed value diverges from the last auto-computed default.
func (f newWorkspaceForm) update(msg tea.Msg) (newWorkspaceForm, tea.Cmd, newFormAction) {
	if f.form == nil {
		return f, nil, newFormActionNone
	}
	m, cmd := f.form.Update(msg)
	if updated, ok := m.(*huh.Form); ok {
		f.form = updated
	}

	if f.startFromField != nil && f.startFromField.ConsumePickPending() {
		return f, cmd, newFormActionOpenPicker
	}

	f = f.syncBookmarkAuto()

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

// syncBookmarkAuto detects which of the two bound values changed since
// the previous update tick and adjusts the bookmark field accordingly:
//
//   - If the user typed in the bookmark field with a value that differs
//     from the most recent auto value, we mark the field "dirty" and
//     stop auto-syncing.
//   - If the workspace value changed and the field isn't dirty, we
//     regenerate the bookmark value from the new workspace name.
func (f newWorkspaceForm) syncBookmarkAuto() newWorkspaceForm {
	if f.workspaceVal == nil || f.bookmarkVal == nil {
		return f
	}
	ws := *f.workspaceVal
	bm := *f.bookmarkVal

	if bm != f.prevBookmark && bm != f.lastAutoBook {
		// User typed something different from the auto value. Mark
		// dirty so the next workspace-name change doesn't clobber it.
		// Exception: an explicit clear is treated as "give me the
		// default back" — un-dirty so the next workspace edit (or this
		// tick, if the workspace already changed) refills the field.
		if strings.TrimSpace(bm) == "" {
			f.bookmarkDirty = false
		} else {
			f.bookmarkDirty = true
		}
	}

	if ws != f.prevWorkspace || (!f.bookmarkDirty && strings.TrimSpace(bm) == "") {
		if !f.bookmarkDirty {
			newAuto := computeAutoBookmark(f.bookmarkPrefix, ws)
			f.setBookmarkValue(newAuto)
			f.lastAutoBook = newAuto
			bm = newAuto
		}
		f.prevWorkspace = ws
	}
	f.prevBookmark = bm
	return f
}

// setBookmarkValue writes to both the bound pointer and the underlying
// textinput. The double-write is required: huh.Input renders from its
// own textinput state and only resyncs from the bound pointer when
// .Value() is called on the *Input, so a pointer-only write would leave
// the field showing its previous value on screen.
func (f *newWorkspaceForm) setBookmarkValue(value string) {
	if f.bookmarkVal != nil {
		*f.bookmarkVal = value
	}
	if f.bookmarkInput != nil {
		f.bookmarkInput.Value(f.bookmarkVal)
	}
}

// SetPickedBookmark records a bookmark chosen via the OpenPicker action
// and rebuilds the Start-from field's option list so the second
// option's label reads as the picked bookmark instead of the generic
// "pick a bookmark…" placeholder. Called by the caller after the
// picker modal returns successfully.
func (f *newWorkspaceForm) SetPickedBookmark(name string) {
	name = strings.TrimSpace(name)
	if f.pickedBookmark != nil {
		*f.pickedBookmark = name
	}
	if name == "" {
		*f.startFromVal = f.trunkName
	} else {
		*f.startFromVal = startFromPick
	}
	if f.startFromField != nil {
		f.startFromField.SetOptions(startFromOptions(f.trunkName, name))
	}
}

// RevertStartFrom resets the Start-from selection to the trunk and
// restores the second option's generic label. Called when the bookmark
// picker is cancelled.
func (f *newWorkspaceForm) RevertStartFrom() {
	*f.startFromVal = f.trunkName
	if f.pickedBookmark != nil {
		*f.pickedBookmark = ""
	}
	if f.startFromField != nil {
		f.startFromField.SetOptions(startFromOptions(f.trunkName, ""))
	}
}

// request packages the form values into a NewWorkspaceRequest for the
// downstream create handler.
//
//   - Bookmark = the revision to anchor the new workspace's @ on (the
//     trunk name, or the bookmark picked via the picker).
//   - BookmarkToCreate = the user's "Bookmark name" input. Downstream
//     creates this bookmark on @ if non-empty; blank skips.
func (f newWorkspaceForm) request() NewWorkspaceRequest {
	r := NewWorkspaceRequest{}
	if f.workspaceVal != nil {
		r.Name = strings.TrimSpace(*f.workspaceVal)
	}
	if f.startFromVal != nil {
		switch *f.startFromVal {
		case startFromPick:
			if f.pickedBookmark != nil {
				r.Bookmark = strings.TrimSpace(*f.pickedBookmark)
			}
		default:
			r.Bookmark = *f.startFromVal
		}
	}
	if f.bookmarkVal != nil {
		r.BookmarkToCreate = strings.TrimSpace(*f.bookmarkVal)
	}
	if f.promptVal != nil {
		r.Prompt = strings.TrimSpace(*f.promptVal)
	}
	return r
}

// view renders the form inside the deck's centered card.
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
