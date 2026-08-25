package deckui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// The bug: a pane whose process died closed with nothing said, so a `zmx
// attach` that would not attach — an old session the daemon declines, a
// workspace whose working copy is gone — looked like the deck popping a pane up
// and crashing. The reason was on the pane's screen and the close threw it away.
func TestAPaneThatDiesSaysWhy(t *testing.T) {
	backend := allKinds()
	backend.script = "echo 'zmx: no such session awp.old.default.agent'; exit 3"
	m, p := openedPane(t, backend)
	m.refresher = func() tea.Cmd { return nil }

	// The real exit, so the emulator has the process's last words in it: Start
	// finishes copying output before it reports the exit.
	exit, ok := p.term.AwaitExit()().(vterm.ExitMsg)
	if !ok {
		t.Fatal("AwaitExit did not report an exit")
	}
	p.update(&m, exit)

	if m.active != nil {
		t.Fatal("the pane stayed open after its process died")
	}
	if !strings.Contains(m.status, "no such session awp.old.default.agent") {
		t.Errorf("status is %q, want the pane's own last line — nothing else knows why zmx refused", m.status)
	}
	if !strings.Contains(m.status, "exit status 3") {
		t.Errorf("status is %q, want the exit status", m.status)
	}
	if !strings.Contains(m.status, "agent") {
		t.Errorf("status is %q, want the pane it was about", m.status)
	}
}

func TestOnlyASurprisingExitIsReported(t *testing.T) {
	const label = "agent · proj/ws"
	for _, tc := range []struct {
		name   string
		err    error
		lived  time.Duration
		reason string
		want   []string // substrings the status must contain; empty means silence
	}{
		{
			// You typed `exit`. Echoing that back is the noise the deck's
			// cancellation rule exists to avoid.
			name: "worked in and left", lived: time.Minute, reason: "$",
		},
		{
			// No error and yet instantly gone: a program that declined to start
			// and said so on stdout.
			name: "gone before you could read it", lived: 30 * time.Millisecond,
			reason: "zmx: session is not attachable",
			want:   []string{"exited immediately", "zmx: session is not attachable"},
		},
		{
			name: "exited badly", err: errors.New("exit status 1"), lived: time.Minute,
			want: []string{"exited: exit status 1"},
		},
		{
			// A failure long after the open is still a failure — the pane
			// vanishing under you needs a reason whenever it happens.
			name: "exited badly much later", err: errors.New("signal: killed"), lived: time.Hour,
			reason: "Killed", want: []string{"signal: killed", "Killed"},
		},
		{
			name: "no last line to quote", err: errors.New("exit status 127"), lived: time.Second,
			want: []string{"exit status 127"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := paneExitStatus(label, tc.err, tc.lived, tc.reason)
			if len(tc.want) == 0 {
				if got != "" {
					t.Errorf("status is %q, want silence", got)
				}
				return
			}
			if !strings.HasPrefix(got, label+": ") {
				t.Errorf("status is %q, want it to name the pane first", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("status is %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

// The reason is a whole terminal row wide. Unbounded it pushes the activity
// segment and the hint out of the status bar.
func TestALongReasonIsBounded(t *testing.T) {
	got := paneExitStatus("agent · p/w", errors.New("exit status 1"), 0, strings.Repeat("x", 400))
	if len(got) > paneExitReasonMax+80 {
		t.Errorf("the status is %d characters wide", len(got))
	}
}
