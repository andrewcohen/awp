package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `C` opens the review in a tmux window beside the agent rather than in the
// deck's popup. The scope is the same one `c` opens on, and the standalone viewer
// resolves that for itself, so the sentinel expands to a bare `awp diff` rather
// than to a range named twice.

func TestExpandWindowArgRunsPlainAWPDiff(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent-change"}
	item := deckui.Item{Path: "/ws/child", Bookmark: "andrew/child"}
	got := expandWindowArg(r, item, deckui.ReviewStackArg)
	if got != "review:awp diff" {
		t.Fatalf("got %q", got)
	}
	// No revset spliced in, so no base resolved here: the viewer resolves the same
	// scope by itself, and a second copy of that decision is the one that goes
	// stale. It also means nothing has to be quoted on its way through a shell.
	if len(r.revs) != 0 {
		t.Errorf("expected no base resolution, got jj calls %v", r.revs)
	}
}

// Every other window arg is none of this function's business.
func TestExpandWindowArgLeavesOtherArgsAlone(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/p"}
	for _, arg := range []string{"", "editor", "agent", "review", "pr:gh pr view 5 | less -R"} {
		if got := expandWindowArg(r, deckui.Item{Path: "/ws/x"}, arg); got != arg {
			t.Errorf("expandWindowArg(%q) = %q, want it untouched", arg, got)
		}
	}
	if len(r.revs) != 0 {
		t.Errorf("a non-review arg must not resolve a base, got jj calls %v", r.revs)
	}
}

// End to end: the sentinel becomes the command the pane actually runs.
func TestReviewWindowRunsTheViewerInThePane(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                    "$1\t[awp]repo__qa\n",
		"tmux list-windows -t [awp]repo__qa -F #{window_id}\t#{window_name}":      "@1\tagent\n@2\treview\n",
		"tmux display-message -p -t [awp]repo__qa:review #{pane_current_command}": "zsh\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws", Bookmark: "andrew/qa"}

	arg := expandWindowArg(nil, item, deckui.ReviewStackArg)
	if err := openNamedWindow(client, svc, item, arg, noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	// The command goes in literally (send-keys -l) and Enter follows as its own
	// call, so the assertion is over every call rather than the last.
	var sent, switched bool
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if joined == "tmux send-keys -t [awp]repo__qa:review -l awp diff" {
			sent = true
		}
		// Summoned, not merely launched: landing in the window is the point of the
		// key.
		if joined == "tmux select-window -t [awp]repo__qa:review" {
			switched = true
		}
	}
	if !sent {
		t.Fatalf("expected the review command sent to the pane, calls: %#v", runner.calls)
	}
	if !switched {
		t.Fatalf("expected the review window focused, calls: %#v", runner.calls)
	}
}

// A window already running the viewer is focused rather than sent a second
// command — that is what makes `C` a summon rather than a relaunch.
func TestReviewWindowAlreadyRunningIsJustFocused(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                    "$1\t[awp]repo__qa\n",
		"tmux list-windows -t [awp]repo__qa -F #{window_id}\t#{window_name}":      "@1\tagent\n@2\treview\n",
		"tmux display-message -p -t [awp]repo__qa:review #{pane_current_command}": "awp\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws"}

	arg := expandWindowArg(nil, item, deckui.ReviewStackArg)
	if err := openNamedWindow(client, svc, item, arg, noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call, " "), "send-keys") {
			t.Fatalf("expected no relaunch over a running viewer: %#v", runner.calls)
		}
	}
}
