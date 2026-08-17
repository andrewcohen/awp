package deckui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// freeTextForm is the deck's front door to a new workspace: one box, one
// sentence, in the user's own words. What they type is handed to an agent
// that turns it into the four things creation needs, and the workspace is
// created from that answer without a further confirmation.
//
// The box is what is on screen for as long as someone is typing and not one
// moment longer: submit closes it, and both the model call and the create
// run in the background. So there is no busy state here, and no error
// state — those belong to the deck the user is handed back to.
//
// A plain struct rather than a tea.Model, like every other modal here — see
// doc.go. It composes into the deck's single program instead of running
// nested.
//
// A few lines rather than one, with the same controls the structured
// form's prompt field has — ctrl+g opens $EDITOR. What people actually type
// here is not always one sentence: a bug worth describing comes with the
// symptom and where to look, and a box that showed one line of it made the
// user write for a field they could not read back.
//
// The cost is that enter can no longer submit, since in a multi-line field
// enter is a newline. Submit is freeTextSubmitKey (ctrl+d), which is the
// same "I am done with this input" key a shell uses, and it is advertised
// in the footer rather than left to be discovered.
type freeTextForm struct {
	form    *huh.Form
	textVal *string

	// projectName is where the workspace will be created unless the text
	// says otherwise. On screen because the answer is not always the row
	// the user was on — the agent may retarget it — and the point at which
	// to notice that is before the call, not after.
	projectName string
}

// freeTextAction is what an update tick tells the deck to do next.
type freeTextAction int

const (
	freeTextActionNone freeTextAction = iota
	freeTextActionCancel
	// freeTextActionSubmit asks the deck to resolve the text.
	freeTextActionSubmit
	// freeTextActionFallback asks for the structured form instead, with
	// whatever has been typed carried into it. The power-user door, and
	// the one out of a box whose agent is not answering.
	freeTextActionFallback
)

// freeTextCardWidth is the box's card, matching the structured form's so
// the handoff between them does not resize under the user.
const freeTextCardWidth = 84

// freeTextFormWidth is the width huh lays the field out at: the card less
// its padding.
//
// Set at construction as well as on every frame. A textarea wraps its lines
// as they arrive and does not re-wrap them when the width changes later, so
// text that is already in the box when it opens — reopened after a
// resolution, say — would keep the wrap of huh's ~40-column default and
// render half the width of its own card.
const freeTextFormWidth = freeTextCardWidth - 4

// freeTextSubmitKey sends the text to be resolved.
//
// ctrl+enter is the key people already use to send a multi-line box.
// ctrl+d is bound too but not advertised: a terminal without the kitty
// keyboard protocol cannot encode ctrl+enter at all, and the fallback is
// worth having where it silently does nothing.
var freeTextSubmitKey = key.NewBinding(
	key.WithKeys("ctrl+enter", "ctrl+d"),
	key.WithHelp("ctrl+enter", "create"),
)

// freeTextEditorKey matches the structured form's prompt field.
var freeTextEditorKey = key.NewBinding(
	key.WithKeys("ctrl+g"),
	key.WithHelp("ctrl+g", "editor"),
)

// freeTextFallbackKey opens the structured form directly, skipping the
// model call.
//
// ctrl+f rather than a plain letter because every letter is text here.
// Advertised in the box's own footer rather than only in `?`: a user whose
// agent is broken needs to find it at the moment it is broken, and the box
// is where they are looking.
var freeTextFallbackKey = key.NewBinding(
	key.WithKeys("ctrl+f"),
	key.WithHelp("ctrl+f", "form"),
)

