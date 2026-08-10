package vterm

import (
	"image/color"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// startHost is start with something said about the outer terminal.
func startHost(t *testing.T, host HostColors, args ...string) *Term {
	t.Helper()
	term, err := Start(1, 60, 10, exec.Command(args[0], args[1:]...), host) //nolint:gosec // fixed test commands
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return term
}

// The lie: a pane answered "what colour is your background?" out of x/vt's own
// defaults, so every hosted program was told white on black whatever was really
// behind it. A program that derives a dim grey by blending toward the background
// was blending toward a background that was not on screen.
//
// The reply is read back through the hosted program: it goes out of the emulator
// and down the pty like a real terminal's would, and `cat -v` shows what
// arrived — which is the only place the answer is observable at all.
func TestAPaneAnswersAColourQueryWithTheRealTerminalsColour(t *testing.T) {
	host := HostColors{
		Fg:     color.RGBA{R: 0xca, G: 0xd3, B: 0xf5, A: 0xff},
		Bg:     color.RGBA{R: 0x24, G: 0x27, B: 0x3a, A: 0xff},
		Cursor: color.RGBA{R: 0xf4, G: 0xdb, B: 0xd6, A: 0xff},
	}
	for _, tc := range []struct {
		name  string
		query string
		want  color.Color
	}{
		{"foreground", `\033]10;?\007`, host.Fg},
		{"background", `\033]11;?\007`, host.Bg},
		{"cursor", `\033]12;?\007`, host.Cursor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := startHost(t, host, "sh", "-c", `printf '`+tc.query+`'; exec cat -v`)
			want := ansi.XRGBColor{Color: tc.want}.String()
			awaitScreen(t, term, want)
		})
	}
}

// With nothing known the emulator's own defaults stand — the same wrong answer
// as before, which is honest about not knowing rather than a fresh invention.
// This is what a pane opened before the terminal has answered gets.
func TestAnUnknownHostLeavesTheEmulatorsDefaults(t *testing.T) {
	term := startHost(t, HostColors{}, "sh", "-c", `printf '\033]11;?\007'; exec cat -v`)
	black := ansi.XRGBColor{Color: color.Black}.String()
	awaitScreen(t, term, black)

	// And the point of the fix: that default is not the terminal the deck is on.
	catppuccin := ansi.XRGBColor{Color: color.RGBA{R: 0x24, G: 0x27, B: 0x3a, A: 0xff}}.String()
	if strings.Contains(render(term), catppuccin) {
		t.Error("an unknown host reported a colour nobody told it")
	}
}
