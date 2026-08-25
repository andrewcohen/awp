package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

var keyO = runeKey("o")

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

	_, cmd = dm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	updated, _ = dm.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	dm = updated.(Model)
	if dm.active != nil {
		t.Fatalf("esc should clear the active modal, got %T", dm.active)
	}
	if opened != "" {
		t.Fatal("esc must not open any project")
	}
}

// A deck that hosts its own panes has no tmux client for the ProjectOpener's
// switch-client to move, so `o` → enter went through the motions and nothing
// appeared. It takes the importer instead: record the project, then open its
// agent pane in place.
func TestOpenPickerImportsAndOpensAPaneOnAPaneHost(t *testing.T) {
	var opened, imported string
	m := openPickerModel(t, &opened).
		WithPaneBackend(allKinds()).
		WithProjectImporter(func(p ProjectItem) (Item, error) {
			imported = p.Name
			return Item{ProjectName: p.Name, WorkspaceName: "default", Path: p.Path, RepoRoot: p.Path}, nil
		})
	m.width, m.height = 120, 40

	updated, cmd := m.Update(keyO)
	dm := updated.(Model)
	updated, _ = dm.Update(execCmd(t, cmd)) // ProjectsDoneMsg
	dm = updated.(Model)

	updated, _ = dm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	dm = updated.(Model)

	if imported != "alpha" {
		t.Fatalf("projectImporter not invoked with the selection, imported=%q", imported)
	}
	if opened != "" {
		t.Fatalf("a pane host must not take the tmux opener, opened=%q", opened)
	}
	if _, ok := dm.active.(*panePopover); !ok {
		t.Fatalf("expected the imported project's pane to open, active=%T status=%q", dm.active, dm.status)
	}
}

// The importer failing is the one thing the user has to be told about: the
// picker closes on success, so a silent failure looks like the same no-op
// this whole branch exists to fix.
func TestOpenPickerReportsAnImportFailure(t *testing.T) {
	var opened string
	m := openPickerModel(t, &opened).
		WithPaneBackend(allKinds()).
		WithProjectImporter(func(ProjectItem) (Item, error) { return Item{}, errImportFailed })
	m.width, m.height = 120, 40

	updated, cmd := m.Update(keyO)
	dm := updated.(Model)
	updated, _ = dm.Update(execCmd(t, cmd)) // ProjectsDoneMsg
	dm = updated.(Model)

	updated, _ = dm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	dm = updated.(Model)

	if !strings.Contains(dm.status, errImportFailed.Error()) {
		t.Fatalf("import failure not surfaced, status=%q", dm.status)
	}
	if _, ok := dm.active.(*openPicker); !ok {
		t.Fatalf("picker should stay open after a failed import, active=%T", dm.active)
	}
}

var errImportFailed = errors.New("no write access to workspace state")
