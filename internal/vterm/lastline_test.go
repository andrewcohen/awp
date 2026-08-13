//go:build ghosttyvt

package vterm

import "testing"

// LastLine is read at the one moment the screen is about to be thrown away, so
// it has to find the output among the blank rows the emulator renders below it.
func TestLastLineFindsTheLowestLineWithAnythingOnIt(t *testing.T) {
	term := start(t, "sh", "-c", "echo first; echo 'zmx: no such session'; sleep 30")
	awaitScreen(t, term, "no such session")

	if got := term.LastLine(); got != "zmx: no such session" {
		t.Errorf("LastLine is %q, want the last line written", got)
	}
}

// The reason goes into a status bar, so escape sequences would arrive as
// literal garbage in the middle of it.
func TestLastLineIsPlainText(t *testing.T) {
	term := start(t, "sh", "-c", `printf '\033[31mred error\033[0m\n'; sleep 30`)
	awaitScreen(t, term, "red error")

	if got := term.LastLine(); got != "red error" {
		t.Errorf("LastLine is %q, want it without the colour", got)
	}
}

func TestLastLineOfABlankScreenIsEmpty(t *testing.T) {
	term := start(t, "sleep", "30")
	if got := term.LastLine(); got != "" {
		t.Errorf("LastLine of a screen with nothing on it is %q", got)
	}
}
