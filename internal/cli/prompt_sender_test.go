package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/zmx"
)

// promptRunner answers for both substrates at once, which is the only way to
// watch this decision: what makes a wrong answer here dangerous is that the other
// substrate happily accepts the prompt, so a test that stubbed one of them would
// pass on a send to the wrong agent.
type promptRunner struct {
	// live is whether zmx reports a live agent session for the workspace.
	live bool
	// pasted is what reached the zmx agent; tmuxed is every tmux command run.
	pasted []string
	tmuxed []string
	// tmuxErr is returned for tmux calls, so a test can prove the prompt got
	// there without walking the whole session-creation path behind it.
	tmuxErr error
}

func (r *promptRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	switch {
	case name == "zmx" && len(args) > 0 && args[0] == "ls":
		if !r.live {
			return "", nil
		}
		return "name=awp.repo.qa.agent\tpid=42\tclients=0\tcreated=1786124270\n", nil
	case name == "zmx" && len(args) > 0 && args[0] == "send":
		if len(args) >= 3 {
			r.pasted = append(r.pasted, args[2])
		}
	case name == "tmux":
		r.tmuxed = append(r.tmuxed, strings.Join(args, " "))
		return "", r.tmuxErr
	}
	return "", nil
}

// zmxOnPath puts a shim named zmx first on PATH.
//
// liveZmxAgent looks the binary up before asking anything, so without this the
// resolver reports "no zmx agent" on a machine that has no zmx and the test would
// pass by never reaching the branch it is about. The shim is never executed — the
// runner above is what answers — so its contents do not matter.
func zmxOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zmx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // a test shim has to be executable
		t.Fatalf("write the zmx shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// assertReceived checks the agent got the prompt and was told to run it.
//
// A paste is more than the text: it arrives wrapped in the bracketed-paste
// escapes that make an agent's input box read it as pasted rather than typed, and
// then a return, without which the remark sits in the box unsent — which looks
// from the deck exactly like a send that worked.
func assertReceived(t *testing.T, r *promptRunner, want string) {
	t.Helper()
	got := strings.Join(r.pasted, "")
	if !strings.Contains(got, want) {
		t.Errorf("the agent got %q, which does not contain %q", got, want)
	}
	if !strings.Contains(got, "\r") {
		t.Errorf("the prompt was pasted but never entered: %q", got)
	}
}

// TestAPaneHostAnswersForItsOwnAgent. The host is asked and nothing behind it is:
// a fallback to tmux is what starts the second agent, and the host refusing
// because no agent is running is the answer the reviewer can act on.
func TestAPaneHostAnswersForItsOwnAgent(t *testing.T) {
	r := &promptRunner{live: true, tmuxErr: errors.New("tmux should not have been asked")}
	panes := zmxPanes{client: zmx.New(r.Run)}

	send := agentPromptSender(panes, r, tmux.New(r), nil)
	if err := send(paneItem(), "look at line 12", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	assertReceived(t, r, "look at line 12")
	if len(r.tmuxed) > 0 {
		t.Errorf("tmux was asked %v — that is the second, invisible agent", r.tmuxed)
	}
}

// TestWithNoHostThePromptFollowsALiveZmxAgent is standalone `awp diff` run from
// inside a zdeck pane: there is no host object to ask, and the workspace's agent
// is a zmx session all the same.
func TestWithNoHostThePromptFollowsALiveZmxAgent(t *testing.T) {
	zmxOnPath(t)
	r := &promptRunner{live: true, tmuxErr: errors.New("tmux should not have been asked")}

	send := agentPromptSender(nil, r, tmux.New(r), nil)
	if err := send(paneItem(), "look at line 12", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	assertReceived(t, r, "look at line 12")
	if len(r.tmuxed) > 0 {
		t.Errorf("tmux was asked %v while a zmx agent was running", r.tmuxed)
	}
}

// TestWithNothingOnZmxThePromptGoesToTmux keeps `awp deck` whole. tmux is still
// the whole answer there, including starting an agent when there is none — which
// is the one thing this cannot do through zmx.
func TestWithNothingOnZmxThePromptGoesToTmux(t *testing.T) {
	zmxOnPath(t)
	stop := errors.New("tmux was asked")
	r := &promptRunner{live: false, tmuxErr: stop}

	send := agentPromptSender(nil, r, tmux.New(r), nil)
	err := send(paneItem(), "look at line 12", nil)
	if err == nil || !strings.Contains(err.Error(), stop.Error()) {
		t.Fatalf("the prompt did not reach tmux: %v (tmux calls: %v)", err, r.tmuxed)
	}
	if len(r.pasted) > 0 {
		t.Errorf("something was pasted into zmx with no session live: %v", r.pasted)
	}
}

// TestTheReviewStoreSendsThroughTheSenderItWasGiven is the seam the bug was in.
// The store used to build a tmux send of its own, so a comment written in the
// deck's `c` went somewhere the deck's `A` did not.
func TestTheReviewStoreSendsThroughTheSenderItWasGiven(t *testing.T) {
	var got []string
	cs := reviewStoreWithSend(nil, func(_ deckui.Item, text string, _ deckui.Reporter) error {
		got = append(got, text)
		return nil
	})
	if cs.Send == nil {
		t.Fatal("the review store has no send exit, so the viewer's ctrl+s does nothing")
	}
	// The error is expected: there is no review on disk to mark the comment sent
	// in. What matters is that the prompt was handed to the sender first.
	_ = cs.Send(paneItem(), review.Comment{ID: "c1", Body: "this loop is quadratic"})
	if len(got) != 1 || !strings.Contains(got[0], "this loop is quadratic") {
		t.Fatalf("the sender was given %v", got)
	}
}
