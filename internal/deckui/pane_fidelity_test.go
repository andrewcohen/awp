package deckui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"

	"github.com/andrewcohen/awp/internal/vterm"
)

// A pane's whole promise is that the program inside it looks the same as it
// would in its own terminal. Everything between the two — the emulator, the
// popover's border, lipgloss composing the frame, Bubble Tea's cell diff and
// its colour-profile conversion — can drop what it does not understand, and
// nothing fails when it does. The screen is just quietly wrong.
//
// These pin the two cases a real agent's UI is built from, measured on the
// bytes the deck's terminal actually receives rather than on any intermediate
// string:
//
//   - SGR 2 (faint) with no colour, which is how a dim hint is usually written
//   - a truecolor foreground and background, which is how Claude Code draws its
//     input box (fg rgb(80,80,80) on bg rgb(55,55,55), from a real capture)
//
// Both are measured twice, because they reach the terminal by different paths:
// once on the first full paint, and once as an incremental repaint after the
// pane's program rewrites the line. The renderer emits a style transition in
// the second case and a whole style in the first, and only one of those has to
// be wrong for dim text to arrive bright.
func TestAPanesStylesReachTheTerminalIntact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		emits string // what the hosted program writes
		want  string // what the deck's terminal must receive
	}{
		{
			name:  "faint with no colour",
			emits: `\033[0m\033[2mMARKER\033[0m`,
			want:  "\x1b[2mMARKER",
		},
		{
			name:  "faint with a truecolor foreground",
			emits: `\033[38;2;248;248;242m\033[2mMARKER\033[0m`,
			want:  "\x1b[38;2;248;248;242;2mMARKER",
		},
		{
			name:  "truecolor foreground on a truecolor background",
			emits: `\033[48;2;55;55;55m\033[38;2;80;80;80mMARKER\033[0m`,
			want:  "\x1b[38;2;80;80;80;48;2;55;55;55mMARKER",
		},
	} {
		for _, repaint := range []bool{false, true} {
			name := tc.name
			if repaint {
				name += ", repainted"
			}
			t.Run(name, func(t *testing.T) {
				got := paintedByADeck(t, tc.emits, repaint)
				if !strings.Contains(got, tc.want) {
					t.Errorf("the terminal never received %q.\nAround the marker: %s",
						tc.want, aroundMarker(got))
				}
			})
		}
	}
}

// paintedByADeck hosts a program that writes emits, runs the deck as a real
// Bubble Tea program against a real pty, and returns everything the program
// wrote to it.
//
// A real pty rather than a buffer, because Bubble Tea asks its output whether
// it is a terminal and the colour profile follows from the answer — against a
// buffer this would measure a monochrome deck nobody runs.
//
// repaint delays the styled write until after the first frame, so the bytes
// under test come from the renderer's cell diff rather than from a cold paint.
func paintedByADeck(t *testing.T, emits string, repaint bool) string {
	t.Helper()

	backend := allKinds()
	// The leading character gives the first frame something to paint, so a
	// repainted case has a previous style for the diff to transition from.
	backend.script = `printf '.'; ` + delayFor(repaint) + `printf '` + emits + `'; sleep 30`
	m, p := openedRealPane(t, backend)
	eventually(t, "the pane to paint", func() bool { return strings.Contains(p.term.View(), ".") })

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close() }()
	if err := pty.Setsize(tty, &pty.Winsize{Cols: 120, Rows: 40}); err != nil {
		t.Fatal(err)
	}

	var painted strings.Builder
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&painted, ptmx)
	}()

	// Nothing types at this deck; it only has to paint.
	quiet, _ := io.Pipe()
	program := tea.NewProgram(m, tea.WithInput(quiet), tea.WithOutput(tty))
	go func() {
		// The AwaitOutput that opened the pane was returned outside this
		// program, so nothing is watching the pty yet. One synthetic message
		// makes update hand back a real one, and repaints flow from there.
		time.Sleep(200 * time.Millisecond)
		program.Send(vterm.OutputMsg{Gen: p.term.Gen()})
		eventuallyPainted(&painted, "MARKER")
		program.Kill()
	}()
	_, _ = program.Run()
	_ = tty.Close()
	<-drained
	return painted.String()
}

func delayFor(repaint bool) string {
	if repaint {
		return "sleep 1; "
	}
	return ""
}

// eventuallyPainted waits for the marker to reach the terminal, so the program
// is not killed before the frame under test is written. It gives up rather than
// hanging — an absent marker is the failure the test reports.
func eventuallyPainted(painted *strings.Builder, want string) {
	for i := 0; i < 150; i++ {
		if strings.Contains(painted.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// aroundMarker is the slice of the paint a failure is worth reading: the whole
// capture is tens of kilobytes of frame.
func aroundMarker(painted string) string {
	i := strings.Index(painted, "MARKER")
	if i < 0 {
		return "the marker never reached the terminal at all"
	}
	return fmt.Sprintf("%q", painted[max(i-100, 0):i+10])
}
