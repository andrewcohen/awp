package zmx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// requireRealZmx skips unless it is safe to drive the actual zmx binary.
//
// The unsafe case is running inside a zmx session, and it is not a matter of
// degree: `zmx attach` means two different things depending on ZMX_SESSION.
// From outside a session it creates an independent client. From inside one it
// tells the daemon to switch the *calling* client's session — the daemon log
// records those as `switch session cur=<yours> next=<theirs>`. So a test that
// attaches to a probe session from inside a zmx-hosted terminal does not get
// its own client, it steals the developer's, and the session it was pulled off
// is re-created empty afterwards. That is not flakiness; the agent that was
// running in it is gone.
//
// This repo is developed from inside a zdeck pane, which is a zmx session. So
// `go test ./...` there killed the session the work was happening in, until
// this guard existed.
//
// Deliberately not an opt-in flag: there is no value of a flag that makes
// attaching from inside a session correct. Run these from a terminal that is
// not in one.
func requireRealZmx(t *testing.T) {
	t.Helper()
	if s := os.Getenv("ZMX_SESSION"); s != "" {
		t.Skipf("running inside zmx session %q — attaching from here would switch this client's session, not make a new one; run from a terminal outside zmx", s)
	}
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx is not installed")
	}
}

// TestEveryRealZmxTestAsksFirst: the guard is only worth anything if nothing
// reaches the daemon around it. A new test that forgets would not fail — it
// would quietly start stealing the developer's session again, which is the
// failure mode least likely to be attributed to a test.
func TestEveryRealZmxTestAsksFirst(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	var checked int
	for _, f := range files {
		name := f.Name()
		if name == "real_zmx_test.go" || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		// The tell is executing something, not building it: AttachCmd appears
		// in the fake-based unit tests too, because it only assembles an
		// *exec.Cmd. A file that spawns a real process is one that can reach
		// the daemon.
		if !strings.Contains(text, "exec.CommandContext") &&
			!strings.Contains(text, `exec.Command("zmx"`) {
			continue
		}
		checked++
		if !strings.Contains(text, "requireRealZmx(t)") {
			t.Errorf("%s reaches the real zmx binary without calling requireRealZmx(t)", name)
		}
	}
	if checked == 0 {
		t.Error("found no tests that drive the real zmx binary — this guard is watching nothing, so its detection has broken")
	}
}
