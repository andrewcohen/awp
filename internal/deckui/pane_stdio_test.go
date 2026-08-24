package deckui

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/andrewcohen/awp/internal/vterm"
)

// A pane's program talks to the pty and to nothing else.
//
// creack/pty wires the pty into the descriptors it finds empty and leaves any the
// caller filled in, so a command arriving with the deck's own os.Stdout draws
// over the deck's screen while the pane it is supposedly in stays blank — which
// reads as the program never starting. That is exactly what happened when the
// diff viewer's `e` first learned to open in a split half: the editor command was
// built for tea.ExecProcess, which wants the terminal's descriptors, and hosting
// the same command in a pane made it paint over the deck.
func TestAPanesCommandIsNotWiredToTheDecksOwnScreen(t *testing.T) {
	m := splitDeck(t)
	var gotIn io.Reader
	var gotOut, gotErr io.Writer
	m.openTerm = func(gen, w, h int, c *exec.Cmd, hc vterm.HostColors) (vterm.Hosted, error) {
		gotIn, gotOut, gotErr = c.Stdin, c.Stdout, c.Stderr
		return openFakeTerm(gen, w, h, c, hc)
	}

	cmd := exec.Command("true")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	p, _, err := m.paneRunning(cmd, m.childBox(), panePopover{label: "editor"})
	if err != nil {
		t.Fatalf("paneRunning: %v", err)
	}
	t.Cleanup(func() { p.close(&m) })

	if gotIn != nil || gotOut != nil || gotErr != nil {
		t.Errorf("the terminal was handed a command still wired to the deck: in=%v out=%v err=%v", gotIn, gotOut, gotErr)
	}
}
