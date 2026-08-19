//go:build ghosttyvt

package vterm

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestRepaintsAreCappedAt100Hz is that a program printing faster than anyone can
// read does not buy a frame per chunk.
//
// Every chunk the pty delivered used to be one repaint, and a repaint is a
// formatter pass over the pane's whole buffer. Measured against yes(1) before the
// cap: 1155 repaints in 300ms, against 27 after it. A deck left open on a working
// agent spent most of a core on the frames in between, none of which anyone could
// perceive.
//
// Driven through AwaitOutput rather than by counting renders, because that is the
// seam every consumer goes through — the pane, the captain and both halves of a
// split all get their repaints from it, so a cap here is a cap for all of them.
func TestRepaintsAreCappedAt100Hz(t *testing.T) {
	// yes(1) is the cheapest way to ask for output at the pty's full rate, which
	// is the case the cap exists for.
	term := start(t, "yes", "spin")
	awaitScreen(t, term, "spin")

	const window = 300 * time.Millisecond
	frames := 0
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		got := make(chan tea.Msg, 1)
		await := term.AwaitOutput()
		go func() { got <- await() }()
		select {
		case <-got:
			frames++
		case <-time.After(time.Second):
			t.Fatal("no repaint within 1s while yes(1) was printing — the output signal is not arriving")
		}
	}

	// An absolute ceiling, deliberately not derived from frameInterval. Deriving it
	// is how the first version of this test came to pass with the cap removed: the
	// bound moved with the constant it was supposed to be checking, so setting the
	// interval to a nanosecond satisfied it. 100 Hz over 300ms is 30 frames; 60 is
	// twice that, which no honest cap at or below 100 Hz exceeds and the
	// uncapped 1155 is nowhere near.
	const ceiling = 60
	if frames > ceiling {
		t.Fatalf("%d repaints in %s, want at most %d — bursts are not being folded (frameInterval is %s)", frames, window, ceiling, frameInterval)
	}
	// Cheap guard against the opposite failure: a cap that throttled everything to
	// nothing would also pass the assertion above.
	if frames == 0 {
		t.Fatalf("no repaints at all in %s while yes(1) was printing", window)
	}
}
