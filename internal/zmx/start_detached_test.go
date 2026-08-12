package zmx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shimZmx puts a fake `zmx` first on PATH, running the given script body with
// `$marker` set to the path that stands for "the session exists now", and returns
// that path.
//
// Touching it is the body's job rather than the harness's, and that is the whole
// staging. It used to be an unconditional first line, so the shim for an attach
// that dies claimed the session and then exited — and StartDetached watches for
// both of those at once, so which one it saw was a coin toss that full-suite load
// could tip. A shim that makes no session must say so by staying silent.
//
// No requireRealZmx here, and deliberately: the guard exists because reaching the
// real daemon from inside a zmx session steals the developer's session, and a shim
// is not the daemon. What these tests need is a process that behaves like an
// attach client — one that holds its pty until someone ends it, and one that does
// not — which is the part of StartDetached worth pinning.
func shimZmx(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := "#!/bin/sh\nmarker='" + marker + "'\n" + body
	if err := os.WriteFile(filepath.Join(dir, "zmx"), []byte(script), 0o755); err != nil { //nolint:gosec // a test shim has to be executable
		t.Fatalf("write the zmx shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// makesASession is the body of a shim that creates the session and then holds its
// pty, the way a real attach client does. The sleep outlasts every wait in this
// file, so a shim still running at the end of a test is one StartDetached forgot.
const makesASession = "touch \"$marker\"\nsleep 5\n"

// listsOnce reports a live session named `name`, but only once the shim has said
// it made one — the same reason the daemon cannot report one earlier: it lists a
// session because zmx made one.
func listsOnce(name, after string) RunFunc {
	return func(_ context.Context, _, bin string, args ...string) (string, error) {
		if bin != "zmx" || len(args) == 0 || args[0] != "ls" {
			return "", nil
		}
		if _, err := os.Stat(after); err != nil {
			return "", nil
		}
		return "name=" + name + "\tpid=4242\tclients=1\tcreated=1786124270\n", nil
	}
}

// TestADetachedStartLeavesNoClientBehind is the invariant that makes this safe to
// call from a create job, which exits moments later. An attach client left running
// reparents to init holding a pty nobody will ever read — see internal/vterm's
// reap.go for what an accumulation of those looks like.
func TestADetachedStartLeavesNoClientBehind(t *testing.T) {
	marker := shimZmx(t, makesASession)
	err := New(listsOnce("awp.p.w.agent", marker)).
		StartDetached(context.Background(), t.TempDir(), "awp.p.w.agent", []string{"claude", "go"}, os.Environ())
	if err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	// The shim sleeps far longer than this, so anything still alive is the client
	// we were supposed to have ended.
	if pids := shimProcesses(t); len(pids) > 0 {
		t.Errorf("%d attach client(s) still running after the session was made: %v", len(pids), pids)
	}
}

// TestAnAttachThatDiesIsReportedNotWaitedOut: the client's own complaint goes to
// the pty this discards, so the error has to name the likely cause itself. Without
// this branch the call would sit out the whole appear-timeout on the commonest
// failure there is — no daemon.
//
// The shim makes no session, which is the scenario and not an omission. Once it
// also touched the marker there was a live session for StartDetached to find, so
// the test could pass because the exit was noticed first rather than because a
// dead client is reported — and under load it sometimes wasn't. There is now no
// success for either branch to reach: the exit is an error, and failing to notice
// it runs into the timing assertion below.
func TestAnAttachThatDiesIsReportedNotWaitedOut(t *testing.T) {
	marker := shimZmx(t, "exit 3\n")
	start := time.Now()
	err := New(listsOnce("awp.p.w.agent", marker)).
		StartDetached(context.Background(), t.TempDir(), "awp.p.w.agent", []string{"claude", "go"}, os.Environ())
	if _, serr := os.Stat(marker); serr == nil {
		t.Fatal("the shim claimed a session, so this is no longer an attach that died without making one")
	}
	if err == nil {
		t.Fatal("an attach that exited immediately reported success")
	}
	if !strings.Contains(err.Error(), "awp.p.w.agent") || !strings.Contains(err.Error(), "daemon") {
		t.Errorf("error %q names neither the session nor what to check", err)
	}
	if took := time.Since(start); took > detachedAppearWait/2 {
		t.Errorf("waited %s for a client that had already exited; the whole timeout is %s", took, detachedAppearWait)
	}
}

// TestADetachedStartNeedsACommand. An attach with no argv asks zmx for a login
// shell, so accepting an empty one would create a session holding a bare shell
// under the name of the agent that was supposed to be in it — and the deck would
// then read that shell as a live agent.
func TestADetachedStartNeedsACommand(t *testing.T) {
	marker := shimZmx(t, makesASession)
	if err := New(listsOnce("awp.p.w.agent", marker)).
		StartDetached(context.Background(), t.TempDir(), "awp.p.w.agent", nil, os.Environ()); err == nil {
		t.Fatal("started a session with nothing to run in it")
	}
}

// TestADetachedStartNeedsAName mirrors Kill's guard: an empty name is a caller
// that lost track of which session it meant, and zmx would take it as a request
// about something else entirely.
func TestADetachedStartNeedsAName(t *testing.T) {
	marker := shimZmx(t, makesASession)
	if err := New(listsOnce("", marker)).
		StartDetached(context.Background(), t.TempDir(), "  ", []string{"claude"}, os.Environ()); err == nil {
		t.Fatal("started a session with no name")
	}
}

// shimProcesses lists the pids of shims from this test's PATH that are still
// running, by asking the OS rather than by remembering what we spawned — the
// point is to catch a client this package forgot about.
func shimProcesses(t *testing.T) []string {
	t.Helper()
	dir := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	out, err := runPS(t)
	if err != nil {
		t.Skipf("cannot list processes: %v", err)
	}
	var found []string
	for line := range strings.Lines(out) {
		if !strings.Contains(line, filepath.Join(dir, "zmx")) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			found = append(found, fields[0])
		}
	}
	return found
}

func runPS(t *testing.T) (string, error) {
	t.Helper()
	out, err := exec.Command("ps", "-ax", "-o", "pid,command").Output()
	return string(out), err
}
