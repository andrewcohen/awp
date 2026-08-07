package cli

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zmx"
)

// TestPaneHostedCreateStartsNoTmuxAgent is the bug this flag exists for.
//
// Creating a workspace from a deck that hosts its own panes used to start an
// agent in tmux anyway, so the workspace had two: the zmx one you could see,
// and an invisible tmux one holding the prompt you typed. Nothing errored —
// the deck just showed an idle workspace whose agent was somewhere else.
func TestPaneHostedCreateStartsNoTmuxAgent(t *testing.T) {
	svc := &fakeService{}
	runner := &recordingRunner{}
	err := openWorkspaceWithReporter(runner, svc, openRequest{
		Name:       "qa",
		Prompt:     "fix tests",
		Yes:        true,
		PaneHosted: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	for _, call := range runner.calls {
		if len(call) >= 2 && call[0] == "tmux" {
			switch call[1] {
			case "new-session", "send-keys", "switch-client":
				t.Fatalf("pane-hosted create ran tmux %s: %#v", call[1], runner.calls)
			}
		}
	}
	if svc.prepareName != "qa" {
		t.Fatalf("expected the jj workspace to still be prepared; prepareName = %q", svc.prepareName)
	}
}

// TestPaneHostedCreateParksThePrompt: the prompt has to survive creation,
// because the agent that will receive it does not exist yet and may not until
// the user opens its pane.
func TestPaneHostedCreateParksThePrompt(t *testing.T) {
	svc := &fakeService{}
	if err := openWorkspaceWithReporter(&recordingRunner{}, svc, openRequest{
		Name:       "qa",
		Prompt:     "fix tests",
		Yes:        true,
		PaneHosted: true,
	}, nil); err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	got := svc.pendingPrompts["qa"]
	if got.Text != "fix tests" {
		t.Fatalf("pending prompt = %q, want %q", got.Text, "fix tests")
	}
	if got.Review {
		t.Error("a create parked its prompt as a review brief")
	}
}

// TestTmuxCreateStillStartsAnAgent guards the other side: the flag must not
// have quietly turned the ordinary deck's create into a no-op.
func TestTmuxCreateStillStartsAnAgent(t *testing.T) {
	svc := &fakeService{}
	runner := &recordingRunner{}
	if err := openWorkspaceWithReporter(runner, svc, openRequest{
		Name:     "qa",
		Prompt:   "fix tests",
		Yes:      true,
		NoSwitch: true,
	}, nil); err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	var sawNewSession, sawSendKeys bool
	for _, call := range runner.calls {
		if len(call) < 2 || call[0] != "tmux" {
			continue
		}
		switch call[1] {
		case "new-session":
			sawNewSession = true
		case "send-keys":
			sawSendKeys = true
		}
	}
	if !sawNewSession || !sawSendKeys {
		t.Fatalf("tmux create must still make a session and launch an agent; new-session=%v send-keys=%v", sawNewSession, sawSendKeys)
	}
	if len(svc.pendingPrompts) != 0 {
		t.Fatalf("tmux create delivered the prompt, so nothing should be parked; got %v", svc.pendingPrompts)
	}
}

// fakeZmx answers the two questions Open asks, and records what it was told
// to paste. sendErr fails only the paste, which is the interesting failure:
// by then the prompt has already been taken.
//
// A live session prints no `ended` key at all — its presence is what marks a
// session finished, whatever the value (see zmx.parseSession).
type fakeZmx struct {
	live    bool
	pasted  []string
	sendErr error
}

func (f *fakeZmx) run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name != "zmx" || len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "ls":
		if !f.live {
			return "", nil
		}
		return "name=awp.repo.qa.agent\tpid=42\tclients=0\tcreated=1786124270\n", nil
	case "send":
		if f.sendErr != nil {
			return "", f.sendErr
		}
		if len(args) >= 3 {
			f.pasted = append(f.pasted, args[2])
		}
	}
	return "", nil
}

// promptSvc is a workspace.Service that only answers the pending-prompt
// questions — the rest of the interface is not reached by pane opening.
type promptSvc struct {
	workspace.Service
	pending  workspace.PendingPrompt
	reparked workspace.PendingPrompt
}

func (p *promptSvc) TakePendingPrompt(string) (workspace.PendingPrompt, error) {
	took := p.pending
	p.pending = workspace.PendingPrompt{}
	return took, nil
}

func (p *promptSvc) RecordPendingPrompt(_ string, pp workspace.PendingPrompt) error {
	p.reparked = pp
	return nil
}

func paneItem() deckui.Item {
	return deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo", Path: "/repo/qa"}
}

