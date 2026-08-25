package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/awplog"
)

// errJJFailure is the shape a failed `jj diff` actually arrives in: the action, the
// exit status, and jj's own stderr on the lines beneath. The stderr is the only
// part that says why, and it is the part that used to reach nothing.
var errJJFailure = errors.New(`load diff: "jj" exited 1:
Error: Revision "main..@" doesn't exist
Hint: Did you mean "main@origin"?`)

func failedLoadModel(t *testing.T) Model {
	t.Helper()
	m := New("/repo", func(int) (string, error) { return "", errJJFailure }, nil)
	m.SetSize(100, 20)
	updated, _ := m.Update(diffLoadedMsg{err: errJJFailure})
	return updated.(Model)
}

// The reason a diff would not load has to be on the screen, not only on a footer
// row too short for it.
func TestAFailedLoadShowsJJsOwnComplaint(t *testing.T) {
	m := failedLoadModel(t)
	body := m.Body(100, 20)
	for _, want := range []string{"Could not load the diff", `doesn't exist`, `main@origin`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in the body, got:\n%s", want, body)
		}
	}
}

// The footer is one row, so it gets one line — a status carrying jj's stderr
// wraps instead of truncating, and in the deck's diff modal that pushes the
// footer into the body.
func TestTheStatusLineStaysOneRow(t *testing.T) {
	awplog.SetPathForTest(t, filepath.Join(t.TempDir(), "awp.log"))
	m := failedLoadModel(t)
	if strings.Contains(m.status, "\n") {
		t.Fatalf("status spans rows: %q", m.status)
	}
	// And the whole complaint still reaches the log, which is what the panel
	// points at when it does not fit.
	if got := strings.Join(logLines(t), "\n"); !strings.Contains(got, "main@origin") {
		t.Fatalf("expected jj's hint in the log, got %q", got)
	}
}

// A load that fails while a diff is already up keeps the diff: the panel is for
// the case where the panes would otherwise be two empty boxes.
func TestAFailedRefreshKeepsTheDiff(t *testing.T) {
	m := New("/repo", func(int) (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(100, 20)
	updated, _ := m.Update(loadDiffCmd(m.LoadDiff, m.contextLines)())
	m = updated.(Model)
	updated, _ = m.Update(diffLoadedMsg{err: errJJFailure})
	m = updated.(Model)
	if body := m.Body(100, 20); strings.Contains(body, "Could not load the diff") {
		t.Fatalf("the error panel replaced a loaded diff:\n%s", body)
	}
}

// A successful load clears the failure, so a diff that loads on the retry does
// not keep a stale panel.
func TestASuccessfulLoadClearsTheFailure(t *testing.T) {
	m := failedLoadModel(t)
	updated, _ := m.Update(loadDiffCmd(func(int) (string, error) { return sampleDiff, nil }, m.contextLines)())
	m = updated.(Model)
	if m.loadErr != nil {
		t.Fatalf("load error survived a successful load: %v", m.loadErr)
	}
}