// newFreeTextForm builds the box. The returned tea.Cmd MUST be dispatched
// so huh activates the group — without it, enter no-ops. Same contract as
// newNewWorkspaceForm.
func newFreeTextForm(initial, projectName string) (freeTextForm, tea.Cmd) {
	textVal := strings.TrimSpace(initial)

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Text.Editor = freeTextEditorKey
	// huh's Text takes enter as "next field" and puts newline on
	// alt+enter. In a box that is one field and a few lines that is
	// backwards: enter has nowhere to advance to, so it would end the
	// input the user is in the middle of writing. Swap them — enter writes
	// a line, tab still leaves the field.
	km.Text.NewLine = key.NewBinding(
		key.WithKeys("enter", "alt+enter", "ctrl+j"),
		key.WithHelp("enter", "new line"),
	)
	km.Text.Next = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next"))

	form := huh.NewForm(
		huh.NewGroup(
			// No title. The card already says "New workspace" and names the
			// project, and the field is the only thing under it — a prompt
			// asking what you want to work on is a third line saying what the
			// first two said, above the box you are trying to think in.
			huh.NewText().
				CharLimit(0).
				Lines(4).
				ShowLineNumbers(false).
				ExternalEditor(true).
				Value(&textVal),
		),
	).WithTheme(charm.HuhTheme()).WithKeyMap(km).WithShowHelp(false).WithWidth(freeTextFormWidth)

	f := freeTextForm{form: form, textVal: &textVal, projectName: strings.TrimSpace(projectName)}
	return f, form.Init()
}

// text is what the user has typed, trimmed.
func (f freeTextForm) text() string {
	if f.textVal == nil {
		return ""
	}
	return strings.TrimSpace(*f.textVal)
}

// update routes a message into the box.
//
// Two keys are handled before huh sees them. The submit key, because the
// field is multi-line and huh has no notion of "this group is done" short of
// moving off the field. And the fallback key, because huh would otherwise
// take ctrl+f as text.
func (f freeTextForm) update(msg tea.Msg) (freeTextForm, tea.Cmd, freeTextAction) {
	if f.form == nil {
		return f, nil, freeTextActionNone
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if key.Matches(keyMsg, freeTextFallbackKey) {
			return f, nil, freeTextActionFallback
		}
		if key.Matches(keyMsg, freeTextSubmitKey) {
			// An empty box is not a request. Rather than erroring at
			// someone who pressed the key to see what it did, treat it as
			// the question it probably is and open the form they can fill
			// in by hand.
			if f.text() == "" {
				return f, nil, freeTextActionFallback
			}
			return f, nil, freeTextActionSubmit
		}
	}

	m, cmd := f.form.Update(msg)
	if updated, ok := m.(*huh.Form); ok {
		f.form = updated
	}

	switch f.form.State {
	case huh.StateAborted:
		return f, cmd, freeTextActionCancel
	case huh.StateCompleted:
		// Reachable when huh decides the group is finished on its own — a
		// tab off the only field. Treated exactly like the submit key
		// rather than ignored, so there is no keystroke that leaves the box
		// completed and inert.
		if f.text() == "" {
			return f, cmd, freeTextActionFallback
		}
		return f, cmd, freeTextActionSubmit
	}
	return f, cmd, freeTextActionNone
}

// view renders the box in the deck's centered card, matching the structured
// form it can hand off to.
func (f freeTextForm) view(width, height int) string {
	if f.form == nil {
		return ""
	}
	theme := charm.DefaultTheme()
	card := theme.Card.Width(freeTextCardWidth)

	var b strings.Builder
	title := theme.Title.Render("New workspace")
	if f.projectName != "" {
		title += theme.Hint.Render("  " + f.projectName)
	}
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(f.form.WithWidth(freeTextFormWidth).View())
	b.WriteString("\n")
	b.WriteString(f.footer(theme))

	rendered := card.Render(b.String())
	if width <= 0 || height <= 0 {
		return rendered
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}

// footer is the one line under the box: which keys there are.
//
// There is no in-flight state to show any more. Submit closes the box, so
// the spinner for what happens next belongs to the activity bar in the
// deck the user is handed back to.
func (f freeTextForm) footer(theme charm.Theme) string {
	return theme.Hint.Render(helpPair(freeTextSubmitKey) + "  ·  " +
		helpPair(freeTextEditorKey) + "  ·  " +
		helpPair(freeTextFallbackKey) + "  ·  esc  cancel")
}

// helpPair renders a binding as "key  description" for the footer, so the
// footer and the binding cannot describe the same key differently.
func helpPair(b key.Binding) string {
	return b.Help().Key + "  " + b.Help().Desc
}
