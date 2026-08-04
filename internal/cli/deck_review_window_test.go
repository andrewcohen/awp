package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `C` opens the review in a tmux window beside the agent rather than in the
// deck's popup. The scope is the same one `c` opens on, so the two entry points
// cannot disagree about what "review this change" means — which is why the arg is
// a sentinel the handler expands once it has resolved the base.

func TestExpandWindowArgResolvesTheReviewScope(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/parent-change"}
	item := deckui.Item{Path: "/ws/child", Bookmark: "andrew/child"}
	got := expandWindowArg(r, item, deckui.ReviewStackArg)
	if got != "review:awp diff -r 'andrew/parent-change..@'" {
		t.Fatalf("got %q", got)
	}
}

// The trunk fallback's revset has parentheses in it, and it reaches a shell.
// Unquoted, `trunk()..@` is a syntax error rather than a revset.
func TestExpandWindowArgQuotesTheRevset(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: ""}
	item := deckui.Item{Path: "/ws/x", Bookmark: "andrew/x"}
	got := expandWindowArg(r, item, deckui.ReviewStackArg)
	if got != "review:awp diff -r 'trunk()..@'" {
		t.Fatalf("got %q — trunk()..@ must reach the shell quoted", got)
	}
}

// With no directory to ask jj in, the base falls back to the literal trunk() —
// which the window's own cwd resolves — so there is still a command to run rather
// than a half-built `-r ..@`.
func TestExpandWindowArgIsAlwaysAnswerable(t *testing.T) {
	r := &reviewBaseRunner{trunk: "main", parent: "andrew/p"}
	got := expandWindowArg(r, deckui.Item{}, deckui.ReviewStackArg)
	if got != "review:awp diff -r 'trunk()..@'" {
		t.Fatalf("got %q", got)
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
func TestReviewWindowRunsAWPDiffAtTheResolvedScope(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                    "$1\t[awp]repo__qa\n",
		"tmux list-windows -t [awp]repo__qa -F #{window_id}\t#{window_name}":      "@1\tagent\n@2\treview\n",
		"tmux display-message -p -t [awp]repo__qa:review #{pane_current_command}": "zsh\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws", Bookmark: "andrew/qa"}

	// A base runner for the jj side, and the tmux fake for everything else: the
	// handler's runner answers both, so give it the tmux fake and resolve the base
	// through expandWindowArg's own runner in the arg it is handed.
	arg := expandWindowArg(&reviewBaseRunner{trunk: "main", parent: ""}, item, deckui.ReviewStackArg)
	if err := openNamedWindow(client, svc, item, arg, noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	// The command goes in literally (send-keys -l) and Enter follows as its own
	// call, so the assertion is over every call rather than the last.
	var sent, switched bool
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if joined == `tmux send-keys -t [awp]repo__qa:review -l awp diff -r 'trunk()..@'` {
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

	arg := expandWindowArg(&reviewBaseRunner{trunk: "main"}, item, deckui.ReviewStackArg)
	if err := openNamedWindow(client, svc, item, arg, noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call, " "), "send-keys") {
			t.Fatalf("expected no relaunch over a running viewer: %#v", runner.calls)
		}
	}
}
