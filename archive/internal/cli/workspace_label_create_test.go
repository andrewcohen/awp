package cli

import "testing"

// A workspace created with a label wears it from the first frame the deck
// draws, rather than appearing under its slug until something renames it.
func TestOpenWorkspaceAppliesLabel(t *testing.T) {
	svc := &fakeService{}
	err := openWorkspaceWithReporter(&recordingRunner{}, svc, openRequest{
		Name:       "sidebar-cursor-bug",
		Label:      "Sidebar cursor bug",
		Yes:        true,
		PaneHosted: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	if got := svc.displayNames["sidebar-cursor-bug"]; got != "Sidebar cursor bug" {
		t.Errorf("label = %q, want %q", got, "Sidebar cursor bug")
	}
}

// No label means no label — not an empty one, which SetDisplayName reads as
// "clear it".
func TestOpenWorkspaceWithoutLabelDoesNotSetOne(t *testing.T) {
	svc := &fakeService{}
	err := openWorkspaceWithReporter(&recordingRunner{}, svc, openRequest{
		Name:       "qa",
		Yes:        true,
		PaneHosted: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	if _, ok := svc.displayNames["qa"]; ok {
		t.Errorf("SetDisplayName was called with %q", svc.displayNames["qa"])
	}
}
