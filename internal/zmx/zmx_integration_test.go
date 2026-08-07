package zmx

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The unit tests above run against a fake, so they prove the client's logic but
// not that it agrees with the real zmx. This one drives the actual binary. It
// skips when zmx is not installed, so it costs nothing on a machine without it.
func TestAgainstRealZmx(t *testing.T) {
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx is not installed")
	}
	realRun := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}

	ctx := context.Background()
	c := New(realRun)
	name := SessionName("awptest", "zmxclient", "probe")
	t.Cleanup(func() { _ = c.Kill(ctx, name) })
	_ = c.Kill(ctx, name)

	// The session is created by attaching to it, which needs a pty — awp hosts
	// this command on one (see internal/vterm), so the test does the same.
	// The command reports its own parent, which is how "is this really the
	// session's process, or a line typed into a shell?" gets answered.
	attach := AttachCmd(t.TempDir(), name,
		[]string{"sh", "-c", `echo "PARENT=$(ps -o comm= -p $PPID)"; sleep 60`}, os.Environ())
	ptmx, err := pty.StartWithSize(attach, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("host `zmx attach` on a pty: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	t.Cleanup(func() {
		_ = ptmx.Close()
		if attach.Process != nil {
			_ = attach.Process.Kill()
		}
	})

	// The name SessionName produced has to be one zmx actually accepted — it
	// silently declines names containing a slash, so a bad scheme shows up as
	// a session that is simply not there.
	var found Session
	waitFor(t, "the session to appear in `zmx ls`", func() bool {
		var ok bool
		found, ok, err = c.Lookup(ctx, name)
		return err == nil && ok
	})
	if !found.Live() || found.PID == 0 {
		t.Errorf("a freshly created session parsed as %+v", found)
	}

	// `zmx run` would have made the session a login bash with the command
	// typed at its prompt. `zmx attach <name> <argv>` makes argv the process,
	// with zmx as its parent and no shell in between.
	var parent string
	waitFor(t, "the hosted command to report its parent", func() bool {
		hist, err := c.History(ctx, name)
		if err != nil {
			return false
		}
		_, after, ok := strings.Cut(hist, "PARENT=")
		if !ok {
			return false
		}
		parent, _, _ = strings.Cut(after, "\n")
		return true
	})
	if !strings.Contains(parent, "zmx") || strings.Contains(parent, "bash") {
		t.Errorf("the session's command was parented by %q; want zmx, with no shell wrapping it", parent)
	}

	// Reap must leave a running session alone — attaching to it is the point.
	if removed, err := c.Reap(ctx, name); err != nil || removed {
		t.Errorf("Reap on a live session returned removed=%v err=%v, want false/nil", removed, err)
	}

	if err := c.Label(ctx, name, map[string]string{"kind": "probe"}); err != nil {
		t.Fatalf("Label: %v", err)
	}
	labelled, _, err := c.Lookup(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if labelled.Labels["kind"] != "probe" {
		t.Errorf("labels round-tripped as %v, want kind=probe", labelled.Labels)
	}

	// Closing the pane closes the client, not the session. This is the whole
	// reason zdeck's agent and editor panes go through zmx at all, so it is
	// worth asserting against the real thing rather than trusting the docs.
	_ = ptmx.Close()
	_ = attach.Process.Kill()
	_, _ = attach.Process.Wait()

	survivor, ok, err := c.Lookup(ctx, name)
	if err != nil {
		t.Fatalf("Lookup after the client went away: %v", err)
	}
	if !ok || !survivor.Live() {
		t.Fatalf("the session did not survive its client: found=%v %+v", ok, survivor)
	}
	// History reads the screen back with nothing attached.
	if hist, err := c.History(ctx, name); err != nil {
		t.Fatalf("History with no client attached: %v", err)
	} else if !strings.Contains(hist, "PARENT=") {
		t.Errorf("scrollback lost the session's output once the client left: %q", hist)
	}

	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, stillThere, _ := c.Lookup(ctx, name); stillThere {
		t.Error("the session was still listed after Kill")
	}
}

// waitFor polls until cond holds, failing the test if it never does. zmx work
// happens in another process, so every observation here is eventually
// consistent.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
