package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// ctrl+g has to produce a command — the exec that suspends the program for
// $EDITOR — rather than being swallowed as literal input into the textarea.
func TestCtrlGAsksForTheEditor(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m = press(m, "c")
	if !m.editing {
		t.Fatal("fixture is wrong: expected the compose box open")
	}
	before := m.editor.area.Value()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected ctrl+g to return a command")
	}
	if !m.editing {
		t.Fatal("expected the box to stay open while the editor runs")
	}
	if got := m.editor.area.Value(); got != before {
		t.Fatalf("ctrl+g typed into the box: %q → %q", before, got)
	}
}

// What comes back replaces the body, and the kind chosen before leaving survives
// the round trip — the editor is for the text, not for the whole comment.
func TestBodyFromTheEditorReplacesTheBox(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m = press(m, "c")
	m = press(m, "t") // some typing, to be replaced
	m = press(m, "tab")
	kind := m.editor.kind

	updated, _ := m.Update(composeEditedMsg{body: "a much longer remark\n\nwith a second paragraph"})
	m = updated.(Model)
	if got := m.editor.area.Value(); got != "a much longer remark\n\nwith a second paragraph" {
		t.Fatalf("body not taken from the editor: %q", got)
	}
	if m.editor.kind != kind {
		t.Fatalf("the kind changed across the round trip: %q → %q", kind, m.editor.kind)
	}
	if !m.editing {
		t.Fatal("expected the box still open, holding the edited text")
	}
	// And saving it stores what came back.
	m = press(m, "enter")
	if len(m.comments) != 1 || !strings.Contains(m.comments[0].Body, "second paragraph") {
		t.Fatalf("expected the edited body saved, got %+v", m.comments)
	}
}

// A failed round trip must not lose what the reviewer already typed.
func TestEditorFailureKeepsWhatWasTyped(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m = press(m, "c")
	m = press(m, "t")
	typed := m.editor.area.Value()

	updated, _ := m.Update(composeEditedMsg{err: errors.New("editor: exit status 1")})
	m = updated.(Model)
	if got := m.editor.area.Value(); got != typed {
		t.Fatalf("a failed editor round trip lost the draft: %q → %q", typed, got)
	}
	if !m.statusErr {
		t.Fatal("expected the failure reported")
	}
}

// The trailing newline every editor adds would otherwise render as a blank body
// row, since a blank line in a comment is a deliberate paragraph break.
func TestEditorTrailingNewlinesAreTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte("a remark\n\nwith a paragraph\n\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg, ok := composeBodyFrom(path).(composeEditedMsg)
	if !ok {
		t.Fatalf("expected a composeEditedMsg, got %T", composeBodyFrom(path))
	}
	if msg.err != nil {
		t.Fatalf("read: %v", msg.err)
	}
	if msg.body != "a remark\n\nwith a paragraph" {
		t.Fatalf("body = %q", msg.body)
	}
}

// A file that vanished (or an editor that never wrote it) is reported rather than
// silently blanking the draft.
func TestEditorReadFailureIsReported(t *testing.T) {
	msg, ok := composeBodyFrom(filepath.Join(t.TempDir(), "never-written.md")).(composeEditedMsg)
	if !ok {
		t.Fatal("expected a composeEditedMsg")
	}
	if msg.err == nil {
		t.Fatal("expected a read failure to be reported")
	}
}

// It is in the reference, which is the only place the compose box's keys are
// written down.
func TestHelpDocumentsTheEditorKey(t *testing.T) {
	view := stripANSI(helpContent(120, nil, nil))
	if !strings.Contains(view, "ctrl+g") {
		t.Fatalf("expected ctrl+g documented:\n%s", view)
	}
}

// And in the box's own hint, which is what a reviewer sees without opening help.
func TestComposeHintNamesTheEditorKey(t *testing.T) {
	e := newCommentEditor(review.Anchor{Path: "a.go", LineHint: 1}, 100)
	if hint := stripANSI(e.view(100)); !strings.Contains(hint, "ctrl+g") {
		t.Fatalf("expected the hint to name ctrl+g:\n%s", hint)
	}
}
