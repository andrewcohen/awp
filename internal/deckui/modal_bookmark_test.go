package deckui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// bookmarkLinkModel wires a deck whose B-key linker fetches one bookmark
// and records what gets linked.
func bookmarkLinkModel(t *testing.T, linked *[2]string) Model {
	t.Helper()
	return New([]Item{{ProjectName: "p", WorkspaceName: "ws", RepoRoot: "/tmp"}}, func(ActionRequest) error { return nil }).
		WithBookmarkFetcher(func(string) tea.Cmd {
			return func() tea.Msg { return BookmarksDoneMsg{Bookmarks: []string{"andrew/feat"}} }
		}).
		WithBookmarkLinkHandler(func(target Item, name string) error {
			linked[0] = target.WorkspaceName
			linked[1] = name
			return nil
		})
}

var keyB = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}}

func TestBookmarkPickerLinkFlow(t *testing.T) {
	var linked [2]string
	m := bookmarkLinkModel(t, &linked)

	updated, cmd := m.Update(keyB)
	dm := updated.(Model)
	bp, ok := dm.active.(*bookmarkPicker)
	if !ok || !bp.loading {
		t.Fatalf("expected loading bookmarkPicker, got %T", dm.active)
	}
	if bp.purpose != bookmarkPurposeLinkExisting {
		t.Fatalf("expected link-existing purpose, got %v", bp.purpose)
	}

	// Fetch completes → picker populates.
	updated, _ = dm.Update(execCmd(t, cmd))
	dm = updated.(Model)
	bp, ok = dm.active.(*bookmarkPicker)
	if !ok || bp.loading {
		t.Fatal("expected populated bookmarkPicker after fetch")
	}

	// Enter links the selected bookmark to the target workspace.
	updated, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	dm = updated.(Model)
	if dm.active != nil {
		t.Fatalf("expected picker closed after selection, got %T", dm.active)
	}
	if linked[0] != "ws" || linked[1] != "andrew/feat" {
		t.Fatalf("link handler got %v, want [ws andrew/feat]", linked)
	}
}

func TestBookmarkPickerEscClosesWithoutLinking(t *testing.T) {
	var linked [2]string
	m := bookmarkLinkModel(t, &linked)

	updated, cmd := m.Update(keyB)
	dm := updated.(Model)
	updated, _ = dm.Update(execCmd(t, cmd)) // BookmarksDoneMsg
	dm = updated.(Model)

	updated, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	dm = updated.(Model)
	if dm.active != nil {
		t.Fatalf("esc should clear the modal, got %T", dm.active)
	}
	if linked[0] != "" || linked[1] != "" {
		t.Fatalf("esc must not link anything, got %v", linked)
	}
}
