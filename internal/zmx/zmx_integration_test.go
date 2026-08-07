package zmx

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
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

	created, err := c.Ensure(ctx, name, t.TempDir(), []string{"sh"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !created {
		t.Fatal("Ensure reported it did not create a session that did not exist")
	}

	// The name SessionName produced has to be one zmx actually accepted — it
	// silently declines names containing a slash, so a bad scheme shows up as
	// a session that is simply not there.
	found, ok, err := c.Lookup(ctx, name)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok {
		t.Fatalf("zmx did not accept the session name %q", name)
	}
	if !found.Live() || found.PID == 0 {
		t.Errorf("a freshly created session parsed as %+v", found)
	}

	// A second Ensure must attach to what is there rather than start a rival.
	if again, err := c.Ensure(ctx, name, t.TempDir(), []string{"sh"}); err != nil || again {
		t.Errorf("second Ensure returned created=%v err=%v, want false/nil", again, err)
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

	// History reads the session's screen with no client attached.
	if _, err := realRun(ctx, "", "zmx", "send", name, "echo SCROLLBACK-MARKER\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		hist, err := c.History(ctx, name)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if strings.Contains(hist, "SCROLLBACK-MARKER") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for the marker in scrollback; last read was %q", hist)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, stillThere, _ := c.Lookup(ctx, name); stillThere {
		t.Error("the session was still listed after Kill")
	}
}
