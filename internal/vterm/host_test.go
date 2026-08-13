//go:build ghosttyvt

package vterm

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

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

// With nothing known the query goes unanswered — no colour at all, rather than a
// default nobody chose. That is what a pane opened before the outer terminal has
// answered awp's own query gets, and it is a change: x/vt replied out of its own
// defaults, so a program was told white on black whatever was behind it.
//
// Silence is the better of the two. A program that asks and is not answered falls
// back to whatever it would have done on a terminal that does not support the
// query, which is a case it has to handle anyway; one that is answered wrongly
// blends its dim greys toward a background that is not on screen. In practice the
// deck fills the colours in from tea.BackgroundColorMsg before any pane opens.
func TestAnUnknownHostAnswersNothing(t *testing.T) {
	term := startHost(t, HostColors{}, "sh", "-c", `printf '\033]11;?\007'; exec cat -v`)
	// `cat -v` would print the reply if one came. Give it long enough to be a
	// silence rather than a race, then assert nothing arrived.
	time.Sleep(500 * time.Millisecond)
	if got := strings.TrimSpace(render(term)); got != "" {
		t.Errorf("an unknown host answered %q, want nothing", got)
	}
	// Named explicitly, because the two wrong answers are different mistakes: the
	// emulator's own default, and a colour nobody told it at all.
	for _, wrong := range []struct {
		what string
		c    color.Color
	}{
		{"its own default", color.Black},
		{"a colour nobody told it", color.RGBA{R: 0x24, G: 0x27, B: 0x3a, A: 0xff}},
	} {
		if strings.Contains(render(term), ansi.XRGBColor{Color: wrong.c}.String()) {
			t.Errorf("an unknown host answered with %s", wrong.what)
		}
	}
}
