package vterm

import (
	"os"
	"strings"
)

// The parts of a hosted terminal that are not the emulator: the messages a pane's
// terminal sends its host, and the environment its process is given.
//
// They live apart from the emulator because they outlive any particular one. These
// were in the x/vt implementation's file until that implementation was deleted,
// and every one of them is either read by the deck (which is built without the
// emulator's tag) or by internal/zmx (which starts the processes a pane later
// attaches to), so none of them could go with it.

// OutputMsg reports that a terminal's screen changed and the view should repaint.
// Gen identifies which terminal produced it: one that has been closed can still
// have a frame in flight, and it must not repaint the one that replaced it.
type OutputMsg struct{ Gen int }

// ExitMsg reports that the hosted process is gone. Err is nil for a clean
// exit.
type ExitMsg struct {
	Gen int
	Err error
}

// TermType is what a hosted process should be told its terminal is.
//
// It has to be stated rather than inherited, because the process is talking to
// this emulator and not to whatever awp itself is running under. Inheriting it
// while awp runs inside tmux hands the child TERM=tmux-256color, and a
// screen-class terminal quietly loses capabilities the emulator has.
const TermType = "xterm-256color"

// Env prepares the environment for a hosted process: TermType, and no
// inherited multiplexer markers.
//
// A marker says "you are already inside me", and a hosted process is not
// inside whatever awp is inside — it is inside this emulator. Under tmux the
// markers make a nested client refuse to start, which is loud. ZMX_SESSION is
// the quiet one: `zmx attach` reads it and, finding one, tells the daemon to
// switch the *calling* client's session rather than making a new client. So a
// pane that inherited it does not open a session beside awp's, it steals the
// terminal awp is running in, and the session it was pulled off is re-created
// empty — losing whatever agent was in it. zmx sets the variable itself for a
// session's own child, so dropping the inherited value is not information lost.
func Env(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "TERM="),
			strings.HasPrefix(kv, "TMUX="),
			strings.HasPrefix(kv, "TMUX_PANE="),
			strings.HasPrefix(kv, "ZMX_SESSION="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+TermType)
}

// HostTerm restores the real terminal's TERM in an environment Env prepared.
//
// Env states TERM because the child is talking to this emulator. A child handed
// the terminal itself — see the pane handover — is talking to the same terminal
// awp is, so the honest answer is awp's own TERM: xterm-ghostty describes what
// is on the other end, and claiming xterm-256color there gives up capabilities
// that are genuinely present.
//
// Only TERM is restored. The multiplexer markers Env dropped stay dropped, and
// ZMX_SESSION most of all: `zmx attach` reads it and switches the *calling*
// client's session rather than making a new one, so a handed-over attach that
// inherited it would steal the terminal awp is running in.
func HostTerm(env []string) []string {
	term := os.Getenv("TERM")
	if term == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+term)
}
