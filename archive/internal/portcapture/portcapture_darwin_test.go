//go:build darwin

package portcapture

import (
	"context"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The narrowing is only a narrowing if lsof ANDs it with the socket selection.
// `-p X -iTCP` without `-a` is an OR, which reports every listening socket on
// the machine — the expensive answer, from a command that reads as cheap.
func TestLsofAndsThePIDFilterWithTheSocketFilter(t *testing.T) {
	args := lsofArgs([]int{42, 7})

	p := slices.Index(args, "-p")
	if p < 0 {
		t.Fatalf("args = %v, want a -p selection", args)
	}
	if !slices.Contains(args, "-a") {
		t.Fatalf("args = %v, want -a — without it the -p filter widens the result instead of narrowing it", args)
	}
	if got := args[p+1]; got != "42,7" {
		t.Fatalf("-p %q, want the pids comma-separated with no spaces", got)
	}
	if !slices.Contains(args, "-iTCP") || !slices.Contains(args, "-sTCP:LISTEN") {
		t.Fatalf("args = %v, want the LISTEN-socket selection kept", args)
	}
	if strings.Contains(strings.Join(args, " "), "  ") {
		t.Fatalf("args = %v, want no empty argument", args)
	}
}

// The pids come from a `ps` taken moments earlier, so by the time lsof runs one
// of them has often exited — a shell finishing is not an unusual event in a
// deck. lsof exits 1 over it while still reporting everything it found, and
// reading that status as "nothing listening" loses the dev URL of every row,
// intermittently, for a reason nothing surfaces.
//
// Run against the real lsof: the exit status is the whole subject, and a fake
// would be asserting what this test exists to find out.
func TestADeadPIDDoesNotHideTheLiveOnesSockets(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	// A pid well above the machine's range: nothing to find, so lsof complains.
	const gone = 4194303
	got, err := listListeners(context.Background(), []int{os.Getpid(), gone})
	if err != nil {
		t.Fatalf("listListeners: %v", err)
	}
	if !slices.ContainsFunc(got, func(l Listener) bool { return l.Port == port && l.PID == os.Getpid() }) {
		t.Fatalf("listeners = %+v, want the socket this test is holding on port %d", got, port)
	}
}

// No PIDs means the caller has nothing it would keep. Running lsof to be told
// so is the cost this exists to avoid, so the answer comes back without one.
func TestNoPIDsRunsNoLsof(t *testing.T) {
	// A cancelled context would fail any command this tried to run, so
	// returning cleanly is proof it ran none.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := listListeners(ctx, nil)
	if err != nil {
		t.Fatalf("listListeners: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no listeners", got)
	}
}
