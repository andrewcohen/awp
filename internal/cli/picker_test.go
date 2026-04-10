package cli

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPickerEnterSelectsCurrentWorkspace(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "dev"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(pickerModel)
	if got.choice != "qa" {
		t.Fatalf("expected qa selected, got %q", got.choice)
	}
	if got.cancel {
		t.Fatal("did not expect cancel")
	}
}

func TestPickerCancelOutsideFilterQuits(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(pickerModel)
	if !got.cancel {
		t.Fatal("expected cancel")
	}
}

func TestPickerFilterInputTracksTypedQuery(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "qa-hotfix", "prod"})
	model = applyPickerMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = applyPickerMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = applyPickerMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = applyPickerMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if got := model.list.FilterValue(); got != "hot" {
		t.Fatalf("expected filter value hot, got %q", got)
	}
}

func TestPickerEscWhileFilteringClearsFilterInsteadOfCancelling(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "prod"})
	got := applyPickerMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if got.list.FilterState() != list.Filtering {
		t.Fatalf("expected filtering state, got %v", got.list.FilterState())
	}
	got = applyPickerMsg(got, tea.KeyMsg{Type: tea.KeyEsc})
	if got.cancel {
		t.Fatal("did not expect cancel while clearing filter")
	}
	if got.list.FilterState() == list.Filtering {
		t.Fatalf("expected filtering to end, got %v", got.list.FilterState())
	}
}

func applyPickerMsg(model pickerModel, msg tea.Msg) pickerModel {
	updated, cmd := model.Update(msg)
	got := updated.(pickerModel)
	if cmd == nil {
		return got
	}
	next := cmd()
	if next == nil {
		return got
	}
	switch next.(type) {
	case list.FilterMatchesMsg:
		updated, _ = got.Update(next)
		return updated.(pickerModel)
	default:
		return got
	}
}
