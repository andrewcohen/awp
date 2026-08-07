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

// A zmx session's terminal size is fixed when it is created. If a later client
// attaches at a different size and zmx does not propagate it, the hosted
// program keeps laying out for the old width while the new client's emulator
// wraps at the new one — which shows up as leftover text at the end of a line
// that was rewritten shorter.
//
// This is the exact situation zdeck creates: a session made in one terminal,
// resumed in a deck pane whose size is the deck's, minus its chrome.
func TestASecondClientsSizeReachesTheProgram(t *testing.T) {
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx is not installed")
	}
	run := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
	ctx := context.Background()
	c := New(run)
	name := SessionName("awptest", "sizeprobe", "probe")
	t.Cleanup(func() { _ = c.Kill(ctx, name) })
	_ = c.Kill(ctx, name)

	dir := t.TempDir()
	out := dir + "/size.txt"
	// Report the pty size once a second, so we can see it before and after a
	// second client attaches at a different size.
	script := `while :; do stty size > ` + out + ` 2>/dev/null; sleep 0.2; done`

	first := AttachCmd(dir, name, []string{"sh", "-c", script}, os.Environ())
	p1, err := pty.StartWithSize(first, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, p1) }()

	read := func() string {
		b, _ := os.ReadFile(out)
		return strings.TrimSpace(string(b))
	}
	waitFor(t, "the program to report its size", func() bool { return read() != "" })
	created := read()
	t.Logf("size at creation (client was 100x30): %q", created)

	// Now the zdeck case: drop the first client, attach a second at a
	// different size, as a deck pane would.
	_ = p1.Close()
	if first.Process != nil {
		_ = first.Process.Kill()
	}
	time.Sleep(300 * time.Millisecond)

	second := AttachCmd(dir, name, nil, os.Environ())
	p2, err := pty.StartWithSize(second, &pty.Winsize{Cols: 64, Rows: 20})
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, p2) }()
	t.Cleanup(func() {
		_ = p2.Close()
		if second.Process != nil {
			_ = second.Process.Kill()
		}
	})

	time.Sleep(1500 * time.Millisecond)
	after := read()
	t.Logf("size after a 64x20 client attached: %q", after)
	if after != "20 64" {
		t.Errorf("the program still thinks its terminal is %q, but the client is 64x20 — "+
			"it lays out for the wrong width and the pane shows residue", after)
	}
}
