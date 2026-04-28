package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenFormSubmitRequiresNameOrBookmark(t *testing.T) {
	model := newOpenFormModel(openRequest{}, nil)
	model.activeField = 3
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(openFormModel)
	if got.err == "" {
		t.Fatal("expected validation error")
	}
	if got.cancel {
		t.Fatal("did not expect cancel")
	}
}

func TestOpenFormTabMovesToActions(t *testing.T) {
	model := newOpenFormModel(openRequest{}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyTab})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(openFormModel)
	if got.activeField != 3 {
		t.Fatalf("expected active field 3, got %d", got.activeField)
	}
}

func TestOpenFormHJKLAreTextInsideInputs(t *testing.T) {
	model := newOpenFormModel(openRequest{}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	got := updated.(openFormModel)
	if got.currentRequest().Name != "hjkl" {
		t.Fatalf("expected hjkl text input, got %q", got.currentRequest().Name)
	}
}

func TestOpenFormCtrlGWithoutEditorSetsError(t *testing.T) {
	t.Setenv("EDITOR", "")
	model := newOpenFormModel(openRequest{}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyTab})
	updated, _ = updated.(openFormModel).Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	got := updated.(openFormModel)
	if !strings.Contains(got.err, "$EDITOR") {
		t.Fatalf("expected editor error, got %q", got.err)
	}
}

func TestOpenFormPromptHasNoCharLimit(t *testing.T) {
	model := newOpenFormModel(openRequest{}, nil)
	if model.promptInput.CharLimit != 0 {
		t.Fatalf("expected prompt char limit 0, got %d", model.promptInput.CharLimit)
	}
}
