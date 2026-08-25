package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keySubmit is the box's ctrl+enter.
var keySubmit = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}

// newTestFreeTextForm builds the box and runs its init command, which is
// what activates huh's group. Without it every key press no-ops.
func newTestFreeTextForm(t *testing.T, initial string) freeTextForm {
	t.Helper()
	f, cmd := newFreeTextForm(initial, "awp")
	if cmd == nil {
		t.Fatal("newFreeTextForm returned no init command")
	}
	if msg := cmd(); msg != nil {
		f, _, _ = f.update(msg)
	}
	return f
}

func typeInto(f freeTextForm, s string) freeTextForm {
	for _, r := range s {
		f, _, _ = f.update(runeKey(string(r)))
	}
	return f
}

// send delivers a key and then drains the commands huh returns, the way
// Bubble Tea does for a real program.
//
// huh does not complete a form inside the key press: enter produces a
// nextFieldMsg, which produces a nextGroupMsg, and only then does State
// become Completed. A test that looked at the action from the key press
// alone would see "nothing happened" and be testing the pump rather than
// the box.
func send(f freeTextForm, msg tea.Msg) (freeTextForm, freeTextAction) {
	f, cmd, action := f.update(msg)
	for range 10 {
		if cmd == nil || action != freeTextActionNone {
			break
		}
		next := cmd()
		if next == nil {
			break
		}
		f, cmd, action = f.update(next)
	}
	return f, action
}

func TestFreeTextFormSubmitKeySendsWhatWasTyped(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "fix the sidebar cursor bug")
	f, action := send(f, keySubmit)
	if action != freeTextActionSubmit {
		t.Fatalf("action = %v, want submit", action)
	}
	if f.text() != "fix the sidebar cursor bug" {
		t.Errorf("text = %q", f.text())
	}
}

// Submitting an empty box is a question, not a request — it opens the form
// rather than erroring at someone who pressed the key to see what it did.
func TestFreeTextFormEmptySubmitFallsBackToTheForm(t *testing.T) {
	f := newTestFreeTextForm(t, "")
	_, action := send(f, keySubmit)
	if action != freeTextActionFallback {
		t.Fatalf("action = %v, want fallback", action)
	}
}

func TestFreeTextFormEscCancels(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "something")
	_, action := send(f, tea.KeyPressMsg{Code: tea.KeyEscape})
	if action != freeTextActionCancel {
		t.Fatalf("action = %v, want cancel", action)
	}
}

// The power-user door, and the way out when the agent is not answering.
func TestFreeTextFormFallbackKeyOpensTheForm(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "spike a jj backed undo")
	f2, action := send(f, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if action != freeTextActionFallback {
		t.Fatalf("action = %v, want fallback", action)
	}
	// The text has to survive: it is what the form gets pre-filled from.
	if f2.text() != "spike a jj backed undo" {
		t.Errorf("text = %q, want it carried into the form", f2.text())
	}
}

// ctrl+f must not be typed into the box as text.
func TestFreeTextFormFallbackKeyIsNotText(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "abc")
	f, _, _ = f.update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if f.text() != "abc" {
		t.Errorf("text = %q, want abc — ctrl+f was inserted as text", f.text())
	}
}

func TestFreeTextFormViewShowsWhatItNeedsTo(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "fix the bug")

	view := f.view(100, 30)
	if !strings.Contains(view, "New workspace") {
		t.Error("view is missing its title")
	}
	// Every key the box has, named on the box.
	for _, want := range []string{"ctrl+enter", "ctrl+g", "ctrl+f", "esc"} {
		if !strings.Contains(view, want) {
			t.Errorf("the footer does not advertise %s", want)
		}
	}
	// Where it will create, before the call rather than after it.
	if !strings.Contains(view, "awp") {
		t.Error("the box does not say which project it will create in")
	}
}

// The box is the sentence and the keys, and nothing that repeats what the
// card above it already said. A user typing into it is thinking about their
// own words, not reading a prompt for them.
func TestFreeTextFormHasNoFieldTitle(t *testing.T) {
	view := newTestFreeTextForm(t, "").view(100, 30)
	if strings.Contains(view, "work on") {
		t.Error("the field still carries a title above the box")
	}
}

// The box is pre-filled when the deck reopens it with text already typed.
func TestFreeTextFormCarriesInitialText(t *testing.T) {
	f := newTestFreeTextForm(t, "  look at PR 2320  ")
	if f.text() != "look at PR 2320" {
		t.Errorf("text = %q", f.text())
	}
}

// The field takes more than one line: enter is a newline here, not submit.
func TestFreeTextFormIsMultiLine(t *testing.T) {
	f := typeInto(newTestFreeTextForm(t, ""), "the sidebar cursor drifts")
	f, action := send(f, tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != freeTextActionNone {
		t.Fatalf("enter produced %v, want none — it must insert a newline", action)
	}
	f = typeInto(f, "look at renderMetaText")
	if !strings.Contains(f.text(), "\n") {
		t.Errorf("text = %q, want two lines", f.text())
	}
	if !strings.Contains(f.text(), "renderMetaText") {
		t.Errorf("text = %q, want the second line kept", f.text())
	}
}

// Text already in the box when it opens must lay out at the box's width.
//
// A textarea wraps lines as they arrive and does not re-wrap them when the
// width changes afterwards, so a form built at huh's default width renders
// pre-filled text at roughly half its card and only straightens out once
// the user edits it.
func TestFreeTextFormWrapsPrefilledTextAtFullWidth(t *testing.T) {
	f := newTestFreeTextForm(t, "the sidebar cursor drifts a column to the left whenever a row has an emoji")
	view := f.view(120, 30)

	// At the card's width this phrase is one row. At huh's ~29-column
	// default it is split across three.
	if !containsOnOneRow(view, "the sidebar cursor drifts a column to the left") {
		t.Error("pre-filled text wrapped narrower than the box it is in")
	}
}

// containsOnOneRow reports whether any single rendered row contains s.
func containsOnOneRow(view, s string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}
