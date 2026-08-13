//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"strings"
	"testing"
)

// Scrolling back through a pane's history.
//
// A pane had none: View renders the screen, the screen is the visible rows, and
// what left the top was gone. The emulator has kept the history all along.

// scrolledTerm fills a short terminal with numbered lines.
func scrolledTerm(t *testing.T, w, h, lines int) Hosted {
	t.Helper()
	var payload strings.Builder
	for i := 1; i <= lines; i++ {
		payload.WriteString("line" + itoa(i) + "\r\n")
	}
	term, err := Open(1, w, h, exec.Command("printf", "%s", payload.String()), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "printf did not finish")
	awaitScreen(t, term, "line"+itoa(lines))
	return term
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestScrollingUpShowsHistory — the whole point. If this fails because the screen
// is unchanged, the formatter renders the active area rather than the viewport and
// the view has to be composed from the scrollback's rows instead.
func TestScrollingUpShowsHistory(t *testing.T) {
	term := scrolledTerm(t, 40, 6, 40)
	tail := render(term)
	if !strings.Contains(tail, "line40") {
		t.Fatalf("the screen does not start on the tail:\n%s", tail)
	}

	term.ScrollBy(-10)
	back := render(term)
	if back == tail {
		t.Fatalf("scrolling up did not change the screen:\n%s", back)
	}
	if strings.Contains(back, "line40") {
		t.Errorf("the tail is still on screen after scrolling up 10 rows:\n%s", back)
	}
	if !strings.Contains(back, "line30") {
		t.Errorf("scrolling up 10 rows did not bring line30 into view:\n%s", back)
	}
}

// TestScrollingBackToTheBottomFollowsAgain.
func TestScrollingBackToTheBottomFollowsAgain(t *testing.T) {
	term := scrolledTerm(t, 40, 6, 40)
	term.ScrollBy(-10)
	term.ScrollToBottom()
	if got := render(term); !strings.Contains(got, "line40") {
		t.Errorf("the view did not return to the tail:\n%s", got)
	}
}

// TestScrollbackSaysHowFarBackTheViewIs, which is what tells a pane whether it is
// showing the latest output and whether there is anything above to go to.
func TestScrollbackSaysHowFarBackTheViewIs(t *testing.T) {
	term := scrolledTerm(t, 40, 6, 40)
	above, atBottom := term.Scrollback()
	if !atBottom {
		t.Error("a terminal showing its tail does not report being at the bottom")
	}
	if above == 0 {
		t.Error("40 lines in a 6-row terminal left no history above the view")
	}

	term.ScrollBy(-10)
	scrolledAbove, scrolledAtBottom := term.Scrollback()
	if scrolledAtBottom {
		t.Error("a view scrolled up 10 rows reports being at the bottom")
	}
	if scrolledAbove >= above {
		t.Errorf("scrolling up left %d rows above the view, was %d — it should be fewer",
			scrolledAbove, above)
	}
}

// TestAShortScreenHasNoHistory: a terminal that never overflowed is at the bottom
// with nothing above, so a pane can tell there is nothing to scroll to.
func TestAShortScreenHasNoHistory(t *testing.T) {
	term := scrolledTerm(t, 40, 20, 3)
	above, atBottom := term.Scrollback()
	if above != 0 {
		t.Errorf("three lines in a 20-row terminal left %d rows of history", above)
	}
	if !atBottom {
		t.Error("a terminal that never scrolled is not at the bottom")
	}
}

// TestScrollingAClosedTerminalIsSafe: a pane can be torn down under a wheel event.
func TestScrollingAClosedTerminalIsSafe(t *testing.T) {
	term := scrolledTerm(t, 40, 6, 40)
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	term.ScrollBy(-5)
	term.ScrollToBottom()
	if above, atBottom := term.Scrollback(); above != 0 || !atBottom {
		t.Errorf("a closed terminal reports %d rows above, atBottom=%v", above, atBottom)
	}
}
