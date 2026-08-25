//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"testing"
	"time"
)

// TestCloseAllStopsATermNobodyClosed is the leak: two `zmx attach` clients with
// ppid 1, one holding a defunct agent, left behind after the deck that spawned
// them went away. The session surviving is the point of zmx; the client
// surviving is a process holding a pty for a deck that no longer exists, and it
// accumulates one per pane per deck run.
func TestCloseAllStopsATermNobodyClosed(t *testing.T) {
	term, err := Open(1, 40, 10, exec.Command("sh", "-c", "echo UP; sleep 60"), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitScreen(t, term, "UP")

	CloseAll()

	awaitExited(t, term, "CloseAll left the process running")
}

// A Term that closed itself is out of the registry, so CloseAll neither trips
// over it nor keeps it alive: the set must not grow for the life of the process.
func TestAClosedTermLeavesTheRegistry(t *testing.T) {
	term, err := Open(1, 40, 10, exec.Command("sh", "-c", "echo UP; sleep 60"), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	awaitScreen(t, term, "UP")
	if err := term.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	live.Lock()
	_, still := live.terms[term]
	live.Unlock()
	if still {
		t.Error("a closed Term is still registered, so the set grows once per pane forever")
	}
	// And a second sweep over it is a no-op rather than a panic.
	CloseAll()
}

// CloseAll takes the registry lock and Close takes it again to unregister, so a
// naive implementation deadlocks the first time it is used for real.
func TestCloseAllDoesNotDeadlockOnItsOwnLock(t *testing.T) {
	for i := range 3 {
		term, err := Open(i+1, 40, 10, exec.Command("sh", "-c", "echo UP; sleep 60"), HostColors{})
		if err != nil {
			t.Fatal(err)
		}
		awaitScreen(t, term, "UP")
	}

	done := make(chan struct{})
	go func() { CloseAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll blocked on the lock Close needs")
	}
}
