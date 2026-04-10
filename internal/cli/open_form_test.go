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

func TestOpenFormDownSelectsExistingWorkspace(t *testing.T) {
	model := newOpenFormModel(openRequest{}, []string{"qa", "qa-hotfix"})
	model.workspaceIndex = -1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(openFormModel)
	if got.currentRequest().Name != "qa" {
		t.Fatalf("expected first workspace selected, got %q", got.currentRequest().Name)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(openFormModel)
	if got.currentRequest().Name != "qa-hotfix" {
		t.Fatalf("expected second workspace selected, got %q", got.currentRequest().Name)
	}
}

func TestOpenFormFiltersWorkspaceMatches(t *testing.T) {
	model := newOpenFormModel(openRequest{Name: "qa"}, []string{"qa", "default", "qa-hotfix"})
	matches := model.filteredWorkspaceOptions()
	if len(matches) != 2 || matches[0] != "qa" || matches[1] != "qa-hotfix" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestOpenFormPreviewText(t *testing.T) {
	model := newOpenFormModel(openRequest{Name: "qa", Prompt: "fix tests"}, []string{"qa"})
	if got := model.previewText(); !strings.Contains(got, "Prompt will not auto-run") {
		t.Fatalf("unexpected existing preview: %q", got)
	}
	model = newOpenFormModel(openRequest{Name: "new-workspace", Prompt: "fix tests"}, nil)
	if got := model.previewText(); !strings.Contains(got, "run the prompt") {
		t.Fatalf("unexpected create preview: %q", got)
	}
}

func TestOpenFormViewShowsPreview(t *testing.T) {
	model := newOpenFormModel(openRequest{Name: "new-workspace"}, nil)
	view := model.View()
	if !strings.Contains(view, "Will create workspace") {
		t.Fatalf("expected preview in view, got %q", view)
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
