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

// sessionMarkers are the inherited variables a hosted process must not keep: the
// ones that say "you are already inside me".
//
// A hosted process is not inside whatever awp is inside — it is inside this
// emulator, in a session of its own. Three families, and they fail differently,
// which is why the list is worth reading rather than trusting:
//
// The multiplexers fail loudly. Under tmux the markers make a nested client refuse
// to start, and you find out immediately.
//
// ZMX_SESSION fails quietly and expensively. `zmx attach` reads it and, finding
// one, tells the daemon to switch the *calling* client's session rather than making
// a new client — so a pane that inherited it does not open a session beside awp's,
// it steals the terminal awp is running in, and the session it was pulled off is
// re-created empty, losing whatever agent was in it.
//
// Claude Code's markers fail quietly and invisibly, which is worse than either. A
// deck launched from inside a Claude Code session — which is how awp is usually
// developed — hands every agent it starts that session's identity, and the child
// responds by turning transcript saving off, because as far as it can tell it is a
// sub-agent of a session that is already recording. The agent works normally and
// writes nothing down. `awp watch` reads transcripts, so the dev-loop view goes
// blind for that workspace with no error anywhere; the captain is only where it
// surfaced, because Claude Code says so on its own start-up line there.
//
// What is *not* here is deliberate. CLAUDE_CODE_EXECPATH, CLAUDE_CODE_TMPDIR and
// the feature flags are configuration the user chose, and a child agent should go
// on honouring them — stripping those would make a hosted agent behave differently
// from the same agent in a terminal, which is the thing a pane is trying not to do.
// The rule is: drop what identifies the parent *session* or reaches back into it,
// keep what describes how the user likes their agent.
var sessionMarkers = []string{
	"TERM=",
	"TMUX=",
	"TMUX_PANE=",
	"ZMX_SESSION=",
	// Claude Code's identity for the session awp itself was launched from.
	"CLAUDE_CODE_CHILD_SESSION=",
	"CLAUDE_CODE_SESSION_ID=",
	"CLAUDE_CODE_ENTRYPOINT=",
	// And its IPC back to that session: a channel the new agent has no business on,
	// and whose token is the parent's.
	"CLAUDE_CODE_MESSAGING_SOCKET=",
	"CLAUDE_CODE_MESSAGING_TOKEN=",
}

// Env prepares the environment for a hosted process: TermType, and none of the
// inherited session markers — see sessionMarkers for what those are and how each
// one fails.
func Env(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if hasSessionMarker(kv) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+TermType)
}

// hasSessionMarker reports whether this KEY=VALUE is one of the markers to drop.
func hasSessionMarker(kv string) bool {
	for _, prefix := range sessionMarkers {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
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
