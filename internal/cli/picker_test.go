package cli

import (
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

func TestPickerEnterSelectsCurrentWorkspace(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "dev"})
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := model.Update(runeKey("q"))
	got := updated.(pickerModel)
	if !got.cancel {
		t.Fatal("expected cancel")
	}
}

func TestPickerFilterInputTracksTypedQuery(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "qa-hotfix", "prod"})
	model = applyPickerMsg(model, runeKey("/"))
	model = applyPickerMsg(model, runeKey("h"))
	model = applyPickerMsg(model, runeKey("o"))
	model = applyPickerMsg(model, runeKey("t"))
	if got := model.list.FilterValue(); got != "hot" {
		t.Fatalf("expected filter value hot, got %q", got)
	}
}

func TestPickerEscWhileFilteringClearsFilterInsteadOfCancelling(t *testing.T) {
	model := newPickerModel("Select workspace", []string{"qa", "prod"})
	got := applyPickerMsg(model, runeKey("/"))
	if got.list.FilterState() != list.Filtering {
		t.Fatalf("expected filtering state, got %v", got.list.FilterState())
	}
	got = applyPickerMsg(got, tea.KeyPressMsg{Code: tea.KeyEsc})
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