// TestOpeningTheAgentDeliversTheParkedPrompt: the prompt goes in as the
// agent's own argument when the session is about to be created, which is the
// whole reason parking is safe — no waiting for the agent to boot, and no
// paste racing its input box.
func TestOpeningTheAgentDeliversTheParkedPrompt(t *testing.T) {
	fz := &fakeZmx{}
	svc := &promptSvc{pending: workspace.PendingPrompt{Text: "fix the tests"}}
	panes := zmxPanes{
		client: zmx.New(fz.run),
		svcFor: func(string) workspace.Service { return svc },
	}
	cmd, _, err := panes.Open(paneItem(), deckui.PaneKindAgent, 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !hasArg(cmd, "fix the tests") {
		t.Fatalf("expected the prompt in argv; got %v", cmd.Args)
	}
	if len(fz.pasted) != 0 {
		t.Fatalf("a session being created should take argv, not a paste; pasted %v", fz.pasted)
	}
	if !svc.pending.Empty() {
		t.Fatal("the prompt should have been taken, so it is not delivered twice")
	}
}

// TestOpeningALiveAgentPastesTheParkedPrompt: zmx ignores argv for a session
// that already exists, so a live agent has to be pasted to instead.
func TestOpeningALiveAgentPastesTheParkedPrompt(t *testing.T) {
	fz := &fakeZmx{live: true}
	svc := &promptSvc{pending: workspace.PendingPrompt{Text: "fix the tests"}}
	panes := zmxPanes{
		client: zmx.New(fz.run),
		svcFor: func(string) workspace.Service { return svc },
	}
	cmd, _, err := panes.Open(paneItem(), deckui.PaneKindAgent, 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if hasArg(cmd, "fix the tests") {
		t.Fatalf("argv is ignored for a live session, so the prompt must not go there; got %v", cmd.Args)
	}
	var sawPaste bool
	for _, p := range fz.pasted {
		if strings.Contains(p, "fix the tests") {
			sawPaste = true
		}
	}
	if !sawPaste {
		t.Fatalf("expected the prompt pasted into the live session; pasted %v", fz.pasted)
	}
}

// TestAFailedDeliveryReparksThePrompt: a prompt still parked can be delivered
// next time; one we took and dropped is gone.
func TestAFailedDeliveryReparksThePrompt(t *testing.T) {
	svc := &promptSvc{pending: workspace.PendingPrompt{Text: "fix the tests"}}
	panes := zmxPanes{
		client: zmx.New((&fakeZmx{live: true, sendErr: errors.New("zmx is not answering")}).run),
		svcFor: func(string) workspace.Service { return svc },
	}
	if _, _, err := panes.Open(paneItem(), deckui.PaneKindAgent, 80, 24); err == nil {
		t.Fatal("expected Open to report the failure")
	}
	if svc.reparked.Text != "fix the tests" {
		t.Fatalf("prompt was not re-parked; reparked = %q", svc.reparked.Text)
	}
}

// TestOnlyTheAgentPaneTakesThePrompt: a shell or an editor is not a recipient,
// and taking the prompt to open one would silently eat it.
func TestOnlyTheAgentPaneTakesThePrompt(t *testing.T) {
	svc := &promptSvc{pending: workspace.PendingPrompt{Text: "fix the tests"}}
	panes := zmxPanes{
		client: zmx.New((&fakeZmx{}).run),
		svcFor: func(string) workspace.Service { return svc },
	}
	if _, _, err := panes.Open(paneItem(), "editor", 80, 24); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if svc.pending.Text != "fix the tests" {
		t.Fatalf("the editor pane took the agent's prompt; pending = %q", svc.pending.Text)
	}
}

func hasArg(cmd *exec.Cmd, want string) bool {
	for _, a := range cmd.Args {
		if a == want {
			return true
		}
	}
	return false
}

// TestAParkedReviewPromptStartsAReviewer is the defect that splitting the
// review flow created: the review path deliberately launches without the
// dev-loop preamble, but the pane's agent kind launches with it. A reviewer
// told to work in units, run gates and commit starts doing the author's job on
// someone else's PR.
func TestAParkedReviewPromptStartsAReviewer(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)

	argvFor := func(p workspace.PendingPrompt) []string {
		svc := &promptSvc{pending: p}
		panes := zmxPanes{
			client: zmx.New((&fakeZmx{}).run),
			svcFor: func(string) workspace.Service { return svc },
		}
		item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: dir, Path: dir}
		cmd, _, err := panes.Open(item, deckui.PaneKindAgent, 80, 24)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return cmd.Args
	}

	// The comparison only means something if the coding agent does get a
	// preamble from this fixture — otherwise both sides are trivially equal
	// and the test would pass with the distinction deleted.
	coding := argvFor(workspace.PendingPrompt{Text: "review PR 12"})
	if !slices.Contains(coding, appendPreambleFlag) {
		t.Fatalf("fixture is wrong: the coding agent got no preamble to distinguish from: %v", coding)
	}
	review := argvFor(workspace.PendingPrompt{Text: "review PR 12", Review: true})
	if slices.Contains(review, appendPreambleFlag) {
		t.Errorf("a parked review prompt started an agent carrying the dev-loop preamble: %v", review)
	}
	if !slices.Contains(review, "review PR 12") {
		t.Errorf("the review prompt did not reach the agent: %v", review)
	}
}
