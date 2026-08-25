package ui

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// cursorToFirstLine parks the cursor on the first diff line of the stream.
func cursorToFirstLine(t *testing.T, m Model) Model {
	t.Helper()
	for m.stream.rows[m.cursorRow].kind != rowLine {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached a diff line")
		}
	}
	return m
}

// C files a remark about the file the cursor is in — the scope a reviewer
// otherwise fakes by picking whichever line is nearest and writing "this file".
func TestCComposesAFileComment(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m = cursorToFirstLine(t, m)
	m = press(m, "C")

	if !m.editing {
		t.Fatalf("C did not open the compose box (status %q)", m.status)
	}
	if got := m.editor.anchor.Scope(); got != review.FileScope {
		t.Errorf("the box is on scope %v, want FileScope (anchor %+v)", got, m.editor.anchor)
	}
	if m.editor.anchor.Path != "a.go" {
		t.Errorf("the box is on %q, want a.go", m.editor.anchor.Path)
	}
	// The header has to say which of the three scopes you are typing into: "on a.go"
	// and "on a.go:12" differ by a detail the eye skips, and this is the moment the
	// scope is being decided.
	if head := stripANSI(m.editor.view(80)); !strings.Contains(head, "all of a.go") {
		t.Errorf("the compose header does not name the file scope:\n%s", head)
	}
}

// It saves as a file-scoped record — no line — which is what the store, the
// publish path and the placement all key off.
func TestAFileCommentSavesWithNoLine(t *testing.T) {
	var saved review.Comment
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SaveComment = func(c review.Comment) error { saved = c; return nil }
	m = cursorToFirstLine(t, m)
	m = press(m, "C")

	m, cmd := typeAndSave(m, "this belongs in internal/review")
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	if saved.Body == "" {
		t.Fatalf("nothing was saved (status %q)", m.status)
	}
	if saved.Anchor.LineHint != 0 {
		t.Errorf("saved with line %d — a file comment has no line", saved.Anchor.LineHint)
	}
	if got := saved.Anchor.Scope(); got != review.FileScope {
		t.Errorf("saved on scope %v, want FileScope (anchor %+v)", got, saved.Anchor)
	}
	if saved.Anchor.Path != "a.go" {
		t.Errorf("saved on %q, want a.go", saved.Anchor.Path)
	}
}

// A range under selection is answered rather than ignored: asking for the file's
// scope says what the remark covers, so the range goes away instead of quietly
// narrowing the anchor back to lines.
func TestCDropsAnOpenRange(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m = cursorToFirstLine(t, m)
	m = press(m, "v")
	m = press(m, "j")
	if !m.visualActive() {
		t.Fatal("precondition: expected a range to be up")
	}
	m = press(m, "C")

	if m.visualActive() {
		t.Error("the range is still up after asking for the file's scope")
	}
	if got := m.editor.anchor.Scope(); got != review.FileScope {
		t.Errorf("the range narrowed the box to scope %v, want FileScope", got)
	}
}

// Outside any file there is no file to be about, and saying so beats opening a box
// whose remark would have nowhere to go.
func TestCOutsideAFileSaysSo(t *testing.T) {
	m := commentModel(t)
	m = press(m, "C")

	if m.editing {
		t.Error("C opened a box with no file to comment on")
	}
	if m.status == "" {
		t.Error("C did nothing and said nothing")
	}
}

// The key is discoverable: ? is the only place the viewer's bindings are written
// down, so a binding missing from it is a feature nobody finds.
func TestTheHelpNamesTheFileCommentKey(t *testing.T) {
	var found bool
	for _, g := range viewerKeyGroups(nil) {
		for _, k := range g.Keys {
			if k[0] == "C" {
				found = true
				if !strings.Contains(k[1], "file") {
					t.Errorf("C is listed as %q, which does not say it is about a file", k[1])
				}
			}
		}
	}
	if !found {
		t.Error("? does not list C — a binding missing from the only place they are written down")
	}
}
