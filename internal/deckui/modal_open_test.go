package deckui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// openPickerModel wires a deck with a project finder that returns one
// project and an opener that records the pick. keyO is the `o` binding.
func openPickerModel(t *testing.T, opened *string) Model {
	t.Helper()
	return New([]Item{{ProjectName: "p", WorkspaceName: "w"}}, func(ActionRequest) error { return nil }).
		WithProjectFinder(func() tea.Cmd {
			return func() tea.Msg {
				return ProjectsDoneMsg{Projects: []ProjectItem{{Name: "alpha", Path: "/a"}}}
			}
		}).
		WithProjectOpener(func(p ProjectItem) error { *opened = p.Name; return nil })
}

var keyO = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}

func TestOpenPickerOpensLoadingThenPopulates(t *testing.T) {
	var opened string
	m := openPickerModel(t, &opened)

	updated, cmd := m.Update(keyO)
	dm := updated.(Model)
	op, ok := dm.active.(*openPicker)
	if !ok {
		t.Fatalf("expected active *openPicker, got %T", dm.active)
	}
	if !op.loading {
		t.Fatal("picker should start in loading state")
	}

	// Running the finder command yields ProjectsDoneMsg, which populates.
	msg := execCmd(t, cmd)
	updated, _ = dm.Update(msg)
	dm = updated.(Model)
	op, ok = dm.active.(*openPicker)
	if !ok {
		t.Fatal("picker should stay open after projects arrive")
	}
	if op.loading {
		t.Fatal("picker should leave loading after ProjectsDoneMsg")
	}
}

func TestOpenPickerEnterSelectsAndQuits(t *testing.T) {
	var opened string
	m := openPickerModel(t, &opened)

	updated, cmd := m.Update(keyO)
	dm := updated.(Model)
	updated, _ = dm.Update(execCmd(t, cmd)) // ProjectsDoneMsg
	dm = updated.(Model)

	_, cmd = dm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if opened != "alpha" {
		t.Fatalf("projectOpener not invoked with selection, opened=%q", opened)
	}
	if cmd == nil {
		t.Fatal("expected a command (tea.Quit) after selecting a project")
	}
	if msg := cmd(); msg == nil {
		// tea.Quit's command returns a tea.QuitMsg; just confirm non-nil.
		t.Fatal("expected quit message")
	}
}

func TestOpenPickerEscClosesToRowMode(t *testing.T) {
	var opened string
	m := openPickerModel(t, &opened)

	updated, cmd := m.Update(keyO)
	dm := updated.(Model)
	updated, _ = dm.Update(execCmd(t, cmd)) // ProjectsDoneMsg
	dm = updated.(Model)

	updated, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	dm = updated.(Model)
	if dm.active != nil {
		t.Fatalf("esc should clear the active modal, got %T", dm.active)
	}
	if opened != "" {
		t.Fatal("esc must not open any project")
	}
}
