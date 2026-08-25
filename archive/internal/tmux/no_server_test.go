package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/andrewcohen/awp/internal/cmderr"
)

// With no tmux server running, listing is not a failure.
//
// It is the ordinary state of a deck that hosts its own panes, and the guard that
// says so was matching the words of the error rather than the fact of the exit —
// so it stopped recognising the case the moment the runners started writing their
// own messages. See ranAndSaidNo.

// noServer is what the real runner returns for `tmux list-sessions` with nothing
// to talk to: the runner's own message, wrapping the exit it describes.
func noServer(t *testing.T) error {
	t.Helper()
	// A real non-zero exit, so the wrapping is the same one production does rather
	// than a hand-made stand-in.
	err := exec.Command("sh", "-c", "exit 1").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("sh -c 'exit 1' did not produce an ExitError: %v", err)
	}
	return cmderr.Exited(fmt.Sprintf("%q exited 1:\nno server running on /private/tmp/tmux-502/default", "tmux"), exitErr)
}

func TestListSessionsWithNoServerIsNoSessions(t *testing.T) {
	client := New(&fakeRunner{err: noServer(t)})
	sessions, err := client.ListSessions()
	if err != nil {
		t.Fatalf("listing sessions with no server returned an error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("with no server there are %d sessions: %#v", len(sessions), sessions)
	}
}

// TestSessionIDByNameWithNoServerIsEmpty is the call the workspace delete makes,
// and the one whose error aborted the delete's zmx half.
func TestSessionIDByNameWithNoServerIsEmpty(t *testing.T) {
	client := New(&fakeRunner{err: noServer(t)})
	id, err := client.SessionIDByName("[awp]repo__qa")
	if err != nil {
		t.Fatalf("asking for a session id with no server returned an error: %v", err)
	}
	if id != "" {
		t.Errorf("with no server the session id is %q", id)
	}
}

func TestSessionExistsWithNoServerIsFalse(t *testing.T) {
	client := New(&fakeRunner{err: noServer(t)})
	exists, err := client.SessionExists("[awp]repo__qa")
	if err != nil {
		t.Fatalf("asking whether a session exists with no server returned an error: %v", err)
	}
	if exists {
		t.Error("with no server a session exists")
	}
}

func TestWindowExistsWithNoServerIsFalse(t *testing.T) {
	client := New(&fakeRunner{err: noServer(t)})
	exists, err := client.WindowExists("agent")
	if err != nil {
		t.Fatalf("asking whether a window exists with no server returned an error: %v", err)
	}
	if exists {
		t.Error("with no server a window exists")
	}
}

// TestAnUnrunnableTmuxIsStillAnError. The distinction is the point: tmux missing
// from $PATH is something to tell the user about, and swallowing it the way the
// no-server case is swallowed would make every tmux call quietly answer "no".
func TestAnUnrunnableTmuxIsStillAnError(t *testing.T) {
	client := New(&fakeRunner{err: fmt.Errorf("%q is not on $PATH for this process.", "tmux")})
	if _, err := client.ListSessions(); err == nil {
		t.Error("a tmux that could not be run at all listed zero sessions instead of failing")
	}
}
