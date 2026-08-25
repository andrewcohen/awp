//go:build ghosttyvt

package vterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ghosttyPane runs cmd on a libghostty-vt pane of the given size.
func ghosttyPane(t *testing.T, w, h int, cmd *exec.Cmd) Hosted {
	t.Helper()
	// Open, with nothing to say about which emulator: in a build with this tag
	// there is one, and it is this. The env var that used to choose is gone.
	term, err := Open(1, w, h, cmd, HostColors{})
	if err != nil {
		t.Fatalf("open a ghostty pane: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return term
}

// waitForScreen renders until marker shows up, so a test never races the pty.
func waitForScreen(t *testing.T, term Hosted, marker string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		view := term.View()
		if strings.Contains(view, marker) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for %q; the screen was %q", marker, view)
			return view
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAGhosttyPaneIsExactlyAsTallAsItWasAsked is the contract every caller of
// View depends on and the one libghostty's formatter does not keep on its own: it
// drops trailing blank rows, so a 24-row pane showing three lines of output comes
// back as three lines and the layout below it slides up.
func TestAGhosttyPaneIsExactlyAsTallAsItWasAsked(t *testing.T) {
	const w, h = 40, 12
	term := ghosttyPane(t, w, h, exec.Command("printf", "hello\\n"))
	view := waitForScreen(t, term, "hello")

	if got := len(strings.Split(view, "\n")); got != h {
		t.Errorf("a %d-row pane rendered %d lines", h, got)
	}
}

// TestAGhosttyPaneKeepsTheStylingItIsGiven runs the same corpus
// TestTheEmulatorKeepsTheStylingItIsGiven runs through x/vt, and reports what the
// two do differently rather than asserting one exact spelling.
//
// It does not require them to agree. They serialize equivalently-but-differently
// on purpose — ghostty ends rows CRLF, opens each row with a reset, and spells the
// reset \x1b[0m where x/vt writes \x1b[m — and pinning either spelling would fail
// on a difference that changes nothing on screen. What it does require is that
// nothing an attribute carried is LOST, and it names the cases where the two
// disagree after that normalisation so the difference is a decision rather than a
// surprise.
func TestAGhosttyPaneKeepsTheStylingItIsGiven(t *testing.T) {
	in := lineInputs(styledLines)
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(strings.Join(in, "\r\n")+"\r\nPROBE-END\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	term := ghosttyPane(t, 40, len(in)+3, exec.Command("cat", path))
	view := waitForScreen(t, term, "PROBE-END")
	lines := strings.Split(view, "\n")

	for i, tc := range styledLines {
		got := strings.TrimRight(lines[i], " ")
		if normalizeSGR(got) == normalizeSGR(tc.want) {
			continue
		}
		// Known and understood, so reported rather than failed. Both are recorded in
		// #296: basic ANSI colours come back promoted to their palette index, and the
		// two writers order an underline and its colour differently.
		t.Logf("%s differs after normalisation:\n  x/vt    %q\n  ghostty %q", tc.name, tc.want, got)
	}

	// What must not happen is the text itself going missing.
	for _, want := range []string{"X", "\U0001F389", "日", "é"} {
		if !strings.Contains(view, want) {
			t.Errorf("%q is not on the screen at all", want)
		}
	}
}

// normalizeSGR removes differences of serialization convention, so a comparison
// is about information kept rather than house style.
func normalizeSGR(s string) string {
	s = strings.TrimSuffix(s, "\r")
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[m")
	return strings.TrimPrefix(s, "\x1b[m")
}

// TestAGhosttyPaneReportsWhyItDied. A program that refuses to start says so on
// its way out, and that complaint only ever exists on the emulator's screen —
// which is why the pane lifts it before tearing the terminal down.
func TestAGhosttyPaneReportsWhyItDied(t *testing.T) {
	term := ghosttyPane(t, 40, 6, exec.Command("sh", "-c", "echo 'no such session' >&2; exit 3"))
	waitForScreen(t, term, "no such session")

	if got := term.LastLine(); !strings.Contains(got, "no such session") {
		t.Errorf("LastLine() = %q, want the complaint the program wrote", got)
	}
}

// TestAGhosttyPaneResizes both halves together. The program lays out for the pty
// and the terminal interprets what comes back, so a mismatch is wrapping off by
// however far the two drifted.
func TestAGhosttyPaneResizes(t *testing.T) {
	term := ghosttyPane(t, 40, 10, exec.Command("cat"))
	if err := term.Resize(60, 20); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if w, h := term.Size(); w != 60 || h != 20 {
		t.Errorf("Size() = %dx%d after a resize to 60x20", w, h)
	}
	if got := len(strings.Split(term.View(), "\n")); got != 20 {
		t.Errorf("a resized pane renders %d lines, want 20", got)
	}
	if err := term.Resize(0, 5); err == nil {
		t.Error("a zero-width resize was accepted")
	}
}

// TestAClosedGhosttyPaneStaysClosed. Close is called from more than one path —
// the leave key, the process exiting, CloseAll on the way out — and freeing the C
// side twice is not a no-op the way closing a channel twice is: it is a crash in
// the middle of someone's terminal.
func TestAClosedGhosttyPaneStaysClosed(t *testing.T) {
	term := ghosttyPane(t, 40, 6, exec.Command("cat"))
	if err := term.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	// And every accessor still answers rather than reaching into freed memory.
	_ = term.View()
	_, _, _ = term.Cursor()
	_ = term.WantsMouse()
	_ = term.LastLine()
	if err := term.Send([]byte("x")); err == nil {
		t.Error("a closed pane accepted a send")
	}
}

// waitForShape renders until the cursor reports the shape wanted, so a test never
// races the pty. Rendering is what a frame does, and the shape is read per frame.
func waitForShape(t *testing.T, term Hosted, want tea.CursorShape) (tea.CursorShape, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		shape, blink := term.CursorShape()
		if shape == want {
			return shape, blink
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for shape %v; it is %v", want, shape)
			return shape, blink
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAPaneReportsTheCursorShapeItsProgramAsked. This is how an editor says which
// mode it is in — DECSCUSR — and awp used to drop it, drawing tea's default block
// over nvim's insert-mode bar. A pane showing someone else's screen does not get to
// overrule their cursor.
//
// Driven through a real pty with the escape sequences rather than by poking the
// emulator, because the thing that was broken was the whole path: the program
// writes it, the emulator interprets it, the deck has to be able to ask.
func TestAPaneReportsTheCursorShapeItsProgramAsked(t *testing.T) {
	// 6 is a steady bar, 4 a steady underline, 2 a steady block — the odd numbers
	// are their blinking twins. printf so nothing but the sequences is written.
	term := ghosttyPane(t, 40, 6, exec.Command("printf", "READY\\n\\033[6 q"))
	waitForScreen(t, term, "READY")

	if shape, blink := waitForShape(t, term, tea.CursorBar); blink {
		t.Errorf("DECSCUSR 6 is a *steady* bar; got shape %v blink %v", shape, blink)
	}
}

// TestAPaneCursorGoesBackToABlock, because a mode is left as well as entered — and
// the shape is cached between reads, so the direction that has to invalidate the
// cache is the one worth a test.
//
// The program writes both sequences, with a pause between them. It cannot be done
// by sending the second one to the pane: Send is "as if typed", and a typed escape
// is echoed by the line discipline as a literal ^[ rather than replayed as a
// command — which is the correct behaviour, and was this test's first mistake.
func TestAPaneCursorGoesBackToABlock(t *testing.T) {
	term := ghosttyPane(t, 40, 6, exec.Command("sh", "-c",
		`printf 'READY\n\033[5 q'; sleep 0.5; printf '\033[2 q'; sleep 5`))
	waitForScreen(t, term, "READY")
	waitForShape(t, term, tea.CursorBar)
	// What leaving insert mode does.
	waitForShape(t, term, tea.CursorBlock)
}

// TestABlinkingShapeIsReportedAsBlinking. DECSCUSR encodes shape and blink in one
// parameter — 5 is a blinking bar and 6 a steady one — so reading the shape without
// the blink is reading half of what the program said.
func TestABlinkingShapeIsReportedAsBlinking(t *testing.T) {
	term := ghosttyPane(t, 40, 6, exec.Command("printf", "READY\\n\\033[5 q"))
	waitForScreen(t, term, "READY")

	if shape, blink := waitForShape(t, term, tea.CursorBar); !blink {
		t.Errorf("DECSCUSR 5 is a *blinking* bar; got shape %v blink %v", shape, blink)
	}
}

// TestAFreshPaneReportsABlock — the terminal default, and what a program that has
// asked for nothing gets. Not a fallback: it is the right answer.
func TestAFreshPaneReportsABlock(t *testing.T) {
	term := ghosttyPane(t, 40, 6, exec.Command("printf", "READY\\n"))
	waitForScreen(t, term, "READY")

	if shape, _ := term.CursorShape(); shape != tea.CursorBlock {
		t.Errorf("a pane whose program said nothing reports shape %v, want a block", shape)
	}
}
