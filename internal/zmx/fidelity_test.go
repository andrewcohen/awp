package zmx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/vterm"
)

// TestASessionCostsNothingInFidelity answers the question a long-lived pane
// raises: it is emulated twice.
//
// An ephemeral pane is one emulator — awp's. A session pane is two: ghostty_vt
// inside zmx interprets the program's output and re-serializes a screen for
// the client, and awp's emulator interprets that. A round trip through another
// terminal is exactly where colors and attributes go missing, so this renders
// the same payload both ways and requires the screens to be identical.
//
// If it ever fails, the loss is zmx's re-serialization and the fix is there.
// While it passes, anything wrong with how a pane looks is awp's own emulator
// (see internal/vterm/fidelity_test.go) or the layout around it.
func TestASessionCostsNothingInFidelity(t *testing.T) {
	requireRealZmx(t)

	payload := strings.Join([]string{
		"\x1b[38;2;255;100;50mtruecolor\x1b[0m",
		"\x1b[38;5;208m256-color\x1b[0m",
		"\x1b[48;5;24mbackground\x1b[0m",
		"\x1b[1mbold\x1b[0m \x1b[2mdim\x1b[0m \x1b[3mitalic\x1b[0m",
		"\x1b[4munderline\x1b[0m \x1b[4:3mcurly\x1b[0m \x1b[9mstrike\x1b[0m",
		"\x1b[7mreverse\x1b[0m",
		"\U0001F389 日本 \U0001F469\u200d\U0001F4BB",
		"PROBE-END",
	}, "\r\n") + "\r\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "payload")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	const cols, rows = 40, 12
	direct := hostUntilDone(t, cols, rows, exec.Command("cat", path))

	name := SessionName("awptest", "fidelity", "probe")
	_ = exec.Command("zmx", "kill", name, "--force").Run()
	t.Cleanup(func() { _ = exec.Command("zmx", "kill", name, "--force").Run() })
	// The session has to outlive `cat`: a client attaching to a session whose
	// command has exited gets nothing to render.
	viaSession := hostUntilDone(t, cols, rows,
		AttachCmd(dir, name, []string{"sh", "-c", "cat " + path + "; sleep 60"}, os.Environ()))

	if viaSession != direct {
		t.Errorf("a session changed the screen.\n  direct  %q\n  session %q", direct, viaSession)
	}
}

// hostUntilDone runs cmd on a pty awp owns and returns the emulator's screen
// once the payload's end marker has arrived.
func hostUntilDone(t *testing.T, w, h int, cmd *exec.Cmd) string {
	t.Helper()
	term, err := vterm.Start(1, w, h, cmd, vterm.HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		view := term.View()
		if strings.Contains(view, "PROBE-END") {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 10s for the payload; the screen was %q", view)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
